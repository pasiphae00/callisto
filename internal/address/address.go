// Package address handles Ethereum address validation and display: EIP-55
// checksum verification of user input and canonical checksummed formatting for
// display. Anywhere Callisto accepts or shows an address, it goes through here so
// behaviour is consistent (a mistyped, bad-checksum address is rejected at the
// point of entry rather than silently accepted).
package address

import (
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// ErrInvalidFormat means the string is not a 20-byte hex address.
var ErrInvalidFormat = errors.New("not a valid hex address")

// ErrBadChecksum means the string is a hex address but its mixed-case EIP-55
// checksum does not verify — a strong signal of a typo or corruption.
var ErrBadChecksum = errors.New("address has an invalid EIP-55 checksum")

// Parse validates a user-supplied address string and returns the parsed address.
//
// An all-lowercase or all-uppercase address carries no checksum information and
// is accepted (returned in canonical checksummed form). A mixed-case address is
// treated as EIP-55 checksummed and must verify exactly, otherwise ErrBadChecksum
// is returned — this is what catches typos.
func Parse(s string) (common.Address, error) {
	s = strings.TrimSpace(s)
	if !common.IsHexAddress(s) {
		return common.Address{}, ErrInvalidFormat
	}
	addr := common.HexToAddress(s)

	hexPart := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if hasMixedCase(hexPart) {
		// Compare against the canonical EIP-55 form (which includes "0x").
		if !strings.EqualFold(s, "0x"+hexPart) { // defensive; always true here
			return common.Address{}, ErrInvalidFormat
		}
		if addr.Hex() != ensure0x(s) {
			return common.Address{}, ErrBadChecksum
		}
	}
	return addr, nil
}

// Valid reports whether s is an acceptable address (see Parse).
func Valid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// Format returns the canonical EIP-55 checksummed representation of an address.
func Format(a common.Address) string {
	return a.Hex()
}

// Short returns a truncated display form like "0x1234…abcd" for compact UI.
func Short(a common.Address) string {
	h := a.Hex()
	if len(h) <= 12 {
		return h
	}
	return h[:6] + "…" + h[len(h)-4:]
}

// hasMixedCase reports whether a hex string contains both an uppercase and a
// lowercase letter (i.e. it encodes an EIP-55 checksum). Digits are ignored.
func hasMixedCase(hex string) bool {
	var hasUpper, hasLower bool
	for _, r := range hex {
		switch {
		case r >= 'a' && r <= 'f':
			hasLower = true
		case r >= 'A' && r <= 'F':
			hasUpper = true
		}
		if hasUpper && hasLower {
			return true
		}
	}
	return false
}

// ensure0x normalizes the prefix to lowercase "0x".
func ensure0x(s string) string {
	if strings.HasPrefix(s, "0X") {
		return "0x" + s[2:]
	}
	if !strings.HasPrefix(s, "0x") {
		return "0x" + s
	}
	return s
}
