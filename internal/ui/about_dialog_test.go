package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/pasiphae00/callisto/internal/config"
)

func TestEmbeddedLogosPresent(t *testing.T) {
	if len(appLogoPNG) == 0 {
		t.Error("app logo (images/logo.png) was not embedded")
	}
	if len(aboutLogoPNG) == 0 {
		t.Error("about-dialog logo (images/callisto-transparent.png) was not embedded")
	}
}

func TestShowAboutDoesNotPanic(t *testing.T) {
	a := New(&config.Config{}, nil)
	a.fyneApp = test.NewApp()
	// The real app always applies the embedded Berkeley Mono theme before
	// building any window (see Run); the default Fyne test theme has no
	// monospace-bold face, which the About dialog's bold mono title needs.
	a.applyMonoFont()
	a.window = test.NewWindow(nil)
	defer a.window.Close()

	// Reaching here without panicking confirms the dialog content builds and
	// lays out correctly (canvas.Text/Image, embedded resources, stacking).
	showAbout(a)
}

// TestAboutBackgroundMatchesDialogChrome pins the dark theme's overlay
// background — the color Fyne dialogs actually render on, and what
// dialogBackgroundColor is meant to read — to the exact value confirmed with a
// color-picker measurement of the real running dialog: RGB(24,29,37). An
// earlier version read ColorNameBackground (the main window's background, a
// different/lighter color) instead, leaving a visible seam against the real
// dialog chrome. dialogBackgroundColor's own correctness (delegating to
// theme.ColorNameOverlayBackground) is a direct, obviously-correct one-line
// read exercised for panics by TestShowAboutDoesNotPanic; what's worth pinning
// here is the color value itself.
func TestAboutBackgroundMatchesDialogChrome(t *testing.T) {
	got := theme.DefaultTheme().Color(theme.ColorNameOverlayBackground, theme.VariantDark)
	nrgba, ok := got.(color.NRGBA)
	if !ok {
		t.Fatalf("overlay background color type = %T, want color.NRGBA", got)
	}
	if nrgba.R != 24 || nrgba.G != 29 || nrgba.B != 37 {
		t.Errorf("overlay background = rgb(%d,%d,%d), want rgb(24,29,37)", nrgba.R, nrgba.G, nrgba.B)
	}
}
