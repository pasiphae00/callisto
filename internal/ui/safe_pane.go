package ui

import (
	"context"
	"fmt"
	"image/color"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/history"
	"codeberg.org/pasiphae/callisto/internal/safe"
	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/tx"
)

// safePane is the dedicated Safe multisig workspace: import a Safe, view its
// owners/threshold/nonce/balances, propose transfers and owner/threshold changes,
// collect owner signatures (by switching unlocked wallets in the Wallets tab), and
// execute or reject once the threshold is met. It layers entirely on top of the
// existing signer sessions and the basic tx pipeline.
type safePane struct {
	app *App

	safeSelect   *widget.Select
	tabs         *container.AppTabs // Overview | Proposals | Assets | Build
	proposalsTab *container.TabItem // kept to update its label with the active count
	detailsBox   *fyne.Container    // Overview tab body
	proposalBox  *fyne.Container    // Proposals tab body
	status       *widget.Label
	assetsView   *assetsView     // Assets tab: balances for the selected Safe
	buildView    *safeBuildView  // Build tab: curated ecosystem actions as proposals

	proposals []safe.Proposal

	// liveLoaded is set once on-chain Safe info has been read for the current
	// selection; liveLoading guards against overlapping reads. Together they let a
	// new-head handler load live info as soon as the RPC connects (auto-connect
	// finishes after the pane is first built), instead of being stuck on cached.
	liveLoaded  bool
	liveLoading bool
}

func newSafePane(a *App) *safePane {
	p := &safePane{app: a}
	// The Assets sub-tab reuses the shared assetsView, keyed on the selected Safe's
	// address — so discovery, persistence, hide/sort all work exactly as for EOAs.
	p.assetsView = newAssetsView(a, "Select a Safe to view its balances.",
		func() (common.Address, string, bool) {
			desc, ok := p.selectedSafe()
			if !ok {
				return common.Address{}, "", false
			}
			addr, err := address.Parse(desc.Address)
			if err != nil {
				return common.Address{}, "", false
			}
			label := desc.Label
			if label == "" {
				label = "Safe"
			}
			return addr, label, true
		})
	// The Safe balances refresh on new heads only while the Safe pane is shown and its
	// Assets sub-tab is the active one — not on every block behind other tabs/panes.
	p.assetsView.headVisible = func() bool {
		return p.app.navShown("Safe") && p.tabs != nil && p.tabs.Selected() != nil && p.tabs.Selected().Text == "Assets"
	}
	p.buildView = newSafeBuildView(p)
	return p
}

func (p *safePane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	p.safeSelect = widget.NewSelect(nil, func(string) { p.onSafeSelected() })
	p.safeSelect.PlaceHolder = "Select a Safe"

	importBtn := widget.NewButton("Import Safe…", p.showImportSafe)
	removeBtn := widget.NewButton("Remove", p.removeSelectedSafe)
	refreshBtn := widget.NewButton("Refresh", func() { p.onSafeSelected() })

	p.detailsBox = container.NewVBox()
	p.proposalBox = container.NewVBox()

	header := widget.NewLabelWithStyle("Safe multisig", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Import an existing Safe by address to use Callisto to prepare and execute Safe transactions. You can Propose a transaction or an owner/threshold change, then collect owner signatures until the threshold is met.\n\nTo sign a Proposal, unlock and switch to owner accounts in the Wallets tab and click Sign.")
	help.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(
		header,
		help,
		container.NewBorder(nil, nil, nil, container.NewHBox(importBtn, removeBtn, refreshBtn), p.safeSelect),
		widget.NewSeparator(),
	)

	// Overview | Proposals | Assets sub-tabs. Proposals is second and named for the
	// primary action (propose / sign / execute), and its label carries a live count
	// of active proposals so it's obvious where to go. Overview holds the
	// details/owners/actions; Assets the (shared) balances view.
	overview := container.NewVScroll(p.detailsBox)
	proposals := container.NewVScroll(p.proposalBox)
	assetsTab := p.assetsView.build("",
		"Tokens held by this Safe, detected automatically on each block. Hide spam to keep the list clean.")
	p.proposalsTab = container.NewTabItem("Proposals", proposals)
	p.tabs = container.NewAppTabs(
		container.NewTabItem("Overview", overview),
		p.proposalsTab,
		container.NewTabItem("Assets", assetsTab),
		container.NewTabItem("Build", p.buildView.build()),
	)
	p.tabs.OnSelected = func(ti *container.TabItem) {
		switch ti.Text {
		case "Assets":
			p.assetsView.reload() // refresh balances when the tab is shown
		case "Build":
			p.buildView.reloadActions() // refresh the chain-scoped action list
		}
	}

	// Auto-connect finishes asynchronously, often after this pane is first built,
	// so refreshLiveInfo may have run while still disconnected and fallen back to
	// cached details. Once a connection is up (new heads arrive), load live info
	// for the selected Safe — guarded by liveLoaded so this runs once per
	// selection, not on every block.
	p.app.rpc.OnNewHead(func(*types.Header) {
		fyne.Do(func() {
			if p.liveLoaded || p.liveLoading {
				return
			}
			if desc, ok := p.selectedSafe(); ok {
				p.refreshLiveInfo(desc)
			}
		})
	})

	p.refreshSafeSelect()
	return container.NewBorder(top, p.status, nil, nil, p.tabs)
}

// refreshSafeSelect repopulates the Safe dropdown from config and restores the
// active selection.
func (p *safePane) refreshSafeSelect() {
	opts := make([]string, len(p.app.cfg.Safes))
	activeIdx := -1
	for i, s := range p.app.cfg.Safes {
		opts[i] = p.safeOption(s)
		if s.ID == p.app.cfg.ActiveSafe {
			activeIdx = i
		}
	}
	p.safeSelect.Options = opts
	p.safeSelect.Refresh()
	if activeIdx >= 0 {
		p.safeSelect.SetSelectedIndex(activeIdx)
	} else {
		p.onSafeSelected()
	}
}

func (p *safePane) safeOption(s safe.Descriptor) string {
	label := s.Label
	if label == "" {
		label = "(unnamed)"
	}
	short := s.Address
	if a, err := address.Parse(s.Address); err == nil {
		short = address.Short(a)
	}
	return fmt.Sprintf("%s — %s", label, short)
}

// selectedSafe returns the descriptor for the current dropdown selection.
func (p *safePane) selectedSafe() (safe.Descriptor, bool) {
	i := p.safeSelect.SelectedIndex()
	if i < 0 || i >= len(p.app.cfg.Safes) {
		return safe.Descriptor{}, false
	}
	return p.app.cfg.Safes[i], true
}

// onSafeSelected persists the selection, renders cached details, then refreshes
// live info and proposals.
func (p *safePane) onSafeSelected() {
	desc, ok := p.selectedSafe()
	if !ok {
		p.detailsBox.Objects = nil
		p.detailsBox.Refresh()
		p.proposalBox.Objects = nil
		p.proposalBox.Refresh()
		p.status.SetText("No Safe selected. Import one to begin.")
		p.assetsView.reload()
		p.relayout()
		return
	}
	if p.app.cfg.ActiveSafe != desc.ID {
		p.app.cfg.ActiveSafe = desc.ID
		_ = p.app.cfg.Save()
	}
	p.liveLoaded = false // new selection: live info not yet read
	p.renderDetails(desc)
	p.refreshProposals(desc)
	p.refreshLiveInfo(desc)
	p.assetsView.reload() // load the selected Safe's balances into the Assets tab
}

// renderDetails draws the Safe details from the (possibly cached) descriptor.
func (p *safePane) renderDetails(desc safe.Descriptor) {
	rows := [][2]string{
		{"Address", desc.Address},
		{"Chain", fmt.Sprintf("%d", desc.ChainID)},
		{"Threshold", fmt.Sprintf("%d of %d owners", desc.Threshold, len(desc.Owners))},
	}
	if desc.Version != "" {
		rows = append(rows, [2]string{"Version", desc.Version})
	}
	grid := container.New(layout.NewFormLayout())
	addRow := func(k string, v fyne.CanvasObject) {
		grid.Add(widget.NewLabelWithStyle(k, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(v)
	}
	// Address gets a Copy button (parity with the wallet detail view).
	copyBtn := widget.NewButton("Copy", func() { p.app.fyneApp.Clipboard().SetContent(desc.Address) })
	copyBtn.Importance = widget.LowImportance
	addRow(rows[0][0], container.NewBorder(nil, nil, nil, copyBtn, monoLabel(rows[0][1])))
	for _, r := range rows[1:] {
		addRow(r[0], monoLabel(r[1]))
	}

	ownersHead := widget.NewLabelWithStyle("Owners", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	ownersBox := container.NewVBox()
	for _, o := range desc.Owners {
		ownersBox.Add(p.ownerRow(desc, o))
	}

	actions := container.NewHBox(
		widget.NewButton("New transfer…", func() { p.showNewTransfer(desc) }),
		widget.NewButton("Owners & threshold…", func() { p.showOwnerActions(desc) }),
	)

	p.detailsBox.Objects = []fyne.CanvasObject{grid, ownersHead, ownersBox, actions}
	p.detailsBox.Refresh()
	p.relayout()
}

// ownerRow renders one owner with its (editable) client-side label.
func (p *safePane) ownerRow(desc safe.Descriptor, o safe.OwnerLabel) fyne.CanvasObject {
	name := o.Label
	if name == "" {
		name = "(unlabeled)"
	}
	short := o.Address
	if a, err := address.Parse(o.Address); err == nil {
		short = address.Short(a)
	}
	label := monoLabel(fmt.Sprintf("%s  %s", short, name))
	edit := widget.NewButton("Label", func() { p.editOwnerLabel(desc, o.Address) })
	return container.NewBorder(nil, nil, nil, edit, label)
}

// editOwnerLabel updates the client-side label for an owner and persists it.
func (p *safePane) editOwnerLabel(desc safe.Descriptor, ownerAddr string) {
	entry := widget.NewEntry()
	entry.SetText(desc.OwnerLabelFor(ownerAddr))
	// Show the address as a monospace field in the body (a dialog title can't be
	// monospaced) so it renders in the fixed-width font like every other address.
	dialog.ShowForm("Label owner", "Save", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Owner", monoLabel(ownerAddr)),
			widget.NewFormItem("Label", entry),
		},
		func(ok bool) {
			if !ok {
				return
			}
			cur, found := p.app.cfg.SafeByID(desc.ID)
			if !found {
				return
			}
			for i := range cur.Owners {
				if cur.Owners[i].Address == ownerAddr {
					cur.Owners[i].Label = entry.Text
				}
			}
			_ = p.app.cfg.UpsertSafe(cur)
			_ = p.app.cfg.Save()
			p.renderDetails(cur)
		}, p.app.window)
}

// refreshLiveInfo reads the Safe's current owners/threshold/nonce from chain and
// updates the cached descriptor + details. Best-effort: no connection just leaves
// the cached view in place.
func (p *safePane) refreshLiveInfo(desc safe.Descriptor) {
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.status.SetText("Showing cached Safe details. Connect an RPC in Settings for live owners, threshold, and nonce.")
		return
	}
	if p.liveLoading {
		return // a read is already in flight
	}
	safeAddr, err := address.Parse(desc.Address)
	if err != nil {
		return
	}
	p.liveLoading = true
	p.status.SetText("Refreshing Safe from chain…")
	client := conn.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, ierr := safe.ReadInfo(ctx, client, safeAddr)
		fyne.Do(func() {
			p.liveLoading = false
			if ierr != nil {
				p.status.SetText("Could not read Safe: " + ierr.Error())
				return
			}
			p.liveLoaded = true
			updated := mergeInfo(desc, info, conn.ChainID.Uint64())
			_ = p.app.cfg.UpsertSafe(updated)
			_ = p.app.cfg.Save()
			p.renderDetails(updated)
			p.status.SetText(fmt.Sprintf("Ready · %s · nonce %d", conn.ChainInfo.Name, info.Nonce))
		})
	}()
}

// mergeInfo folds fresh on-chain info into a descriptor, preserving client-side
// owner labels for owners that are still present.
func mergeInfo(desc safe.Descriptor, info safe.Info, chainID uint64) safe.Descriptor {
	desc.ChainID = chainID
	desc.Threshold = info.Threshold
	desc.Version = info.Version
	owners := make([]safe.OwnerLabel, 0, len(info.Owners))
	for _, o := range info.Owners {
		hex := address.Format(o)
		owners = append(owners, safe.OwnerLabel{Address: hex, Label: desc.OwnerLabelFor(hex)})
	}
	desc.Owners = owners
	return desc
}

// --- import / remove --------------------------------------------------------

func (p *safePane) showImportSafe() {
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint in Settings first — importing reads the Safe's owners on-chain"), p.app.window)
		return
	}
	label := widget.NewEntry()
	label.SetPlaceHolder("e.g. Treasury")
	addr := newAddressField(p.app.currentResolver, nil)

	d := dialog.NewForm("Import Safe", "Import", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Label", label),
			widget.NewFormItem("Safe address", addr.container()),
		},
		func(okBtn bool) {
			if !okBtn {
				return
			}
			safeAddr, valid := addr.Address()
			if !valid {
				dialog.ShowError(fmt.Errorf("enter a valid Safe address or ENS name"), p.app.window)
				return
			}
			p.doImport(conn.ChainID.Uint64(), safeAddr, label.Text)
		}, p.app.window)
	d.Resize(fyne.NewSize(560, 260))
	d.Show()
}

func (p *safePane) doImport(chainID uint64, safeAddr common.Address, label string) {
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connection lost; reconnect and try again"), p.app.window)
		return
	}
	progress := dialog.NewCustomWithoutButtons("Reading Safe…",
		widget.NewLabel("Reading owners and threshold from chain."), p.app.window)
	progress.Show()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, err := safe.ReadInfo(ctx, conn.Client, safeAddr)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(fmt.Errorf("could not read Safe at %s: %w", safeAddr.Hex(), err), p.app.window)
				return
			}
			desc := mergeInfo(safe.Descriptor{
				ID:      newWalletID(),
				Label:   label,
				Address: address.Format(safeAddr),
			}, info, chainID)
			if err := p.app.cfg.UpsertSafe(desc); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			p.app.cfg.ActiveSafe = desc.ID
			if err := p.app.cfg.Save(); err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			p.refreshSafeSelect()
			showAddressInfo(p.app.window, "Safe imported", desc.Label, desc.Address,
				fmt.Sprintf("%d of %d owners", desc.Threshold, len(desc.Owners)))
		})
	}()
}

func (p *safePane) removeSelectedSafe() {
	desc, ok := p.selectedSafe()
	if !ok {
		return
	}
	dialog.ShowConfirm("Remove Safe",
		fmt.Sprintf("Forget %q?\nThis only removes it from Callisto's list; the on-chain Safe is untouched.", p.safeOption(desc)),
		func(okBtn bool) {
			if !okBtn {
				return
			}
			p.app.cfg.RemoveSafe(desc.ID)
			_ = p.app.cfg.Save()
			p.refreshSafeSelect()
		}, p.app.window)
}

// --- proposals list ---------------------------------------------------------

func (p *safePane) refreshProposals(desc safe.Descriptor) {
	p.proposals = nil
	if p.app.safeProposals != nil {
		safeAddr, err := address.Parse(desc.Address)
		if err == nil {
			if list, lerr := p.app.safeProposals.ListBySafe(safeAddr, desc.ChainID); lerr == nil {
				p.proposals = list
			}
		}
	}
	// Split live proposals (still actionable) from terminal ones (history), and
	// detect same-nonce conflicts among the active ones — only one proposal per
	// nonce can ultimately execute, so sharing a nonce is worth flagging.
	var active, hist []safe.Proposal
	nonceCount := map[uint64]int{}
	for _, pr := range p.proposals {
		switch pr.Status {
		case safe.StatusCollecting, safe.StatusReady:
			active = append(active, pr)
			nonceCount[pr.SafeNonce]++
		default:
			hist = append(hist, pr)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].SafeNonce != active[j].SafeNonce {
			return active[i].SafeNonce < active[j].SafeNonce
		}
		return active[i].CreatedAt < active[j].CreatedAt
	})
	sort.SliceStable(hist, func(i, j int) bool { return hist[i].CreatedAt > hist[j].CreatedAt })

	sectionHead := func(s string) fyne.CanvasObject {
		return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	importBtn := indentToText(container.NewHBox(
		widget.NewButton("Import proposal / signatures…", func() { p.showImportProposal(desc) })))
	box := container.NewVBox(importBtn, widget.NewSeparator())
	if len(p.proposals) == 0 {
		box.Add(widget.NewLabel("No proposals yet."))
	} else {
		box.Add(sectionHead(fmt.Sprintf("Active (%d)", len(active))))
		if len(active) == 0 {
			box.Add(widget.NewLabel("No active proposals."))
		}
		for _, pr := range active {
			box.Add(p.proposalRow(desc, pr, nonceCount[pr.SafeNonce] > 1))
		}
		if len(hist) > 0 {
			box.Add(widget.NewSeparator())
			box.Add(sectionHead(fmt.Sprintf("History (%d)", len(hist))))
			for _, pr := range hist {
				box.Add(p.proposalRow(desc, pr, false))
			}
		}
	}
	p.proposalBox.Objects = []fyne.CanvasObject{box}
	p.proposalBox.Refresh()

	// Surface the active count on the tab label so it's obvious where to act.
	if p.proposalsTab != nil {
		label := "Proposals"
		if len(active) > 0 {
			label = fmt.Sprintf("Proposals (%d)", len(active))
		}
		p.proposalsTab.Text = label
	}
	p.app.updateSafeBadge() // keep the sidebar "Safe (n)" count in sync
	p.relayout()
}

// proposalStatusColor maps a proposal status to a status dot color: amber while
// collecting, green when ready to execute, gray once executed (done), red for a
// rejected/failed one.
func proposalStatusColor(s safe.ProposalStatus) color.Color {
	switch s {
	case safe.StatusCollecting:
		return statusAmber
	case safe.StatusReady:
		return statusGreen
	case safe.StatusRejected, safe.StatusFailed:
		return statusRed
	default: // executed
		return statusGray
	}
}

// proposalStatusWord is a capitalized label for a proposal status.
func proposalStatusWord(s safe.ProposalStatus) string {
	switch s {
	case safe.StatusCollecting:
		return "Collecting"
	case safe.StatusReady:
		return "Ready"
	case safe.StatusExecuted:
		return "Executed"
	case safe.StatusRejected:
		return "Rejected"
	case safe.StatusFailed:
		return "Failed"
	default:
		return string(s)
	}
}

// relayout re-runs the parent VBox's layout so that resized detail/proposal
// sections reposition instead of overlapping. Fyne re-lays-out a container when
// it (not just a child) is refreshed, so mutating a child's Objects needs an
// explicit parent refresh.
func (p *safePane) relayout() {
	if p.tabs != nil {
		p.tabs.Refresh()
	}
}

// proposalRow renders one proposal: a status-colored dot, a summary line (status,
// nonce, description, signature progress, kind — with a same-nonce conflict flag),
// and an Open button into the review dialog.
func (p *safePane) proposalRow(desc safe.Descriptor, prop safe.Proposal, conflict bool) fyne.CanvasObject {
	dot := canvas.NewText("●", proposalStatusColor(prop.Status))
	text := fmt.Sprintf("%s · nonce %d · %s · %d/%d sigs · %s",
		proposalStatusWord(prop.Status), prop.SafeNonce, prop.Description,
		len(prop.Signatures), desc.Threshold, prop.Kind)
	if conflict {
		text += "  · ⚠ same-nonce conflict"
	}
	lbl := widget.NewLabel(text)
	lbl.Wrapping = fyne.TextWrapWord
	open := widget.NewButton("Open", func() { p.showProposalReview(desc, prop) })
	return container.NewBorder(nil, nil, dot, open, lbl)
}

// --- create proposals -------------------------------------------------------

// showNewTransfer proposes an ETH/ERC-20 transfer from the Safe.
func (p *safePane) showNewTransfer(desc safe.Descriptor) {
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	safeAddr, err := address.Parse(desc.Address)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}

	assetSel := widget.NewSelect(nil, nil)
	assetSel.PlaceHolder = "Loading assets…"
	recipient := newAddressField(p.app.currentResolver, nil)
	amount := widget.NewEntry()
	amount.SetPlaceHolder("0.0")

	// Load the Safe's balances to populate the asset picker, using the same
	// auto-discovered + hidden-filtered token set as the Assets sub-tab (so tokens
	// the Safe holds beyond the curated list appear here too).
	chainID := conn.ChainID.Uint64()
	tokens := p.app.knownTokens(chainID, safeAddr)
	p.app.disc.ensure(chainID, safeAddr, conn.Client)
	var items []assets.Asset
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		got, lerr := p.app.assetService(chainID, conn.Client).Load(ctx, safeAddr, tokens)
		fyne.Do(func() {
			if lerr != nil {
				assetSel.PlaceHolder = "Could not load assets"
				assetSel.Refresh()
				return
			}
			// Only offer assets the Safe actually holds — drop hidden + zero/dust
			// tokens (native always kept) — and show them in a stable order.
			got = p.app.displayAssets(chainID, safeAddr, got)
			got, _ = visibleAssets(got)
			items = got
			opts := make([]string, len(got))
			for i, a := range got {
				opts[i] = fmt.Sprintf("%s (%s)", a.Symbol, assets.FormatDisplay(a.Balance, a.Decimals, assets.DisplayDecimals))
			}
			assetSel.Options = opts
			assetSel.PlaceHolder = "Select an asset"
			assetSel.Refresh()
		})
	}()

	d := dialog.NewForm("New Safe transfer", "Create proposal", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Asset", assetSel),
			widget.NewFormItem("To", recipient.container()),
			widget.NewFormItem("Amount", amount),
		},
		func(okBtn bool) {
			if !okBtn {
				return
			}
			idx := assetSel.SelectedIndex()
			if idx < 0 || idx >= len(items) {
				dialog.ShowError(fmt.Errorf("select an asset"), p.app.window)
				return
			}
			asset := items[idx]
			to, valid := recipient.Address()
			if !valid {
				dialog.ShowError(fmt.Errorf("enter a valid recipient"), p.app.window)
				return
			}
			amt, perr := assets.ParseUnits(amount.Text, asset.Decimals)
			if perr != nil || amt.Sign() <= 0 {
				dialog.ShowError(fmt.Errorf("enter a positive amount"), p.app.window)
				return
			}
			p.buildTransferProposal(desc, safeAddr, asset, to, amt)
		}, p.app.window)
	d.Resize(fyne.NewSize(600, 360))
	d.Show()
}

// buildTransferProposal turns a transfer request into the inner call and creates
// the proposal (reading the Safe's current nonce for the hash).
func (p *safePane) buildTransferProposal(desc safe.Descriptor, safeAddr common.Address, asset assets.Asset, to common.Address, amount *big.Int) {
	var innerTo common.Address
	var value *big.Int
	var data []byte
	var summary string

	if asset.Kind == assets.Native {
		innerTo, value, data = to, amount, nil
		summary = fmt.Sprintf("Send %s %s to %s", assets.FormatUnits(amount, asset.Decimals), asset.Symbol, address.Short(to))
	} else {
		send, err := tx.BuildERC20Send(safeAddr, asset.Contract, to, amount, asset.Symbol, asset.Decimals)
		if err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		innerTo, value, data = send.Call.To, send.Call.Value, send.Call.Data
		summary = fmt.Sprintf("Transfer %s %s to %s", assets.FormatUnits(amount, asset.Decimals), asset.Symbol, address.Short(to))
	}
	p.createProposal(desc, safeAddr, innerTo, value, data, safe.KindTransfer, summary)
}

// showOwnerActions proposes an owner-management or threshold change (a Safe tx
// whose inner call targets the Safe itself).
func (p *safePane) showOwnerActions(desc safe.Descriptor) {
	safeAddr, err := address.Parse(desc.Address)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	actionSel := widget.NewSelect([]string{"Add owner", "Remove owner", "Replace owner", "Change threshold"}, nil)
	actionSel.SetSelected("Add owner")
	ownerField := newAddressField(p.app.currentResolver, nil)
	newOwnerField := newAddressField(p.app.currentResolver, nil)
	threshold := widget.NewEntry()
	threshold.SetText(strconv.FormatUint(desc.Threshold, 10))

	d := dialog.NewForm("Owners & threshold", "Create proposal", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Action", actionSel),
			widget.NewFormItem("Owner", ownerField.container()),
			widget.NewFormItem("New owner (Replace)", newOwnerField.container()),
			widget.NewFormItem("Threshold", threshold),
		},
		func(okBtn bool) {
			if !okBtn {
				return
			}
			p.buildOwnerProposal(desc, safeAddr, actionSel.Selected, ownerField, newOwnerField, threshold.Text)
		}, p.app.window)
	d.Resize(fyne.NewSize(620, 420))
	d.Show()
}

func (p *safePane) buildOwnerProposal(desc safe.Descriptor, safeAddr common.Address, action string, ownerField, newOwnerField *addressField, thresholdText string) {
	owners := ownerAddrs(desc)
	parseThreshold := func() (uint64, bool) {
		t, err := strconv.ParseUint(thresholdText, 10, 64)
		if err != nil || t == 0 || t > uint64(len(owners)) {
			dialog.ShowError(fmt.Errorf("threshold must be between 1 and the owner count"), p.app.window)
			return 0, false
		}
		return t, true
	}

	var data []byte
	var kind safe.ProposalKind
	var summary string
	var err error

	switch action {
	case "Add owner":
		owner, ok := ownerField.Address()
		if !ok {
			dialog.ShowError(fmt.Errorf("enter the owner to add"), p.app.window)
			return
		}
		t, ok := parseThresholdWith(thresholdText, len(owners)+1)
		if !ok {
			dialog.ShowError(fmt.Errorf("threshold must be between 1 and the new owner count (%d)", len(owners)+1), p.app.window)
			return
		}
		data, err = safe.EncodeAddOwner(owner, t)
		kind = safe.KindAddOwner
		summary = fmt.Sprintf("Add owner %s, threshold %d", address.Short(owner), t)
	case "Remove owner":
		owner, ok := ownerField.Address()
		if !ok {
			dialog.ShowError(fmt.Errorf("enter the owner to remove"), p.app.window)
			return
		}
		t, ok := parseThreshold()
		if !ok {
			return
		}
		prev, perr := safe.PrevOwner(owners, owner)
		if perr != nil {
			dialog.ShowError(perr, p.app.window)
			return
		}
		data, err = safe.EncodeRemoveOwner(prev, owner, t)
		kind = safe.KindRemoveOwner
		summary = fmt.Sprintf("Remove owner %s, threshold %d", address.Short(owner), t)
	case "Replace owner":
		owner, ok := ownerField.Address()
		newOwner, ok2 := newOwnerField.Address()
		if !ok || !ok2 {
			dialog.ShowError(fmt.Errorf("enter both the current owner and the new owner"), p.app.window)
			return
		}
		prev, perr := safe.PrevOwner(owners, owner)
		if perr != nil {
			dialog.ShowError(perr, p.app.window)
			return
		}
		data, err = safe.EncodeSwapOwner(prev, owner, newOwner)
		kind = safe.KindSwapOwner
		summary = fmt.Sprintf("Replace owner %s with %s", address.Short(owner), address.Short(newOwner))
	case "Change threshold":
		t, ok := parseThreshold()
		if !ok {
			return
		}
		data, err = safe.EncodeChangeThreshold(t)
		kind = safe.KindChangeThreshold
		summary = fmt.Sprintf("Change threshold to %d", t)
	default:
		return
	}
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	// Owner-management calls target the Safe itself with zero value.
	p.createProposal(desc, safeAddr, safeAddr, big.NewInt(0), data, kind, summary)
}

// createProposal reads the Safe's current nonce, computes the canonical
// safeTxHash on-chain, and persists a new proposal.
func (p *safePane) createProposal(desc safe.Descriptor, safeAddr, innerTo common.Address, value *big.Int, data []byte, kind safe.ProposalKind, summary string) {
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	client := conn.Client
	chainID := new(big.Int).Set(conn.ChainID)

	p.status.SetText("Preparing proposal…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		nonce, nerr := safe.Nonce(ctx, client, safeAddr)
		if nerr != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("read Safe nonce: %w", nerr), p.app.window) })
			return
		}
		stx := safe.NewSafeTx(innerTo, value, data, nonce)
		hash, herr := stx.OnChainHash(ctx, client, safeAddr)
		if herr != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("compute safeTxHash: %w", herr), p.app.window) })
			return
		}
		prop := safe.Proposal{
			SafeAddress: safeAddr,
			ChainID:     chainID.Uint64(),
			To:          innerTo,
			Value:       value,
			Data:        data,
			Operation:   safe.Call,
			SafeNonce:   nonce,
			SafeTxHash:  hash,
			Kind:        kind,
			Description: summary,
		}
		if p.app.safeProposals != nil {
			if _, ierr := p.app.safeProposals.Insert(prop); ierr != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("save proposal: %w", ierr), p.app.window) })
				return
			}
		}
		fyne.Do(func() {
			p.status.SetText("Proposal created: " + summary)
			p.refreshProposals(desc)
		})
	}()
}

// --- proposal review / sign / execute / reject ------------------------------

func (p *safePane) showProposalReview(desc safe.Descriptor, prop safe.Proposal) {
	content := container.NewVBox()
	var render func()
	render = func() {
		latest := p.latestProposal(prop.ID, prop)
		content.Objects = p.reviewObjects(desc, latest, render)
		content.Refresh()
	}
	render()
	d := dialog.NewCustom("Proposal", "Close", content, p.app.window)
	d.Resize(fyne.NewSize(680, 560))
	d.Show()
}

// reviewObjects builds the review dialog body for a proposal's current state.
// render rebuilds the body in place after any action (sign/execute/reject) or a
// manual refresh, so the buttons and signature list stay current without
// reopening the dialog.
func (p *safePane) reviewObjects(desc safe.Descriptor, prop safe.Proposal, render func()) []fyne.CanvasObject {
	rows := [][2]string{
		{"Action", prop.Description},
		{"Status", proposalStatusWord(prop.Status)},
		{"Signatures", fmt.Sprintf("%d of %d", len(prop.Signatures), desc.Threshold)},
		{"Safe nonce", fmt.Sprintf("%d", prop.SafeNonce)},
		{"safeTxHash", prop.SafeTxHash.Hex()},
		{"To", address.Format(prop.To)},
	}
	if prop.Value != nil && prop.Value.Sign() > 0 {
		rows = append(rows, [2]string{"Value", assets.FormatUnits(prop.Value, 18)})
	}
	if prop.CreatedAt > 0 {
		rows = append(rows, [2]string{"Created", time.Unix(prop.CreatedAt, 0).Local().Format("2006-01-02 15:04")})
	}
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		grid.Add(widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(r[1]))
	}

	// Signatures, showing the owner's label where one is set.
	signers := container.NewVBox()
	for _, s := range prop.Signatures {
		hex := address.Format(s.Signer)
		who := address.Short(s.Signer)
		if lbl := desc.OwnerLabelFor(hex); lbl != "" {
			who = lbl + " (" + address.Short(s.Signer) + ")"
		}
		signers.Add(monoLabel("✓ " + who))
	}

	objs := []fyne.CanvasObject{grid, widget.NewSeparator(), signers}

	// For an executed proposal, link the execution tx; for a failed one, show why.
	if prop.ExecutedTxHash != "" {
		info, _ := chain.Lookup(prop.ChainID)
		if url := info.TxURL(prop.ExecutedTxHash); url != "" {
			objs = append(objs, container.NewHBox(
				widget.NewLabelWithStyle("Executed tx", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				monoHyperlink(prop.ExecutedTxHash, url)))
		} else {
			objs = append(objs, monoLabel("Executed tx: "+prop.ExecutedTxHash))
		}
	}
	if prop.Error != "" {
		errLbl := widget.NewLabel("Error: " + prop.Error)
		errLbl.Wrapping = fyne.TextWrapWord
		objs = append(objs, errLbl)
	}

	signMsg := widget.NewLabel(p.signGuidance(desc, prop))
	signMsg.Wrapping = fyne.TextWrapWord
	objs = append(objs, signMsg, p.reviewButtons(desc, prop, render))
	return objs
}

// --- distributed signing: export / import proposals + signatures ------------

// ownerAddresses returns the Safe's current owner addresses.
func ownerAddresses(desc safe.Descriptor) []common.Address {
	out := make([]common.Address, 0, len(desc.Owners))
	for _, o := range desc.Owners {
		if a, err := address.Parse(o.Address); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// showExportProposal presents a shareable envelope (copy-paste text + save-to-file)
// for a proposal, so a co-owner on another machine can import, review, and sign it.
// The envelope carries the transaction details and any signatures collected so far —
// no keys or secrets.
func (p *safePane) showExportProposal(desc safe.Descriptor, prop safe.Proposal) {
	env := safe.ExportEnvelope(prop)
	text, err := env.EncodeText()
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}

	info := widget.NewLabel("Send this to a co-owner. They import it in Callisto, review, sign as an owner, then send a signature envelope back to you. It contains the transaction details and the signatures collected so far — no keys or secrets.")
	info.Wrapping = fyne.TextWrapWord
	blob := widget.NewMultiLineEntry()
	blob.SetText(text)
	blob.Wrapping = fyne.TextWrapBreak

	writeEnvelope := func(path string) {
		jsonBytes, jerr := env.EncodeJSON()
		if jerr != nil {
			dialog.ShowError(jerr, p.app.window)
			return
		}
		if werr := os.WriteFile(path, jsonBytes, 0o600); werr != nil {
			dialog.ShowError(werr, p.app.window)
			return
		}
		dialog.ShowInformation("Saved", "Proposal envelope written to:\n"+path, p.app.window)
	}
	defaultName := fmt.Sprintf("safe-proposal-nonce-%d.json", prop.SafeNonce)
	copyBtn := widget.NewButton("Copy", func() { p.app.fyneApp.Clipboard().SetContent(text) })
	// Native save panel on macOS; Fyne's dialog elsewhere (so file save works on Linux).
	saveBtn := widget.NewButton("Save to file…", func() {
		if nativeDialogsAvailable() {
			go func() {
				path, perr := nativeSavePath("Save proposal envelope", defaultName)
				fyne.Do(func() {
					if perr != nil || path == "" {
						return
					}
					writeEnvelope(path)
				})
			}()
			return
		}
		fs := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
			if err != nil || wc == nil {
				return
			}
			path := wc.URI().Path()
			_ = wc.Close()
			writeEnvelope(path)
		}, p.app.window)
		fs.SetFileName(defaultName)
		fs.Show()
	})
	actions := container.NewHBox(copyBtn, saveBtn)

	content := container.NewBorder(info, actions, nil, nil, container.NewVScroll(blob))
	d := dialog.NewCustom("Export proposal", "Close", content, p.app.window)
	d.Resize(fyne.NewSize(660, 480))
	d.Show()
}

// showImportProposal prompts for an envelope (paste or file) and imports it.
func (p *safePane) showImportProposal(desc safe.Descriptor) {
	entry := widget.NewMultiLineEntry()
	entry.SetPlaceHolder("Paste a proposal or signature envelope here…")
	entry.Wrapping = fyne.TextWrapBreak

	info := widget.NewLabel("Paste an envelope shared by a co-owner, or load it from a file. Callisto recomputes the safeTxHash from the details and verifies every signature against this Safe's current owners before accepting anything.")
	info.Wrapping = fyne.TextWrapWord
	// Native open panel on macOS; Fyne's dialog elsewhere (so file load works on Linux).
	loadBtn := widget.NewButton("Load from file…", func() {
		if nativeDialogsAvailable() {
			go func() {
				path, perr := nativeOpenPath("Open proposal envelope")
				if perr != nil || path == "" {
					return
				}
				data, rerr := os.ReadFile(path)
				fyne.Do(func() {
					if rerr != nil {
						dialog.ShowError(rerr, p.app.window)
						return
					}
					entry.SetText(string(data))
				})
			}()
			return
		}
		fo := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			path := rc.URI().Path()
			_ = rc.Close()
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				dialog.ShowError(rerr, p.app.window)
				return
			}
			entry.SetText(string(data))
		}, p.app.window)
		fo.Show()
	})
	top := container.NewVBox(info, loadBtn)

	content := container.NewBorder(top, nil, nil, nil, container.NewVScroll(entry))
	d := dialog.NewCustomConfirm("Import proposal / signatures", "Import", "Cancel", content,
		func(ok bool) {
			if ok {
				p.doImportEnvelope(desc, entry.Text)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(660, 480))
	d.Show()
}

// doImportEnvelope decodes an envelope, recomputes the safeTxHash from its fields,
// verifies every signature against the Safe's current owners, and merges the result
// into a matching local proposal (or creates one). Nothing from the envelope is
// trusted: the hash is re-derived and each signer re-recovered.
func (p *safePane) doImportEnvelope(desc safe.Descriptor, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	env, err := safe.DecodeEnvelope([]byte(raw))
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	if !addrEqual(env.SafeAddr(), desc.Address) {
		dialog.ShowError(fmt.Errorf("this envelope is for a different Safe (%s)", address.Short(env.SafeAddr())), p.app.window)
		return
	}
	if env.ChainID != desc.ChainID {
		dialog.ShowError(fmt.Errorf("this envelope is for chain %d, but the Safe is on chain %d", env.ChainID, desc.ChainID), p.app.window)
		return
	}
	tx, err := env.SafeTx()
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	safeAddr, err := address.Parse(desc.Address)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}

	// Authoritative safeTxHash: on-chain when connected (exactly what owners signed),
	// local EIP-712 otherwise.
	var hash common.Hash
	if conn, ok := p.app.rpc.Active(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if h, herr := tx.OnChainHash(ctx, conn.Client, safeAddr); herr == nil {
			hash = h
		}
		cancel()
	}
	if (hash == common.Hash{}) {
		hash = tx.LocalHash(new(big.Int).SetUint64(desc.ChainID), safeAddr)
	}

	valid, rejected := env.Verify(hash, ownerAddresses(desc))
	p.mergeImported(desc, env, hash, valid, rejected)
}

// mergeImported folds verified signatures into an existing local proposal with the
// same safeTxHash, or inserts a new one, then reports what was accepted.
func (p *safePane) mergeImported(desc safe.Descriptor, env safe.Envelope, hash common.Hash, valid []safe.Signature, rejected int) {
	if p.app.safeProposals == nil {
		dialog.ShowError(fmt.Errorf("proposal store unavailable"), p.app.window)
		return
	}

	var target int64
	existingSigners := map[common.Address]bool{}
	for _, pr := range p.proposals {
		if pr.SafeTxHash == hash {
			target = pr.ID
			for _, s := range pr.Signatures {
				existingSigners[s.Signer] = true
			}
			break
		}
	}
	newProposal := target == 0
	if newProposal {
		prop, perr := env.Proposal(hash, nil)
		if perr != nil {
			dialog.ShowError(perr, p.app.window)
			return
		}
		id, ierr := p.app.safeProposals.Insert(prop)
		if ierr != nil {
			dialog.ShowError(ierr, p.app.window)
			return
		}
		target = id
	}

	added := 0
	for _, s := range valid {
		if !existingSigners[s.Signer] {
			added++
		}
		_ = p.app.safeProposals.AddSignature(target, s.Signer, s.Sig)
	}
	p.maybeMarkReady(target, desc.Threshold)
	p.refreshProposals(desc)

	msg := fmt.Sprintf("Nonce %d · %s\n\n", env.SafeNonce, env.Description)
	if newProposal {
		msg += "Imported as a new proposal.\n"
	} else {
		msg += "Merged into the existing proposal.\n"
	}
	msg += fmt.Sprintf("%d owner signature(s) accepted", len(valid))
	if added != len(valid) {
		msg += fmt.Sprintf(" (%d new)", added)
	}
	msg += "."
	if rejected > 0 {
		msg += fmt.Sprintf("\n%d signature(s) rejected — not a current owner, duplicate, or malformed.", rejected)
	}
	dialog.ShowInformation("Import complete", msg, p.app.window)
}

// reviewButtons builds the action row for a proposal's current state:
//   - below threshold, with a matching owner unlocked → Sign; and, when that
//     signature would be the final one, also Sign & execute (bundle the last
//     signature with execution);
//   - at/over threshold → Execute (no more signing needed);
//   - plus Reject (non-rejection proposals) and a Refresh that re-reads state.
func (p *safePane) reviewButtons(desc safe.Descriptor, prop safe.Proposal, render func()) fyne.CanvasObject {
	sigs := len(prop.Signatures)
	threshold := int(desc.Threshold)
	met := sigs >= threshold
	terminal := prop.Status == safe.StatusExecuted || prop.Status == safe.StatusRejected

	canSign := false
	if s, _, ok := p.app.currentSigner(); ok {
		_, isHashSigner := s.(signer.SafeHashSigner)
		canSign = isHashSigner && isOwner(desc, s.Address()) && !prop.SignedBy(s.Address())
	}

	buttons := container.NewHBox()
	if !terminal {
		if !met && canSign {
			buttons.Add(widget.NewButton("Sign", func() { p.signProposal(desc, prop, render) }))
			if sigs+1 >= threshold {
				se := widget.NewButton("Sign & execute", func() { p.signAndExecute(desc, prop, render) })
				se.Importance = widget.HighImportance
				buttons.Add(se)
			}
		}
		if met {
			ex := widget.NewButton("Execute", func() { p.executeProposal(desc, prop, render) })
			ex.Importance = widget.HighImportance
			buttons.Add(ex)
		}
		if prop.Kind != safe.KindReject {
			buttons.Add(widget.NewButton("Reject…", func() { p.rejectProposal(desc, prop) }))
		}
	}
	buttons.Add(widget.NewButton("Export…", func() { p.showExportProposal(desc, prop) }))
	buttons.Add(widget.NewButton("Refresh", render))
	return buttons
}

// latestProposal re-reads a proposal by id so an open dialog reflects newly
// collected signatures or a status change, falling back to the snapshot when
// there is no store (tests) or on error.
func (p *safePane) latestProposal(id int64, fallback safe.Proposal) safe.Proposal {
	if p.app.safeProposals != nil {
		if fresh, err := p.app.safeProposals.Get(id); err == nil {
			return fresh
		}
	}
	return fallback
}

// signGuidance explains the next step based on the proposal state and the
// currently unlocked wallet.
func (p *safePane) signGuidance(desc safe.Descriptor, prop safe.Proposal) string {
	switch prop.Status {
	case safe.StatusExecuted:
		return "Executed."
	case safe.StatusRejected:
		return "Rejected (a same-nonce rejection was executed)."
	}
	met := len(prop.Signatures) >= int(desc.Threshold)
	s, _, ok := p.app.currentSigner()
	ownerUnlocked := ok && isOwner(desc, s.Address())
	if met {
		if ownerUnlocked {
			return "Threshold met — ready to execute with " + address.Short(s.Address()) + " (it pays the gas)."
		}
		return "Threshold met. Unlock any owner in the Wallets tab to execute (it pays the gas)."
	}
	if !ok {
		return "Unlock an owner wallet in the Wallets tab, then Sign. Switch wallets to collect more signatures."
	}
	if !isOwner(desc, s.Address()) {
		return "The unlocked wallet (" + address.Short(s.Address()) + ") is not an owner of this Safe."
	}
	if prop.SignedBy(s.Address()) {
		return "This owner has already signed. Unlock a different owner to add another signature."
	}
	if len(prop.Signatures)+1 >= int(desc.Threshold) {
		return "Ready to sign with " + address.Short(s.Address()) + ". This would be the final signature — use Sign & execute to finish in one step, or Sign to collect it without executing yet."
	}
	return "Ready to sign with " + address.Short(s.Address()) + "."
}

// signProposal collects a signature from the currently unlocked owner, then runs
// after (e.g. re-render the review dialog) on success.
func (p *safePane) signProposal(desc safe.Descriptor, prop safe.Proposal, after func()) {
	s, _, ok := p.app.currentSigner()
	if !ok {
		dialog.ShowError(fmt.Errorf("unlock an owner wallet in the Wallets tab first"), p.app.window)
		return
	}
	if !isOwner(desc, s.Address()) {
		dialog.ShowError(fmt.Errorf("%s is not an owner of this Safe", address.Short(s.Address())), p.app.window)
		return
	}
	hs, ok := s.(signer.SafeHashSigner)
	if !ok {
		dialog.ShowError(fmt.Errorf("this wallet type cannot produce Safe signatures yet"), p.app.window)
		return
	}
	if prop.SignedBy(s.Address()) {
		dialog.ShowError(fmt.Errorf("this owner has already signed; unlock a different owner"), p.app.window)
		return
	}

	p.status.SetText("Signing on " + address.Short(s.Address()) + "… (confirm on device if prompted)")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		sig, err := hs.SignSafeTxHash(ctx, prop.SafeTxHash)
		if err != nil {
			fyne.Do(func() {
				p.status.SetText("")
				dialog.ShowError(fmt.Errorf("signing failed: %w", err), p.app.window)
			})
			return
		}
		if p.app.safeProposals != nil {
			if aerr := p.app.safeProposals.AddSignature(prop.ID, s.Address(), sig); aerr != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("save signature: %w", aerr), p.app.window) })
				return
			}
			p.maybeMarkReady(prop.ID, desc.Threshold)
		}
		fyne.Do(func() {
			p.status.SetText("Signature collected from " + address.Short(s.Address()))
			p.refreshProposals(desc)
			if after != nil {
				after()
			}
		})
	}()
}

// signAndExecute collects the final signature and, on success, immediately
// executes — the one-step path when the current owner provides the signature that
// meets the threshold.
func (p *safePane) signAndExecute(desc safe.Descriptor, prop safe.Proposal, after func()) {
	p.signProposal(desc, prop, func() {
		p.executeProposal(desc, p.latestProposal(prop.ID, prop), after)
	})
}

// maybeMarkReady flips a proposal to "ready" once the threshold is met.
func (p *safePane) maybeMarkReady(proposalID int64, threshold uint64) {
	if p.app.safeProposals == nil {
		return
	}
	got, err := p.app.safeProposals.Get(proposalID)
	if err != nil {
		return
	}
	if got.Status == safe.StatusCollecting && len(got.Signatures) >= int(threshold) {
		_ = p.app.safeProposals.SetStatus(proposalID, safe.StatusReady, "", "")
	}
}

// executeProposal packs the collected signatures and executes the Safe tx as a
// normal EOA transaction from the currently unlocked owner, then runs after (e.g.
// re-render the review dialog) once submitted.
func (p *safePane) executeProposal(desc safe.Descriptor, prop safe.Proposal, after func()) {
	executor, _, ok := p.app.currentSigner()
	if !ok {
		dialog.ShowError(fmt.Errorf("unlock an owner wallet to execute (it pays the gas)"), p.app.window)
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	// Reload the proposal to pack the freshest signature set.
	if p.app.safeProposals != nil {
		if fresh, err := p.app.safeProposals.Get(prop.ID); err == nil {
			prop = fresh
		}
	}
	if len(prop.Signatures) < int(desc.Threshold) {
		dialog.ShowError(fmt.Errorf("need %d signatures, have %d", desc.Threshold, len(prop.Signatures)), p.app.window)
		return
	}
	packed, err := safe.PackSignatures(prop.SignatureMap())
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	safeAddr := prop.SafeAddress
	execData, err := safe.EncodeExec(prop.SafeTx(), packed)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}

	client := conn.Client
	info := conn.ChainInfo
	from := executor.Address()
	send := tx.Send{From: from, Call: tx.Call{To: safeAddr, Value: big.NewInt(0), Data: execData}}

	p.status.SetText("Estimating gas for execution…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prep, perr := tx.Prepare(ctx, client, new(big.Int).Set(conn.ChainID), send)
		if perr != nil {
			fyne.Do(func() {
				p.status.SetText("")
				dialog.ShowError(fmt.Errorf("prepare execution: %w", perr), p.app.window)
			})
			return
		}
		signed, serr := executor.SignTx(ctx, prep.Tx, prep.ChainID)
		if serr != nil {
			fyne.Do(func() {
				p.status.SetText("")
				dialog.ShowError(fmt.Errorf("sign execution: %w", serr), p.app.window)
			})
			return
		}
		hash, berr := tx.Broadcast(ctx, client, signed)
		if berr != nil {
			p.finishProposalError(prop.ID, berr.Error())
			return
		}
		recID := p.recordExecHistory(prop, info, from, hash.Hex())
		if p.app.safeProposals != nil {
			_ = p.app.safeProposals.SetStatus(prop.ID, safe.StatusExecuted, hash.Hex(), "")
			if prop.Kind == safe.KindReject {
				_ = p.app.safeProposals.MarkRejectedByNonce(safeAddr, prop.ChainID, prop.SafeNonce, prop.ID)
			}
		}
		fyne.Do(func() {
			p.status.SetText("Execution submitted: " + hash.Hex())
			p.refreshProposals(desc)
			p.showExecResult(hash.Hex(), info)
			p.notifyHistory()
			if after != nil {
				after()
			}
		})
		go p.trackExecInclusion(desc, prop, recID, hash, info)
	}()
}

// trackExecInclusion waits for the execution receipt and reflects the outcome: it
// marks the history record included/failed, updates the proposal on a revert, and
// pops a result dialog (matching the Send flow) so the user gets clear confirmation
// that the execution landed — the "Execution submitted" dialog itself is static.
func (p *safePane) trackExecInclusion(desc safe.Descriptor, prop safe.Proposal, recID int64, hash common.Hash, info chain.Info) {
	conn, ok := p.app.rpc.Active()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	receipt, err := tx.WaitForReceipt(ctx, conn.Client, hash)
	if err != nil {
		fyne.Do(func() { p.status.SetText("Could not confirm execution: " + err.Error()) })
		return
	}
	success := tx.Succeeded(receipt)
	blockNum := receipt.BlockNumber.Int64()

	var blockTime int64
	if head, herr := conn.Client.HeaderByNumber(ctx, receipt.BlockNumber); herr == nil && head != nil {
		blockTime = int64(head.Time)
	}
	if p.app.history != nil && recID != 0 {
		_ = p.app.history.MarkIncluded(recID, blockNum, blockTime, success)
	}
	if p.app.safeProposals != nil && !success {
		_ = p.app.safeProposals.SetStatus(prop.ID, safe.StatusFailed, hash.Hex(), "execution reverted")
	}

	fyne.Do(func() {
		outcome := "succeeded"
		if !success {
			outcome = "reverted"
		}
		p.status.SetText(fmt.Sprintf("Execution %s in block %d", outcome, blockNum))
		p.refreshProposals(desc)
		p.notifyHistory()
		p.showExecInclusionResult(hash.Hex(), blockNum, blockTime, success, info)
	})
}

// showExecInclusionResult reports the mined outcome of a Safe execution.
func (p *safePane) showExecInclusionResult(hash string, block, blockTime int64, success bool, info chain.Info) {
	title := "Safe execution included"
	status := "success ✓"
	if !success {
		title = "Safe execution reverted"
		status = "failed ✗"
	}
	rows := [][2]string{
		{"Execution:", status},
		{"Block:", fmt.Sprintf("%d", block)},
	}
	if blockTime > 0 {
		rows = append(rows, [2]string{"Time:", time.Unix(blockTime, 0).Local().Format(time.RFC1123)})
	}
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		grid.Add(widget.NewLabel(r[0]))
		grid.Add(monoLabel(r[1]))
	}
	body := container.NewVBox(grid)
	if link := info.TxURL(hash); link != "" {
		body.Add(widget.NewButton("View on explorer", func() { p.app.openURL(link) }))
	}
	dialog.ShowCustom(title, "Close", body, p.app.window)
}

// rejectProposal creates a rejection proposal at the same Safe nonce.
func (p *safePane) rejectProposal(desc safe.Descriptor, prop safe.Proposal) {
	dialog.ShowConfirm("Reject proposal",
		"Create a rejection at Safe nonce "+strconv.FormatUint(prop.SafeNonce, 10)+"?\nOnce the rejection reaches threshold and executes, it consumes the nonce and cancels this proposal.",
		func(okBtn bool) {
			if !okBtn {
				return
			}
			p.createRejection(desc, prop)
		}, p.app.window)
}

func (p *safePane) createRejection(desc safe.Descriptor, prop safe.Proposal) {
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	client := conn.Client
	safeAddr := prop.SafeAddress
	nonce := prop.SafeNonce
	summary := fmt.Sprintf("Reject nonce %d (cancels: %s)", nonce, prop.Description)

	p.status.SetText("Preparing rejection…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// A rejection is a 0-value, no-data self-call at the same nonce.
		stx := safe.NewSafeTx(safeAddr, big.NewInt(0), nil, nonce)
		hash, herr := stx.OnChainHash(ctx, client, safeAddr)
		if herr != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("compute rejection hash: %w", herr), p.app.window) })
			return
		}
		rej := safe.Proposal{
			SafeAddress: safeAddr, ChainID: prop.ChainID, To: safeAddr, Value: big.NewInt(0),
			Data: nil, Operation: safe.Call, SafeNonce: nonce, SafeTxHash: hash,
			Kind: safe.KindReject, Description: summary,
		}
		if p.app.safeProposals != nil {
			if _, ierr := p.app.safeProposals.Insert(rej); ierr != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("save rejection: %w", ierr), p.app.window) })
				return
			}
		}
		fyne.Do(func() {
			p.status.SetText("Rejection created — collect signatures and execute it to cancel the original.")
			p.refreshProposals(desc)
		})
	}()
}

// --- helpers ----------------------------------------------------------------

func (p *safePane) showExecResult(hash string, info chain.Info) {
	body := container.NewVBox(
		widget.NewLabel("Execution submitted. Waiting for inclusion…"),
		monoLabel(hash),
	)
	if link := info.TxURL(hash); link != "" {
		body.Add(widget.NewButton("View on explorer", func() { p.app.openURL(link) }))
	}
	dialog.ShowCustom("Safe execution", "Close", body, p.app.window)
}

// recordExecHistory inserts a history record for a Safe execution and returns its
// id (0 if history is unavailable) so trackExecInclusion can mark it included.
func (p *safePane) recordExecHistory(prop safe.Proposal, info chain.Info, from common.Address, hash string) int64 {
	if p.app.history == nil {
		return 0
	}
	rec := history.Record{
		ChainID:       info.ID,
		WalletAddress: address.Format(from),
		Kind:          "safe-exec",
		Instructions:  fmt.Sprintf("Safe %s: %s", address.Short(prop.SafeAddress), prop.Description),
		ToAddress:     address.Format(prop.SafeAddress),
		ValueWei:      "0",
		TxHash:        hash,
		Status:        history.StatusSubmitted,
	}
	id, err := p.app.history.Insert(rec)
	if err != nil {
		return 0
	}
	_ = p.app.history.MarkSubmitted(id, hash)
	return id
}

func (p *safePane) finishProposalError(proposalID int64, msg string) {
	if p.app.safeProposals != nil {
		_ = p.app.safeProposals.SetStatus(proposalID, safe.StatusFailed, "", msg)
	}
	fyne.Do(func() {
		p.status.SetText("Execution failed: " + msg)
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
	})
}

func (p *safePane) notifyHistory() {
	if p.app.historyReload != nil {
		p.app.historyReload()
	}
}

// ownerAddrs returns the descriptor's owner addresses as common.Address, in order.
func ownerAddrs(desc safe.Descriptor) []common.Address {
	out := make([]common.Address, 0, len(desc.Owners))
	for _, o := range desc.Owners {
		if a, err := address.Parse(o.Address); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// isOwner reports whether addr is an owner of the Safe (per the cached descriptor).
func isOwner(desc safe.Descriptor, addr common.Address) bool {
	for _, o := range ownerAddrs(desc) {
		if o == addr {
			return true
		}
	}
	return false
}

// parseThresholdWith validates a threshold against a specific owner count.
func parseThresholdWith(text string, ownerCount int) (uint64, bool) {
	t, err := strconv.ParseUint(text, 10, 64)
	if err != nil || t == 0 || t > uint64(ownerCount) {
		return 0, false
	}
	return t, true
}
