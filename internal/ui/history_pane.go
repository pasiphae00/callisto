package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

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
		p.openInExplorer(id)
	}

	refreshBtn := widget.NewButton("Refresh", func() { p.reload() })
	header := widget.NewLabelWithStyle("History", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	hint := widget.NewLabel("Transactions Callisto has prepared. Select a row to open it in a block explorer.")
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

// openInExplorer opens the selected record's tx on its chain's explorer.
func (p *historyPane) openInExplorer(i int) {
	if i < 0 || i >= len(p.records) {
		return
	}
	rec := p.records[i]
	if rec.TxHash == "" {
		return
	}
	info, _ := chain.Lookup(rec.ChainID)
	if url := info.TxURL(rec.TxHash); url != "" {
		p.app.openURL(url)
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
