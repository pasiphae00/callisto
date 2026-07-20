package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// buildAIBox is the Settings → AI features strip: a master enable toggle (off by
// default) and the bring-your-own Anthropic API key. When disabled, no AI client is
// ever built and the key is never read — the whole path stays cold.
func (p *settingsPane) buildAIBox() fyne.CanvasObject {
	header := widget.NewLabelWithStyle("AI features", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	enableChk := widget.NewCheck("Enable AI-assisted transaction preparation", func(b bool) {
		p.app.cfg.AI.Enabled = b
		_ = p.app.cfg.Save()
	})
	enableChk.Checked = p.app.cfg.AI.Enabled

	keyEntry := widget.NewPasswordEntry()
	keyEntry.SetPlaceHolder("sk-ant-… (Anthropic API key)")
	keyEntry.SetText(p.app.cfg.AI.APIKey)

	saveKey := widget.NewButton("Save key", func() {
		p.app.cfg.AI.APIKey = strings.TrimSpace(keyEntry.Text)
		if err := p.app.cfg.Save(); err != nil {
			dialog.ShowError(err, p.app.window)
			return
		}
		dialog.ShowInformation("Saved", "Anthropic API key saved.", p.app.window)
	})
	deleteKey := widget.NewButton("Delete key", func() {
		p.app.cfg.AI.APIKey = ""
		keyEntry.SetText("")
		_ = p.app.cfg.Save()
	})
	keyRow := container.NewBorder(nil, nil, nil, container.NewHBox(saveKey, deleteKey), keyEntry)

	note := widget.NewLabel("Optional and off by default. When enabled, Callisto sends your natural-language request — plus the connected network and the list of available actions — to Anthropic's API using your key, to map it to a supported action. Claude never builds or signs anything: you review the decoded call and confirm, exactly as on the manual path. Bring your own key (usage is billed to you); it's stored in Callisto's local 0600 config — Delete key removes it. Leaving this off keeps all AI features inert.")
	note.Wrapping = fyne.TextWrapWord

	return container.NewVBox(widget.NewSeparator(), header,
		indentToText(container.NewVBox(enableChk, keyRow, note)))
}
