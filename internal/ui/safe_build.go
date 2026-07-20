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
	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/safe"
)

// safeBuildView is the Safe "Build" sub-tab: prepare an ecosystem action (wrap ETH,
// stake with Lido, wrap/unwrap wstETH, request/claim a Lido withdrawal) as a Safe
// proposal, from the curated action registry (internal/actions). Callisto builds and
// decodes the call deterministically; any token approval the action needs is batched
// atomically into the same MultiSend proposal, so owners sign and execute once.
//
// EOAs deliberately do not get this — they connect to dApps over WalletConnect and use
// native flows. A Safe can't drive a synchronous dApp handshake, so curated on-Safe
// preparation is how it reaches the same ecosystem actions.
type safeBuildView struct {
	app  *App
	pane *safePane // for the selected Safe + refreshing the proposal list

	actionSel  *widget.Select
	fieldsBox  *fyne.Container
	content    *fyne.Container // the top VBox, refreshed when fields change
	entries    map[string]*widget.Entry
	prepareBtn *widget.Button
	status     *widget.Label

	acts    []actions.Action
	current *actions.Action
}

func newSafeBuildView(pane *safePane) *safeBuildView {
	return &safeBuildView{app: pane.app, pane: pane, entries: map[string]*widget.Entry{}}
}

// safeBuildNoRPC is the disconnected status; kept as a const so reloadActions can tell
// a stale disconnect message from a transient one (and clear it once connected).
const safeBuildNoRPC = "Connect an RPC endpoint in Settings to prepare actions."

func (b *safeBuildView) build() fyne.CanvasObject {
	b.status = widget.NewLabel("")
	b.status.Wrapping = fyne.TextWrapWord

	b.actionSel = widget.NewSelect(nil, func(string) { b.onActionSelected() })
	b.actionSel.PlaceHolder = "Select an action"
	b.fieldsBox = container.NewVBox()
	b.prepareBtn = widget.NewButton("Prepare proposal", b.prepare)
	b.prepareBtn.Importance = widget.HighImportance
	b.prepareBtn.Disable()

	help := widget.NewLabel("Prepare an ecosystem action (wrap ETH, stake with Lido, wrap/unwrap wstETH, request/claim a Lido withdrawal) as a Safe proposal. Callisto builds and decodes the call for you to review; any token approval it needs is batched atomically into the same proposal. Collect owner signatures and execute it under Proposals.")
	help.Wrapping = fyne.TextWrapWord

	// Actions are chain-scoped, so reload the list when the connection changes.
	b.app.rpc.OnNewHead(func(*types.Header) { fyne.Do(b.reloadActions) })

	top := container.NewVBox(
		help,
		container.New(layout.NewFormLayout(),
			widget.NewLabelWithStyle("Action", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), b.actionSel),
		b.fieldsBox,
		indentToText(container.NewHBox(b.prepareBtn)),
		b.status,
	)
	b.content = top
	b.reloadActions()
	return container.NewVScroll(top)
}

// reloadActions repopulates the action dropdown for the connected chain, preserving
// the current selection.
func (b *safeBuildView) reloadActions() {
	conn, ok := b.app.rpc.Active()
	if !ok {
		b.acts = nil
		b.actionSel.Options = nil
		b.actionSel.Refresh()
		b.status.SetText(safeBuildNoRPC)
		b.prepareBtn.Disable()
		return
	}
	b.acts = actions.All(conn.ChainID.Uint64())
	names := make([]string, len(b.acts))
	for i, a := range b.acts {
		names[i] = a.Name
	}
	prev := ""
	if b.current != nil {
		prev = b.current.Name
	}
	b.actionSel.Options = names
	b.actionSel.Refresh()
	if prev != "" {
		b.actionSel.SetSelected(prev)
	}
	// Only set the idle status when it's empty or the stale disconnect message — don't
	// clobber a transient status ("Reading Safe nonce…", "Ready", etc.).
	if b.status.Text == "" || b.status.Text == safeBuildNoRPC {
		if len(names) == 0 {
			b.status.SetText(fmt.Sprintf("No actions available on %s yet.", conn.ChainInfo.Name))
		} else {
			b.status.SetText(fmt.Sprintf("%d actions on %s", len(names), conn.ChainInfo.Name))
		}
	}
}

func (b *safeBuildView) onActionSelected() {
	i := b.actionSel.SelectedIndex()
	if i < 0 || i >= len(b.acts) {
		b.current = nil
		b.fieldsBox.Objects = nil
		b.fieldsBox.Refresh()
		if b.content != nil {
			b.content.Refresh()
		}
		b.prepareBtn.Disable()
		return
	}
	a := b.acts[i]
	b.current = &a
	b.entries = map[string]*widget.Entry{}

	form := container.New(layout.NewFormLayout())
	desc := widget.NewLabel(a.Description)
	desc.Wrapping = fyne.TextWrapWord
	form.Add(widget.NewLabel(""))
	form.Add(desc)
	for _, f := range a.Fields {
		e := widget.NewEntry()
		e.SetPlaceHolder(f.Hint)
		b.entries[f.Key] = e
		form.Add(widget.NewLabelWithStyle(f.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		form.Add(e)
	}
	b.fieldsBox.Objects = []fyne.CanvasObject{form}
	b.fieldsBox.Refresh()
	// Re-lay-out the parent so subsequent rows (Prepare, status) reposition instead of
	// overlapping the newly-sized fields.
	if b.content != nil {
		b.content.Refresh()
	}
	b.prepareBtn.Enable()
}

// prepare parses inputs, builds the action for the selected Safe, and shows a proposal
// review.
func (b *safeBuildView) prepare() {
	if b.current == nil {
		return
	}
	desc, ok := b.pane.selectedSafe()
	if !ok {
		dialog.ShowError(fmt.Errorf("select a Safe first (top of this tab)"), b.app.window)
		return
	}
	conn, ok := b.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), b.app.window)
		return
	}
	safeAddr, err := address.Parse(desc.Address)
	if err != nil {
		dialog.ShowError(err, b.app.window)
		return
	}

	// Parse inputs. Account is the Safe itself, so owner/recipient params resolve to it.
	in := actions.Inputs{Amounts: map[string]*big.Int{}, Uints: map[string]*big.Int{}, Account: safeAddr}
	for _, f := range b.current.Fields {
		raw := strings.TrimSpace(b.entries[f.Key].Text)
		switch f.Kind {
		case actions.FieldAmount18:
			v, perr := assets.ParseUnits(raw, 18)
			if perr != nil {
				dialog.ShowError(fmt.Errorf("%s: %w", f.Label, perr), b.app.window)
				return
			}
			in.Amounts[f.Key] = v
		case actions.FieldUint256:
			v, okv := new(big.Int).SetString(raw, 10)
			if !okv {
				dialog.ShowError(fmt.Errorf("%s: enter a whole number", f.Label), b.app.window)
				return
			}
			in.Uints[f.Key] = v
		}
	}

	prepared, err := b.current.Build(conn.ChainID.Uint64(), in)
	if err != nil {
		dialog.ShowError(err, b.app.window)
		return
	}
	b.prepareSafeProposal(desc, safeAddr, prepared)
}

// prepareSafeProposal reads the Safe nonce, batches any required approvals with the
// action into one atomic MultiSend, computes the safeTxHash, and shows a review.
func (b *safeBuildView) prepareSafeProposal(desc safe.Descriptor, safeAddr common.Address, prepared actions.Prepared) {
	conn, ok := b.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), b.app.window)
		return
	}
	client := conn.Client
	b.prepareBtn.Disable()
	b.status.SetText("Reading Safe nonce…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		nonce, nerr := safe.Nonce(ctx, client, safeAddr)
		if nerr != nil {
			b.failPrepare(fmt.Errorf("read Safe nonce: %w", nerr))
			return
		}
		need, aerr := b.neededApprovals(ctx, client, safeAddr, prepared.Approvals)
		if aerr != nil {
			b.failPrepare(aerr)
			return
		}
		var stx safe.SafeTx
		if len(need) > 0 {
			var calls []safe.MultiSendCall
			for _, ap := range need {
				data, derr := assets.EncodeApprove(ap.Spender, ap.Amount)
				if derr != nil {
					b.failPrepare(derr)
					return
				}
				calls = append(calls, safe.MultiSendCall{To: ap.Token, Value: big.NewInt(0), Data: data})
			}
			calls = append(calls, safe.MultiSendCall{To: prepared.Call.To, Value: prepared.Call.Value, Data: prepared.Call.Data})
			var berr error
			if stx, berr = safe.BuildMultiSend(calls, nonce); berr != nil {
				b.failPrepare(berr)
				return
			}
		} else {
			stx = safe.NewSafeTx(prepared.Call.To, prepared.Call.Value, prepared.Call.Data, nonce)
		}
		hash, herr := stx.OnChainHash(ctx, client, safeAddr)
		if herr != nil {
			b.failPrepare(fmt.Errorf("compute safeTxHash: %w", herr))
			return
		}
		fyne.Do(func() {
			b.prepareBtn.Enable()
			b.status.SetText("Ready")
			b.showSafeReview(desc, prepared, need, stx, nonce, hash)
		})
	}()
}

func (b *safeBuildView) failPrepare(err error) {
	fyne.Do(func() {
		b.prepareBtn.Enable()
		b.status.SetText("Ready")
		dialog.ShowError(err, b.app.window)
	})
}

// neededApprovals returns the approvals whose live allowance (from the Safe) is below
// the required amount, so they can be prepended to the MultiSend batch.
func (b *safeBuildView) neededApprovals(ctx context.Context, client rpc.Client, owner common.Address, approvals []actions.Approval) ([]actions.Approval, error) {
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
// proposal details, then creates the proposal on confirm.
func (b *safeBuildView) showSafeReview(desc safe.Descriptor, prepared actions.Prepared, need []actions.Approval, stx safe.SafeTx, nonce uint64, hash common.Hash) {
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
		var sb strings.Builder
		fmt.Fprintf(&sb, "Batched atomically (MultiSend) with %d approval(s) so nothing is approved separately:", len(need))
		for _, ap := range need {
			fmt.Fprintf(&sb, "\n• approve %s to %s", address.Short(ap.Token), address.Short(ap.Spender))
		}
		body.Add(cautionBox(sb.String()))
	}
	if prepared.Note != "" {
		body.Add(cautionBox(prepared.Note))
	}
	body.Add(widget.NewSeparator())
	info := widget.NewLabel("This creates a Safe proposal — collect owner signatures and execute it under Proposals.")
	info.Wrapping = fyne.TextWrapWord
	body.Add(info)

	d := dialog.NewCustomConfirm("Review proposal — "+prepared.Summary, "Create proposal", "Cancel", body,
		func(confirm bool) {
			if confirm {
				b.createSafeProposal(desc, prepared, stx, nonce, hash)
			}
		}, b.app.window)
	d.Resize(fyne.NewSize(660, 520))
	d.Show()
}

func (b *safeBuildView) createSafeProposal(desc safe.Descriptor, prepared actions.Prepared, stx safe.SafeTx, nonce uint64, hash common.Hash) {
	if b.app.safeProposals == nil {
		dialog.ShowError(fmt.Errorf("proposal store unavailable"), b.app.window)
		return
	}
	safeAddr, _ := address.Parse(desc.Address)
	_, err := b.app.safeProposals.Insert(safe.Proposal{
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
		dialog.ShowError(fmt.Errorf("save proposal: %w", err), b.app.window)
		return
	}
	b.pane.refreshProposals(desc)
	b.status.SetText(fmt.Sprintf("Proposal created (nonce %d) — sign & execute it under Proposals", nonce))
	// Jump to the Proposals tab so the new proposal is right there.
	if b.pane.tabs != nil && b.pane.proposalsTab != nil {
		b.pane.tabs.Select(b.pane.proposalsTab)
	}
}
