package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/rpc"
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
