package ui

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"

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

	safeSelect  *widget.Select
	content     *fyne.Container // inner VBox holding details/proposals/status
	detailsBox  *fyne.Container
	proposalBox *fyne.Container
	status      *widget.Label

	proposals []safe.Proposal
}

func newSafePane(a *App) *safePane {
	return &safePane{app: a}
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
	help := widget.NewLabel("Import an existing Safe by address. Propose a transfer or an owner/threshold change, then collect owner signatures — unlock each owner in the Wallets tab and click Sign — until the threshold is met, then execute.")
	help.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(
		header,
		help,
		container.NewBorder(nil, nil, nil, container.NewHBox(importBtn, removeBtn, refreshBtn), p.safeSelect),
		widget.NewSeparator(),
	)

	p.content = container.NewVBox(p.detailsBox, widget.NewSeparator(), p.proposalBox, p.status)
	body := container.NewVScroll(p.content)
	p.refreshSafeSelect()
	return container.NewBorder(top, nil, nil, nil, body)
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
		p.relayout()
		return
	}
	if p.app.cfg.ActiveSafe != desc.ID {
		p.app.cfg.ActiveSafe = desc.ID
		_ = p.app.cfg.Save()
	}
	p.renderDetails(desc)
	p.refreshProposals(desc)
	p.refreshLiveInfo(desc)
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
	for _, r := range rows {
		grid.Add(widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(r[1]))
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
	dialog.ShowForm("Label owner "+ownerAddr, "Save", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Label", entry)},
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
	safeAddr, err := address.Parse(desc.Address)
	if err != nil {
		return
	}
	p.status.SetText("Refreshing Safe from chain…")
	client := conn.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, ierr := safe.ReadInfo(ctx, client, safeAddr)
		fyne.Do(func() {
			if ierr != nil {
				p.status.SetText("Could not read Safe: " + ierr.Error())
				return
			}
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
			dialog.ShowInformation("Safe imported",
				fmt.Sprintf("%s\n%s\n%d of %d owners", desc.Label, desc.Address, desc.Threshold, len(desc.Owners)), p.app.window)
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
	head := widget.NewLabelWithStyle("Proposals", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	rows := container.NewVBox()
	if len(p.proposals) == 0 {
		rows.Add(widget.NewLabel("No proposals yet."))
	}
	for i := range p.proposals {
		rows.Add(p.proposalRow(desc, p.proposals[i]))
	}
	p.proposalBox.Objects = []fyne.CanvasObject{head, rows}
	p.proposalBox.Refresh()
	p.relayout()
}

// relayout re-runs the parent VBox's layout so that resized detail/proposal
// sections reposition instead of overlapping. Fyne re-lays-out a container when
// it (not just a child) is refreshed, so mutating a child's Objects needs an
// explicit parent refresh.
func (p *safePane) relayout() {
	if p.content != nil {
		p.content.Refresh()
	}
}

func (p *safePane) proposalRow(desc safe.Descriptor, prop safe.Proposal) fyne.CanvasObject {
	summary := fmt.Sprintf("[%s] %s — %d/%d sigs · %s",
		prop.Status, prop.Description, len(prop.Signatures), desc.Threshold, prop.Kind)
	open := widget.NewButton("Open", func() { p.showProposalReview(desc, prop) })
	return container.NewBorder(nil, nil, nil, open, widget.NewLabel(summary))
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

	// Load the Safe's balances to populate the asset picker.
	var items []assets.Asset
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		got, lerr := assets.NewService(conn.Client, conn.ChainID.Uint64()).Load(ctx, safeAddr, p.app.cfg.TokensForChain(conn.ChainID.Uint64()))
		fyne.Do(func() {
			if lerr != nil {
				assetSel.PlaceHolder = "Could not load assets"
				assetSel.Refresh()
				return
			}
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
		{"Status", string(prop.Status)},
		{"Signatures", fmt.Sprintf("%d of %d", len(prop.Signatures), desc.Threshold)},
		{"Safe nonce", fmt.Sprintf("%d", prop.SafeNonce)},
		{"safeTxHash", prop.SafeTxHash.Hex()},
		{"To", address.Format(prop.To)},
	}
	if prop.Value != nil && prop.Value.Sign() > 0 {
		rows = append(rows, [2]string{"Value", assets.FormatUnits(prop.Value, 18)})
	}
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		grid.Add(widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(r[1]))
	}

	signers := container.NewVBox()
	for _, s := range prop.Signatures {
		signers.Add(monoLabel("✓ " + address.Short(s.Signer)))
	}
	signMsg := widget.NewLabel(p.signGuidance(desc, prop))
	signMsg.Wrapping = fyne.TextWrapWord

	return []fyne.CanvasObject{grid, widget.NewSeparator(), signers, signMsg, p.reviewButtons(desc, prop, render)}
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
		p.recordExecHistory(prop, info, from, hash.Hex())
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
		go p.trackExecInclusion(desc, prop, hash, info)
	}()
}

// trackExecInclusion waits for the execution receipt and reflects the outcome.
func (p *safePane) trackExecInclusion(desc safe.Descriptor, prop safe.Proposal, hash common.Hash, info chain.Info) {
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
	if p.app.safeProposals != nil && !success {
		_ = p.app.safeProposals.SetStatus(prop.ID, safe.StatusFailed, hash.Hex(), "execution reverted")
	}
	fyne.Do(func() {
		outcome := "succeeded"
		if !success {
			outcome = "reverted"
		}
		p.status.SetText(fmt.Sprintf("Execution %s in block %d", outcome, receipt.BlockNumber.Int64()))
		p.refreshProposals(desc)
		p.notifyHistory()
	})
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

func (p *safePane) recordExecHistory(prop safe.Proposal, info chain.Info, from common.Address, hash string) {
	if p.app.history == nil {
		return
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
	if id, err := p.app.history.Insert(rec); err == nil {
		_ = p.app.history.MarkSubmitted(id, hash)
	}
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
