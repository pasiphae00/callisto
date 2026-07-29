package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae00/callisto/internal/chain"
)

// copiedFlashFor is how long a Copy button confirms itself before reverting.
const copiedFlashFor = 2 * time.Second

// showTxResult presents a broadcast transaction: the caller's own message rows,
// the full hash in monospace, and a consistent action row.
//
// Every place that hands the user a transaction hash uses this — basic Send,
// WalletConnect, Safe execution, approval revocation, and the inclusion reports
// that follow each of them — so the actions available after a broadcast never
// depend on which pane it came from. Before this, some dialogs rendered the hash
// as a clickable link and others as a plain label with an explorer button, and
// none of them let the user copy the hash without selecting it by hand.
//
// The hash is always shown in full: it is the one piece of state the user may
// need to take somewhere else (a block explorer, a support thread, a wallet on
// another machine), and truncating it would defeat that.
func (a *App) showTxResult(title, hash string, info chain.Info, top ...fyne.CanvasObject) {
	body := container.NewVBox(top...)
	body.Add(monoLabel(hash))
	body.Add(a.txActionRow(hash, info))
	dialog.ShowCustom(title, "Close", body, a.window)
}

// txActionRow builds the Copy hash / View on explorer pair. The explorer button
// is omitted (rather than shown disabled) on a chain with no known explorer —
// there is nothing it could do there.
func (a *App) txActionRow(hash string, info chain.Info) fyne.CanvasObject {
	copyBtn := widget.NewButton("Copy hash", nil)
	copyBtn.OnTapped = func() { a.copyWithFeedback(copyBtn, "Copy hash", hash) }

	link := info.TxURL(hash)
	if link == "" {
		return container.NewHBox(copyBtn)
	}
	return container.NewGridWithColumns(2,
		copyBtn,
		widget.NewButton("View on explorer", func() { a.openURL(link) }),
	)
}

// copyWithFeedback copies text and briefly confirms it on the button itself, so
// the user knows it worked without a second dialog to dismiss.
func (a *App) copyWithFeedback(btn *widget.Button, label, text string) {
	a.copyToClipboard(text)
	btn.SetText("Copied ✓")
	time.AfterFunc(copiedFlashFor, func() {
		fyne.Do(func() { btn.SetText(label) })
	})
}

// copyToClipboard writes to the system clipboard, tolerating the absence of a
// running Fyne app (unit tests construct panes without one).
func (a *App) copyToClipboard(text string) {
	app := a.fyneApp
	if app == nil {
		app = fyne.CurrentApp()
	}
	if app == nil {
		return
	}
	app.Clipboard().SetContent(text)
}
