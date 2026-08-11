package ui

import (
	"encoding/json"
	"strings"
	"testing"

	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"

	"github.com/pasiphae00/callisto/internal/signer/hot"
)

// A throwaway key used only to build test keystores.
const testPrivKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

// TestImportRestoresACallistoBackup is the regression that matters: Callisto's
// own "Export encrypted backup" file must be importable again.
//
// It was not. The importer only understood geth/MetaMask V3, so restoring a
// Callisto backup failed with "wrong password or unsupported format" no matter
// how correct the password was — discovered by a user trying to recover a wallet
// from exactly such a backup.
func TestImportRestoresACallistoBackup(t *testing.T) {
	// Build what "Export encrypted backup" writes.
	backupPass := "backup passphrase"
	ks, wantAddr, err := hot.NewPrivateKeyKeystore(testPrivKey, backupPass)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := json.Marshal(ks)
	if err != nil {
		t.Fatal(err)
	}

	got, addr, err := decryptImportedKeystore(backup, backupPass, "new callisto passphrase")
	if err != nil {
		t.Fatalf("importing a Callisto backup failed: %v", err)
	}
	if addr != wantAddr {
		t.Errorf("address = %s; want %s", addr.Hex(), wantAddr.Hex())
	}

	// The re-encrypted keystore must open under the new passphrase and yield the
	// same account — otherwise the import "succeeded" but restored nothing usable.
	w, err := hot.OpenFromKeystore(got, "new callisto passphrase", hot.DefaultPath(0))
	if err != nil {
		t.Fatalf("re-encrypted keystore does not open under the new passphrase: %v", err)
	}
	defer w.Lock()
	if w.Address() != wantAddr {
		t.Errorf("restored address = %s; want %s", w.Address().Hex(), wantAddr.Hex())
	}
}

// TestImportBackupPreservesTheRawKeyLabel guards the same class of bug as
// keystore.Rekey's: a raw-key import that loses its "private-key" label is later
// mis-derived as an HD seed, giving a different address under a correct
// passphrase.
func TestImportBackupPreservesTheRawKeyLabel(t *testing.T) {
	ks, _, err := hot.NewPrivateKeyKeystore(testPrivKey, "backup passphrase")
	if err != nil {
		t.Fatal(err)
	}
	backup, _ := json.Marshal(ks)

	got, _, err := decryptImportedKeystore(backup, "backup passphrase", "new passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if got.Secret != "private-key" {
		t.Errorf("Secret = %q; want \"private-key\" so it isn't later treated as an HD seed", got.Secret)
	}
}

func TestImportRejectsAWrongBackupPassword(t *testing.T) {
	ks, _, err := hot.NewPrivateKeyKeystore(testPrivKey, "the right passphrase")
	if err != nil {
		t.Fatal(err)
	}
	backup, _ := json.Marshal(ks)

	if _, _, err := decryptImportedKeystore(backup, "the wrong passphrase", "new passphrase"); err == nil {
		t.Fatal("a wrong backup password must not import")
	}
}

func TestImportRejectsUnknownFormatClearly(t *testing.T) {
	_, _, err := decryptImportedKeystore([]byte(`{"hello":"world"}`), "pass", "new passphrase")
	if err == nil {
		t.Fatal("an unrecognized file must not import")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error = %q; it should say the format wasn't recognized", err)
	}
}

// TestImportStillAcceptsGethV3 checks the original path did not regress.
func TestImportStillAcceptsGethV3(t *testing.T) {
	key, err := crypto.HexToECDSA(strings.TrimPrefix(testPrivKey, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	v3, err := gethV3JSON(key.D.Bytes(), "file password")
	if err != nil {
		t.Skipf("could not build a V3 keystore fixture: %v", err)
	}

	_, addr, err := decryptImportedKeystore(v3, "file password", "new passphrase")
	if err != nil {
		t.Fatalf("geth V3 import regressed: %v", err)
	}
	if addr != crypto.PubkeyToAddress(key.PublicKey) {
		t.Errorf("address = %s; want %s", addr.Hex(), crypto.PubkeyToAddress(key.PublicKey).Hex())
	}
}

// gethV3JSON builds a geth/MetaMask V3 keystore for the given key, using the
// weakest scrypt parameters go-ethereum allows so the test stays fast.
func gethV3JSON(keyBytes []byte, password string) ([]byte, error) {
	priv, err := crypto.ToECDSA(keyBytes)
	if err != nil {
		return nil, err
	}
	k := &gethkeystore.Key{
		Id:         uuid.New(),
		Address:    crypto.PubkeyToAddress(priv.PublicKey),
		PrivateKey: priv,
	}
	return gethkeystore.EncryptKey(k, password, gethkeystore.LightScryptN, gethkeystore.LightScryptP)
}
