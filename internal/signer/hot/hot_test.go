package hot

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/pasiphae00/callisto/internal/keystore"
)

// Well-known BIP-44 (m/44'/60'/0'/0/i) test vectors.
const (
	// The Hardhat/Foundry default mnemonic and its first accounts.
	junkMnemonic = "test test test test test test test test test test test junk"
	junkAcct0    = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	junkAcct1    = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	// The canonical all-"abandon" + "about" vector (Trezor/iancoleman).
	abandonMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	abandonAcct0    = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
)

func TestOpenDerivesKnownAddress(t *testing.T) {
	cases := []struct {
		name     string
		mnemonic string
		want     string
	}{
		{"junk-0", junkMnemonic, junkAcct0},
		{"abandon-0", abandonMnemonic, abandonAcct0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, err := Open(c.mnemonic, "", DefaultPath(0))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer w.Lock()
			if got := w.Address().Hex(); got != c.want {
				t.Errorf("account 0 = %s, want %s", got, c.want)
			}
		})
	}
}

func TestPrivateKeyKeystoreRoundTrip(t *testing.T) {
	privHex := "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	ks, addr, err := NewPrivateKeyKeystore(privHex, "pw-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if ks.Secret != secretPrivateKey {
		t.Errorf("keystore.Secret = %q, want %q", ks.Secret, secretPrivateKey)
	}
	// Opening ignores the derivation path and uses the key directly.
	w, err := OpenFromKeystore(ks, "pw-passphrase", DefaultPath(9))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()
	if w.Address() != addr {
		t.Errorf("address = %s, want %s", w.Address().Hex(), addr.Hex())
	}
	if pk, _ := w.ExportPrivateKey(); pk != privHex {
		t.Errorf("exported key = %s, want %s", pk, privHex)
	}
	// A single-key wallet has no HD accounts to derive.
	if _, err := w.DeriveAccounts(0, 1); err == nil {
		t.Error("single-key wallet should not derive HD accounts")
	}
	// Wrong passphrase is rejected.
	if _, err := OpenFromKeystore(ks, "nope", ""); err != keystore.ErrBadPassphrase {
		t.Errorf("wrong passphrase = %v", err)
	}
	// Bad key material is rejected up front.
	if _, _, err := NewPrivateKeyKeystore("0x1234", "pw"); err == nil {
		t.Error("short private key should be rejected")
	}
}

func TestExportPrivateKey(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	pk, err := w.ExportPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	// 0x + 64 hex chars, and it recovers the account address.
	if len(pk) != 66 || pk[:2] != "0x" {
		t.Fatalf("private key format: %q", pk)
	}
	priv, err := crypto.HexToECDSA(pk[2:])
	if err != nil || crypto.PubkeyToAddress(priv.PublicKey) != w.Address() {
		t.Errorf("exported key doesn't match address (err %v)", err)
	}
	// Locked wallet refuses to export.
	w.Lock()
	if _, err := w.ExportPrivateKey(); err != ErrLocked {
		t.Errorf("export after lock = %v, want ErrLocked", err)
	}
}

func TestSelectAccountSwitchesAddress(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()
	if w.Address().Hex() != junkAcct0 {
		t.Fatalf("acct0 = %s", w.Address().Hex())
	}
	if err := w.SelectAccount(1); err != nil {
		t.Fatal(err)
	}
	if got := w.Address().Hex(); got != junkAcct1 {
		t.Errorf("acct1 = %s, want %s", got, junkAcct1)
	}
	if w.Path() != DefaultPath(1) {
		t.Errorf("path = %s, want %s", w.Path(), DefaultPath(1))
	}
}

func TestDeriveAccountsDoesNotChangeSelection(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()
	accts, err := w.DeriveAccounts(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(accts) != 2 {
		t.Fatalf("got %d accounts", len(accts))
	}
	if accts[0].Address.Hex() != junkAcct0 || accts[1].Address.Hex() != junkAcct1 {
		t.Errorf("derived = %s, %s", accts[0].Address.Hex(), accts[1].Address.Hex())
	}
	// Selection unchanged.
	if w.Address().Hex() != junkAcct0 {
		t.Errorf("selection changed to %s", w.Address().Hex())
	}
}

func TestInvalidMnemonicRejected(t *testing.T) {
	if _, err := Open("not a valid mnemonic phrase at all here nope", "", DefaultPath(0)); err != ErrInvalidMnemonic {
		t.Errorf("err = %v, want ErrInvalidMnemonic", err)
	}
	// Valid words but bad checksum.
	if _, err := Open("test test test test test test test test test test test test", "", DefaultPath(0)); err != ErrInvalidMnemonic {
		t.Errorf("bad-checksum mnemonic err = %v, want ErrInvalidMnemonic", err)
	}
}

func TestSignTxRecoversSender(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()

	chainID := big.NewInt(11155111) // Sepolia
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		To:        &to,
		Value:     big.NewInt(1_000_000_000_000_000), // 0.001 ETH
		Gas:       21000,
		GasFeeCap: big.NewInt(30_000_000_000),
		GasTipCap: big.NewInt(1_000_000_000),
	})

	signed, err := w.SignTx(context.Background(), tx, chainID)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	sender, err := types.Sender(types.LatestSignerForChainID(chainID), signed)
	if err != nil {
		t.Fatalf("recover sender: %v", err)
	}
	if sender != w.Address() {
		t.Errorf("recovered sender %s != wallet %s", sender.Hex(), w.Address().Hex())
	}
	// Input tx must not have been mutated (still unsigned).
	if v, _, _ := tx.RawSignatureValues(); v != nil && v.Sign() != 0 {
		t.Error("input transaction was mutated by SignTx")
	}
}

func TestLockWipesSecretsAndBlocksSigning(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	addr := w.Address()

	w.Lock()

	// White-box: secret material must be gone.
	if w.seed != nil {
		t.Error("seed not cleared after Lock")
	}
	if w.key != nil {
		t.Error("private key not cleared after Lock")
	}
	if !w.Locked() {
		t.Error("Locked() should be true after Lock")
	}
	// Address (public) remains available.
	if w.Address() != addr {
		t.Error("address should remain readable after Lock")
	}
	// Signing must now fail.
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(1), Gas: 21000})
	if _, err := w.SignTx(context.Background(), tx, big.NewInt(1)); err != ErrLocked {
		t.Errorf("SignTx after Lock err = %v, want ErrLocked", err)
	}
	// Lock is idempotent.
	w.Lock()
	// Account operations fail once locked.
	if err := w.SelectAccount(2); err != ErrLocked {
		t.Errorf("SelectAccount after Lock err = %v, want ErrLocked", err)
	}
}

func TestParsePath(t *testing.T) {
	idx, err := parsePath("m/44'/60'/0'/0/5")
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{44 + hardenedOffset, 60 + hardenedOffset, 0 + hardenedOffset, 0, 5}
	if len(idx) != len(want) {
		t.Fatalf("len = %d, want %d", len(idx), len(want))
	}
	for i := range want {
		if idx[i] != want[i] {
			t.Errorf("idx[%d] = %d, want %d", i, idx[i], want[i])
		}
	}
	for _, bad := range []string{"", "44'/60'", "n/1", "m/'", "m/4294967296"} {
		if _, err := parsePath(bad); err == nil {
			t.Errorf("parsePath(%q) should error", bad)
		}
	}
}
