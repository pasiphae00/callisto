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
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/pasiphae00/callisto/internal/signer"
	"github.com/pasiphae00/callisto/internal/signer/hardware/usbwallet"
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
	_ signer.Signer          = (*Signer)(nil)
	_ signer.Lockable        = (*Signer)(nil)
	_ signer.SafeHashSigner  = (*Signer)(nil)
	_ signer.PersonalSigner  = (*Signer)(nil)
	_ signer.TypedDataSigner = (*Signer)(nil)
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
	// The device signs the personal-message digest; Safe validates the eth_sign
	// form (v = recovery id + 27 + 4 = 31/32).
	return finalizeDeviceSig(raw, accounts.TextHash(safeTxHash.Bytes()), s.account.Address, 31)
}

// SignPersonalMessage signs an EIP-191 personal message on the device (v 27/28),
// for WalletConnect personal_sign. Both Ledger and Trezor implement SignText.
func (s *Signer) SignPersonalMessage(ctx context.Context, message []byte) ([]byte, error) {
	raw, err := s.wallet.SignText(s.account, message)
	if err != nil {
		return nil, fmt.Errorf("device message signing: %w", err)
	}
	return finalizeDeviceSig(raw, accounts.TextHash(message), s.account.Address, 27)
}

// SignTypedData signs EIP-712 typed data on the device (v 27/28), for
// WalletConnect eth_signTypedData_v4. Works on Ledger via upstream; Trezor
// typed-data support is added in the usbwallet fork.
func (s *Signer) SignTypedData(ctx context.Context, typedDataJSON []byte) ([]byte, error) {
	_, _, digest, err := signer.TypedDataHashes(typedDataJSON)
	if err != nil {
		return nil, err
	}

	// Trezor: native streaming EthereumSignTypedData — works on stock firmware and
	// shows the decoded data on-device (unlike the experimental hashed path).
	if s.kind == signer.KindTrezor {
		if ts, ok := s.wallet.(usbwallet.TypedDataStreamer); ok {
			raw, serr := ts.SignTypedDataStreaming(s.account, typedDataJSON)
			if serr != nil {
				return nil, fmt.Errorf("device typed-data signing: %w", serr)
			}
			return finalizeDeviceSig(raw, digest, s.account.Address, 27)
		}
	}

	// Ledger (and fallback): the EIP-712 hashed-message path (SignData routes
	// MimetypeTypedData + 0x1901||domainHash||messageHash to the device).
	domainHash, messageHash, _, err := signer.TypedDataHashes(typedDataJSON)
	if err != nil {
		return nil, err
	}
	data := append([]byte{0x19, 0x01}, domainHash...)
	data = append(data, messageHash...)
	raw, err := s.wallet.SignData(s.account, accounts.MimetypeTypedData, data)
	if err != nil {
		return nil, fmt.Errorf("device typed-data signing: %w", err)
	}
	return finalizeDeviceSig(raw, digest, s.account.Address, 27)
}

// finalizeDeviceSig normalizes a 65-byte device signature: it recovers the true
// recovery id against digest (devices report v inconsistently), verifies it
// reproduces account, and sets v to recid+vOffset (27 for standard EIP-191/712,
// 31 for the Safe eth_sign form).
func finalizeDeviceSig(raw, digest []byte, account common.Address, vOffset byte) ([]byte, error) {
	if len(raw) != 65 {
		return nil, fmt.Errorf("device returned a %d-byte signature, want 65", len(raw))
	}
	sig := make([]byte, 65)
	copy(sig, raw)
	var recid byte = 0xff
	for _, rec := range []byte{0, 1} {
		sig[64] = rec
		pub, rerr := crypto.SigToPub(digest, sig)
		if rerr == nil && crypto.PubkeyToAddress(*pub) == account {
			recid = rec
			break
		}
	}
	if recid == 0xff {
		return nil, fmt.Errorf("device signature does not recover to %s", account.Hex())
	}
	sig[64] = recid + vOffset
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
// Both Ledger and Trezor use Callisto's local fork of go-ethereum's usbwallet
// (internal/signer/hardware/usbwallet), which runs on github.com/karalabe/usb:
// Ledger over HID, and the Trezor Safe over raw libusb (its WebUSB interface,
// which hidapi cannot claim) — so Trezor works with no Trezor Suite/Bridge
// running. We build both the WebUSB (raw) and HID (Trezor One) hubs for Trezor
// and probe each. See the fork's package doc comment for the full rationale.
//
// Errors constructing a backend (e.g. the underlying USB subsystem being
// unavailable on this platform/build) are collected rather than discarded: if
// every backend fails to construct, the caller needs that reason, not a generic
// "no device" message that implies enumeration ran and simply found nothing.
func newHubs(kind signer.Kind) ([]accounts.Backend, error) {
	switch kind {
	case signer.KindLedger:
		h, err := usbwallet.NewLedgerHub()
		if err != nil {
			return nil, fmt.Errorf("open USB subsystem: %w", err)
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
// Direct USB (libusb, via the fork) is now tried FIRST — this is the whole point
// of the karalabe/usb migration: libusb can claim the Trezor Safe's WebUSB
// interface that hidapi could not, so Trezor works with no Trezor Suite/Bridge
// running. The Bridge is kept only as a fallback for Trezor (rarely hit now, e.g.
// if libusb can't claim the interface on some setup), so existing Bridge users
// aren't regressed. Reversed from the old HID-era ordering, where direct USB
// hung on reads and Bridge had to go first.
//
// passphrase is forwarded to Trezor's Open (see trezorDriver's reactive
// PassphraseRequest handling); "" selects the standard, non-hidden wallet.
// Ignored for Ledger, which has no equivalent concept.
func firstWallet(kind signer.Kind, passphrase string) (accounts.Wallet, error) {
	var lastErr error

	// 1) Direct USB (libusb raw for Trezor Safe / HID for Ledger + Trezor One).
	if hubs, err := newHubs(kind); err == nil {
		for _, hub := range hubs {
			wallets := hub.Wallets()
			if len(wallets) == 0 {
				continue
			}
			w := wallets[0]
			if oerr := w.Open(passphrase); oerr != nil {
				// Release whatever a partially-completed Open acquired rather than
				// leaving it dangling for the next attempt to trip over.
				_ = w.Close()
				lastErr = fmt.Errorf("open %s: %w", kind, oerr)
				continue
			}
			return w, nil
		}
	} else {
		lastErr = err
	}

	// 2) Trezor only: fall back to Trezor Bridge if direct USB didn't yield a
	// working wallet (e.g. libusb couldn't claim the interface on this setup).
	if kind == signer.KindTrezor {
		if w, err := bridgeWallet(passphrase); err == nil {
			return w, nil
		}
	}

	if lastErr != nil {
		return nil, lastErr
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
