// Package ui builds Callisto's Fyne GUI. It depends on the domain packages but
// they never depend on it — the GUI is a thin presentation layer over config,
// store, and (in later phases) the RPC/asset/tx services.
//
// Root layout construction (buildRoot) is intentionally separable from the
// windowing/run loop so it can be exercised in headless tests via fyne's test
// app, without requiring a display.
package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae/callisto/internal/config"
	"github.com/pasiphae/callisto/internal/store"
)

// App holds the wiring shared across panes. Panes read/update Config and persist
// via Config.Save(); Store backs history and the contract address book.
type App struct {
	cfg   *config.Config
	store *store.Store

	fyneApp fyne.App
	window  fyne.Window
}

// New constructs the App wiring. It does not create any windows or a driver, so
// it is safe to call in tests; call Run to actually launch the GUI.
func New(cfg *config.Config, st *store.Store) *App {
	return &App{cfg: cfg, store: st}
}

// Run creates the Fyne application + main window and blocks until the window is
// closed. It must be called on the main goroutine.
func (a *App) Run() {
	a.fyneApp = app.NewWithID("io.pasiphae.callisto")
	a.window = a.fyneApp.NewWindow("Callisto")
	a.window.SetContent(a.buildRoot())
	a.window.Resize(fyne.NewSize(1024, 720))
	a.window.CenterOnScreen()
	a.window.ShowAndRun()
}

// buildRoot assembles the top-level tabbed layout. Panes are placeholders in the
// bootstrap phase and are filled in by subsequent phases; keeping the tab shell
// here means each phase slots its pane in without touching the frame.
func (a *App) buildRoot() fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem("Wallets", a.placeholder("Wallets", "Add, select, and unlock wallets.")),
		container.NewTabItem("Assets", a.placeholder("Assets", "Balances for the selected wallet.")),
		container.NewTabItem("Send", a.placeholder("Send", "Prepare a basic ETH or ERC-20 transfer.")),
		container.NewTabItem("History", a.placeholder("History", "Transactions Callisto has prepared.")),
		container.NewTabItem("Settings", a.placeholder("Settings", "Configure RPC endpoints.")),
	)
	tabs.SetTabLocation(container.TabLocationLeading)
	return container.NewBorder(nil, a.statusBar(), nil, nil, tabs)
}

// placeholder is a temporary pane body used until a phase provides the real one.
func (a *App) placeholder(title, subtitle string) fyne.CanvasObject {
	head := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	body := widget.NewLabel(subtitle)
	body.Wrapping = fyne.TextWrapWord
	return container.NewVBox(head, body)
}

// statusBar summarizes connection/wallet state at the bottom of the window.
func (a *App) statusBar() fyne.CanvasObject {
	endpoint := "no RPC configured"
	if e, ok := a.cfg.ActiveEndpointConfig(); ok {
		endpoint = "RPC: " + e.Name
	}
	wallet := "no wallet selected"
	if w, ok := a.cfg.WalletByID(a.cfg.ActiveWallet); ok && w.Label != "" {
		wallet = "Wallet: " + w.Label
	}
	return container.NewHBox(
		widget.NewLabel(endpoint),
		widget.NewSeparator(),
		widget.NewLabel(wallet),
	)
}
