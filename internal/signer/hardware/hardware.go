// Package hardware implements the Signer interface for hardware wallets, wrapping
// go-ethereum's battle-tested accounts/usbwallet backends for Ledger and Trezor.
// The device holds the keys and performs signing; Callisto never sees key
// material — it only derives an address and requests signatures (which the user
// confirms on the device).
//
// GridPlus Lattice is declared in the Signer interface but not yet implemented:
// there is no maintained Go SDK for it (the official SDK is JavaScript). Open with
// KindLattice returns ErrLatticeUnsupported; adding it later is non-invasive.
package hardware

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/usbwallet"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/signer"
)

var (
	// ErrNoDevice means no supported device of the requested kind was found.
	ErrNoDevice = errors.New("hardware: no device found — connect and unlock it")
	// ErrUnsupportedKind means the requested kind is not a hardware wallet.
	ErrUnsupportedKind = errors.New("hardware: unsupported signer kind")
	// ErrLatticeUnsupported means GridPlus Lattice is not yet implemented (no Go SDK).
	ErrLatticeUnsupported = errors.New("hardware: GridPlus Lattice is not yet supported")
)

// Signer signs via a connected hardware wallet. It implements signer.Signer and
// signer.Lockable (Lock closes the device connection).
type Signer struct {
	wallet  accounts.Wallet
	account accounts.Account
	kind    signer.Kind
}

var (
	_ signer.Signer   = (*Signer)(nil)
	_ signer.Lockable = (*Signer)(nil)
)

// Address returns the derived account address.
func (s *Signer) Address() common.Address { return s.account.Address }

// Kind reports the backing device type.
func (s *Signer) Kind() signer.Kind { return s.kind }

// SignTx asks the device to sign tx. The user must confirm on the device; this
// call blocks until they do (or the device times out).
func (s *Signer) SignTx(ctx context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	return s.wallet.SignTx(s.account, tx, chainID)
}

// Lock closes the device connection, releasing it for other applications.
func (s *Signer) Lock() {
	if s.wallet != nil {
		_ = s.wallet.Close()
	}
}

// DerivationPath returns the BIP-44 path for account index i (m/44'/60'/0'/0/i),
// the standard used by MetaMask and most wallets. (Ledger Live's alternative
// m/44'/60'/i'/0/0 layout is a possible future option.)
func DerivationPath(index uint32) accounts.DerivationPath {
	path := make(accounts.DerivationPath, len(accounts.DefaultBaseDerivationPath))
	copy(path, accounts.DefaultBaseDerivationPath)
	path[len(path)-1] = index
	return path
}

// newHub creates the usbwallet backend for a hardware kind.
func newHub(kind signer.Kind) (accounts.Backend, error) {
	switch kind {
	case signer.KindLedger:
		return usbwallet.NewLedgerHub()
	case signer.KindTrezor:
		return usbwallet.NewTrezorHubWithHID()
	case signer.KindLattice:
		return nil, ErrLatticeUnsupported
	default:
		return nil, ErrUnsupportedKind
	}
}

// firstWallet opens a hub and returns its first connected, opened wallet.
func firstWallet(kind signer.Kind) (accounts.Wallet, error) {
	hub, err := newHub(kind)
	if err != nil {
		return nil, err
	}
	wallets := hub.Wallets()
	if len(wallets) == 0 {
		return nil, ErrNoDevice
	}
	w := wallets[0]
	if err := w.Open(""); err != nil {
		return nil, fmt.Errorf("open %s: %w", kind, err)
	}
	return w, nil
}

// Open connects to the first available device of the given kind, derives the
// account at the standard path for index, and returns a ready Signer. The device
// must be connected and unlocked (and, for Ledger, have the Ethereum app open).
func Open(kind signer.Kind, index uint32) (*Signer, error) {
	return openWithPath(kind, DerivationPath(index))
}

// OpenPath is like Open but takes an explicit derivation path string (used to
// reconnect a saved hardware wallet by its stored path).
func OpenPath(kind signer.Kind, path string) (*Signer, error) {
	dp, err := accounts.ParseDerivationPath(path)
	if err != nil {
		return nil, fmt.Errorf("parse path %q: %w", path, err)
	}
	return openWithPath(kind, dp)
}

// openWithPath connects, derives (and pins) the account at dp, and wraps it.
func openWithPath(kind signer.Kind, dp accounts.DerivationPath) (*Signer, error) {
	w, err := firstWallet(kind)
	if err != nil {
		return nil, err
	}
	account, err := w.Derive(dp, true)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("derive account: %w", err)
	}
	return &Signer{wallet: w, account: account, kind: kind}, nil
}

// Account is a derived hardware account for selection UIs.
type Account struct {
	Index   uint32
	Address common.Address
}

// Accounts derives count addresses starting at index start from the first
// connected device, without retaining the connection — used to let the user pick
// which account to use before Open.
func Accounts(kind signer.Kind, start, count uint32) ([]Account, error) {
	w, err := firstWallet(kind)
	if err != nil {
		return nil, err
	}
	defer w.Close()

	out := make([]Account, 0, count)
	for i := uint32(0); i < count; i++ {
		index := start + i
		acct, err := w.Derive(DerivationPath(index), false)
		if err != nil {
			return nil, fmt.Errorf("derive account %d: %w", index, err)
		}
		out = append(out, Account{Index: index, Address: acct.Address})
	}
	return out, nil
}
