// Package ui builds Callisto's Fyne GUI. It depends on the domain packages but
// they never depend on it — the GUI is a thin presentation layer over config,
// store, and (in later phases) the RPC/asset/tx services.
//
// Root layout construction (buildRoot) is intentionally separable from the
// windowing/run loop so it can be exercised in headless tests via fyne's test
// app, without requiring a display.
package ui

import (
	"fmt"
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/ens"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/store"
)

// App holds the wiring shared across panes. Panes read/update Config and persist
// via Config.Save(); Store backs history and the contract address book; rpc is
// the live connection manager.
type App struct {
	cfg   *config.Config
	store *store.Store
	rpc   *rpc.Manager

	fyneApp fyne.App
	window  fyne.Window

	statusBarBox *fyne.Container

	// Live signer session for the currently unlocked wallet, if any. Held in
	// memory only; wiped on lock/disconnect/close. Never persisted.
	signerMu     sync.Mutex
	activeSigner signer.Signer
	signerWallet string // wallet ID the active signer belongs to
}

// New constructs the App wiring. It does not create any windows or a driver, so
// it is safe to call in tests; call Run to actually launch the GUI.
func New(cfg *config.Config, st *store.Store) *App {
	return &App{cfg: cfg, store: st, rpc: rpc.NewManager()}
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
	a.window = a.fyneApp.NewWindow("Callisto")
	a.window.SetContent(a.buildRoot())
	a.window.Resize(fyne.NewSize(1024, 720))
	a.window.CenterOnScreen()
	// Ensure the live connection (and its head-watching goroutine) is torn down,
	// and any unlocked signer's key material is wiped, when the window closes.
	defer a.rpc.Disconnect()
	defer a.clearSigner()
	a.window.ShowAndRun()
}

// buildRoot assembles the top-level tabbed layout. Panes are placeholders in the
// bootstrap phase and are filled in by subsequent phases; keeping the tab shell
// here means each phase slots its pane in without touching the frame.
func (a *App) buildRoot() fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem("Wallets", newWalletsPane(a).build()),
		container.NewTabItem("Assets", newAssetsPane(a).build()),
		container.NewTabItem("Send", a.placeholder("Send", "Prepare a basic ETH or ERC-20 transfer.")),
		container.NewTabItem("History", a.placeholder("History", "Transactions Callisto has prepared.")),
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
)

// refreshStatusBar rebuilds the bottom status bar to reflect live connection and
// wallet-selection state. Safe to call from the UI thread at any time; callers on
// a background goroutine must wrap it in fyne.Do.
func (a *App) refreshStatusBar() {
	if a.statusBarBox == nil {
		return
	}
	// Connection: colored dot + label.
	dotColor := statusGray
	endpoint := "no RPC connected"
	if conn, ok := a.rpc.Active(); ok {
		dotColor = statusGreen
		endpoint = conn.Endpoint.Name + " · " + conn.ChainInfo.Name
	} else if e, ok := a.cfg.ActiveEndpointConfig(); ok {
		dotColor = statusAmber
		endpoint = e.Name + " (not connected)"
	}
	dot := canvas.NewText("●", dotColor)

	// Wallet: label + lock state.
	wallet := "no wallet selected"
	if w, ok := a.cfg.WalletByID(a.cfg.ActiveWallet); ok && w.Label != "" {
		state := "locked"
		if _, id, unlocked := a.currentSigner(); unlocked && id == w.ID {
			state = "unlocked"
		}
		wallet = fmt.Sprintf("%s (%s)", w.Label, state)
	}

	a.statusBarBox.Objects = []fyne.CanvasObject{
		dot,
		widget.NewLabel(endpoint),
		widget.NewSeparator(),
		widget.NewLabel(wallet),
	}
	a.statusBarBox.Refresh()
}
