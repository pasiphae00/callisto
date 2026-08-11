package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/rpc"
	"github.com/pasiphae00/callisto/internal/tx"
)

func TestSetDefaultSelected(t *testing.T) {
	test.NewApp()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	cfg := &config.Config{}
	_ = cfg.UpsertEndpoint(rpc.Endpoint{Name: "a", URL: "https://a.example", AutoConnect: true})
	_ = cfg.UpsertEndpoint(rpc.Endpoint{Name: "b", URL: "https://b.example"})

	p := newSettingsPane(New(cfg, nil))
	_ = p.build()

	// Make "b" the default; it becomes exclusive.
	p.selected = 1
	p.setDefaultSelected()

	got, _ := cfg.AutoConnectEndpoint()
	if got.Name != "b" {
		t.Errorf("auto-connect default = %q, want b", got.Name)
	}
	if a, _ := cfg.EndpointByName("a"); a.AutoConnect {
		t.Error("previous default should have been cleared (exclusive)")
	}
	// Persisted.
	if reloaded, err := config.Load(); err != nil {
		t.Fatal(err)
	} else if d, _ := reloaded.AutoConnectEndpoint(); d.Name != "b" {
		t.Errorf("persisted default = %q, want b", d.Name)
	}
}

func TestSaveEditRenameAndURL(t *testing.T) {
	test.NewApp()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	cfg := &config.Config{}
	_ = cfg.UpsertEndpoint(rpc.Endpoint{Name: "old", URL: "https://old.example", AutoConnect: true})
	cfg.ActiveEndpoint = "old"
	p := newSettingsPane(New(cfg, nil))
	_ = p.build()

	// Rename the label and change the URL; default + active selection must carry.
	p.saveEdit("old", "new", "https://new.example", true)

	if _, ok := cfg.EndpointByName("old"); ok {
		t.Error("old endpoint should be gone after rename")
	}
	got, ok := cfg.EndpointByName("new")
	if !ok || got.URL != "https://new.example" {
		t.Errorf("renamed endpoint = %+v (ok=%v)", got, ok)
	}
	if cfg.ActiveEndpoint != "new" {
		t.Errorf("active endpoint should follow the rename, got %q", cfg.ActiveEndpoint)
	}
	if d, _ := cfg.AutoConnectEndpoint(); d.Name != "new" {
		t.Errorf("default should follow the rename, got %q", d.Name)
	}
}

func TestMonoHyperlink(t *testing.T) {
	test.NewApp()
	// Valid URL → a clickable hyperlink in the mono style.
	obj := monoHyperlink("0xabc", "https://etherscan.io/tx/0xabc")
	h, ok := obj.(*widget.Hyperlink)
	if !ok {
		t.Fatalf("expected *widget.Hyperlink, got %T", obj)
	}
	if !h.TextStyle.Monospace {
		t.Error("hyperlink should use the monospace style")
	}
	// No URL → a plain mono label (still shows the hash, just not clickable).
	if _, ok := monoHyperlink("0xabc", "").(*widget.Label); !ok {
		t.Error("empty URL should fall back to a mono label")
	}
}

// TestFeesBoxBuildsAndReflectsConfig checks the priority picker constructs under
// the headless driver and opens on the configured tier.
func TestFeesBoxBuildsAndReflectsConfig(t *testing.T) {
	test.NewApp()
	cfg := &config.Config{}
	cfg.SetTxPriorityTier(tx.PriorityRapid)

	p := &settingsPane{app: New(cfg, nil)}
	box := p.buildFeesBox()
	if box == nil {
		t.Fatal("buildFeesBox returned nil")
	}
	w := test.NewWindow(box)
	defer w.Close()

	sel := findSelect(box)
	if sel == nil {
		t.Fatal("no Select in the fees box")
	}
	if sel.Selected != tx.PriorityRapid.Label() {
		t.Errorf("picker shows %q; want the configured %q", sel.Selected, tx.PriorityRapid.Label())
	}
	if len(sel.Options) != len(tx.Priorities()) {
		t.Errorf("picker has %d options; want %d", len(sel.Options), len(tx.Priorities()))
	}
}

// TestFeesPickerWritesConfig checks choosing a tier persists it.
func TestFeesPickerWritesConfig(t *testing.T) {
	test.NewApp()
	cfg := &config.Config{}
	p := &settingsPane{app: New(cfg, nil)}
	sel := findSelect(p.buildFeesBox())
	if sel == nil {
		t.Fatal("no Select in the fees box")
	}

	sel.SetSelected(tx.PriorityStandard.Label())
	if got := cfg.TxPriorityTier(); got != tx.PriorityStandard {
		t.Errorf("config tier = %v after selecting Standard; want Standard", got)
	}
}

func findSelect(o fyne.CanvasObject) *widget.Select {
	switch v := o.(type) {
	case *widget.Select:
		return v
	case *fyne.Container:
		for _, child := range v.Objects {
			if got := findSelect(child); got != nil {
				return got
			}
		}
	}
	return nil
}
