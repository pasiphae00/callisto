// Package walletconnect implements the WalletConnect v2 Sign protocol in the
// wallet role: Callisto pairs with a dApp from a pasted `wc:` URI, approves a
// session exposing the active wallet, and signs the dApp's transaction and
// signature requests. There is no Go SDK, so the relay transport, envelope
// encryption, and session state machine are implemented directly against the
// published v2 spec (specs.walletconnect.com) — using only the standard library,
// already-vendored x/crypto, and the existing gorilla/websocket dependency.
package walletconnect

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrBadURI means a pasted string is not a valid WalletConnect v2 pairing URI.
var ErrBadURI = errors.New("walletconnect: not a valid v2 pairing URI")

// PairingURI is a parsed `wc:` pairing URI:
//
//	wc:<topic>@2?relay-protocol=irn&symKey=<hex>&expiryTimestamp=<unix>
//
// The topic and symKey identify and encrypt the pairing channel on which the dApp
// has already published its session proposal.
type PairingURI struct {
	Topic         string // pairing topic (hex)
	Version       string // "2"
	SymKey        []byte // 32-byte symmetric key for the pairing channel
	RelayProtocol string // e.g. "irn"
	Expiry        int64  // unix seconds (0 if absent)
}

// ParseURI parses and validates a WalletConnect v2 pairing URI.
func ParseURI(raw string) (PairingURI, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "wc:") {
		return PairingURI{}, ErrBadURI
	}
	// Format: wc:<topic>@<version>?<query>. The scheme-specific part is not a
	// standard hierarchical URL, so split it by hand.
	body := strings.TrimPrefix(raw, "wc:")
	at := strings.IndexByte(body, '@')
	if at <= 0 {
		return PairingURI{}, fmt.Errorf("%w: missing @version", ErrBadURI)
	}
	topic := body[:at]
	rest := body[at+1:]

	version := rest
	query := ""
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		version = rest[:q]
		query = rest[q+1:]
	}
	if version != "2" {
		return PairingURI{}, fmt.Errorf("%w: unsupported version %q (need 2)", ErrBadURI, version)
	}

	vals, err := url.ParseQuery(query)
	if err != nil {
		return PairingURI{}, fmt.Errorf("%w: bad query: %v", ErrBadURI, err)
	}
	symHex := vals.Get("symKey")
	if symHex == "" {
		return PairingURI{}, fmt.Errorf("%w: missing symKey", ErrBadURI)
	}
	symKey, err := hex.DecodeString(symHex)
	if err != nil || len(symKey) != symKeyLen {
		return PairingURI{}, fmt.Errorf("%w: symKey must be %d hex bytes", ErrBadURI, symKeyLen)
	}
	if topic == "" {
		return PairingURI{}, fmt.Errorf("%w: empty topic", ErrBadURI)
	}

	return PairingURI{
		Topic:         topic,
		Version:       version,
		SymKey:        symKey,
		RelayProtocol: vals.Get("relay-protocol"),
		Expiry:        parseInt64(vals.Get("expiryTimestamp")),
	}, nil
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	var v int64
	_, err := fmt.Sscan(s, &v)
	if err != nil {
		return 0
	}
	return v
}
