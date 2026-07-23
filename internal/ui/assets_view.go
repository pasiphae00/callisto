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

	"github.com/pasiphae00/callisto/internal/address"
	"github.com/pasiphae00/callisto/internal/assets"
	"github.com/pasiphae00/callisto/internal/rpc"
)

// assetsView is a reusable balances list for a single account (an EOA wallet or a
// Safe): it auto-discovers held tokens, hides spam, sorts native-first, refreshes on
// each block, and lets the user hide/unhide or add a token. The account it shows is
// supplied by the target callback, so the same component drives the Assets pane
// (active wallet) and the Safe tab's Assets sub-tab (selected Safe) without drift.
type assetsView struct {
	app *App
	// target returns the address + display label to show and whether one is
	// available (e.g. a wallet/Safe is selected). Re-read on every reload.
	target func() (addr common.Address, label string, ok bool)
	// emptyMsg is shown when target reports nothing selected.
	emptyMsg string

	status   *widget.Label
	list     *widget.List
	items    []assets.Asset
	loading  bool
	selected int
	hideBtn  *widget.Button
	lastAddr common.Address // account the current items belong to (for switch detection)

	// headVisible gates head-driven reloads so a hidden view doesn't hit the RPC every
	// block (nil = always reload). lastHeadReload throttles them to headReloadInterval.
	headVisible    func() bool
	lastHeadReload time.Time
}

// onHead is the new-head handler: reload only when this view is visible and not more
// often than headReloadInterval. Explicit reloads (show, switch, post-send) bypass this.
func (v *assetsView) onHead() {
	if v.headVisible != nil && !v.headVisible() {
		return
	}
	if time.Since(v.lastHeadReload) < headReloadInterval {
		return
	}
	v.lastHeadReload = time.Now()
	v.reload()
}

func newAssetsView(app *App, emptyMsg string, target func() (common.Address, string, bool)) *assetsView {
	return &assetsView{app: app, target: target, emptyMsg: emptyMsg, selected: -1}
}

// build composes the view (optional header + hint, the Add/Hide/Hidden button row, a
// status line, and the scrollable list) and wires the reload hooks. Call once.
func (v *assetsView) build(header, hint string) fyne.CanvasObject {
	v.status = widget.NewLabel("")
	v.status.Wrapping = fyne.TextWrapWord

	v.list = widget.NewList(
		func() int { return len(v.items) },
		func() fyne.CanvasObject { return monoLabel("template") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(assetRow(v.items[i]))
		},
	)
	v.list.OnSelected = func(id widget.ListItemID) { v.selected = id; v.updateHideButton() }
	v.list.OnUnselected = func(widget.ListItemID) { v.selected = -1; v.updateHideButton() }

	addTokenBtn := widget.NewButton("Add token…", v.showAddToken)
	v.hideBtn = widget.NewButton("Hide (spam)", v.hideSelected)
	v.hideBtn.Disable()
	hiddenBtn := widget.NewButton("Hidden…", v.showHidden)

	// Reload when balances are refreshed from anywhere (any assets view or Send).
	v.app.registerAssetsReloader(v.reload)
	// Auto-refresh on each new head (fires only while connected), gated to the visible
	// view and throttled so we don't reload every block on every pane.
	v.app.rpc.OnNewHead(func(*types.Header) {
		fyne.Do(v.onHead)
	})

	var topObjs []fyne.CanvasObject
	if header != "" {
		topObjs = append(topObjs, widget.NewLabelWithStyle(header, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	}
	if hint != "" {
		h := widget.NewLabel(hint)
		h.Wrapping = fyne.TextWrapWord
		topObjs = append(topObjs, h)
	}
	topObjs = append(topObjs,
		indentToText(container.NewHBox(addTokenBtn, v.hideBtn, hiddenBtn)),
		v.status, widget.NewSeparator())
	top := container.NewVBox(topObjs...)

	v.reload()
	return container.NewBorder(top, nil, nil, nil, v.list)
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

// assetKey is a stable identity for an asset (native, or a token by contract), used
// to preserve the selection across reloads even when the sort order shifts.
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

// reload refreshes balances for the current target account/connection. It is a
// no-op while a previous load is in flight.
func (v *assetsView) reload() {
	addr, label, ok := v.target()
	if !ok {
		v.lastAddr = common.Address{}
		v.setEmpty(v.emptyMsg)
		return
	}
	conn, ok := v.app.rpc.Active()
	if !ok {
		v.setEmpty("Connect an RPC endpoint in Settings to load balances.")
		return
	}

	// On an account switch, clear the previous wallet's balances immediately so the
	// new account shows "Loading…" rather than lingering on stale numbers.
	if addr != v.lastAddr {
		v.lastAddr = addr
		v.items = nil
		v.selected = -1
		if v.list != nil {
			v.list.UnselectAll()
			v.list.Refresh()
		}
		v.updateHideButton()
	}

	if v.loading {
		return // a load is already in flight; its completion reconciles the state
	}
	v.loading = true
	// Only show the "Loading…" text on the first (empty) load; on steady-state
	// refreshes keep the current status so it doesn't flicker every block.
	if len(v.items) == 0 {
		v.status.SetText("Loading balances…")
	}
	client := conn.Client
	chainID := conn.ChainID.Uint64()
	tokens := v.app.knownTokens(chainID, addr)

	// Kick off / advance token discovery so held tokens populate automatically.
	v.app.disc.ensure(chainID, addr, client)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		svc := v.app.assetService(chainID, client)
		got, loadErr := svc.Load(ctx, addr, tokens)

		fyne.Do(func() {
			v.loading = false
			// If the account changed while this load ran, discard it and load the
			// now-current account.
			if addr != v.lastAddr {
				v.reload()
				return
			}
			if loadErr != nil {
				v.status.SetText("Could not load balances: " + loadErr.Error())
				return
			}
			// Preserve the selection by token identity, not index: the sort order
			// can shift, so the old index may point at a different asset.
			var selKey string
			if v.selected >= 0 && v.selected < len(v.items) {
				selKey = assetKey(v.items[v.selected])
			}

			got = v.app.displayAssets(chainID, addr, got)
			visible, hidden := visibleAssets(got)
			v.items = visible
			v.list.UnselectAll()
			v.selected = -1
			if selKey != "" {
				for i, a := range v.items {
					if assetKey(a) == selKey {
						v.selected = i
						v.list.Select(i)
						break
					}
				}
			}
			v.updateHideButton()
			v.list.Refresh()
			status := fmt.Sprintf("%d assets · %s · %s", len(visible), label, conn.ChainInfo.Name)
			if hidden > 0 {
				status += fmt.Sprintf(" · %d dust hidden", hidden)
			}
			v.status.SetText(status)
		})
	}()
}

// setEmpty clears the list and shows a status message.
func (v *assetsView) setEmpty(msg string) {
	v.items = nil
	v.selected = -1
	if v.list != nil {
		v.list.UnselectAll()
		v.list.Refresh()
	}
	v.updateHideButton()
	v.status.SetText(msg)
}

// updateHideButton enables Hide only when a (non-native) token row is selected.
func (v *assetsView) updateHideButton() {
	if v.hideBtn == nil {
		return
	}
	if v.selected >= 0 && v.selected < len(v.items) && v.items[v.selected].Kind == assets.Token {
		v.hideBtn.Enable()
	} else {
		v.hideBtn.Disable()
	}
}

// targetAddrChain resolves the current target address and the connected chain,
// reporting a user-facing error if either is unavailable.
func (v *assetsView) targetAddrChain() (addr common.Address, chainID uint64, client rpc.Client, ok bool) {
	a, _, has := v.target()
	if !has {
		dialog.ShowInformation("No account", "Select a wallet or Safe first.", v.app.window)
		return
	}
	conn, connected := v.app.rpc.Active()
	if !connected {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), v.app.window)
		return
	}
	return a, conn.ChainID.Uint64(), conn.Client, true
}

// hideSelected hides the selected token as spam (persisted) and reloads. The
// decision is reversible via the Hidden… manager, so no confirmation is needed.
func (v *assetsView) hideSelected() {
	if v.selected < 0 || v.selected >= len(v.items) {
		return
	}
	a := v.items[v.selected]
	if a.Kind != assets.Token {
		return
	}
	addr, chainID, _, ok := v.targetAddrChain()
	if !ok {
		return
	}
	v.app.disc.setHidden(chainID, addr, a.Contract, true)

	// Optimistic update: drop the row now so it disappears instantly instead of
	// waiting for a full balance reload; reload() reconciles counts/order in the
	// background (displayAssets already filters the hidden token out).
	v.items = append(v.items[:v.selected], v.items[v.selected+1:]...)
	v.selected = -1
	v.list.UnselectAll()
	v.updateHideButton()
	v.list.Refresh()

	v.reload()
}

// showHidden lists the tokens hidden for the current account, each with an Unhide
// action. Symbols are fetched best-effort so junk-named spam is still identifiable.
func (v *assetsView) showHidden() {
	addr, chainID, client, ok := v.targetAddrChain()
	if !ok {
		return
	}

	box := container.NewVBox()
	var rebuild func()
	rebuild = func() {
		box.RemoveAll()
		hid := v.app.disc.hiddenTokens(chainID, addr)
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
				v.app.disc.setHidden(chainID, addr, t, false)
				v.reload()
				rebuild()
			})
			box.Add(container.NewBorder(nil, nil, nil, unhideBtn, lbl))
		}
		box.Refresh()
	}
	rebuild()

	d := dialog.NewCustom("Hidden tokens", "Close", container.NewVScroll(box), v.app.window)
	d.Resize(fyne.NewSize(480, 420))
	d.Show()
}

// showAddToken prompts for an ERC-20 contract address and, if connected, records it
// for the active chain and reloads.
func (v *assetsView) showAddToken() {
	conn, ok := v.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), v.app.window)
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
				dialog.ShowError(err, v.app.window)
				return
			}
			ref := assets.TokenRef{ChainID: conn.ChainID.Uint64(), Address: addr.Hex()}
			if !v.app.cfg.AddToken(ref) {
				dialog.ShowInformation("Already added", "That token is already in your list.", v.app.window)
				return
			}
			if err := v.app.cfg.Save(); err != nil {
				dialog.ShowError(err, v.app.window)
			}
			v.reload()
		}, v.app.window)
	d.Resize(fyne.NewSize(520, 200))
	d.Show()
}
