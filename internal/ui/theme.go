package ui

import (
	_ "embed"
	"image/color"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/config"
)

// BerkeleyMono is bundled and embedded under the project's font license so that
// distributed binaries carry it (no runtime font files needed). A user may still
// override it via CALLISTO_FONT_DIR (see fontSearchDirs).
//
//go:embed fonts/BerkeleyMono-Regular.otf
var embeddedMonoRegular []byte

//go:embed fonts/BerkeleyMono-Bold.otf
var embeddedMonoBold []byte

// monoLabel returns a label rendered in the monospace font — used for rows and
// values containing addresses, hashes, and numeric amounts.
func monoLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle = fyne.TextStyle{Monospace: true}
	return l
}

// monoTheme wraps the default Fyne theme and substitutes a monospace font (e.g.
// BerkeleyMono) for text tagged with a monospace style — used for addresses,
// hashes, and numeric amounts. Everything else falls through to the base theme.
type monoTheme struct {
	base     fyne.Theme
	mono     fyne.Resource
	monoBold fyne.Resource
}

func (t *monoTheme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Monospace {
		if s.Bold && t.monoBold != nil {
			return t.monoBold
		}
		if t.mono != nil {
			return t.mono
		}
	}
	return t.base.Font(s)
}

func (t *monoTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return t.base.Color(n, v)
}

func (t *monoTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.base.Icon(n) }
func (t *monoTheme) Size(n fyne.ThemeSizeName) float32       { return t.base.Size(n) }

// applyMonoFont installs the custom theme if a monospace font is found on disk.
// If none is found, the app keeps Fyne's default theme (and default mono font).
func (a *App) applyMonoFont() {
	regular, bold := loadMonoFont()
	if regular == nil {
		return // no bundled font available; fall back silently
	}
	a.fyneApp.Settings().SetTheme(&monoTheme{
		base:     theme.DefaultTheme(),
		mono:     regular,
		monoBold: bold,
	})
}

// monoFontFiles are the BerkeleyMono files Callisto looks for (regular required,
// bold optional).
const (
	monoRegularFile = "BerkeleyMono-Regular.otf"
	monoBoldFile    = "BerkeleyMono-Bold.otf"
)

// loadMonoFont returns the monospace font: a user override from disk if present
// (CALLISTO_FONT_DIR or the config dir's fonts/), otherwise the embedded
// BerkeleyMono. Regular is required; bold is optional.
func loadMonoFont() (regular, bold fyne.Resource) {
	for _, dir := range fontSearchDirs() {
		if r := tryLoadFont(dir, monoRegularFile); r != nil {
			return r, tryLoadFont(dir, monoBoldFile)
		}
	}
	if len(embeddedMonoRegular) > 0 {
		reg := fyne.NewStaticResource(monoRegularFile, embeddedMonoRegular)
		var b fyne.Resource
		if len(embeddedMonoBold) > 0 {
			b = fyne.NewStaticResource(monoBoldFile, embeddedMonoBold)
		}
		return reg, b
	}
	return nil, nil
}

// fontSearchDirs lists, in priority order, where a user-provided override font
// may live: an explicit env override and the OS config dir's fonts/ folder.
// When neither has a font, the embedded BerkeleyMono is used instead.
func fontSearchDirs() []string {
	var dirs []string
	if d := os.Getenv("CALLISTO_FONT_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if cd, err := config.Dir(); err == nil {
		dirs = append(dirs, filepath.Join(cd, "fonts"))
	}
	return dirs
}

// tryLoadFont reads a font file into a Fyne resource, or returns nil.
func tryLoadFont(dir, name string) fyne.Resource {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	return fyne.NewStaticResource(name, data)
}
