package hot

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	bip39 "github.com/tyler-smith/go-bip39"

	"codeberg.org/pasiphae/callisto/internal/keystore"
	"codeberg.org/pasiphae/callisto/internal/signer"
)

var (
	// ErrInvalidMnemonic is returned when the supplied phrase fails BIP-39
	// checksum validation.
	ErrInvalidMnemonic = errors.New("hot: invalid mnemonic phrase")
	// ErrLocked is returned by SignTx after the wallet has been locked.
	ErrLocked = errors.New("hot: wallet is locked")
)

// Wallet is an in-memory, seed-derived signer. While unlocked it holds the BIP-39
// seed (needed to switch between derived accounts) and the currently selected
// account's 32-byte private key. Lock zeroes both and drops references; nothing
// secret is ever persisted or logged.
//
// The zero value is not usable; construct with Open.
type Wallet struct {
	mu sync.Mutex

	seed   []byte // BIP-39 seed; nil when locked
	key    []byte // selected account private key (32 bytes); nil when locked
	addr   common.Address
	path   string
	locked bool
}

// Account describes a derived account for selection UIs (address is public;
// no key material is exposed).
type Account struct {
	Index   uint32
	Path    string
	Address common.Address
}

// compile-time interface checks.
var (
	_ signer.Signer          = (*Wallet)(nil)
	_ signer.Lockable        = (*Wallet)(nil)
	_ signer.SafeHashSigner  = (*Wallet)(nil)
	_ signer.PersonalSigner  = (*Wallet)(nil)
	_ signer.TypedDataSigner = (*Wallet)(nil)
)

// Open unlocks a hot wallet from a BIP-39 mnemonic and selects the account at the
// given derivation path (use DefaultPath(i) for the standard Ethereum path). An
// optional BIP-39 passphrase ("25th word") may be supplied; pass "" if unused.
func Open(mnemonic, passphrase, path string) (*Wallet, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, ErrInvalidMnemonic
	}
	// NewSeed applies PBKDF2 to the (already checksum-valid) mnemonic.
	seed := bip39.NewSeed(mnemonic, passphrase)
	// newFromSeed copies the seed; drop this transient one.
	defer zero(seed)
	return newFromSeed(seed, path)
}

// newFromSeed builds an unlocked wallet holding a copy of seed, with the account
// at path selected. The caller retains ownership of the passed seed slice.
func newFromSeed(seed []byte, path string) (*Wallet, error) {
	w := &Wallet{seed: append([]byte(nil), seed...)}
	if err := w.selectPathLocked(path); err != nil {
		w.Lock()
		return nil, err
	}
	return w, nil
}

// NewKeystore validates a mnemonic and returns an encrypted keystore of the
// derived BIP-39 seed, sealed under keystorePassphrase, for one-time import. The
// seed and mnemonic are not retained. An optional BIP-39 passphrase ("25th word")
// is folded into the seed here; it is not needed again to unlock, since the
// encrypted seed already incorporates it.
func NewKeystore(mnemonic, bip39Passphrase, keystorePassphrase string) (*keystore.Keystore, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, ErrInvalidMnemonic
	}
	seed := bip39.NewSeed(mnemonic, bip39Passphrase)
	defer zero(seed)
	return keystore.Encrypt(seed, keystorePassphrase)
}

// OpenFromKeystore decrypts an encrypted keystore with keystorePassphrase and
// returns an unlocked wallet. For a seed keystore it selects the account at path;
// for a single imported private key (ks.Secret == "private-key") it ignores path
// and uses the key directly. Returns keystore.ErrBadPassphrase on a wrong
// passphrase / corrupt keystore.
func OpenFromKeystore(ks *keystore.Keystore, keystorePassphrase, path string) (*Wallet, error) {
	secret, err := ks.Decrypt(keystorePassphrase)
	if err != nil {
		return nil, err
	}
	defer zero(secret)
	if ks.Secret == secretPrivateKey {
		return newFromPrivateKey(secret)
	}
	return newFromSeed(secret, path)
}

// OpenFromKeystoreWithKey opens a keystore using a pre-derived AES key (from
// keystore.DeriveKey, cached in the OS keychain for Touch ID unlock) instead of a
// passphrase. Same account semantics as OpenFromKeystore.
func OpenFromKeystoreWithKey(ks *keystore.Keystore, key []byte, path string) (*Wallet, error) {
	secret, err := ks.DecryptWithKey(key)
	if err != nil {
		return nil, err
	}
	defer zero(secret)
	if ks.Secret == secretPrivateKey {
		return newFromPrivateKey(secret)
	}
	return newFromSeed(secret, path)
}

// secretPrivateKey marks a keystore whose sealed bytes are a raw 32-byte account
// private key (a single-account import) rather than a BIP-39 seed.
const secretPrivateKey = "private-key"

// NewPrivateKeyKeystore validates a hex private key and returns an encrypted
// keystore of the 32-byte key (single-account, non-HD) plus its address.
func NewPrivateKeyKeystore(privHex, keystorePassphrase string) (*keystore.Keystore, common.Address, error) {
	key, err := parsePrivKey(privHex)
	if err != nil {
		return nil, common.Address{}, err
	}
	defer zero(key)
	priv, err := crypto.ToECDSA(key)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("hot: invalid private key: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	ks, err := keystore.Encrypt(key, keystorePassphrase)
	if err != nil {
		return nil, common.Address{}, err
	}
	ks.Secret = secretPrivateKey
	return ks, addr, nil
}

// parsePrivKey decodes a 0x-optional 64-hex-char private key into 32 bytes.
func parsePrivKey(privHex string) ([]byte, error) {
	s := strings.TrimSpace(privHex)
	s = strings.TrimPrefix(s, "0x")
	key, err := hex.DecodeString(s)
	if err != nil || len(key) != 32 {
		return nil, errors.New("hot: private key must be 64 hex characters (32 bytes)")
	}
	return key, nil
}

// newFromPrivateKey builds an unlocked single-account wallet from a 32-byte key.
func newFromPrivateKey(key []byte) (*Wallet, error) {
	priv, err := crypto.ToECDSA(key)
	if err != nil {
		return nil, fmt.Errorf("hot: invalid private key: %w", err)
	}
	return &Wallet{
		key:  append([]byte(nil), key...),
		addr: crypto.PubkeyToAddress(priv.PublicKey),
	}, nil
}

// PreviewAccounts derives count accounts starting at start from a mnemonic without
// retaining an unlocked wallet — used to show an index→address list at import so
// the user can pick the account(s) they recognize instead of guessing an index.
func PreviewAccounts(mnemonic, bip39Passphrase string, start, count uint32) ([]Account, error) {
	w, err := Open(mnemonic, bip39Passphrase, DefaultPath(start))
	if err != nil {
		return nil, err
	}
	defer w.Lock()
	return w.DeriveAccounts(start, count)
}

// Address returns the currently selected account address. It remains available
// after Lock (the address is public), but signing does not.
func (w *Wallet) Address() common.Address {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addr
}

// Path returns the currently selected derivation path.
func (w *Wallet) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}

// Kind reports the backing wallet type.
func (w *Wallet) Kind() signer.Kind { return signer.KindHot }

// ExportPrivateKey returns the 0x-prefixed hex private key of the selected account.
// It errors when the wallet is locked. The returned string is HIGHLY sensitive —
// callers must never persist or log it and should clear it from the UI promptly.
func (w *Wallet) ExportPrivateKey() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.locked || w.key == nil {
		return "", ErrLocked
	}
	return "0x" + common.Bytes2Hex(w.key), nil
}

// SelectAccount switches the active account to the standard Ethereum path for
// the given index (m/44'/60'/0'/0/index).
func (w *Wallet) SelectAccount(index uint32) error {
	return w.SelectPath(DefaultPath(index))
}

// SelectPath switches the active account to an explicit derivation path.
func (w *Wallet) SelectPath(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.locked || w.seed == nil {
		return ErrLocked
	}
	return w.selectPathLocked(path)
}

// selectPathLocked derives the account for path and installs it as active. The
// caller must hold w.mu (or be within Open before publishing the wallet).
func (w *Wallet) selectPathLocked(path string) error {
	indices, err := parsePath(path)
	if err != nil {
		return err
	}
	ext, err := derivePath(w.seed, indices)
	if err != nil {
		return err
	}
	defer ext.wipe()

	priv, err := crypto.ToECDSA(ext.key[:])
	if err != nil {
		return err
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	// Install the new key, wiping any previous one.
	w.wipeKeyLocked()
	w.key = make([]byte, 32)
	copy(w.key, ext.key[:])
	w.addr = addr
	w.path = path
	return nil
}

// DeriveAccounts derives count consecutive accounts starting at index (on the
// standard path) without changing the current selection — used to let the user
// pick which derived account to use.
func (w *Wallet) DeriveAccounts(start, count uint32) ([]Account, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.locked || w.seed == nil {
		return nil, ErrLocked
	}
	out := make([]Account, 0, count)
	for i := uint32(0); i < count; i++ {
		index := start + i
		path := DefaultPath(index)
		indices, err := parsePath(path)
		if err != nil {
			return nil, err
		}
		ext, err := derivePath(w.seed, indices)
		if err != nil {
			return nil, err
		}
		priv, err := crypto.ToECDSA(ext.key[:])
		ext.wipe()
		if err != nil {
			return nil, err
		}
		out = append(out, Account{Index: index, Path: path, Address: crypto.PubkeyToAddress(priv.PublicKey)})
	}
	return out, nil
}

// SignTx signs tx for the given chain using the selected account. It returns
// ErrLocked if the wallet has been locked. The input tx is not mutated.
func (w *Wallet) SignTx(ctx context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.locked || w.key == nil {
		return nil, ErrLocked
	}
	priv, err := crypto.ToECDSA(w.key)
	if err != nil {
		return nil, err
	}
	// LatestSignerForChainID handles legacy, access-list, and dynamic-fee txs.
	s := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, s, priv)
	// priv (an *ecdsa.PrivateKey wrapping a big.Int) goes out of scope here. Its
	// internal big.Int words cannot be reliably zeroed from Go, so we keep its
	// lifetime as short as possible rather than pretending to wipe it; the
	// durable secret we do control (w.key) is zeroed on Lock.
	return signed, err
}

// SignSafeTxHash signs a Safe transaction hash (safeTxHash) directly with the
// selected account, producing a 65-byte owner signature with v in {27,28} — the
// EIP-712 "contract signature" form the Safe validates by ecrecover on the hash
// itself (no eth_sign prefix). Returns ErrLocked after the wallet is locked.
func (w *Wallet) SignSafeTxHash(ctx context.Context, safeTxHash common.Hash) ([]byte, error) {
	// A Safe owner signature over the safeTxHash is a direct-hash signature with v
	// in {27,28} — the same encoding as signDigest.
	return w.signDigest(safeTxHash.Bytes())
}

// SignPersonalMessage signs an EIP-191 personal message with the selected
// account (v 27/28), for WalletConnect personal_sign. Returns ErrLocked if locked.
func (w *Wallet) SignPersonalMessage(ctx context.Context, message []byte) ([]byte, error) {
	return w.signDigest(accounts.TextHash(message))
}

// SignTypedData signs EIP-712 typed data with the selected account (v 27/28), for
// WalletConnect eth_signTypedData_v4.
func (w *Wallet) SignTypedData(ctx context.Context, typedDataJSON []byte) ([]byte, error) {
	_, _, digest, err := signer.TypedDataHashes(typedDataJSON)
	if err != nil {
		return nil, err
	}
	return w.signDigest(digest)
}

// signDigest signs a 32-byte digest directly and returns a 65-byte signature with
// v in {27,28}. The seed/private key never leave this package.
func (w *Wallet) signDigest(digest []byte) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.locked || w.key == nil {
		return nil, ErrLocked
	}
	priv, err := crypto.ToECDSA(w.key)
	if err != nil {
		return nil, err
	}
	sig, err := crypto.Sign(digest, priv)
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}

// Lock wipes all in-memory key material and marks the wallet unusable for
// signing. It is idempotent and safe to call from any goroutine.
func (w *Wallet) Lock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seed != nil {
		zero(w.seed)
		w.seed = nil
	}
	w.wipeKeyLocked()
	w.locked = true
}

// Locked reports whether the wallet has been locked.
func (w *Wallet) Locked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.locked
}

// wipeKeyLocked zeroes and drops the selected private key. Caller holds w.mu.
func (w *Wallet) wipeKeyLocked() {
	if w.key != nil {
		zero(w.key)
		w.key = nil
	}
}
