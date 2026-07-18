package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/signer/hot"
	"codeberg.org/pasiphae/callisto/internal/wallet"
)

const junkMnemonic = "test test test test test test test test test test test junk"

func TestSignerSessionLifecycle(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil)

	w, err := hot.Open(junkMnemonic, "", hot.DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	a.setSigner("w1", w)

	got, id, ok := a.currentSigner()
	if !ok || id != "w1" || got.Address() != w.Address() {
		t.Fatalf("currentSigner = %v, %q, %v", got, id, ok)
	}

	// clearSigner must lock (wipe) the hot wallet.
	a.clearSigner()
	if _, _, ok := a.currentSigner(); ok {
		t.Error("signer should be cleared")
	}
	if !w.Locked() {
		t.Error("clearSigner must lock (wipe) the signer's key material")
	}
}

func TestSetSignerReplacesAndLocksPrevious(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil)

	w1, _ := hot.Open(junkMnemonic, "", hot.DefaultPath(0))
	w2, _ := hot.Open(junkMnemonic, "", hot.DefaultPath(1))
	a.setSigner("w1", w1)
	a.setSigner("w2", w2) // should lock w1

	if !w1.Locked() {
		t.Error("replaced signer must be locked")
	}
	if w2.Locked() {
		t.Error("new signer must not be locked")
	}
	_, id, _ := a.currentSigner()
	if id != "w2" {
		t.Errorf("active wallet id = %q, want w2", id)
	}
	a.clearSigner()
}

func TestWalletsPaneRowLabel(t *testing.T) {
	test.NewApp()
	cfg := &config.Config{}
	_ = cfg.UpsertWallet(wallet.Descriptor{
		ID:      "w1",
		Label:   "Main",
		Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		Kind:    wallet.KindHot,
	})
	a := New(cfg, nil)
	p := newWalletsPane(a)
	_ = p.build()

	row := p.rowLabel(cfg.Wallets[0])
	if row == "" {
		t.Fatal("empty row label")
	}
	// Locked wallet shows the lock icon; short address is used.
	if want := "🔒"; row[:len(want)] != want {
		t.Errorf("row = %q, want lock icon prefix", row)
	}
}

func TestWalletsPaneDetailAddress(t *testing.T) {
	test.NewApp()
	cfg := &config.Config{}
	full := "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	_ = cfg.UpsertWallet(wallet.Descriptor{ID: "w1", Label: "Main", Address: full, Kind: wallet.KindHot})
	a := New(cfg, nil)
	p := newWalletsPane(a)
	_ = p.build()

	// No selection -> empty detail address.
	if p.detailAddr.Text != "" {
		t.Errorf("detail address with no selection = %q, want empty", p.detailAddr.Text)
	}

	// Selecting the wallet shows its FULL (not shortened) checksummed address.
	p.selected = 0
	p.updateButtons()
	if p.detailAddr.Text != full {
		t.Errorf("detail address = %q, want full address %q", p.detailAddr.Text, full)
	}

	// Attempting to edit it (simulating user keystrokes) reverts — read-only in
	// effect, while the widget itself stays interactive/selectable.
	p.detailAddr.SetText("tampered")
	if p.detailAddr.Text != full {
		t.Errorf("detail address after edit attempt = %q, want reverted to %q", p.detailAddr.Text, full)
	}

	// Deselecting clears it.
	p.selected = -1
	p.updateButtons()
	if p.detailAddr.Text != "" {
		t.Errorf("detail address after deselect = %q, want empty", p.detailAddr.Text)
	}
}
