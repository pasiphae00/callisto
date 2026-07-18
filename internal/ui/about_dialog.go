package ui

import (
	_ "embed"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/buildinfo"
)

//go:embed images/logo.png
var appLogoPNG []byte

//go:embed images/callisto-transparent.png
var aboutLogoPNG []byte

// appIcon is the app/window/dock icon resource (the simple flat logo).
var appIcon = fyne.NewStaticResource("logo.png", appLogoPNG)

var (
	aboutText = color.NRGBA{R: 0xaa, G: 0xaa, B: 0xaa, A: 0xff}
	aboutDim  = color.NRGBA{R: 0x77, G: 0x77, B: 0x77, A: 0xff}
)

// showAbout displays the About Callisto dialog: gray Berkeley Mono text over a
// background matching the dialog's own chrome (so the content area reads as one
// continuous surface instead of a mismatched inset box — the app runs in dark
// mode by default, so this is black/near-black in practice), the transparent
// Callisto logo, version/commit, and a small disclaimer.
func showAbout(a *App) {
	logo := canvas.NewImageFromResource(fyne.NewStaticResource("callisto-transparent.png", aboutLogoPNG))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(160, 160))

	title := grayMonoText("Callisto "+buildinfo.Version, 20, true)
	commit := grayMonoText("commit "+buildinfo.ShortCommit(), 13, false)
	tagline := grayMonoText("Open-source Ethereum transaction and wallet management utility", 13, false)
	link := grayMonoText("callisto.pasiphae.io", 13, false)
	copyright := grayMonoText("©2026", 13, false)

	disclaimer := canvas.NewText("trust but verify; use at your own risk", aboutDim)
	disclaimer.TextStyle = fyne.TextStyle{Monospace: true, Italic: true}
	disclaimer.TextSize = 11
	disclaimer.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(
		container.NewCenter(logo),
		container.NewCenter(title),
		container.NewCenter(commit),
		widget.NewSeparator(),
		container.NewCenter(tagline),
		container.NewCenter(link),
		container.NewCenter(copyright),
		layout.NewSpacer(),
		container.NewCenter(disclaimer),
	)
	padded := container.NewPadded(content)

	bg := canvas.NewRectangle(dialogBackgroundColor())
	stack := container.NewStack(bg, padded)
	stack.Resize(fyne.NewSize(360, 420))

	d := dialog.NewCustom("About Callisto", "Close", stack, a.window)
	d.Resize(fyne.NewSize(360, 420))
	d.Show()
}

// dialogBackgroundColor reads the theme's overlay background color — what
// dialogs actually render on (ColorNameBackground is the main window's
// background, a different, lighter color; using it here left a visible seam
// against the real dialog chrome, confirmed with a color-picker measurement:
// RGB(24,29,37), which is exactly colorDarkOverlayBackground). Reading it live
// (rather than hardcoding that RGB triple) keeps this correct if the user's
// theme/variant changes.
func dialogBackgroundColor() color.Color {
	settings := fyne.CurrentApp().Settings()
	return settings.Theme().Color(theme.ColorNameOverlayBackground, settings.ThemeVariant())
}

// grayMonoText builds a centered, gray Berkeley Mono text element at the given
// size, optionally bold.
func grayMonoText(s string, size float32, bold bool) *canvas.Text {
	t := canvas.NewText(s, aboutText)
	t.TextStyle = fyne.TextStyle{Monospace: true, Bold: bold}
	t.TextSize = size
	t.Alignment = fyne.TextAlignCenter
	return t
}
