package ui

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestMonoThemeFontSelection(t *testing.T) {
	reg := fyne.NewStaticResource("reg", []byte("x"))
	bold := fyne.NewStaticResource("bold", []byte("y"))
	th := &monoTheme{base: theme.DefaultTheme(), mono: reg, monoBold: bold}

	if th.Font(fyne.TextStyle{Monospace: true}) != reg {
		t.Error("monospace should use the regular mono font")
	}
	if th.Font(fyne.TextStyle{Monospace: true, Bold: true}) != bold {
		t.Error("bold monospace should use the bold mono font")
	}
	if got := th.Font(fyne.TextStyle{}); got == reg || got == bold {
		t.Error("non-monospace text should fall through to the base font")
	}
}

func TestMonoThemeBoldFallsBackToRegular(t *testing.T) {
	reg := fyne.NewStaticResource("reg", []byte("x"))
	th := &monoTheme{base: theme.DefaultTheme(), mono: reg, monoBold: nil}
	if th.Font(fyne.TextStyle{Monospace: true, Bold: true}) != reg {
		t.Error("bold mono should fall back to regular when no bold font is loaded")
	}
}

func TestTryLoadFont(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.otf"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tryLoadFont(dir, "f.otf") == nil {
		t.Error("existing font file should load")
	}
	if tryLoadFont(dir, "missing.otf") != nil {
		t.Error("missing font file should return nil")
	}
}

// TestEmbeddedFontRenders exercises the embedded BerkeleyMono OTF through Fyne,
// confirming it parses and renders (the font is bundled, so this always runs).
func TestEmbeddedFontRenders(t *testing.T) {
	reg, bold := loadMonoFont()
	if reg == nil {
		t.Fatal("embedded BerkeleyMono should always be available")
	}
	test.NewApp().Settings().SetTheme(&monoTheme{base: theme.DefaultTheme(), mono: reg, monoBold: bold})
	w := test.NewWindow(monoLabel("0xd8dA…6045   1.23456 ETH"))
	defer w.Close()
	w.Resize(fyne.NewSize(400, 80))
	// Reaching here without panicking means the OTF renders under Fyne.
}

func TestEmbeddedFontBytesPresent(t *testing.T) {
	if len(embeddedMonoRegular) == 0 {
		t.Error("BerkeleyMono-Regular.otf was not embedded")
	}
	if len(embeddedMonoBold) == 0 {
		t.Error("BerkeleyMono-Bold.otf was not embedded")
	}
}
