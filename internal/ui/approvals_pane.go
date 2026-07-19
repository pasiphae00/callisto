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

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/approvals"
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/history"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/tx"
)

// approvalsPane discovers and revokes the active wallet's outstanding ERC-20 and
// Permit2 approvals. Discovery scans Approval logs on the active RPC (see
// internal/approvals), so it needs a full/archive endpoint; a scan against an
// endpoint that can't serve eth_getLogs surfaces a clear message.
type approvalsPane struct {
	app *App

	status   *widget.Label
	scanBtn  *widget.Button
	list     *widget.List
	items    []approvals.Approval
	scanning bool
}

func newApprovalsPane(a *App) *approvalsPane {
	return &approvalsPane{app: a}
}

func (p *approvalsPane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	p.list = widget.NewList(
		func() int { return len(p.items) },
		func() fyne.CanvasObject { return newApprovalRow() },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			ap := p.items[i]
			row := o.(*approvalRow)
			row.set(ap)
			row.revoke.OnTapped = func() { p.revoke(ap) }
		},
	)

	p.scanBtn = widget.NewButton("Scan approvals", p.scan)

	header := widget.NewLabelWithStyle("Approvals", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	help := widget.NewLabel("Every outstanding token approval for the active wallet — direct ERC-20 allowances and Uniswap Permit2 grants. Unlimited approvals to a compromised contract are a common drain vector; revoke anything you don't recognise. Discovery scans on-chain logs, so it needs a full/archive RPC (the default Flashbots endpoint can't scan).")
	help.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(header, help, indentToText(container.NewHBox(p.scanBtn)), p.status, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, p.list)
}

// scan discovers approvals for the active wallet off the UI thread.
func (p *approvalsPane) scan() {
	if p.scanning {
		return
	}
	desc, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	if !ok {
		p.status.SetText("Select a wallet in the Wallets tab.")
		return
	}
	owner, err := address.Parse(desc.Address)
	if err != nil {
		p.status.SetText("This wallet has an invalid address on record.")
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.status.SetText("Connect an RPC endpoint first (a full/archive node).")
		return
	}

	p.scanning = true
	p.scanBtn.Disable()
	p.items = nil
	p.list.Refresh()
	scanner := approvals.NewScanner(conn.Client, conn.ChainID.Uint64())

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		report := func(s string) { fyne.Do(func() { p.status.SetText(s) }) }
		found, err := scanner.Scan(ctx, owner, report)
		fyne.Do(func() {
			p.scanning = false
			p.scanBtn.Enable()
			if err != nil {
				p.status.SetText("")
				dialog.ShowError(fmt.Errorf("could not scan approvals: %w\n\nThis usually means the active RPC doesn't serve log queries — connect a full/archive endpoint in Settings.", err), p.app.window)
				return
			}
			p.items = found
			p.list.Refresh()
			if len(found) == 0 {
				p.status.SetText("No outstanding approvals for " + address.Short(owner) + ".")
			} else {
				p.status.SetText(fmt.Sprintf("%d outstanding approval(s) for %s.", len(found), address.Short(owner)))
			}
		})
	}()
}

// revoke prepares, reviews, signs, and submits a revocation for ap.
func (p *approvalsPane) revoke(ap approvals.Approval) {
	desc, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	if !ok {
		dialog.ShowError(fmt.Errorf("select a wallet first"), p.app.window)
		return
	}
	owner, err := address.Parse(desc.Address)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	to, data, err := ap.RevokeCall()
	if err != nil {
		dialog.ShowError(fmt.Errorf("build revoke: %w", err), p.app.window)
		return
	}
	send := tx.Send{
		From:      owner,
		Recipient: to,
		Token:     to,
		Symbol:    ap.DisplayToken(),
		Decimals:  ap.TokenDecimals,
		Amount:    big.NewInt(0),
		Call:      tx.Call{To: to, Value: big.NewInt(0), Data: data},
	}

	client := conn.Client
	chainID := new(big.Int).Set(conn.ChainID)
	p.status.SetText("Estimating gas…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		prep, prepErr := tx.Prepare(ctx, client, chainID, send)
		fyne.Do(func() {
			p.status.SetText("")
			if prepErr != nil {
				dialog.ShowError(fmt.Errorf("could not prepare revoke: %w", prepErr), p.app.window)
				return
			}
			p.showRevokeReview(prep, ap, conn.ChainInfo)
		})
	}()
}

// showRevokeReview presents the pre-sign review for a revocation.
func (p *approvalsPane) showRevokeReview(prep tx.Prepared, ap approvals.Approval, info chain.Info) {
	nativeSym := info.Native.Symbol
	rows := [][2]string{
		{"Action", "Revoke approval (" + ap.Layer.String() + ")"},
		{"Token", ap.DisplayToken() + "  " + address.Short(ap.Token)},
		{"Spender", ap.DisplaySpender()},
		{"New allowance", "0"},
		{"From", address.Format(prep.Send.From)},
		{"Network", info.Name},
		{"Nonce", fmt.Sprintf("%d", prep.Nonce)},
		{"Gas limit", fmt.Sprintf("%d", prep.Fees.GasLimit)},
		{"Max fee/gas", assets.FormatUnits(prep.Fees.GasFeeCap, 9) + " gwei"},
		{"Max total fee", assets.FormatUnits(prep.Fees.MaxFeeWei(), 18) + " " + nativeSym},
	}
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		grid.Add(widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(monoLabel(r[1]))
	}

	ready, msg := p.revokeSignAvailability(prep.Send.From)
	notice := widget.NewLabel(msg)
	notice.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(grid, widget.NewSeparator(), notice)

	d := dialog.NewCustomConfirm("Revoke approval", "Sign & revoke", "Cancel", content,
		func(confirm bool) {
			if confirm {
				p.signAndRevoke(prep, ap, info)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(620, 480))
	if !ready {
		d.SetConfirmText("Unlock to sign")
	}
	d.Show()
}

func (p *approvalsPane) revokeSignAvailability(from common.Address) (bool, string) {
	s, _, ok := p.app.currentSigner()
	if !ok {
		return false, "No wallet is unlocked. Unlock this wallet in the Wallets tab, then sign."
	}
	if s.Address() != from {
		return false, "The unlocked wallet does not match this account. Unlock " + address.Short(from) + " to sign."
	}
	return true, "Ready to sign with the unlocked wallet."
}

// signAndRevoke signs and broadcasts the revocation, logs it, tracks inclusion,
// and drops the row once mined.
func (p *approvalsPane) signAndRevoke(prep tx.Prepared, ap approvals.Approval, info chain.Info) {
	ready, msg := p.revokeSignAvailability(prep.Send.From)
	if !ready {
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
		return
	}
	signerObj, _, _ := p.app.currentSigner()
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connection lost; reconnect and try again"), p.app.window)
		return
	}
	client := conn.Client

	rec := history.Record{
		ChainID:       info.ID,
		WalletAddress: address.Format(prep.Send.From),
		Kind:          "revoke",
		Instructions:  fmt.Sprintf("Revoke %s approval: %s → %s", ap.Layer.String(), ap.DisplayToken(), ap.DisplaySpender()),
		ToAddress:     address.Format(prep.Send.Call.To),
		ValueWei:      "0",
		Status:        history.StatusPrepared,
	}
	recID := p.insertHistory(rec)

	p.status.SetText("Signing…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		signed, err := signerObj.SignTx(ctx, prep.Tx, prep.ChainID)
		if err != nil {
			p.revokeError(recID, "sign: "+err.Error())
			return
		}
		hash, err := tx.Broadcast(ctx, client, signed)
		if err != nil {
			p.revokeError(recID, err.Error())
			return
		}
		p.markSubmitted(recID, hash.Hex())
		fyne.Do(func() {
			p.status.SetText("Revoke submitted: " + hash.Hex())
			p.notifyHistory()
			body := container.NewVBox(
				widget.NewLabel("Revocation submitted. Waiting for inclusion…"),
				monoHyperlink(hash.Hex(), info.TxURL(hash.Hex())),
			)
			dialog.ShowCustom("Revoke approval", "Close", body, p.app.window)
		})
		p.trackRevoke(recID, conn.Client, hash, ap, info)
	}()
}

// trackRevoke waits for the receipt, updates history, and removes the row on
// success (the allowance is now zero).
func (p *approvalsPane) trackRevoke(recID int64, client rpc.Client, hash common.Hash, ap approvals.Approval, info chain.Info) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	receipt, err := tx.WaitForReceipt(ctx, client, hash)
	if err != nil {
		fyne.Do(func() { p.status.SetText("Could not confirm revocation: " + err.Error()) })
		return
	}
	success := tx.Succeeded(receipt)
	block := receipt.BlockNumber.Int64()
	var blockTime int64
	if head, herr := client.HeaderByNumber(ctx, receipt.BlockNumber); herr == nil && head != nil {
		blockTime = int64(head.Time)
	}
	p.markIncluded(recID, block, blockTime, success)
	fyne.Do(func() {
		p.notifyHistory()
		if success {
			p.removeItem(ap)
			p.status.SetText(fmt.Sprintf("Revoked %s → %s (block %d).", ap.DisplayToken(), ap.DisplaySpender(), block))
		} else {
			p.status.SetText("Revocation transaction reverted.")
		}
	})
}

// removeItem drops the revoked approval from the list.
func (p *approvalsPane) removeItem(ap approvals.Approval) {
	out := p.items[:0]
	for _, it := range p.items {
		if it.Layer == ap.Layer && it.Token == ap.Token && it.Spender == ap.Spender {
			continue
		}
		out = append(out, it)
	}
	p.items = out
	p.list.Refresh()
}

// --- history helpers (nil-safe for tests without a store) -------------------

func (p *approvalsPane) insertHistory(rec history.Record) int64 {
	if p.app.history == nil {
		return 0
	}
	id, err := p.app.history.Insert(rec)
	if err != nil {
		return 0
	}
	return id
}

func (p *approvalsPane) markSubmitted(id int64, hash string) {
	if p.app.history != nil && id != 0 {
		_ = p.app.history.MarkSubmitted(id, hash)
	}
}

func (p *approvalsPane) markIncluded(id, block, blockTime int64, success bool) {
	if p.app.history != nil && id != 0 {
		_ = p.app.history.MarkIncluded(id, block, blockTime, success)
	}
}

func (p *approvalsPane) revokeError(id int64, msg string) {
	if p.app.history != nil && id != 0 {
		_ = p.app.history.MarkError(id, msg)
	}
	fyne.Do(func() {
		p.status.SetText("Failed: " + msg)
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
		p.notifyHistory()
	})
}

func (p *approvalsPane) notifyHistory() {
	if p.app.historyReload != nil {
		p.app.historyReload()
	}
}

// --- row ---------------------------------------------------------------------

// approvalRow renders one approval: token → spender on top, amount/flags beneath,
// and a red Revoke button on the right.
type approvalRow struct {
	widget.BaseWidget
	title  *widget.Label
	sub    *widget.Label
	revoke *widget.Button
}

func newApprovalRow() *approvalRow {
	r := &approvalRow{
		title:  monoLabel("token → spender"),
		sub:    monoLabel("amount"),
		revoke: widget.NewButton("Revoke", nil),
	}
	r.revoke.Importance = widget.DangerImportance
	r.ExtendBaseWidget(r)
	return r
}

func (r *approvalRow) CreateRenderer() fyne.WidgetRenderer {
	left := container.NewVBox(r.title, r.sub)
	return widget.NewSimpleRenderer(container.NewBorder(nil, nil, nil, r.revoke, left))
}

// set fills the row from an approval.
func (r *approvalRow) set(ap approvals.Approval) {
	r.title.SetText(ap.DisplayToken() + "  →  " + ap.DisplaySpender())
	r.sub.SetText(approvalAmountLine(ap))
}

// approvalAmountLine formats the amount/flags subtitle.
func approvalAmountLine(ap approvals.Approval) string {
	amount := "UNLIMITED"
	if !ap.Unlimited {
		amount = assets.FormatUnits(ap.Amount, ap.TokenDecimals)
		if ap.TokenSymbol != "" {
			amount += " " + ap.TokenSymbol
		}
	}
	line := amount + "   ·   " + ap.Layer.String()
	if ap.Expiration > 0 {
		line += " · expires " + time.Unix(ap.Expiration, 0).Local().Format("2006-01-02")
	}
	return line
}
