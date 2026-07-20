package ui

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/actions"
	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/history"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/safe"
	"codeberg.org/pasiphae/callisto/internal/tx"
)

// prepareTarget is an account an action can be prepared for: the active EOA (signed
// and sent) or an imported Safe (saved as a proposal).
type prepareTarget struct {
	label   string
	address common.Address
	isSafe  bool
	safe    safe.Descriptor
}

// preparePane builds "advanced" single-step transactions from the curated action
// registry (internal/actions): pick an action, fill its parameters, review the
// decoded call, and sign. The Claude-assisted natural-language front end plugs in
// later; this manual path works with no AI and proves the build/review/sign flow.
type preparePane struct {
	app *App

	fromSel    *widget.Select
	actionSel  *widget.Select
	fieldsBox  *fyne.Container
	entries    map[string]*widget.Entry
	prepareBtn *widget.Button
	status     *widget.Label

	acts    []actions.Action
	current *actions.Action
	targets []prepareTarget
}

func newPreparePane(a *App) *preparePane {
	return &preparePane{app: a, entries: map[string]*widget.Entry{}}
}

func (p *preparePane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	p.fromSel = widget.NewSelect(nil, nil)
	p.fromSel.PlaceHolder = "Select an account"
	p.actionSel = widget.NewSelect(nil, func(string) { p.onActionSelected() })
	p.actionSel.PlaceHolder = "Select an action"
	p.fieldsBox = container.NewVBox()
	p.prepareBtn = widget.NewButton("Prepare", p.prepare)
	p.prepareBtn.Importance = widget.HighImportance
	p.prepareBtn.Disable()

	header := widget.NewLabelWithStyle("Prepare", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Prepare a transaction from a known action (e.g. wrap ETH, stake with Lido). Callisto builds and decodes the call for you to review before signing. Natural-language requests (optional, AI-assisted) come later.")
	help.Wrapping = fyne.TextWrapWord

	// Reload the action list when the connection changes (actions are chain-scoped).
	p.app.rpc.OnNewHead(func(*types.Header) { fyne.Do(p.reloadActions) })

	top := container.NewVBox(
		header, help,
		container.New(layout.NewFormLayout(),
			widget.NewLabelWithStyle("From", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), p.fromSel,
			widget.NewLabelWithStyle("Action", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), p.actionSel),
		p.fieldsBox,
		indentToText(container.NewHBox(p.prepareBtn)),
		p.status,
	)
	p.reloadActions()
	return container.NewVScroll(top)
}

// reloadActions repopulates the action dropdown for the connected chain.
func (p *preparePane) reloadActions() {
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.acts = nil
		p.actionSel.Options = nil
		p.actionSel.Refresh()
		p.status.SetText("Connect an RPC endpoint in Settings to prepare transactions.")
		p.prepareBtn.Disable()
		return
	}
	p.reloadTargets(conn.ChainID.Uint64())
	p.acts = actions.All(conn.ChainID.Uint64())
	names := make([]string, len(p.acts))
	for i, a := range p.acts {
		names[i] = a.Name
	}
	// Preserve the current selection if it's still available.
	prev := ""
	if p.current != nil {
		prev = p.current.Name
	}
	p.actionSel.Options = names
	p.actionSel.Refresh()
	if prev != "" {
		p.actionSel.SetSelected(prev)
	}
	if len(names) == 0 {
		p.status.SetText(fmt.Sprintf("No actions available on %s yet.", conn.ChainInfo.Name))
	} else if p.status.Text == "" {
		p.status.SetText(fmt.Sprintf("%d actions on %s", len(names), conn.ChainInfo.Name))
	}
}

// reloadTargets repopulates the From dropdown: the active EOA plus any imported Safes
// on the connected chain, preserving the current selection.
func (p *preparePane) reloadTargets(chainID uint64) {
	p.targets = nil
	if desc, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet); ok {
		if addr, err := address.Parse(desc.Address); err == nil {
			p.targets = append(p.targets, prepareTarget{
				label:   "Wallet · " + firstNonEmpty(desc.Label, "(unnamed)") + " · " + address.Short(addr),
				address: addr,
			})
		}
	}
	for _, sd := range p.app.cfg.Safes {
		if sd.ChainID != chainID {
			continue
		}
		if addr, err := address.Parse(sd.Address); err == nil {
			p.targets = append(p.targets, prepareTarget{
				label:   fmt.Sprintf("Safe · %s · %s (%d-of-%d)", firstNonEmpty(sd.Label, "(unnamed)"), address.Short(addr), sd.Threshold, len(sd.Owners)),
				address: addr,
				isSafe:  true,
				safe:    sd,
			})
		}
	}
	prev := p.fromSel.Selected
	names := make([]string, len(p.targets))
	for i, t := range p.targets {
		names[i] = t.label
	}
	p.fromSel.Options = names
	p.fromSel.Refresh()
	if prev != "" {
		p.fromSel.SetSelected(prev)
	}
	if p.fromSel.SelectedIndex() < 0 && len(names) > 0 {
		p.fromSel.SetSelectedIndex(0)
	}
}

func (p *preparePane) selectedTarget() (prepareTarget, bool) {
	i := p.fromSel.SelectedIndex()
	if i < 0 || i >= len(p.targets) {
		return prepareTarget{}, false
	}
	return p.targets[i], true
}

func (p *preparePane) onActionSelected() {
	i := p.actionSel.SelectedIndex()
	if i < 0 || i >= len(p.acts) {
		p.current = nil
		p.fieldsBox.Objects = nil
		p.fieldsBox.Refresh()
		p.prepareBtn.Disable()
		return
	}
	a := p.acts[i]
	p.current = &a
	p.entries = map[string]*widget.Entry{}

	form := container.New(layout.NewFormLayout())
	desc := widget.NewLabel(a.Description)
	desc.Wrapping = fyne.TextWrapWord
	form.Add(widget.NewLabel(""))
	form.Add(desc)
	for _, f := range a.Fields {
		e := widget.NewEntry()
		e.SetPlaceHolder(f.Hint)
		p.entries[f.Key] = e
		form.Add(widget.NewLabelWithStyle(f.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		form.Add(e)
	}
	p.fieldsBox.Objects = []fyne.CanvasObject{form}
	p.fieldsBox.Refresh()
	p.prepareBtn.Enable()
}

// prepare parses inputs, builds the action, estimates gas, and shows the review.
func (p *preparePane) prepare() {
	if p.current == nil {
		return
	}
	target, ok := p.selectedTarget()
	if !ok {
		dialog.ShowError(fmt.Errorf("select an account under From"), p.app.window)
		return
	}
	from := target.address
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}

	// Parse inputs (18-decimal amounts → base units). Account is the acting account
	// (the Safe when targeting a Safe), so owner/recipient params resolve correctly.
	in := actions.Inputs{Amounts: map[string]*big.Int{}, Account: from}
	for _, f := range p.current.Fields {
		raw := p.entries[f.Key].Text
		switch f.Kind {
		case actions.FieldAmount18:
			v, perr := assets.ParseUnits(raw, 18)
			if perr != nil {
				dialog.ShowError(fmt.Errorf("%s: %w", f.Label, perr), p.app.window)
				return
			}
			in.Amounts[f.Key] = v
		}
	}

	prepared, err := p.current.Build(conn.ChainID.Uint64(), in)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}

	// A Safe target becomes a proposal, not a direct send.
	if target.isSafe {
		p.prepareSafeProposal(target.safe, prepared)
		return
	}

	p.prepareBtn.Disable()
	p.status.SetText("Estimating gas…")
	send := tx.Send{From: from, Call: prepared.Call}
	client := conn.Client
	chainID := new(big.Int).Set(conn.ChainID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		prep, prepErr := tx.Prepare(ctx, client, chainID, send)
		fyne.Do(func() {
			p.prepareBtn.Enable()
			if prepErr != nil {
				p.status.SetText("Ready")
				dialog.ShowError(fmt.Errorf("could not prepare: %w", prepErr), p.app.window)
				return
			}
			p.showReview(prepared, prep, conn.ChainInfo)
		})
	}()
}

// prepareSafeProposal reads the Safe nonce, computes the safeTxHash for the action's
// inner call, and shows a proposal review.
func (p *preparePane) prepareSafeProposal(desc safe.Descriptor, prepared actions.Prepared) {
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
	client := conn.Client
	p.prepareBtn.Disable()
	p.status.SetText("Reading Safe nonce…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		nonce, nerr := safe.Nonce(ctx, client, safeAddr)
		if nerr != nil {
			p.failPrepare(fmt.Errorf("read Safe nonce: %w", nerr))
			return
		}
		stx := safe.NewSafeTx(prepared.Call.To, prepared.Call.Value, prepared.Call.Data, nonce)
		hash, herr := stx.OnChainHash(ctx, client, safeAddr)
		if herr != nil {
			p.failPrepare(fmt.Errorf("compute safeTxHash: %w", herr))
			return
		}
		fyne.Do(func() {
			p.prepareBtn.Enable()
			p.status.SetText("Ready")
			p.showSafeReview(desc, prepared, nonce, hash)
		})
	}()
}

func (p *preparePane) failPrepare(err error) {
	fyne.Do(func() {
		p.prepareBtn.Enable()
		p.status.SetText("Ready")
		dialog.ShowError(err, p.app.window)
	})
}

// showSafeReview presents the decoded action + Safe proposal details, then creates the
// proposal on confirm.
func (p *preparePane) showSafeReview(desc safe.Descriptor, prepared actions.Prepared, nonce uint64, hash common.Hash) {
	grid := container.New(layout.NewFormLayout())
	addRow := func(k, v string) {
		grid.Add(widget.NewLabelWithStyle(k, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(v))
	}
	for _, r := range prepared.Review {
		addRow(r.Key, r.Value)
	}
	addRow("Safe", desc.Address)
	addRow("Safe nonce", fmt.Sprintf("%d", nonce))
	addRow("safeTxHash", hash.Hex())

	body := container.NewVBox(grid)
	if prepared.Note != "" {
		body.Add(cautionBox(prepared.Note))
	}
	body.Add(widget.NewSeparator())
	info := widget.NewLabel("This creates a Safe proposal — collect owner signatures and execute it in the Safe tab.")
	info.Wrapping = fyne.TextWrapWord
	body.Add(info)

	d := dialog.NewCustomConfirm("Review proposal — "+prepared.Summary, "Create proposal", "Cancel", body,
		func(confirm bool) {
			if confirm {
				p.createSafeProposal(desc, prepared, nonce, hash)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(660, 520))
	d.Show()
}

func (p *preparePane) createSafeProposal(desc safe.Descriptor, prepared actions.Prepared, nonce uint64, hash common.Hash) {
	if p.app.safeProposals == nil {
		dialog.ShowError(fmt.Errorf("proposal store unavailable"), p.app.window)
		return
	}
	safeAddr, _ := address.Parse(desc.Address)
	_, err := p.app.safeProposals.Insert(safe.Proposal{
		SafeAddress: safeAddr,
		ChainID:     desc.ChainID,
		To:          prepared.Call.To,
		Value:       prepared.Call.Value,
		Data:        prepared.Call.Data,
		Operation:   safe.Call,
		SafeNonce:   nonce,
		SafeTxHash:  hash,
		Kind:        safe.KindContractCall,
		Description: prepared.Summary,
		Status:      safe.StatusCollecting,
	})
	if err != nil {
		dialog.ShowError(fmt.Errorf("save proposal: %w", err), p.app.window)
		return
	}
	p.app.updateSafeBadge()
	if p.app.safeReload != nil {
		p.app.safeReload() // refresh the Safe pane's proposal list
	}
	p.status.SetText(fmt.Sprintf("Safe proposal created (nonce %d) — sign & execute it in the Safe tab", nonce))
	dialog.ShowInformation("Proposal created",
		fmt.Sprintf("%s\n\nNonce %d. Open the Safe tab to collect owner signatures and execute.", prepared.Summary, nonce),
		p.app.window)
}

// showReview shows the decoded action, the fees, and a Sign & send action.
func (p *preparePane) showReview(prepared actions.Prepared, prep tx.Prepared, info chain.Info) {
	grid := container.New(layout.NewFormLayout())
	addRow := func(k, v string) {
		grid.Add(widget.NewLabelWithStyle(k, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(v))
	}
	for _, r := range prepared.Review {
		addRow(r.Key, r.Value)
	}
	addRow("From", address.Format(prep.Send.From))
	addRow("Network", info.Name)
	addRow("Nonce", fmt.Sprintf("%d", prep.Nonce))
	addRow("Gas limit", fmt.Sprintf("%d", prep.Fees.GasLimit))
	addRow("Max fee/gas", assets.FormatUnits(prep.Fees.GasFeeCap, 9)+" gwei")
	addRow("Max total fee", assets.FormatUnits(prep.Fees.MaxFeeWei(), 18)+" "+info.Native.Symbol)

	signReady, signMsg := p.signAvailability(prep.Send.From)
	notice := widget.NewLabel(signMsg)
	notice.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(grid)
	if prepared.Note != "" {
		// A caveat (e.g. a required token approval) — flag it before signing.
		body.Add(cautionBox(prepared.Note))
	}
	body.Add(widget.NewSeparator())
	body.Add(notice)
	d := dialog.NewCustomConfirm("Review — "+prepared.Summary, "Sign & send", "Cancel", body,
		func(confirm bool) {
			if confirm {
				p.signAndSend(prepared, prep, info)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(640, 520))
	if !signReady {
		d.SetConfirmText("Unlock to sign")
	}
	d.Show()
}

// signAvailability reports whether the active signer can sign for `from`.
func (p *preparePane) signAvailability(from common.Address) (bool, string) {
	s, _, ok := p.app.currentSigner()
	if !ok {
		return false, "No wallet is unlocked. Unlock the sending wallet in the Wallets tab, then sign."
	}
	if s.Address() != from {
		return false, "The unlocked wallet does not match the sender. Unlock " + address.Short(from) + " to sign."
	}
	return true, "Ready to sign with the unlocked wallet."
}

// signAndSend signs, broadcasts, records history, and tracks inclusion.
func (p *preparePane) signAndSend(prepared actions.Prepared, prep tx.Prepared, info chain.Info) {
	ready, msg := p.signAvailability(prep.Send.From)
	if !ready {
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
		return
	}
	s, _, _ := p.app.currentSigner()
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connection lost; reconnect and try again"), p.app.window)
		return
	}
	client := conn.Client

	var recID int64
	if p.app.history != nil {
		id, _ := p.app.history.Insert(history.Record{
			ChainID:       info.ID,
			WalletAddress: address.Format(prep.Send.From),
			Kind:          "prepare",
			Instructions:  prepared.Summary,
			ToAddress:     address.Format(prep.Send.Call.To),
			ValueWei:      prep.Send.Call.Value.String(),
			Status:        history.StatusPrepared,
		})
		recID = id
	}

	p.status.SetText("Signing…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		signed, err := s.SignTx(ctx, prep.Tx, prep.ChainID)
		if err != nil {
			p.finishError(recID, "sign: "+err.Error())
			return
		}
		hash, err := tx.Broadcast(ctx, client, signed)
		if err != nil {
			p.finishError(recID, err.Error())
			return
		}
		if p.app.history != nil && recID != 0 {
			_ = p.app.history.MarkSubmitted(recID, hash.Hex())
		}
		fyne.Do(func() {
			p.status.SetText("Submitted: " + hash.Hex())
			resultLabel := p.showResult(hash.Hex(), info)
			if p.app.historyReload != nil {
				p.app.historyReload()
			}
			go p.trackInclusion(recID, client, hash, info, resultLabel)
		})
	}()
}

// trackInclusion waits for the receipt and updates the (still-open) result dialog's
// status line in place, plus the pane status and history — so inclusion is visible
// rather than the dialog appearing stuck on "waiting".
func (p *preparePane) trackInclusion(recID int64, client rpc.Client, hash common.Hash, info chain.Info, resultLabel *widget.Label) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	receipt, err := tx.WaitForReceipt(ctx, client, hash)
	if err != nil {
		fyne.Do(func() {
			p.status.SetText("Could not confirm inclusion: " + err.Error())
			if resultLabel != nil {
				resultLabel.SetText("Still pending — could not confirm inclusion. Check the explorer.")
			}
		})
		return
	}
	success := tx.Succeeded(receipt)
	blockNum := receipt.BlockNumber.Int64()
	var blockTime int64
	if head, herr := client.HeaderByNumber(ctx, receipt.BlockNumber); herr == nil && head != nil {
		blockTime = int64(head.Time)
	}
	if p.app.history != nil && recID != 0 {
		_ = p.app.history.MarkIncluded(recID, blockNum, blockTime, success)
	}
	fyne.Do(func() {
		outcome := "succeeded ✓"
		if !success {
			outcome = "reverted ✗"
		}
		p.status.SetText(fmt.Sprintf("Included in block %d — %s", blockNum, outcome))
		if resultLabel != nil {
			resultLabel.SetText(fmt.Sprintf("Included in block %d — %s", blockNum, outcome))
		}
		if p.app.historyReload != nil {
			p.app.historyReload()
		}
	})
}

// showResult opens the submitted-transaction dialog and returns its status label so
// trackInclusion can update it in place when the receipt lands.
func (p *preparePane) showResult(hash string, info chain.Info) *widget.Label {
	status := widget.NewLabel("Transaction submitted. Waiting for inclusion…")
	status.Wrapping = fyne.TextWrapWord
	body := container.NewVBox(status, monoLabel(hash))
	if link := info.TxURL(hash); link != "" {
		body.Add(widget.NewButton("View on explorer", func() { p.app.openURL(link) }))
	}
	dialog.ShowCustom("Prepared transaction", "Close", body, p.app.window)
	return status
}

func (p *preparePane) finishError(recID int64, msg string) {
	if p.app.history != nil && recID != 0 {
		_ = p.app.history.MarkError(recID, msg)
	}
	fyne.Do(func() {
		p.status.SetText("Failed: " + msg)
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
		if p.app.historyReload != nil {
			p.app.historyReload()
		}
	})
}
