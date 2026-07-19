// Package ui builds Callisto's Fyne GUI. It depends on the domain packages but
// they never depend on it — the GUI is a thin presentation layer over config,
// store, and (in later phases) the RPC/asset/tx services.
//
// Root layout construction (buildRoot) is intentionally separable from the
// windowing/run loop so it can be exercised in headless tests via fyne's test
// app, without requiring a display.
package ui

import (
	"context"
	"image/color"
	"net/url"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/ens"
	"codeberg.org/pasiphae/callisto/internal/history"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/safe"
	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/store"
	"codeberg.org/pasiphae/callisto/internal/walletconnect"
)

// App holds the wiring shared across panes. Panes read/update Config and persist
// via Config.Save(); Store backs history and the contract address book; rpc is
// the live connection manager.
type App struct {
	cfg           *config.Config
	store         *store.Store
	rpc           *rpc.Manager
	history       *history.Repo
	safeProposals *safe.ProposalRepo

	fyneApp fyne.App
	window  fyne.Window

	statusBarBox *fyne.Container

	// historyReload, if set by the History pane, refreshes it after a send.
	historyReload func()

	// Live signer session for the currently unlocked wallet, if any. Held in
	// memory only; wiped on lock/disconnect/close. Never persisted.
	signerMu     sync.Mutex
	activeSigner signer.Signer
	signerWallet string // wallet ID the active signer belongs to

	// wc is the WalletConnect client, created lazily by the WalletConnect pane on
	// first connect and torn down on app close.
	wcMu sync.Mutex
	wc   *walletconnect.Client
}

// setWalletConnect stores the WalletConnect client for teardown at app close.
func (a *App) setWalletConnect(c *walletconnect.Client) {
	a.wcMu.Lock()
	a.wc = c
	a.wcMu.Unlock()
}

// closeWalletConnect cleanly shuts down WalletConnect at exit: it notifies every
// connected dApp (wc_sessionDelete) before dropping the relay connection, so dApps
// see a proper disconnect rather than a dead socket. Bounded so it can't hang exit.
func (a *App) closeWalletConnect() {
	a.wcMu.Lock()
	c := a.wc
	a.wc = nil
	a.wcMu.Unlock()
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.DisconnectAll(ctx)
	c.Close()
}

// New constructs the App wiring. It does not create any windows or a driver, so
// it is safe to call in tests; call Run to actually launch the GUI.
func New(cfg *config.Config, st *store.Store) *App {
	a := &App{cfg: cfg, store: st, rpc: rpc.NewManager()}
	if st != nil {
		a.history = history.New(st)
		a.safeProposals = safe.NewProposalRepo(st.DB())
	}
	return a
}

// openURL opens a URL in the user's browser if a Fyne app is running.
func (a *App) openURL(raw string) {
	if a.fyneApp == nil || raw == "" {
		return
	}
	if u, err := url.Parse(raw); err == nil {
		_ = a.fyneApp.OpenURL(u)
	}
}

// autoConnectOnStart connects the endpoint marked as the startup default, if any,
// and updates the status bar. Failures are silent (the user can connect manually).
func (a *App) autoConnectOnStart() {
	e, ok := a.cfg.AutoConnectEndpoint()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := a.rpc.Connect(ctx, e); err != nil {
		return
	}
	a.cfg.ActiveEndpoint = e.Name
	fyne.Do(a.refreshStatusBar)
}

// currentResolver returns an ENS resolver bound to the active connection, or nil
// if no RPC is connected. Widgets call this each time they need to resolve so
// they always use the current endpoint.
func (a *App) currentResolver() *ens.Resolver {
	if conn, ok := a.rpc.Active(); ok {
		return ens.NewResolver(conn.Client)
	}
	return nil
}

// setSigner installs a live signer session for a wallet, locking and replacing
// any previous one so key material never lingers.
func (a *App) setSigner(walletID string, s signer.Signer) {
	a.signerMu.Lock()
	old := a.activeSigner
	a.activeSigner = s
	a.signerWallet = walletID
	a.signerMu.Unlock()
	lockSigner(old)
}

// clearSigner locks and drops the active signer session (if any).
func (a *App) clearSigner() {
	a.signerMu.Lock()
	old := a.activeSigner
	a.activeSigner = nil
	a.signerWallet = ""
	a.signerMu.Unlock()
	lockSigner(old)
}

// currentSigner returns the active signer session and the wallet ID it belongs
// to, or ok=false if no wallet is unlocked.
func (a *App) currentSigner() (s signer.Signer, walletID string, ok bool) {
	a.signerMu.Lock()
	defer a.signerMu.Unlock()
	if a.activeSigner == nil {
		return nil, "", false
	}
	return a.activeSigner, a.signerWallet, true
}

// lockSigner wipes a signer's key material if it supports locking.
func lockSigner(s signer.Signer) {
	if l, ok := s.(signer.Lockable); ok {
		l.Lock()
	}
}

// Run creates the Fyne application + main window and blocks until the window is
// closed. It must be called on the main goroutine.
func (a *App) Run() {
	a.fyneApp = app.NewWithID("io.pasiphae.callisto")
	a.fyneApp.SetIcon(appIcon)
	a.applyMonoFont() // BerkeleyMono for addresses/amounts, if available
	a.window = a.fyneApp.NewWindow("Callisto")
	a.window.SetIcon(appIcon)
	// The item label must be exactly "About" (not "About Callisto"): Fyne's
	// macOS driver special-cases that exact string and splices it into the
	// native app menu Cocoa already auto-generates (replacing its default
	// action with ours), rather than creating a menu item of our own. Any
	// other label creates a second, separate "Callisto" menu next to the
	// auto-generated one — confirmed live, that's the bug this fixes.
	a.window.SetMainMenu(fyne.NewMainMenu(
		fyne.NewMenu("Callisto", fyne.NewMenuItem("About", func() { showAbout(a) })),
	))
	a.window.SetContent(a.buildRoot())
	a.window.Resize(fyne.NewSize(1024, 720))
	a.window.CenterOnScreen()
	// Auto-connect the default endpoint (if any) once the event loop is running.
	go a.autoConnectOnStart()

	// Ensure the live connection (and its head-watching goroutine) is torn down,
	// and any unlocked signer's key material is wiped, when the window closes.
	defer a.rpc.Disconnect()
	defer a.clearSigner()
	defer a.closeWalletConnect()
	a.window.ShowAndRun()
}

// buildRoot assembles the top-level tabbed layout. Panes are placeholders in the
// bootstrap phase and are filled in by subsequent phases; keeping the tab shell
// here means each phase slots its pane in without touching the frame.
func (a *App) buildRoot() fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem("Wallets", newWalletsPane(a).build()),
		container.NewTabItem("Assets", newAssetsPane(a).build()),
		container.NewTabItem("Send", newSendPane(a).build()),
		container.NewTabItem("Safe", newSafePane(a).build()),
		container.NewTabItem("WalletConnect", newWalletConnectPane(a).build()),
		container.NewTabItem("History", newHistoryPane(a).build()),
		container.NewTabItem("Settings", newSettingsPane(a).build()),
	)
	tabs.SetTabLocation(container.TabLocationLeading)

	a.statusBarBox = container.NewHBox()
	a.refreshStatusBar()
	return container.NewBorder(nil, a.statusBarBox, nil, nil, tabs)
}

// placeholder is a temporary pane body used until a phase provides the real one.
func (a *App) placeholder(title, subtitle string) fyne.CanvasObject {
	head := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	body := widget.NewLabel(subtitle)
	body.Wrapping = fyne.TextWrapWord
	return container.NewVBox(head, body)
}

// Status-indicator colors. The connection dot is the at-a-glance health signal:
//
//	green  — connected to a live endpoint
//	amber  — an endpoint is selected but not currently connected
//	gray   — no endpoint configured/selected
var (
	statusGreen = color.NRGBA{R: 0x2e, G: 0x7d, B: 0x32, A: 0xff}
	statusAmber = color.NRGBA{R: 0xef, G: 0x6c, B: 0x00, A: 0xff}
	statusGray  = color.NRGBA{R: 0x9e, G: 0x9e, B: 0x9e, A: 0xff}
	// colorTransparent hides a status glyph while preserving its layout space.
	colorTransparent = color.NRGBA{}
)

// refreshStatusBar rebuilds the bottom status bar to reflect live connection and
// wallet-selection state. Safe to call from the UI thread at any time; callers on
// a background goroutine must wrap it in fyne.Do.
func (a *App) refreshStatusBar() {
	if a.statusBarBox == nil {
		return
	}
	// The whole footer is a single monospace RichText line so every piece shares
	// one baseline (mixing fonts/sizes across separate labels looked uneven). The
	// "RPC:"/"Active wallet:" labels are smaller and muted; the values are normal.
	dotColor := theme.ColorNameDisabled
	rpcLabel := "none"
	rpcSuffix := ""
	if conn, ok := a.rpc.Active(); ok {
		dotColor = theme.ColorNameSuccess
		rpcLabel = conn.Endpoint.Name
		rpcSuffix = " · " + conn.ChainInfo.Name
	} else if e, ok := a.cfg.ActiveEndpointConfig(); ok {
		dotColor = theme.ColorNameWarning
		rpcLabel = e.Name
		rpcSuffix = " (not connected)"
	}

	segs := []widget.RichTextSegment{
		statusSeg("● ", theme.SizeNameText, dotColor),
		statusSeg("RPC: ", theme.SizeNameCaptionText, theme.ColorNamePlaceHolder),
		statusSeg(rpcLabel, theme.SizeNameText, theme.ColorNameForeground),
	}
	if rpcSuffix != "" {
		segs = append(segs, statusSeg(rpcSuffix, theme.SizeNameCaptionText, theme.ColorNamePlaceHolder))
	}
	segs = append(segs,
		statusSeg("   |   ", theme.SizeNameText, theme.ColorNamePlaceHolder),
		statusSeg("Active wallet: ", theme.SizeNameCaptionText, theme.ColorNamePlaceHolder),
	)
	if w, ok := a.cfg.WalletByID(a.cfg.ActiveWallet); ok && w.Label != "" {
		state := "locked"
		if _, id, unlocked := a.currentSigner(); unlocked && id == w.ID {
			state = "unlocked"
		}
		segs = append(segs,
			statusSeg(w.Label, theme.SizeNameText, theme.ColorNameForeground),
			statusSeg("  ("+state+")", theme.SizeNameCaptionText, theme.ColorNamePlaceHolder),
		)
	} else {
		segs = append(segs, statusSeg("none", theme.SizeNameText, theme.ColorNameForeground))
	}

	rt := widget.NewRichText(segs...)
	rt.Wrapping = fyne.TextWrapOff
	a.statusBarBox.Objects = []fyne.CanvasObject{rt}
	a.statusBarBox.Refresh()
}

// statusSeg builds a monospace RichText segment for the footer with a theme size
// and color.
func statusSeg(text string, size fyne.ThemeSizeName, color fyne.ThemeColorName) *widget.TextSegment {
	return &widget.TextSegment{
		Text: text,
		Style: widget.RichTextStyle{
			Inline:    true,
			TextStyle: fyne.TextStyle{Monospace: true},
			SizeName:  size,
			ColorName: color,
		},
	}
}
