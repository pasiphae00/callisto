package ui

import (
	"context"
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae00/callisto/internal/buildinfo"
	"github.com/pasiphae00/callisto/internal/updater"
)

// buildUpdatesBox is the "Application" strip at the bottom of the Settings pane:
// the running version and a Check-for-updates button. Updates are pulled from the
// GitHub releases API and cryptographically verified before install (see
// internal/updater).
func (p *settingsPane) buildUpdatesBox() fyne.CanvasObject {
	label := "Callisto v" + buildinfo.Version
	if c := buildinfo.ShortCommit(); c != "unknown" {
		label += " (" + c + ")"
	}
	version := monoLabel(label)
	checkBtn := widget.NewButton("Check for updates", p.checkForUpdates)
	row := container.NewHBox(version, checkBtn)
	return container.NewVBox(widget.NewSeparator(), indentToText(row))
}

// checkForUpdates queries the release server off the UI thread and routes to the
// up-to-date or update-available flow.
func (p *settingsPane) checkForUpdates() {
	u := updater.New(buildinfo.Version)
	progress := dialog.NewCustomWithoutButtons("Checking for updates…",
		widget.NewLabel("Contacting the release server…"), p.app.window)
	progress.Show()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rel, err := u.Check(ctx)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(err, p.app.window)
				return
			}
			if !rel.Newer {
				dialog.ShowInformation("Up to date",
					"You're running the latest version (v"+buildinfo.Version+").", p.app.window)
				return
			}
			p.showUpdateAvailable(u, rel)
		})
	}()
}

// showUpdateAvailable presents the new version's changelog with Update now / Later.
func (p *settingsPane) showUpdateAvailable(u *updater.Updater, rel *updater.Release) {
	notes := widget.NewRichTextFromMarkdown(rel.Notes)
	notes.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(notes)
	scroll.SetMinSize(fyne.NewSize(480, 260))
	content := container.NewBorder(
		widget.NewLabelWithStyle(rel.Version+" is available", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, nil, nil, scroll)

	d := dialog.NewCustomConfirm("Update available", "Update now", "Later", content,
		func(ok bool) {
			if ok {
				p.applyUpdate(u, rel)
			}
		}, p.app.window)
	d.Resize(fyne.NewSize(580, 440))
	d.Show()
}

// applyUpdate downloads, verifies, and installs rel, then restarts on success.
func (p *settingsPane) applyUpdate(u *updater.Updater, rel *updater.Release) {
	msg := widget.NewLabel("Starting…")
	progress := dialog.NewCustomWithoutButtons("Updating Callisto", msg, p.app.window)
	progress.Show()
	report := func(s string) { fyne.Do(func() { msg.SetText(s) }) }

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := u.Apply(ctx, rel, report)
		fyne.Do(func() {
			progress.Hide()
			switch {
			case errors.Is(err, updater.ErrRelaunching):
				dialog.ShowInformation("Update installed",
					"Callisto "+rel.Version+" is installed and will now restart.", p.app.window)
				// Quit cleanly (runs the WalletConnect/RPC/signer teardown); the
				// updater has scheduled the new version to launch as we exit.
				p.app.fyneApp.Quit()
			case err == nil:
				// Apply only returns nil without relaunching in edge cases; treat as done.
				dialog.ShowInformation("Update installed",
					"Restart Callisto to finish updating.", p.app.window)
			default:
				var manual *updater.ManualInstallError
				if errors.As(err, &manual) {
					dialog.ShowInformation("Almost there",
						"The update was downloaded and verified, but Callisto couldn't replace "+
							"itself automatically (it may be installed somewhere this account can't "+
							"write).\n\nThe verified update was saved to:\n"+manual.Path+
							"\n\nReplace your installed Callisto with it to finish.", p.app.window)
				} else {
					dialog.ShowError(err, p.app.window)
				}
			}
		})
	}()
}
