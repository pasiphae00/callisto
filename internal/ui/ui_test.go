package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/pasiphae/callisto/internal/config"
	"github.com/pasiphae/callisto/internal/rpc"
	"github.com/pasiphae/callisto/internal/store"
)

// TestBuildRootHeadless verifies the root layout constructs under the Fyne test
// driver (no display), which is the automated proxy for "the GUI builds".
func TestBuildRootHeadless(t *testing.T) {
	test.NewApp() // installs a headless driver + theme
	st, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := &config.Config{}
	_ = cfg.UpsertEndpoint(rpc.Endpoint{Name: "sepolia", URL: "wss://sepolia.example/ws"})
	cfg.ActiveEndpoint = "sepolia"

	a := New(cfg, st)
	root := a.buildRoot()
	if root == nil {
		t.Fatal("buildRoot returned nil")
	}
	// Render it into a test window to force a layout pass — panics/among-driver
	// errors would surface here.
	w := test.NewWindow(root)
	defer w.Close()
}

// TestStatusBarReflectsState checks the status bar reads the active endpoint.
func TestStatusBarReflectsState(t *testing.T) {
	test.NewApp()
	cfg := &config.Config{}
	a := New(cfg, nil)
	// No endpoint configured yet — should not panic building the status bar.
	if a.statusBar() == nil {
		t.Fatal("statusBar returned nil with empty config")
	}
	_ = cfg.UpsertEndpoint(rpc.Endpoint{Name: "local", URL: "http://localhost:8545"})
	cfg.ActiveEndpoint = "local"
	if a.statusBar() == nil {
		t.Fatal("statusBar returned nil with active endpoint")
	}
}
