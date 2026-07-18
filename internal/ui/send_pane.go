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
	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/history"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/tx"
)

// sendPane is the basic-transfer flow shared by ETH and ERC-20: pick an asset,
// enter a recipient (address or ENS) and amount (with Max), and prepare the
// transfer. Preparing produces a tx.Send (recipient/value/calldata); gas
// estimation, review, and broadcast are added in later phases.
type sendPane struct {
	app *App

	assetSelect *widget.Select
	recipient   *addressField
	amount      *widget.Entry
	maxBtn      *widget.Button
	prepareBtn  *widget.Button
	status      *widget.Label

	items []assets.Asset
}

func newSendPane(a *App) *sendPane {
	return &sendPane{app: a}
}

func (p *sendPane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord

	p.assetSelect = widget.NewSelect(nil, func(string) { p.updatePrepareState() })
	p.assetSelect.PlaceHolder = "Select an asset"

	p.recipient = newAddressField(p.app.currentResolver, p.updatePrepareState)

	p.amount = widget.NewEntry()
	p.amount.SetPlaceHolder("0.0")
	p.amount.OnChanged = func(string) { p.updatePrepareState() }
	p.maxBtn = widget.NewButton("Max", p.fillMax)

	p.prepareBtn = widget.NewButton("Prepare transfer", p.prepare)
	p.prepareBtn.Importance = widget.HighImportance

	refreshBtn := widget.NewButton("Refresh assets", func() { p.reload() })

	amountRow := container.NewBorder(nil, nil, nil, p.maxBtn, p.amount)
	form := widget.NewForm(
		widget.NewFormItem("Asset", p.assetSelect),
		widget.NewFormItem("To", p.recipient.container()),
		widget.NewFormItem("Amount", amountRow),
	)

	header := widget.NewLabelWithStyle("Send", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	top := container.NewVBox(
		header,
		container.NewHBox(refreshBtn),
		form,
		container.NewHBox(p.prepareBtn),
		p.status,
	)

	p.reload()
	return container.NewVScroll(top)
}

// reload refreshes the asset choices for the active wallet/connection.
func (p *sendPane) reload() {
	desc, ok := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	if !ok {
		p.setUnavailable("Select a wallet in the Wallets tab.")
		return
	}
	addr, err := address.Parse(desc.Address)
	if err != nil {
		p.setUnavailable("This wallet has an invalid address on record.")
		return
	}
	conn, ok := p.app.rpc.Active()
	if !ok {
		p.setUnavailable("Connect an RPC endpoint in Settings.")
		return
	}

	p.status.SetText("Loading assets…")
	client := conn.Client
	chainID := conn.ChainID.Uint64()
	userTokens := p.app.cfg.TokensForChain(chainID)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		got, loadErr := assets.NewService(client, chainID).Load(ctx, addr, userTokens)
		fyne.Do(func() {
			if loadErr != nil {
				p.setUnavailable("Could not load assets: " + loadErr.Error())
				return
			}
			p.items = got
			opts := make([]string, len(got))
			for i, a := range got {
				opts[i] = fmt.Sprintf("%s (%s)", a.Symbol, assets.FormatDisplay(a.Balance, a.Decimals, assets.DisplayDecimals))
			}
			p.assetSelect.Options = opts
			p.assetSelect.Refresh()
			p.status.SetText(fmt.Sprintf("Ready · %s · %s", desc.Label, conn.ChainInfo.Name))
			p.updatePrepareState()
		})
	}()
}

func (p *sendPane) setUnavailable(msg string) {
	p.items = nil
	if p.assetSelect != nil {
		p.assetSelect.Options = nil
		p.assetSelect.ClearSelected()
		p.assetSelect.Refresh()
	}
	p.status.SetText(msg)
	p.updatePrepareState()
}

// selectedAsset returns the asset matching the current dropdown selection.
func (p *sendPane) selectedAsset() (assets.Asset, bool) {
	i := p.assetSelect.SelectedIndex()
	if i < 0 || i >= len(p.items) {
		return assets.Asset{}, false
	}
	return p.items[i], true
}

// fillMax sets the amount to the selected asset's full balance. For the native
// asset a gas reserve is applied later at the review/gas stage.
func (p *sendPane) fillMax() {
	a, ok := p.selectedAsset()
	if !ok {
		return
	}
	p.amount.SetText(assets.FormatUnits(a.Balance, a.Decimals))
	p.updatePrepareState()
}

// updatePrepareState enables Prepare only when asset, recipient, and a positive,
// well-formed amount are all present.
func (p *sendPane) updatePrepareState() {
	if p.prepareBtn == nil {
		return
	}
	ready := false
	if a, ok := p.selectedAsset(); ok {
		if _, valid := p.recipient.Address(); valid {
			if v, err := assets.ParseUnits(p.amount.Text, a.Decimals); err == nil && v.Sign() > 0 {
				ready = true
			}
		}
	}
	if ready {
		p.prepareBtn.Enable()
	} else {
		p.prepareBtn.Disable()
	}
}

// prepare validates inputs, builds the tx.Send, and shows a summary. Signing and
// broadcast are wired in a later phase.
func (p *sendPane) prepare() {
	a, ok := p.selectedAsset()
	if !ok {
		return
	}
	recipient, ok := p.recipient.Address()
	if !ok {
		dialog.ShowError(fmt.Errorf("enter a valid recipient address or ENS name"), p.app.window)
		return
	}
	amount, err := assets.ParseUnits(p.amount.Text, a.Decimals)
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}
	if a.Balance != nil && amount.Cmp(a.Balance) > 0 {
		dialog.ShowError(fmt.Errorf("amount exceeds balance (%s %s available)",
			assets.FormatUnits(a.Balance, a.Decimals), a.Symbol), p.app.window)
		return
	}

	desc, _ := p.app.cfg.WalletByID(p.app.cfg.ActiveWallet)
	from, _ := address.Parse(desc.Address)

	var send tx.Send
	if a.Kind == assets.Native {
		send, err = tx.BuildNativeSend(from, recipient, amount, a.Symbol, a.Decimals)
	} else {
		send, err = tx.BuildERC20Send(from, a.Contract, recipient, amount, a.Symbol, a.Decimals)
	}
	if err != nil {
		dialog.ShowError(err, p.app.window)
		return
	}

	// Estimate gas + assemble the transaction off the UI thread, then review.
	conn, ok := p.app.rpc.Active()
	if !ok {
		dialog.ShowError(fmt.Errorf("connect an RPC endpoint first"), p.app.window)
		return
	}
	p.prepareBtn.Disable()
	p.status.SetText("Estimating gas…")
	client := conn.Client
	chainID := new(big.Int).Set(conn.ChainID)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		prep, prepErr := tx.Prepare(ctx, client, chainID, send)
		fyne.Do(func() {
			p.prepareBtn.Enable()
			p.updatePrepareState()
			if prepErr != nil {
				dialog.ShowError(fmt.Errorf("could not prepare transaction: %w", prepErr), p.app.window)
				p.status.SetText("Ready")
				return
			}
			p.showReview(prep, conn.ChainInfo)
		})
	}()
}

// showReview presents the full pre-sign review: decoded transfer, fees, and a
// Sign & send action enabled only when the matching wallet is unlocked.
func (p *sendPane) showReview(prep tx.Prepared, info chain.Info) {
	s := prep.Send
	nativeSym := info.Native.Symbol

	rows := [][2]string{
		{"Amount", fmt.Sprintf("%s %s", assets.FormatUnits(s.Amount, s.Decimals), s.Symbol)},
		{"To", address.Format(s.Recipient)},
		{"From", address.Format(s.From)},
		{"Network", info.Name},
		{"Nonce", fmt.Sprintf("%d", prep.Nonce)},
	}
	if !s.IsNative {
		rows = append(rows,
			[2]string{"Type", "ERC-20 transfer"},
			[2]string{"Token", address.Format(s.Token)},
			[2]string{"Call", fmt.Sprintf("transfer(%s, %s)", address.Short(s.Recipient), assets.FormatUnits(s.Amount, s.Decimals))},
		)
	} else {
		rows = append(rows, [2]string{"Type", "Native transfer"})
	}
	rows = append(rows,
		[2]string{"Gas limit", fmt.Sprintf("%d", prep.Fees.GasLimit)},
		[2]string{"Base fee", assets.FormatUnits(prep.Fees.BaseFee, 9) + " gwei"},
		[2]string{"Priority tip", assets.FormatUnits(prep.Fees.GasTipCap, 9) + " gwei"},
		[2]string{"Max fee/gas", assets.FormatUnits(prep.Fees.GasFeeCap, 9) + " gwei"},
		[2]string{"Max total fee", assets.FormatUnits(prep.Fees.MaxFeeWei(), 18) + " " + nativeSym},
	)

	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		key := widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		grid.Add(key)
		grid.Add(monoLabel(r[1])) // values: addresses, amounts, fees
	}

	// Determine whether we can sign right now.
	signReady, signMsg := p.signAvailability(s.From)
	notice := widget.NewLabel(signMsg)
	notice.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(grid, widget.NewSeparator(), notice)

	d := dialog.NewCustomConfirm("Review transaction", "Sign & send", "Cancel", content,
		func(confirm bool) {
			if confirm {
				p.signAndSend(prep, info)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(640, 520))
	// Only allow confirming when a matching unlocked signer is available.
	if !signReady {
		d.SetConfirmText("Unlock to sign")
	}
	d.Show()
	if !signReady {
		// Fyne has no per-dialog disable of the confirm button, so guard in the
		// handler: if not ready, signAndSend re-checks and shows an error.
	}
}

// signAvailability reports whether the active signer can sign for `from`.
func (p *sendPane) signAvailability(from common.Address) (bool, string) {
	s, _, ok := p.app.currentSigner()
	if !ok {
		return false, "No wallet is unlocked. Unlock the sending wallet in the Wallets tab, then sign."
	}
	if s.Address() != from {
		return false, "The unlocked wallet does not match the sender. Unlock " + address.Short(from) + " to sign."
	}
	return true, "Ready to sign with the unlocked wallet."
}

// signAndSend signs the prepared transaction with the active signer, broadcasts
// it, records history, and tracks inclusion — all off the UI thread.
func (p *sendPane) signAndSend(prep tx.Prepared, info chain.Info) {
	from := prep.Send.From
	ready, msg := p.signAvailability(from)
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

	// Record the intent up front so history reflects even a failed submit.
	rec := history.Record{
		ChainID:       info.ID,
		WalletAddress: address.Format(from),
		Kind:          sendKind(prep.Send),
		Instructions:  sendSummary(prep.Send),
		ToAddress:     address.Format(prep.Send.Recipient),
		ValueWei:      prep.Send.Amount.String(),
		Status:        history.StatusPrepared,
	}
	recID := p.insertHistory(rec)

	p.status.SetText("Signing…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		signed, err := signerObj.SignTx(ctx, prep.Tx, prep.ChainID)
		if err != nil {
			p.finishError(recID, "sign: "+err.Error())
			return
		}
		hash, err := tx.Broadcast(ctx, client, signed)
		if err != nil {
			p.finishError(recID, err.Error())
			return
		}
		p.markSubmitted(recID, hash.Hex())

		fyne.Do(func() {
			p.status.SetText("Submitted: " + hash.Hex())
			p.showBroadcastResult(hash.Hex(), info)
			p.notifyHistory()
		})

		// Track inclusion in the background (own context, survives the dialog).
		go p.trackInclusion(recID, client, hash, info)
	}()
}

// trackInclusion waits for the receipt, records the outcome, and notifies the
// user. It uses its own context so it outlives the review dialog.
func (p *sendPane) trackInclusion(recID int64, client rpc.Client, hash common.Hash, info chain.Info) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	receipt, err := tx.WaitForReceipt(ctx, client, hash)
	if err != nil {
		fyne.Do(func() { p.status.SetText("Could not confirm inclusion: " + err.Error()) })
		return
	}
	success := tx.Succeeded(receipt)
	blockNum := receipt.BlockNumber.Int64()

	// Fetch the block timestamp (best-effort).
	var blockTime int64
	if head, herr := client.HeaderByNumber(ctx, receipt.BlockNumber); herr == nil && head != nil {
		blockTime = int64(head.Time)
	}
	p.markIncluded(recID, blockNum, blockTime, success)

	fyne.Do(func() {
		outcome := "succeeded"
		if !success {
			outcome = "failed"
		}
		p.status.SetText(fmt.Sprintf("Tx %s in block %d", outcome, blockNum))
		p.notifyHistory()
		p.showInclusionResult(hash.Hex(), blockNum, blockTime, success, info)
	})
}

// showBroadcastResult shows the submitted hash with a link to the explorer.
func (p *sendPane) showBroadcastResult(hash string, info chain.Info) {
	body := container.NewVBox(
		widget.NewLabel("Transaction submitted. Waiting for inclusion…"),
		monoLabel(hash),
	)
	if link := info.TxURL(hash); link != "" {
		body.Add(widget.NewButton("View on explorer", func() { p.app.openURL(link) }))
	}
	dialog.ShowCustom("Broadcast", "Close", body, p.app.window)
}

// showInclusionResult reports the mined outcome. Field values (status, block,
// time) render in the monospace font; the labels stay in the default font.
func (p *sendPane) showInclusionResult(hash string, block, blockTime int64, success bool, info chain.Info) {
	title := "Transaction included"
	status := "success ✓"
	if !success {
		title = "Transaction reverted"
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
	// Refresh balances after a state change.
	p.reload()
}

// --- history helpers (nil-safe for tests without a store) -------------------

func (p *sendPane) insertHistory(rec history.Record) int64 {
	if p.app.history == nil {
		return 0
	}
	id, err := p.app.history.Insert(rec)
	if err != nil {
		return 0
	}
	return id
}

func (p *sendPane) markSubmitted(id int64, hash string) {
	if p.app.history != nil && id != 0 {
		_ = p.app.history.MarkSubmitted(id, hash)
	}
}

func (p *sendPane) markIncluded(id, block, blockTime int64, success bool) {
	if p.app.history != nil && id != 0 {
		_ = p.app.history.MarkIncluded(id, block, blockTime, success)
	}
}

// finishError records a terminal error and surfaces it on the UI thread.
func (p *sendPane) finishError(id int64, msg string) {
	if p.app.history != nil && id != 0 {
		_ = p.app.history.MarkError(id, msg)
	}
	fyne.Do(func() {
		p.status.SetText("Failed: " + msg)
		dialog.ShowError(fmt.Errorf("%s", msg), p.app.window)
		p.notifyHistory()
	})
}

func (p *sendPane) notifyHistory() {
	if p.app.historyReload != nil {
		p.app.historyReload()
	}
}

// sendKind classifies a Send for history.
func sendKind(s tx.Send) string {
	if s.IsNative {
		return "send-native"
	}
	return "send-erc20"
}

// sendSummary is a human description stored with the history record.
func sendSummary(s tx.Send) string {
	return fmt.Sprintf("Send %s %s to %s",
		assets.FormatUnits(s.Amount, s.Decimals), s.Symbol, address.Short(s.Recipient))
}
