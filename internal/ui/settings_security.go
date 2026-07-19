package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// autoLockChoices maps the Settings dropdown labels to a minutes value (0 = never).
var autoLockChoices = []struct {
	label   string
	minutes int
}{
	{"Never", 0},
	{"5 minutes", 5},
	{"15 minutes", 15},
	{"30 minutes", 30},
	{"60 minutes", 60},
}

func autoLockLabel(minutes int) string {
	for _, c := range autoLockChoices {
		if c.minutes == minutes {
			return c.label
		}
	}
	return "Never"
}

func autoLockMinutes(label string) int {
	for _, c := range autoLockChoices {
		if c.label == label {
			return c.minutes
		}
	}
	return 0
}

// buildSecurityBox is the Settings → Security strip: auto-lock timeout + lock on
// sleep. Defaults are gentle so they don't interrupt active use.
func (p *settingsPane) buildSecurityBox() fyne.CanvasObject {
	header := widget.NewLabelWithStyle("Security", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	labels := make([]string, len(autoLockChoices))
	for i, c := range autoLockChoices {
		labels[i] = c.label
	}
	lockSel := widget.NewSelect(labels, func(s string) {
		p.app.cfg.Security.AutoLockMinutes = autoLockMinutes(s)
		_ = p.app.cfg.Save()
		p.app.touchActivity() // don't lock instantly off a stale idle timer
	})
	lockSel.Selected = autoLockLabel(p.app.cfg.Security.AutoLockMinutes) // set field directly (no OnChanged at build)

	sleepChk := widget.NewCheck("Lock when the computer wakes from sleep", func(b bool) {
		p.app.cfg.Security.LockOnSleep = b
		_ = p.app.cfg.Save()
	})
	sleepChk.Checked = p.app.cfg.Security.LockOnSleep

	autoRow := container.NewHBox(widget.NewLabel("Auto-lock unlocked wallets after:"), lockSel)
	note := widget.NewLabel("Locking only wipes the in-memory key; unlock again with your passphrase when you need to sign.")
	note.Wrapping = fyne.TextWrapWord

	return container.NewVBox(widget.NewSeparator(), header, indentToText(container.NewVBox(autoRow, sleepChk, note)))
}
