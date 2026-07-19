package keystore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var sampleSecret = []byte("a 64-byte BIP-39 seed would live here; any secret bytes work fine!")

func TestEncryptDecryptRoundTrip(t *testing.T) {
	ks, err := Encrypt(sampleSecret, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ks.KDF != "scrypt" || ks.Cipher != "aes-256-gcm" || ks.Version != Version {
		t.Errorf("unexpected header: %+v", ks)
	}
	// Ciphertext must not contain the plaintext.
	ct, _ := hex.DecodeString(ks.Ciphertext)
	if bytes.Contains(ct, sampleSecret) {
		t.Fatal("ciphertext leaks plaintext")
	}

	got, err := ks.Decrypt("correct horse battery staple")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, sampleSecret) {
		t.Errorf("round trip mismatch: got %q", got)
	}
}

func TestRekey(t *testing.T) {
	ks, err := Encrypt(sampleSecret, "old-pass")
	if err != nil {
		t.Fatal(err)
	}
	rk, err := Rekey(ks, "old-pass", "new-pass")
	if err != nil {
		t.Fatal(err)
	}
	// New passphrase recovers the same secret.
	got, err := rk.Decrypt("new-pass")
	if err != nil || !bytes.Equal(got, sampleSecret) {
		t.Fatalf("rekeyed decrypt = %q (err %v)", got, err)
	}
	// Old passphrase no longer works on the rekeyed store.
	if _, err := rk.Decrypt("old-pass"); err != ErrBadPassphrase {
		t.Errorf("old passphrase should fail on rekeyed store, got %v", err)
	}
	// Fresh salt/nonce (not a copy of the original ciphertext).
	if rk.Ciphertext == ks.Ciphertext || rk.KDFParams.Salt == ks.KDFParams.Salt {
		t.Error("rekey should produce fresh salt + ciphertext")
	}
	// Wrong old passphrase → ErrBadPassphrase, no new store.
	if _, err := Rekey(ks, "wrong", "whatever"); err != ErrBadPassphrase {
		t.Errorf("Rekey with wrong old pass = %v", err)
	}
}

func TestDeriveKeyAndDecryptWithKey(t *testing.T) {
	ks, err := Encrypt(sampleSecret, "pw")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ks.DeriveKey("pw")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ks.DecryptWithKey(key)
	if err != nil || !bytes.Equal(got, sampleSecret) {
		t.Fatalf("DecryptWithKey = %q (err %v)", got, err)
	}
	// A wrong key fails the same way as a wrong passphrase.
	bad := make([]byte, len(key))
	if _, err := ks.DecryptWithKey(bad); err != ErrBadPassphrase {
		t.Errorf("DecryptWithKey(bad key) = %v", err)
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	ks, err := Encrypt(sampleSecret, "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Decrypt("wrong"); err != ErrBadPassphrase {
		t.Errorf("err = %v, want ErrBadPassphrase", err)
	}
}

func TestDecryptTamperedCiphertextAndNonce(t *testing.T) {
	base, err := Encrypt(sampleSecret, "pw")
	if err != nil {
		t.Fatal(err)
	}

	flip := func(hexStr string) string {
		b, _ := hex.DecodeString(hexStr)
		b[0] ^= 0xff
		return hex.EncodeToString(b)
	}

	t.Run("ciphertext", func(t *testing.T) {
		ks := *base
		ks.Ciphertext = flip(base.Ciphertext)
		if _, err := ks.Decrypt("pw"); err != ErrBadPassphrase {
			t.Errorf("tampered ciphertext: err = %v, want ErrBadPassphrase", err)
		}
	})
	t.Run("nonce", func(t *testing.T) {
		ks := *base
		ks.Nonce = flip(base.Nonce)
		if _, err := ks.Decrypt("pw"); err != ErrBadPassphrase {
			t.Errorf("tampered nonce: err = %v, want ErrBadPassphrase", err)
		}
	})
	t.Run("salt", func(t *testing.T) {
		ks := *base
		ks.KDFParams.Salt = flip(base.KDFParams.Salt)
		// A wrong salt derives a wrong key → auth failure.
		if _, err := ks.Decrypt("pw"); err != ErrBadPassphrase {
			t.Errorf("tampered salt: err = %v, want ErrBadPassphrase", err)
		}
	})
}

func TestEncryptRejectsEmptyInputs(t *testing.T) {
	if _, err := Encrypt(nil, "pw"); err == nil {
		t.Error("empty secret should error")
	}
	if _, err := Encrypt(sampleSecret, ""); err == nil {
		t.Error("empty passphrase should error")
	}
}

func TestUniqueSaltAndNoncePerEncrypt(t *testing.T) {
	a, _ := Encrypt(sampleSecret, "pw")
	b, _ := Encrypt(sampleSecret, "pw")
	if a.KDFParams.Salt == b.KDFParams.Salt {
		t.Error("salt should be random per encryption")
	}
	if a.Nonce == b.Nonce {
		t.Error("nonce should be random per encryption")
	}
	if a.Ciphertext == b.Ciphertext {
		t.Error("ciphertext should differ per encryption (random salt/nonce)")
	}
}

func TestSaveLoadRoundTripAndPerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.json")
	ks, _ := Encrypt(sampleSecret, "pw")

	if err := Save(path, ks); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("keystore perms = %o, want 600", perm)
		}
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := loaded.Decrypt("pw")
	if err != nil {
		t.Fatalf("Decrypt after load: %v", err)
	}
	if !bytes.Equal(got, sampleSecret) {
		t.Error("secret did not survive save/load")
	}

	// The stored file must be valid JSON with the expected shape.
	raw, _ := os.ReadFile(path)
	var check Keystore
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("stored file is not valid keystore JSON: %v", err)
	}
}

func TestWipeRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.json")
	ks, _ := Encrypt(sampleSecret, "pw")
	if err := Save(path, ks); err != nil {
		t.Fatal(err)
	}
	if err := Wipe(path); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone after Wipe, stat err = %v", err)
	}
	// Wiping a missing file is not an error.
	if err := Wipe(path); err != nil {
		t.Errorf("Wipe of missing file should be nil, got %v", err)
	}
}
