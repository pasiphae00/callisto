package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"codeberg.org/pasiphae/callisto/internal/ens"
)

func TestAddressFieldValidHex(t *testing.T) {
	test.NewApp()
	f := newAddressField(func() *ens.Resolver { return nil }, nil)
	_ = f.container() // ensure it composes

	f.entry.SetText("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	addr, ok := f.Address()
	if !ok {
		t.Fatal("valid checksummed address should be accepted")
	}
	if addr.Hex() != "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" {
		t.Errorf("resolved = %s", addr.Hex())
	}
}

func TestAddressFieldBadChecksum(t *testing.T) {
	test.NewApp()
	f := newAddressField(func() *ens.Resolver { return nil }, nil)
	// Flipped-case last char -> bad EIP-55 checksum.
	f.entry.SetText("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD")
	if _, ok := f.Address(); ok {
		t.Error("bad checksum address should be rejected")
	}
}

func TestAddressFieldEmptyClears(t *testing.T) {
	test.NewApp()
	changes := 0
	f := newAddressField(func() *ens.Resolver { return nil }, func() { changes++ })
	f.entry.SetText("0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed") // lowercase, valid
	if _, ok := f.Address(); !ok {
		t.Fatal("lowercase address should be valid")
	}
	f.entry.SetText("")
	if _, ok := f.Address(); ok {
		t.Error("cleared field should be invalid")
	}
	if changes == 0 {
		t.Error("onChange should have fired")
	}
}

func TestAddressFieldLowercaseNormalized(t *testing.T) {
	test.NewApp()
	f := newAddressField(func() *ens.Resolver { return nil }, nil)
	f.entry.SetText("0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed")
	addr, ok := f.Address()
	if !ok {
		t.Fatal("lowercase address should be valid")
	}
	// Exposed address is canonical; formatting is checksummed.
	if addr.Hex() != "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" {
		t.Errorf("normalized = %s", addr.Hex())
	}
}
