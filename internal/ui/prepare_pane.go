package ui

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/actions"
	"codeberg.org/pasiphae/callisto/internal/ai"
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

	nlEntry      *widget.Entry
	interpretBtn *widget.Button
	fromSel      *widget.Select
	actionSel    *widget.Select
	fieldsBox    *fyne.Container
	content      *fyne.Container // the top VBox, refreshed when fields change
	entries      map[string]*widget.Entry
	prepareBtn   *widget.Button
	status       *widget.Label

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
	help := widget.NewLabel("Prepare a transaction from a known action (e.g. wrap ETH, stake with Lido). Callisto builds and decodes the call for you to review before signing. With AI enabled (Settings > AI features), you can also describe what you want in plain language.")
	help.Wrapping = fyne.TextWrapWord

	p.nlEntry = widget.NewEntry()
	p.nlEntry.SetPlaceHolder(`Describe what you want — e.g. "stake 5 ETH with Lido" (needs AI enabled in Settings)`)
	p.nlEntry.OnSubmitted = func(string) { p.interpret() }
	p.interpretBtn = widget.NewButton("Interpret with AI", p.interpret)
	nlRow := container.NewBorder(nil, nil, nil, p.interpretBtn, p.nlEntry)

	// Reload the action list when the connection changes (actions are chain-scoped).
	p.app.rpc.OnNewHead(func(*types.Header) { fyne.Do(p.reloadActions) })

	top := container.NewVBox(
		header, help,
		nlRow,
		container.New(layout.NewFormLayout(),
			widget.NewLabelWithStyle("From", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), p.fromSel,
			widget.NewLabelWithStyle("Action", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), p.actionSel),
		p.fieldsBox,
		indentToText(container.NewHBox(p.prepareBtn)),
		p.status,
	)
	p.content = top
	p.reloadActions()
	return container.NewVScroll(top)
}

// interpret sends the natural-language intent to the AI resolver, which maps it to a
// registry action + params. On success the action/fields are populated for the user
// to review and Prepare — Claude never bypasses the deterministic build/review/sign.
func (p *preparePane) interpret() {
	if !p.app.cfg.AIReady() {
		dialog.ShowInformation("AI not enabled",
			"Enable AI-assisted preparation and set your Anthropic API key in Settings > AI features.", p.app.window)
		return
	}
	intent := strings.TrimSpace(p.nlEntry.Text)
	if intent == "" {
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	chainID := conn.ChainID.Uint64()
	chainName := conn.ChainInfo.Name

	acts := actions.All(chainID)
	aiActs := make([]ai.Action, len(acts))
	for i, a := range acts {
		fields := make([]ai.Field, len(a.Fields))
		for j, f := range a.Fields {
			fields[j] = ai.Field{Key: f.Key, Label: f.Label, Hint: f.Hint}
		}
		aiActs[i] = ai.Action{ID: a.ID, Name: a.Name, Desc: a.Description, Fields: fields}
	}

	client := ai.NewClient(p.app.cfg.AI.APIKey)
	p.interpretBtn.Disable()
	p.status.SetText("Interpreting…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Resolve(ctx, intent, chainName, aiActs)
		fyne.Do(func() {
			p.interpretBtn.Enable()
			if err != nil {
				p.status.SetText("Ready")
				dialog.ShowError(fmt.Errorf("AI request failed: %w", err), p.app.window)
				return
			}
			if !res.OK {
				p.status.SetText("Ready")
				dialog.ShowInformation("Couldn't prepare that", res.Reason, p.app.window)
				return
			}
			p.applyResolution(res)
		})
	}()
}

// applyResolution selects the resolved action and fills its fields, leaving the user
// to review and Prepare.
func (p *preparePane) applyResolution(res ai.Resolution) {
	a, ok := actions.ByID(res.ActionID)
	if !ok {
		dialog.ShowError(fmt.Errorf("unknown action: %s", res.ActionID), p.app.window)
		return
	}
	// Set the value directly (not SetSelected, whose OnChanged would asynchronously
	// rebuild the entries and wipe what we fill below), then build the fields once.
	p.actionSel.Selected = a.Name
	p.actionSel.Refresh()
	p.onActionSelected()
	p.fillEntries(res.Params)
	p.status.SetText("Interpreted — review the filled form, then Prepare.")
}

// fillEntries populates the action's inputs from the resolved params: an exact key
// match first, then a fallback for the (common) single-field action so a key the model
// named slightly differently still lands.
func (p *preparePane) fillEntries(params map[string]string) {
	for k, v := range params {
		if e, ok := p.entries[k]; ok {
			e.SetText(v)
		}
	}
	if p.current != nil && len(p.current.Fields) == 1 {
		key := p.current.Fields[0].Key
		if e, ok := p.entries[key]; ok && e.Text == "" {
			for _, v := range params {
				if strings.TrimSpace(v) != "" {
					e.SetText(v)
					break
				}
			}
		}
	}
}

// prepareNoRPC is the disconnected status; kept as a const so reloadActions can tell a
// stale disconnect message from a transient one (and clear it once connected).
const prepareNoRPC = "Connect an RPC endpoint in Settings to prepare transactions."

// reloadActions repopulates the action dropdown for the connected chain.
func (p *preparePane) reloadActions() {
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.acts = nil
		p.actionSel.Options = nil
		p.actionSel.Refresh()
		p.status.SetText(prepareNoRPC)
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
	// Set the idle status only when it's empty or the stale disconnect message —
	// don't clobber a transient status ("Interpreting…", "Submitted…", etc.).
	if p.status.Text == "" || p.status.Text == prepareNoRPC {
		if len(names) == 0 {
			p.status.SetText(fmt.Sprintf("No actions available on %s yet.", conn.ChainInfo.Name))
		} else {
			p.status.SetText(fmt.Sprintf("%d actions on %s", len(names), conn.ChainInfo.Name))
		}
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
		if p.content != nil {
			p.content.Refresh()
		}
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
	// Re-lay-out the parent so subsequent rows (Prepare, status) reposition instead
	// of overlapping the newly-sized fields.
	if p.content != nil {
		p.content.Refresh()
	}
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
	p.prepareEOA(from, conn.Client, new(big.Int).Set(conn.ChainID), conn.ChainInfo, prepared)
}

// prepareEOA checks approvals for an EOA target and either estimates + reviews the
// single action, or (when an approval is missing) reviews an approve→action sequence.
func (p *preparePane) prepareEOA(from common.Address, client rpc.Client, chainID *big.Int, info chain.Info, prepared actions.Prepared) {
	p.prepareBtn.Disable()
	p.status.SetText("Checking approvals…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		need, aerr := p.neededApprovals(ctx, client, from, prepared.Approvals)
		if aerr != nil {
			p.failPrepare(aerr)
			return
		}
		if len(need) == 0 {
			// Single transaction: estimate gas and show the normal review.
			prep, err := tx.Prepare(ctx, client, chainID, tx.Send{From: from, Call: prepared.Call})
			if err != nil {
				p.failPrepare(fmt.Errorf("could not prepare: %w", err))
				return
			}
			fyne.Do(func() {
				p.prepareBtn.Enable()
				p.status.SetText("Ready")
				p.showReview(prepared, prep, info)
			})
			return
		}
		// Approvals needed: the action can't be gas-estimated until they land, so we
		// review the sequence and estimate each transaction at send time.
		var approveCalls []tx.Call
		for _, ap := range need {
			data, derr := assets.EncodeApprove(ap.Spender, ap.Amount)
			if derr != nil {
				p.failPrepare(derr)
				return
			}
			approveCalls = append(approveCalls, tx.Call{To: ap.Token, Value: big.NewInt(0), Data: data})
		}
		fyne.Do(func() {
			p.prepareBtn.Enable()
			p.status.SetText("Ready")
			p.showSequenceReview(from, prepared, need, approveCalls, info)
		})
	}()
}

// showSequenceReview reviews an approve(s)→action sequence, then runs it on confirm.
func (p *preparePane) showSequenceReview(from common.Address, prepared actions.Prepared, need []actions.Approval, approveCalls []tx.Call, info chain.Info) {
	grid := container.New(layout.NewFormLayout())
	addRow := func(k, v string) {
		grid.Add(widget.NewLabelWithStyle(k, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(v))
	}
	for _, r := range prepared.Review {
		addRow(r.Key, r.Value)
	}
	addRow("From", address.Format(from))
	addRow("Network", info.Name)

	var b strings.Builder
	fmt.Fprintf(&b, "Sends %d transactions in order (each is signed and confirmed on-chain before the next):", len(approveCalls)+1)
	for i, ap := range need {
		fmt.Fprintf(&b, "\n%d. approve %s of %s to %s", i+1, assets.FormatUnits(ap.Amount, 18), address.Short(ap.Token), address.Short(ap.Spender))
	}
	fmt.Fprintf(&b, "\n%d. %s", len(approveCalls)+1, prepared.Summary)

	body := container.NewVBox(grid, cautionBox(b.String()))
	if prepared.Note != "" {
		body.Add(cautionBox(prepared.Note))
	}
	signReady, signMsg := p.signAvailability(from)
	notice := widget.NewLabel(signMsg)
	notice.Wrapping = fyne.TextWrapWord
	body.Add(widget.NewSeparator())
	body.Add(notice)

	d := dialog.NewCustomConfirm("Review — "+prepared.Summary, "Sign & send all", "Cancel", body,
		func(confirm bool) {
			if confirm {
				p.runSequence(from, prepared, approveCalls, info)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(660, 560))
	if !signReady {
		d.SetConfirmText("Unlock to sign")
	}
	d.Show()
}

// runSequence sends each approval (waiting for inclusion) then the action, off the UI
// thread. It records history for the action; approvals are intermediate.
func (p *preparePane) runSequence(from common.Address, prepared actions.Prepared, approveCalls []tx.Call, info chain.Info) {
	ready, msg := p.signAvailability(from)
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
	chainID := new(big.Int).Set(conn.ChainID)

	p.status.SetText("Sending approvals…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		for i, call := range approveCalls {
			step := i + 1
			fyne.Do(func() { p.status.SetText(fmt.Sprintf("Approving (%d/%d)…", step, len(approveCalls))) })
			prep, err := tx.Prepare(ctx, client, chainID, tx.Send{From: from, Call: call})
			if err != nil {
				p.finishError(0, "prepare approval: "+err.Error())
				return
			}
			signed, err := s.SignTx(ctx, prep.Tx, prep.ChainID)
			if err != nil {
				p.finishError(0, "sign approval: "+err.Error())
				return
			}
			hash, err := tx.Broadcast(ctx, client, signed)
			if err != nil {
				p.finishError(0, "broadcast approval: "+err.Error())
				return
			}
			fyne.Do(func() { p.status.SetText(fmt.Sprintf("Approval %d/%d submitted — waiting for inclusion…", step, len(approveCalls))) })
			receipt, err := tx.WaitForReceipt(ctx, client, hash)
			if err != nil {
				p.finishError(0, "approval not confirmed: "+err.Error())
				return
			}
			if !tx.Succeeded(receipt) {
				p.finishError(0, "approval transaction reverted")
				return
			}
		}

		// Now the action, with the allowance in place.
		fyne.Do(func() { p.status.SetText("Preparing the action…") })
		prep, err := tx.Prepare(ctx, client, chainID, tx.Send{From: from, Call: prepared.Call})
		if err != nil {
			p.finishError(0, "prepare action: "+err.Error())
			return
		}
		var recID int64
		if p.app.history != nil {
			id, _ := p.app.history.Insert(history.Record{
				ChainID:       info.ID,
				WalletAddress: address.Format(from),
				Kind:          "prepare",
				Instructions:  prepared.Summary,
				ToAddress:     address.Format(prepared.Call.To),
				ValueWei:      prepared.Call.Value.String(),
				Status:        history.StatusPrepared,
			})
			recID = id
		}
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
		// Batch any required approvals with the action into one atomic MultiSend
		// proposal, so owners sign+execute once instead of approving separately.
		need, aerr := p.neededApprovals(ctx, client, safeAddr, prepared.Approvals)
		if aerr != nil {
			p.failPrepare(aerr)
			return
		}
		var stx safe.SafeTx
		batched := len(need) > 0
		if batched {
			var calls []safe.MultiSendCall
			for _, ap := range need {
				data, derr := assets.EncodeApprove(ap.Spender, ap.Amount)
				if derr != nil {
					p.failPrepare(derr)
					return
				}
				calls = append(calls, safe.MultiSendCall{To: ap.Token, Value: big.NewInt(0), Data: data})
			}
			calls = append(calls, safe.MultiSendCall{To: prepared.Call.To, Value: prepared.Call.Value, Data: prepared.Call.Data})
			var berr error
			if stx, berr = safe.BuildMultiSend(calls, nonce); berr != nil {
				p.failPrepare(berr)
				return
			}
		} else {
			stx = safe.NewSafeTx(prepared.Call.To, prepared.Call.Value, prepared.Call.Data, nonce)
		}
		hash, herr := stx.OnChainHash(ctx, client, safeAddr)
		if herr != nil {
			p.failPrepare(fmt.Errorf("compute safeTxHash: %w", herr))
			return
		}
		fyne.Do(func() {
			p.prepareBtn.Enable()
			p.status.SetText("Ready")
			p.showSafeReview(desc, prepared, need, stx, nonce, hash)
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

// neededApprovals returns the approvals whose live allowance (from owner) is below the
// required amount, so the pipeline can prepend approves.
func (p *preparePane) neededApprovals(ctx context.Context, client rpc.Client, owner common.Address, approvals []actions.Approval) ([]actions.Approval, error) {
	var need []actions.Approval
	for _, ap := range approvals {
		allow, err := assets.Allowance(ctx, client, ap.Token, owner, ap.Spender)
		if err != nil {
			return nil, fmt.Errorf("check allowance: %w", err)
		}
		if allow.Cmp(ap.Amount) < 0 {
			need = append(need, ap)
		}
	}
	return need, nil
}

// showSafeReview presents the decoded action (+ any batched approvals) and the Safe
// proposal details, then creates the proposal on confirm. stx is the SafeTx that will
// be proposed (the action alone, or a MultiSend batch of approvals + the action).
func (p *preparePane) showSafeReview(desc safe.Descriptor, prepared actions.Prepared, need []actions.Approval, stx safe.SafeTx, nonce uint64, hash common.Hash) {
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
	if len(need) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Batched atomically (MultiSend) with %d approval(s) so nothing is approved separately:", len(need))
		for _, ap := range need {
			fmt.Fprintf(&b, "\n• approve %s to %s", address.Short(ap.Token), address.Short(ap.Spender))
		}
		body.Add(cautionBox(b.String()))
	}
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
				p.createSafeProposal(desc, prepared, stx, nonce, hash)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(660, 520))
	d.Show()
}

func (p *preparePane) createSafeProposal(desc safe.Descriptor, prepared actions.Prepared, stx safe.SafeTx, nonce uint64, hash common.Hash) {
	if p.app.safeProposals == nil {
		dialog.ShowError(fmt.Errorf("proposal store unavailable"), p.app.window)
		return
	}
	safeAddr, _ := address.Parse(desc.Address)
	_, err := p.app.safeProposals.Insert(safe.Proposal{
		SafeAddress: safeAddr,
		ChainID:     desc.ChainID,
		To:          stx.To,
		Value:       stx.Value,
		Data:        stx.Data,
		Operation:   stx.Operation,
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
