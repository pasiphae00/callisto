package ui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/address"
	"codeberg.org/pasiphae/callisto/internal/assets"
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
				opts[i] = fmt.Sprintf("%s (%s)", a.Symbol, a.HumanBalance())
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
	p.showSummary(send)
}

// showSummary displays the prepared transfer. In later phases this becomes the
// full pre-sign review with gas and a Sign action.
func (p *sendPane) showSummary(s tx.Send) {
	lines := []string{
		fmt.Sprintf("Send %s %s", assets.FormatUnits(s.Amount, s.Decimals), s.Symbol),
		fmt.Sprintf("To:    %s", address.Format(s.Recipient)),
		fmt.Sprintf("From:  %s", address.Format(s.From)),
	}
	if s.IsNative {
		lines = append(lines, fmt.Sprintf("Type:  native transfer"))
	} else {
		lines = append(lines,
			fmt.Sprintf("Type:  ERC-20 transfer"),
			fmt.Sprintf("Token: %s", address.Format(s.Token)),
			fmt.Sprintf("Data:  0x%x", s.Call.Data),
		)
	}
	lines = append(lines, "", "Gas estimation, review, and signing come next.")

	body := widget.NewLabel(joinLines(lines))
	dialog.ShowCustom("Prepared transfer", "Close", container.NewPadded(body), p.app.window)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
