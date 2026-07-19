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

	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/history"
)

// historyPane lists transactions Callisto has prepared, with status and a link to
// the block explorer. It reloads on demand and whenever a send updates a record.
type historyPane struct {
	app *App

	status   *widget.Label
	list     *widget.List
	records  []history.Record
	selected int
}

func newHistoryPane(a *App) *historyPane {
	return &historyPane{app: a, selected: -1}
}

func (p *historyPane) build() fyne.CanvasObject {
	p.status = widget.NewLabel("")

	p.list = widget.NewList(
		func() int { return len(p.records) },
		func() fyne.CanvasObject { return monoLabel("template") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(historyRow(p.records[i]))
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) {
		p.selected = id
		p.showDetail(id)
	}

	refreshBtn := widget.NewButton("Refresh", func() { p.reload() })
	header := widget.NewLabelWithStyle("History", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel("Transactions Callisto has prepared. Select a row for full details and a block-explorer link.")
	hint.Wrapping = fyne.TextWrapWord

	// Register for post-send refreshes.
	p.app.historyReload = func() { fyne.Do(func() { p.reload() }) }

	top := container.NewVBox(header, hint, indentToText(container.NewHBox(refreshBtn)), p.status, widget.NewSeparator())
	p.reload()
	return container.NewBorder(top, nil, nil, nil, p.list)
}

// reload fetches recent records from the store.
func (p *historyPane) reload() {
	if p.app.history == nil {
		p.status.SetText("History is unavailable (no local database).")
		return
	}
	records, err := p.app.history.List(200)
	if err != nil {
		p.status.SetText("Could not load history: " + err.Error())
		return
	}
	p.records = records
	p.list.UnselectAll()
	p.selected = -1
	p.list.Refresh()
	if len(records) == 0 {
		p.status.SetText("No transactions yet.")
	} else {
		p.status.SetText(fmt.Sprintf("%d transaction(s)", len(records)))
	}
}

// showDetail opens a dialog with the full record: wallet, timeline, block, the
// parsed transaction summary, live gas info (fetched from the receipt when the
// matching chain is connected), and a block-explorer link.
func (p *historyPane) showDetail(i int) {
	if i < 0 || i >= len(p.records) {
		return
	}
	rec := p.records[i]
	info, _ := chain.Lookup(rec.ChainID)
	nativeSym := info.Native.Symbol
	if nativeSym == "" {
		nativeSym = "ETH"
	}

	grid := container.New(layout.NewFormLayout())
	addRow := func(key string, value fyne.CanvasObject) {
		grid.Add(widget.NewLabelWithStyle(key, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		grid.Add(value)
	}
	addText := func(key, value string) {
		if value == "" {
			return
		}
		addRow(key, monoLabel(value))
	}

	addText("Wallet", rec.WalletAddress)
	addText("Type", rec.Kind)
	addText("Details", rec.Instructions)
	addText("To", rec.ToAddress)
	addText("Network", info.Name)
	addRow("Status", monoLabel(fmt.Sprintf("%s %s", statusIcon(rec.Status), string(rec.Status))))
	if rec.BlockNumber > 0 {
		addText("Block", fmt.Sprintf("%d", rec.BlockNumber))
	}
	when := func(ts int64) string {
		if ts <= 0 {
			return ""
		}
		return time.Unix(ts, 0).Local().Format("2006-01-02 15:04:05")
	}
	addText("Prepared", when(rec.PreparedAt))
	addText("Submitted", when(rec.SubmittedAt))
	if t := when(rec.BlockTime); t != "" {
		addText("Mined", t)
	} else {
		addText("Included", when(rec.IncludedAt))
	}

	// Gas info comes from the receipt, fetched live below; show placeholders that
	// fill in (or are hidden) once the fetch returns.
	gasUsed := monoLabel("…")
	gasPrice := monoLabel("…")
	gasFee := monoLabel("…")
	haveGas := rec.TxHash != ""
	if haveGas {
		addRow("Gas used", gasUsed)
		addRow("Gas price", gasPrice)
		addRow("Tx fee", gasFee)
	}
	if rec.Error != "" {
		addText("Error", rec.Error)
	}

	body := container.NewVBox(grid)
	if rec.TxHash != "" {
		body.Add(widget.NewSeparator())
		body.Add(widget.NewLabelWithStyle("Transaction", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		if link := info.TxURL(rec.TxHash); link != "" {
			body.Add(monoHyperlink(rec.TxHash, link))
		} else {
			body.Add(monoLabel(rec.TxHash))
		}
	}

	d := dialog.NewCustom("Transaction details", "Close", container.NewVScroll(body), p.app.window)
	d.Resize(fyne.NewSize(620, 560))
	d.Show()

	if haveGas {
		p.loadGasInfo(rec, nativeSym, gasUsed, gasPrice, gasFee)
	}
}

// loadGasInfo fetches the receipt for rec (when its chain is the active connection)
// and fills the gas labels. Best-effort: on any failure the placeholders show "—".
func (p *historyPane) loadGasInfo(rec history.Record, nativeSym string, gasUsed, gasPrice, gasFee *widget.Label) {
	conn, ok := p.app.rpc.Active()
	if !ok || conn.ChainID.Uint64() != rec.ChainID {
		setLabels("(connect this chain to load gas)", gasUsed, gasPrice, gasFee)
		return
	}
	client := conn.Client
	hash := common.HexToHash(rec.TxHash)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		receipt, err := client.TransactionReceipt(ctx, hash)
		fyne.Do(func() {
			if err != nil || receipt == nil {
				setLabels("—", gasUsed, gasPrice, gasFee)
				return
			}
			used := new(big.Int).SetUint64(receipt.GasUsed)
			gasUsed.SetText(fmt.Sprintf("%d", receipt.GasUsed))
			if price := receipt.EffectiveGasPrice; price != nil && price.Sign() > 0 {
				gasPrice.SetText(assets.FormatUnits(price, 9) + " gwei")
				fee := new(big.Int).Mul(used, price)
				gasFee.SetText(assets.FormatUnits(fee, 18) + " " + nativeSym)
			} else {
				gasPrice.SetText("—")
				gasFee.SetText("—")
			}
		})
	}()
}

// setLabels sets the same text on several labels (used for gas placeholders).
func setLabels(text string, labels ...*widget.Label) {
	for _, l := range labels {
		l.SetText(text)
	}
}

// historyRow formats a compact one-line summary of a record.
func historyRow(r history.Record) string {
	when := ""
	if r.PreparedAt > 0 {
		when = time.Unix(r.PreparedAt, 0).Local().Format("2006-01-02 15:04")
	}
	icon := statusIcon(r.Status)
	desc := r.Instructions
	if desc == "" {
		desc = r.Kind
	}
	hash := r.TxHash
	if len(hash) > 12 {
		hash = hash[:10] + "…"
	}
	return fmt.Sprintf("%s  %s  ·  %s  ·  %s  %s", icon, when, desc, string(r.Status), hash)
}

func statusIcon(s history.Status) string {
	switch s {
	case history.StatusIncluded:
		return "✓"
	case history.StatusFailed:
		return "✗"
	case history.StatusSubmitted:
		return "…"
	default:
		return "•"
	}
}
