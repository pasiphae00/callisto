package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"rsc.io/qr"
)

// showAddressQR pops a dialog with a scannable QR code of a receive address, the
// address in monospace below it, and a Copy button. The QR encodes the plain
// checksummed address (maximally compatible — any wallet scanning it gets the address).
func showAddressQR(app *App, title, addr string) {
	code, err := qr.Encode(addr, qr.M)
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not generate QR code: %w", err), app.window)
		return
	}
	img := canvas.NewImageFromResource(fyne.NewStaticResource("address-qr.png", code.PNG()))
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScalePixels // nearest-neighbor: keep the QR crisp when scaled up
	img.SetMinSize(fyne.NewSize(240, 240))

	addrLbl := monoLabel(addr)
	addrLbl.Wrapping = fyne.TextWrapBreak
	copyBtn := widget.NewButton("Copy address", func() { app.fyneApp.Clipboard().SetContent(addr) })

	body := container.NewVBox(
		container.NewCenter(img),
		addrLbl,
		container.NewCenter(copyBtn),
	)
	d := dialog.NewCustom(title, "Close", body, app.window)
	d.Resize(fyne.NewSize(340, 470))
	d.Show()
}
