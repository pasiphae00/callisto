package ui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// assetsPane shows the balances held by the active wallet on the active
// connection: the native asset first, then curated + user-added tokens. It
// reloads on demand and whenever a new block arrives. Viewing balances needs only
// the wallet's address, so it works whether or not the wallet is unlocked.
type assetsPane struct {
	app *App

	status   *widget.Label
	list     *widget.List
	items    []assets.Asset
	loading  bool
	selected int
	hideBtn  *widget.Button
}

func newAssetsPane(a *App) *assetsPane {
	return &assetsPane{app: a, selected: -1}
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
	p.list.OnSelected = func(id widget.ListItemID) { p.selected = id; p.updateHideButton() }
	p.list.OnUnselected = func(widget.ListItemID) { p.selected = -1; p.updateHideButton() }

	addTokenBtn := widget.NewButton("Add token…", p.showAddToken)
	p.hideBtn = widget.NewButton("Hide (spam)", p.hideSelected)
	p.hideBtn.Disable()
	hiddenBtn := widget.NewButton("Hidden…", p.showHidden)

	// Reload when balances are refreshed from anywhere (this pane or Send).
	p.app.registerAssetsReloader(p.reload)

	// Auto-refresh on each new head (fires only while connected). Balances update
	// and any newly-received token is discovered without a manual refresh.
	p.app.rpc.OnNewHead(func(*types.Header) {
		fyne.Do(func() { p.reload() })
	})

	header := widget.NewLabelWithStyle("Assets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel("Balances update automatically on each new block; tokens you hold are detected on their own. Select a token and Hide it to keep spam out of the list; add a token manually only if it isn't picked up.")
	hint.Wrapping = fyne.TextWrapWord
	buttons := container.NewHBox(addTokenBtn, p.hideBtn, hiddenBtn)
	top := container.NewVBox(header, hint, indentToText(buttons), p.status, widget.NewSeparator())

	p.reload()
	return container.NewBorder(top, nil, nil, nil, p.list)
}

// assetRow formats one asset line into fixed-width columns (rendered in the mono
// font, so the columns line up): symbol, display balance (≤5 dp), then name.
func assetRow(a assets.Asset) string {
	tag := ""
	if a.Kind == assets.Native {
		tag = " (native)"
	}
	bal := assets.FormatDisplay(a.Balance, a.Decimals, assets.DisplayDecimals)
	return fmt.Sprintf("%-8s %-18s  —  %s%s", a.Symbol, bal, a.Name, tag)
}

// assetKey is a stable identity for an asset (native, or a token by contract),
// used to preserve the selection across reloads even when the sort order shifts.
func assetKey(a assets.Asset) string {
	if a.Kind == assets.Native {
		return "native"
	}
	return a.Contract.Hex()
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
	// Only show the "Loading…" text on the first (empty) load; on steady-state
	// refreshes (per block, or reconciling after a hide) keep the current status so
	// it doesn't flicker every time.
	if len(p.items) == 0 {
		p.status.SetText("Loading balances…")
	}
	client := conn.Client
	chainID := conn.ChainID.Uint64()
	tokens := p.app.knownTokens(chainID, addr)

	// Kick off / advance token discovery so held tokens populate automatically.
	p.app.disc.ensure(chainID, addr, client)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		svc := p.app.assetService(chainID, client)
		got, loadErr := svc.Load(ctx, addr, tokens)

		fyne.Do(func() {
			p.loading = false
			if loadErr != nil {
				p.status.SetText("Could not load balances: " + loadErr.Error())
				return
			}
			// Preserve the selection across the refresh by token identity, not by
			// index: the sort order can shift, so the old index may point at a
			// different asset. If the selected asset is gone (hidden, or dropped to
			// dust), selection clears.
			var selKey string
			if p.selected >= 0 && p.selected < len(p.items) {
				selKey = assetKey(p.items[p.selected])
			}

			got = p.app.displayAssets(chainID, addr, got)
			visible, hidden := visibleAssets(got)
			p.items = visible
			p.list.UnselectAll()
			p.selected = -1
			if selKey != "" {
				for i, a := range p.items {
					if assetKey(a) == selKey {
						p.selected = i
						p.list.Select(i) // fires OnSelected → updateHideButton
						break
					}
				}
			}
			p.updateHideButton()
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
	p.selected = -1
	if p.list != nil {
		p.list.UnselectAll()
		p.list.Refresh()
	}
	p.updateHideButton()
	p.status.SetText(msg)
}

// updateHideButton enables Hide only when a (non-native) token row is selected.
func (p *assetsPane) updateHideButton() {
	if p.hideBtn == nil {
		return
	}
	if p.selected >= 0 && p.selected < len(p.items) && p.items[p.selected].Kind == assets.Token {
		p.hideBtn.Enable()
	} else {
		p.hideBtn.Disable()
	}
}

// activeWalletChain resolves the active wallet's address and the connected chain,
// reporting a user-facing error if either is unavailable.
func (p *assetsPane) activeWalletChain() (addr common.Address, chainID uint64, client rpc.Client, ok bool) {
	desc, has := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	if !has {
		dialog.ShowInformation("No wallet", "Select a wallet in the Wallets tab first.", p.app.window)
		return
	}
	a, err := address.Parse(desc.Address)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	conn, connected := p.app.rpc.Active()
	if !connected {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	return a, conn.ChainID.Uint64(), conn.Client, true
}

// hideSelected hides the selected token as spam (persisted) and reloads. The
// decision is reversible via the Hidden… manager, so no confirmation is needed.
func (p *assetsPane) hideSelected() {
	if p.selected < 0 || p.selected >= len(p.items) {
		return
	}
	a := p.items[p.selected]
	if a.Kind != assets.Token {
		return
	}
	addr, chainID, _, ok := p.activeWalletChain()
	if !ok {
		return
	}
	p.app.disc.setHidden(chainID, addr, a.Contract, true)

	// Optimistic update: drop the row from the visible list right now so it
	// disappears instantly, instead of waiting for a full balance reload (a
	// network round-trip) to redraw. reload() below reconciles counts/order in
	// the background; displayAssets already filters the hidden token out.
	p.items = append(p.items[:p.selected], p.items[p.selected+1:]...)
	p.selected = -1
	p.list.UnselectAll()
	p.updateHideButton()
	p.list.Refresh()

	p.reload()
}

// showHidden lists the tokens hidden for the active wallet, each with an Unhide
// action. Symbols are fetched best-effort so junk-named spam is still identifiable.
func (p *assetsPane) showHidden() {
	addr, chainID, client, ok := p.activeWalletChain()
	if !ok {
		return
	}

	box := container.NewVBox()
	var rebuild func()
	rebuild = func() {
		box.RemoveAll()
		hid := p.app.disc.hiddenTokens(chainID, addr)
		sort.Slice(hid, func(i, j int) bool { return hid[i].Hex() < hid[j].Hex() })
		if len(hid) == 0 {
			box.Add(widget.NewLabel("No hidden tokens."))
			box.Refresh()
			return
		}
		for _, t := range hid {
			t := t
			lbl := monoLabel(address.Short(t))
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				if m, err := assets.Metadata(ctx, client, t); err == nil && m.Symbol != "" {
					fyne.Do(func() { lbl.SetText(fmt.Sprintf("%-8s %s", m.Symbol, address.Short(t))) })
				}
			}()
			unhideBtn := widget.NewButton("Unhide", func() {
				p.app.disc.setHidden(chainID, addr, t, false)
				p.reload()
				rebuild()
			})
			box.Add(container.NewBorder(nil, nil, nil, unhideBtn, lbl))
		}
		box.Refresh()
	}
	rebuild()

	d := dialog.NewCustom("Hidden tokens", "Close", container.NewVScroll(box), p.app.window)
	d.Resize(fyne.NewSize(480, 420))
	d.Show()
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
