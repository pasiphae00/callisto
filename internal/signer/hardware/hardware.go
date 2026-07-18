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
	upstreamusb "github.com/ethereum/go-ethereum/accounts/usbwallet"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"codeberg.org/pasiphae/callisto/internal/signer"
	"codeberg.org/pasiphae/callisto/internal/signer/hardware/usbwallet"
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
	_ signer.Signer         = (*Signer)(nil)
	_ signer.Lockable       = (*Signer)(nil)
	_ signer.SafeHashSigner = (*Signer)(nil)
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

// SignSafeTxHash produces a Safe owner signature over safeTxHash using the
// device's personal-message (eth_sign) route: the device signs
// keccak256("\x19Ethereum Signed Message:\n32" || safeTxHash), which every
// supported device can do, and the Safe contract validates by re-applying that
// prefix when the signature's v is > 30. The returned v is set to 31/32
// accordingly (recovery id + 27 + 4). This path is used uniformly for Ledger and
// Trezor; hot wallets sign the hash directly instead (v 27/28).
func (s *Signer) SignSafeTxHash(ctx context.Context, safeTxHash common.Hash) ([]byte, error) {
	raw, err := s.wallet.SignText(s.account, safeTxHash.Bytes())
	if err != nil {
		return nil, fmt.Errorf("device message signing: %w", err)
	}
	if len(raw) != 65 {
		return nil, fmt.Errorf("device returned a %d-byte signature, want 65", len(raw))
	}
	sig := make([]byte, 65)
	copy(sig, raw)

	// The device may report v as a recovery id (0/1) or offset (27/28); recover
	// the actual recovery id by checking which one reproduces the account address
	// against the personal-message digest, then encode the Safe eth_sign form.
	digest := accounts.TextHash(safeTxHash.Bytes())
	var recid byte = 0xff
	for _, rec := range []byte{0, 1} {
		sig[64] = rec
		pub, rerr := crypto.SigToPub(digest, sig)
		if rerr == nil && crypto.PubkeyToAddress(*pub) == s.account.Address {
			recid = rec
			break
		}
	}
	if recid == 0xff {
		return nil, fmt.Errorf("device signature does not recover to %s", s.account.Address.Hex())
	}
	sig[64] = recid + 31 // eth_sign Safe form: recovery id + 27 + 4
	return sig, nil
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

// newHubs creates the usbwallet backend(s) to probe for a hardware kind.
//
// Ledger uses upstream go-ethereum's usbwallet unmodified. Trezor uses our local
// fork (internal/signer/hardware/usbwallet), which patches device matching to
// support Trezor Safe-family USB descriptors that upstream doesn't recognize —
// see that package's doc comment for the full rationale and the tracked upstream
// issue. We build both WebUSB and HID hubs for Trezor and probe each, so a
// connected device is found regardless of transport.
//
// Errors constructing a backend (e.g. the underlying HID subsystem being
// unavailable on this platform/build) are collected rather than discarded: if
// every backend fails to construct, the caller needs that reason, not a generic
// "no device" message that implies enumeration ran and simply found nothing.
func newHubs(kind signer.Kind) ([]accounts.Backend, error) {
	switch kind {
	case signer.KindLedger:
		h, err := upstreamusb.NewLedgerHub()
		if err != nil {
			return nil, fmt.Errorf("open USB HID subsystem: %w", err)
		}
		return []accounts.Backend{h}, nil
	case signer.KindTrezor:
		var hubs []accounts.Backend
		var errs []error
		if h, err := usbwallet.NewTrezorHubWithWebUSB(); err == nil {
			hubs = append(hubs, h)
		} else {
			errs = append(errs, fmt.Errorf("webusb: %w", err))
		}
		if h, err := usbwallet.NewTrezorHubWithHID(); err == nil {
			hubs = append(hubs, h)
		} else {
			errs = append(errs, fmt.Errorf("hid: %w", err))
		}
		if len(hubs) == 0 {
			return nil, fmt.Errorf("open USB HID subsystem (%w)", errors.Join(errs...))
		}
		return hubs, nil
	case signer.KindLattice:
		return nil, ErrLatticeUnsupported
	default:
		return nil, ErrUnsupportedKind
	}
}

// firstWallet probes for a connected device of the given kind and returns the
// first opened wallet.
//
// For Trezor, Trezor Bridge is tried first, not direct USB. This is
// deliberate and load-bearing, not an arbitrary preference: on some
// platforms/devices (confirmed: Trezor Safe 5 on macOS) the wallet-protocol USB
// interface isn't reachable via the OS's HID API at all — writes succeed but
// reads never return data — so a direct-USB attempt doesn't fail fast, it hangs
// for the full deviceReadTimeoutMs (60s) before giving up. Bridge already solves
// USB access correctly on every OS, and probing it first fails near-instantly
// (connection refused) when it isn't running, so this ordering is strictly
// better: fast either way, instead of a guaranteed 60s tax on the one path we've
// confirmed is broken for at least one real device. Older Trezors without
// Bridge running still work via the direct-USB fallback below.
//
// passphrase is forwarded to Trezor's Open (see trezorDriver's reactive
// PassphraseRequest handling); "" selects the standard, non-hidden wallet.
// Ignored for Ledger, which has no equivalent concept.
func firstWallet(kind signer.Kind, passphrase string) (accounts.Wallet, error) {
	if kind == signer.KindTrezor {
		if w, err := bridgeWallet(passphrase); err == nil {
			return w, nil
		}
	}

	hubs, err := newHubs(kind)
	if err != nil {
		return nil, err
	}
	for _, hub := range hubs {
		wallets := hub.Wallets()
		if len(wallets) == 0 {
			continue
		}
		w := wallets[0]
		if err := w.Open(passphrase); err != nil {
			// See the matching comment in bridgeWallet: release whatever a
			// partially-completed Open acquired rather than leaving it dangling
			// for the next attempt to trip over.
			_ = w.Close()
			return nil, fmt.Errorf("open %s: %w", kind, err)
		}
		return w, nil
	}
	return nil, ErrNoDevice
}

// bridgeWallet finds and opens the first device Trezor Bridge reports, without
// touching direct USB access at all. Returns an error if Bridge isn't running,
// or if it's running but sees no device.
//
// The endpoint is discovered, not assumed at the default port: trezord binds
// the first free port starting at 21325 and increments if that's taken, and
// this has been observed live to actually happen (a Trezor Suite relaunch
// landed its embedded bridge on 21328) — a hardcoded DefaultBridgeURL alone
// isn't reliable enough to depend on.
func bridgeWallet(passphrase string) (accounts.Wallet, error) {
	url, ok := usbwallet.DiscoverBridgeURL()
	if !ok {
		return nil, usbwallet.ErrBridgeUnavailable
	}
	client := usbwallet.NewBridgeClient(url)
	devices, err := client.Enumerate()
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, ErrNoDevice
	}
	w := usbwallet.NewBridgeWallet(client, devices[0])
	if err := w.Open(passphrase); err != nil {
		// Open acquires the Bridge session before running the handshake, so a
		// failure partway through (e.g. a timed-out passphrase exchange) still
		// leaves an acquired session. Close releases it; without this, the
		// dangling session was observed live to block the *next* attempt with a
		// confusing, unrelated-looking failure — not just wasted resources.
		_ = w.Close()
		return nil, fmt.Errorf("open trezor via bridge: %w", err)
	}
	return w, nil
}

// Open connects to the first available device of the given kind, derives the
// account at the standard path for index, and returns a ready Signer. The device
// must be connected and unlocked (and, for Ledger, have the Ethereum app open).
// passphrase selects a Trezor hidden wallet ("" for the standard wallet);
// ignored for Ledger.
func Open(kind signer.Kind, index uint32, passphrase string) (*Signer, error) {
	return openWithPath(kind, DerivationPath(index), passphrase)
}

// OpenPath is like Open but takes an explicit derivation path string (used to
// reconnect a saved hardware wallet by its stored path).
func OpenPath(kind signer.Kind, path string, passphrase string) (*Signer, error) {
	dp, err := accounts.ParseDerivationPath(path)
	if err != nil {
		return nil, fmt.Errorf("parse path %q: %w", path, err)
	}
	return openWithPath(kind, dp, passphrase)
}

// openWithPath connects, derives (and pins) the account at dp, and wraps it.
func openWithPath(kind signer.Kind, dp accounts.DerivationPath, passphrase string) (*Signer, error) {
	w, err := firstWallet(kind, passphrase)
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
// which account to use before Open. passphrase selects a Trezor hidden wallet
// ("" for the standard wallet); ignored for Ledger.
func Accounts(kind signer.Kind, start, count uint32, passphrase string) ([]Account, error) {
	w, err := firstWallet(kind, passphrase)
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
