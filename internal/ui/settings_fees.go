package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae00/callisto/internal/tx"
)

// buildFeesBox is the Settings section for the default transaction fee tier.
//
// The tier sets only the priority fee (the tip paid to whoever includes the
// transaction). The base fee is fixed by the protocol from the parent block's gas
// usage and is identical for every transaction in a block, so no setting can move
// it — the help text says so, because "priority" invites the assumption that it
// makes the whole fee bigger or smaller.
func (p *settingsPane) buildFeesBox() fyne.CanvasObject {
	header := widget.NewLabelWithStyle("Transaction fees", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	summary := widget.NewLabel(
		"How aggressively new transactions bid for inclusion. This sets the priority fee only — " +
			"the base fee is set by the network and is the same for everyone in a block.\n\n" +
			"Tiers are derived from what transactions in recent blocks actually paid, so they " +
			"track real conditions rather than a fixed number. You see the exact fee in the " +
			"review step before signing.")
	summary.Wrapping = fyne.TextWrapWord

	tiers := tx.Priorities()
	labels := make([]string, len(tiers))
	for i, t := range tiers {
		labels[i] = t.Label()
	}

	detail := widget.NewLabel("")
	detail.Wrapping = fyne.TextWrapWord

	sel := widget.NewSelect(labels, func(s string) {
		tier := priorityByLabel(s)
		p.app.cfg.SetTxPriorityTier(tier)
		_ = p.app.cfg.Save()
		detail.SetText(tier.Description())
	})
	// Set the field directly so building the pane doesn't fire OnChanged (and so
	// doesn't write the config on every launch).
	current := p.app.cfg.TxPriorityTier()
	sel.Selected = current.Label()
	detail.SetText(current.Description())

	row := container.NewHBox(widget.NewLabel("Default priority:"), sel)
	return container.NewVBox(
		widget.NewSeparator(),
		header, summary,
		indentToText(row),
		indentToText(detail),
	)
}

// priorityByLabel maps a picker label back to its tier.
func priorityByLabel(label string) tx.Priority {
	for _, t := range tx.Priorities() {
		if t.Label() == label {
			return t
		}
	}
	return tx.DefaultPriority
}
