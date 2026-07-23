package hot

import (
	"context"
	"testing"

	"github.com/pasiphae00/callisto/internal/keystore"
)

// TestKeystoreRoundTripDerivesSameAddress verifies that importing to a keystore
// and reopening from it (with only the keystore passphrase) yields exactly the
// same account as a direct mnemonic Open.
func TestKeystoreRoundTripDerivesSameAddress(t *testing.T) {
	path := DefaultPath(0)

	direct, err := Open(junkMnemonic, "", path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := direct.Address()
	direct.Lock()

	ks, err := NewKeystore(junkMnemonic, "", "keystore-pass")
	if err != nil {
		t.Fatalf("NewKeystore: %v", err)
	}
	w, err := OpenFromKeystore(ks, "keystore-pass", path)
	if err != nil {
		t.Fatalf("OpenFromKeystore: %v", err)
	}
	defer w.Lock()
	if got := w.Address(); got != want {
		t.Errorf("keystore-derived address = %s, want %s", got.Hex(), want.Hex())
	}

	// And it can actually sign after a passphrase-only unlock.
	if _, err := w.SignSafeTxHash(context.Background(), [32]byte{}); err != nil {
		t.Errorf("signing after keystore unlock failed: %v", err)
	}
}

func TestOpenFromKeystoreWrongPassphrase(t *testing.T) {
	ks, err := NewKeystore(junkMnemonic, "", "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFromKeystore(ks, "wrong", DefaultPath(0)); err != keystore.ErrBadPassphrase {
		t.Errorf("err = %v, want ErrBadPassphrase", err)
	}
}

// TestKeystoreIncorporatesBip39Passphrase confirms the BIP-39 passphrase is folded
// into the encrypted seed at import (so a different 25th word → different wallet),
// and is not needed again to unlock.
func TestKeystoreIncorporatesBip39Passphrase(t *testing.T) {
	plain, _ := NewKeystore(junkMnemonic, "", "kp")
	hidden, _ := NewKeystore(junkMnemonic, "25th-word", "kp")

	wPlain, err := OpenFromKeystore(plain, "kp", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer wPlain.Lock()
	wHidden, err := OpenFromKeystore(hidden, "kp", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer wHidden.Lock()

	if wPlain.Address() == wHidden.Address() {
		t.Error("a BIP-39 passphrase should derive a distinct wallet")
	}
}

func TestPreviewAccountsMatchesDerive(t *testing.T) {
	preview, err := PreviewAccounts(junkMnemonic, "", 0, 3)
	if err != nil {
		t.Fatalf("PreviewAccounts: %v", err)
	}
	if len(preview) != 3 {
		t.Fatalf("preview len = %d, want 3", len(preview))
	}

	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()
	direct, err := w.DeriveAccounts(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := range preview {
		if preview[i].Address != direct[i].Address || preview[i].Index != direct[i].Index {
			t.Errorf("preview[%d] = %+v, want %+v", i, preview[i], direct[i])
		}
	}
	// The first preview address must match the well-known junk account 0.
	if preview[0].Address.Hex() != junkAcct0 {
		t.Errorf("preview[0] = %s, want %s", preview[0].Address.Hex(), junkAcct0)
	}
}

func TestNewKeystoreRejectsBadInputs(t *testing.T) {
	if _, err := NewKeystore("not a valid mnemonic", "", "kp"); err != ErrInvalidMnemonic {
		t.Errorf("invalid mnemonic err = %v, want ErrInvalidMnemonic", err)
	}
	if _, err := NewKeystore(junkMnemonic, "", ""); err == nil {
		t.Error("empty keystore passphrase should error")
	}
}
