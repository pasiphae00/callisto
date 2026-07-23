package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/safe"
	"github.com/pasiphae00/callisto/internal/store"
)

// TestSafePaneBuildsWithImportedSafe verifies the Safe pane constructs, renders a
// cached Safe's details and (empty) proposals, and reflects owner data — all
// without a live connection — under the headless test driver.
func TestSafePaneBuildsWithImportedSafe(t *testing.T) {
	test.NewApp()
	st, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := &config.Config{}
	desc := safe.Descriptor{
		ID:        "s1",
		Label:     "Treasury",
		Address:   "0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1",
		ChainID:   1,
		Threshold: 2,
		Owners: []safe.OwnerLabel{
			{Address: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", Label: "Alice"},
			{Address: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"},
		},
	}
	if err := cfg.UpsertSafe(desc); err != nil {
		t.Fatal(err)
	}
	cfg.ActiveSafe = "s1"

	a := New(cfg, st)
	p := newSafePane(a)
	root := p.build()
	if root == nil {
		t.Fatal("safe pane build returned nil")
	}
	// Selecting the active Safe should have populated the details box.
	if len(p.detailsBox.Objects) == 0 {
		t.Error("details box is empty for an imported Safe")
	}
	// Rendering into a window forces a layout pass (surfaces driver panics).
	w := test.NewWindow(root)
	defer w.Close()
}

// TestSafePaneEmptyState verifies the pane is safe with no Safes configured.
func TestSafePaneEmptyState(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil) // nil store -> nil proposal repo
	p := newSafePane(a)
	root := p.build()
	if root == nil {
		t.Fatal("safe pane build returned nil in empty state")
	}
	w := test.NewWindow(root)
	defer w.Close()
}

// TestIsOwnerAndOwnerAddrs checks the owner-membership helpers used to gate signing.
func TestIsOwnerAndOwnerAddrs(t *testing.T) {
	desc := safe.Descriptor{Owners: []safe.OwnerLabel{
		{Address: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"},
		{Address: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"},
	}}
	if got := ownerAddrs(desc); len(got) != 2 {
		t.Fatalf("ownerAddrs len = %d, want 2", len(got))
	}
	if !isOwner(desc, ownerAddrs(desc)[0]) {
		t.Error("first owner should be recognized")
	}
	stranger := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	if isOwner(desc, stranger) {
		t.Error("a non-owner address should not be recognized as an owner")
	}
}
