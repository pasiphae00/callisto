package hot

import (
	"context"
	"errors"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	bip39 "github.com/tyler-smith/go-bip39"

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
	_ signer.Signer   = (*Wallet)(nil)
	_ signer.Lockable = (*Wallet)(nil)
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

	w := &Wallet{seed: seed}
	if err := w.selectPathLocked(path); err != nil {
		w.Lock()
		return nil, err
	}
	return w, nil
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
