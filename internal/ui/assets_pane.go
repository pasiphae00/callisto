package ui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
)

// assetsPane shows the balances held by the active wallet on the active
// connection: the native asset first, then curated + user-added tokens. It
// reloads on demand and whenever a new block arrives. Viewing balances needs only
// the wallet's address, so it works whether or not the wallet is unlocked.
type assetsPane struct {
	app *App

	status  *widget.Label
	list    *widget.List
	items   []assets.Asset
	loading bool
}

func newAssetsPane(a *App) *assetsPane {
	return &assetsPane{app: a}
}

func (p *assetsPane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	p.list = widget.NewList(
		func() int { return len(p.items) },
		func() fyne.CanvasObject { return monoLabel("template") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(assetRow(p.items[i]))
		},
	)

	refreshBtn := widget.NewButton("Refresh", func() { p.app.refreshAssets() })
	addTokenBtn := widget.NewButton("Add token…", p.showAddToken)

	// Reload when balances are refreshed from anywhere (this pane or Send).
	p.app.registerAssetsReloader(p.reload)

	// Auto-refresh on each new head (fires only while connected).
	p.app.rpc.OnNewHead(func(*types.Header) {
		fyne.Do(func() { p.reload() })
	})

	header := widget.NewLabelWithStyle("Assets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	top := container.NewVBox(header, container.NewHBox(refreshBtn, addTokenBtn), p.status, widget.NewSeparator())

	p.reload()
	return container.NewBorder(top, nil, nil, nil, p.list)
}

// assetRow formats one asset line: symbol, display balance (≤5 dp), and name.
func assetRow(a assets.Asset) string {
	tag := ""
	if a.Kind == assets.Native {
		tag = " (native)"
	}
	bal := assets.FormatDisplay(a.Balance, a.Decimals, assets.DisplayDecimals)
	return fmt.Sprintf("%-8s %s   —   %s%s", a.Symbol, bal, a.Name, tag)
}

// visibleAssets drops dust/zero-balance tokens (the native asset is always kept),
// returning the visible list and the number hidden.
func visibleAssets(all []assets.Asset) (visible []assets.Asset, hidden int) {
	for _, a := range all {
		if a.Kind != assets.Native && assets.IsDust(a.Balance, a.Decimals) {
			hidden++
			continue
		}
		visible = append(visible, a)
	}
	return visible, hidden
}

// reload refreshes balances for the active wallet/connection. It is a no-op while
// a previous load is in flight.
func (p *assetsPane) reload() {
	if p.loading {
		return
	}
	desc, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	if !ok {
		p.setEmpty("Select a wallet in the Wallets tab to view its balances.")
		return
	}
	addr, err := address.Parse(desc.Address)
	if err != nil {
		p.setEmpty("This wallet has an invalid address on record.")
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.setEmpty("Connect an RPC endpoint in Settings to load balances.")
		return
	}

	p.loading = true
	p.status.SetText("Loading balances…")
	client := conn.Client
	chainID := conn.ChainID.Uint64()
	userTokens := p.app.cfg.TokensForChain(chainID)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		svc := assets.NewService(client, chainID)
		got, loadErr := svc.Load(ctx, addr, userTokens)

		fyne.Do(func() {
			p.loading = false
			if loadErr != nil {
				p.status.SetText("Could not load balances: " + loadErr.Error())
				return
			}
			visible, hidden := visibleAssets(got)
			p.items = visible
			p.list.Refresh()
			status := fmt.Sprintf("%d assets · %s · %s", len(visible), desc.Label, conn.ChainInfo.Name)
			if hidden > 0 {
				status += fmt.Sprintf(" · %d dust hidden", hidden)
			}
			p.status.SetText(status)
		})
	}()
}

// setEmpty clears the list and shows a status message.
func (p *assetsPane) setEmpty(msg string) {
	p.items = nil
	if p.list != nil {
		p.list.Refresh()
	}
	p.status.SetText(msg)
}

// showAddToken prompts for an ERC-20 contract address and, if connected, records
// it for the active chain and reloads.
func (p *assetsPane) showAddToken() {
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder("0x… token contract address")

	d := dialog.NewForm("Add token", "Add", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Contract", entry)},
		func(okPressed bool) {
			if !okPressed {
				return
			}
			addr, err := address.Parse(entry.Text)
			if err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			ref := assets.TokenRef{ChainID: conn.ChainID.Uint64(), Address: addr.Hex()}
			if !p.app.cfg.AddToken(ref) {
				dialog.ShowInformation("Already added", "That token is already in your list.", p.app.window)
				return
			}
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
			}
			p.reload()
		}, p.app.window)
	d.Resize(fyne.NewSize(520, 200))
	d.Show()
}
