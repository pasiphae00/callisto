package ui

import (
	"fyne.io/fyne/v2"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/address"
)

// assetsPane is the Assets tab: an assetsView bound to the active wallet. All the
// balance/discovery/hide/sort logic lives in assetsView (shared with the Safe tab's
// Assets sub-tab); this only supplies the "which account" target.
type assetsPane struct {
	view *assetsView
}

func newAssetsPane(a *App) *assetsPane {
	target := func() (common.Address, string, bool) {
		desc, ok := a.cfg.WalletByID(a.cfg.ActiveWallet)
		if !ok {
			return common.Address{}, "", false
		}
		addr, err := address.Parse(desc.Address)
		if err != nil {
			return common.Address{}, "", false
		}
		label := desc.Label
		if label == "" {
			label = "(unnamed)"
		}
		return addr, label, true
	}
	v := newAssetsView(a, "Select a wallet in the Wallets tab to view its balances.", target)
	// Only auto-refresh on new heads while the Assets pane is the one shown.
	v.headVisible = func() bool { return a.navShown("Assets") }
	return &assetsPane{view: v}
}

func (p *assetsPane) build() fyne.CanvasObject {
	return p.view.build("Assets",
		"Balances update automatically on each new block detection; tokens held automatically populate.\n\nSelect a token and Hide it to remove spam and dust from the list. Add a token manually if it isn't detected.")
}

// onShow refreshes balances immediately when the pane is navigated to (bypassing the
// head-reload throttle), so it never sits stale waiting for the next allowed refresh.
func (p *assetsPane) onShow() { p.view.reload() }
