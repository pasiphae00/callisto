package sim

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Revert payload selectors defined by Solidity.
var (
	errorStringSelector = []byte{0x08, 0xc3, 0x79, 0xa0} // Error(string)
	panicSelector       = []byte{0x4e, 0x48, 0x71, 0x71} // Panic(uint256)
)

var (
	revertStringArgs = abi.Arguments{{Type: mustType("string")}}
	revertPanicArgs  = abi.Arguments{{Type: mustType("uint256")}}
)

// panicReasons names the Solidity Panic(uint256) codes a user might plausibly
// hit, so a preview says "arithmetic overflow" instead of "Panic(0x11)".
var panicReasons = map[uint64]string{
	0x01: "assertion failed",
	0x11: "arithmetic overflow or underflow",
	0x12: "division or modulo by zero",
	0x21: "invalid enum value",
	0x22: "malformed storage byte array",
	0x31: "pop on an empty array",
	0x32: "array index out of bounds",
	0x41: "out of memory",
	0x51: "call to an uninitialized function pointer",
}

// decodeRevert renders a revert payload as human-readable text.
//
// Three shapes exist in the wild: the classic Error(string) from require(),
// Panic(uint256) from a failed assert or a language-level check, and custom
// errors, whose four-byte selector we cannot resolve without the contract's
// ABI. For the last one, showing the raw selector is still more useful than
// "reverted" alone -- it is what the user pastes into a block explorer or 4byte
// lookup. Returns "" when there is no payload at all (a bare `revert()`, or an
// out-of-gas), leaving the caller to say only that the transaction reverts.
func decodeRevert(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) < 4 {
		return fmt.Sprintf("reverted with %d unexpected byte(s) of data", len(data))
	}

	selector, payload := data[:4], data[4:]
	switch {
	case string(selector) == string(errorStringSelector):
		vals, err := revertStringArgs.Unpack(payload)
		if err != nil || len(vals) == 0 {
			return "reverted with an undecodable Error(string) payload"
		}
		s, ok := vals[0].(string)
		if !ok {
			return "reverted with an undecodable Error(string) payload"
		}
		return strings.TrimSpace(s)

	case string(selector) == string(panicSelector):
		vals, err := revertPanicArgs.Unpack(payload)
		if err != nil || len(vals) == 0 {
			return "reverted with an undecodable Panic payload"
		}
		code, ok := vals[0].(*big.Int)
		if !ok {
			return "reverted with an undecodable Panic payload"
		}
		if reason, known := panicReasons[code.Uint64()]; known && code.IsUint64() {
			return "panic: " + reason
		}
		return fmt.Sprintf("panic: code %#x", code)

	default:
		return "custom error " + common.Bytes2Hex(selector)
	}
}

// revertDataFrom extracts the revert payload an RPC error carries. geth-family
// nodes attach it via the JSON-RPC error object's "data" member, which
// go-ethereum surfaces through this interface; anything else yields no payload
// and the caller falls back to the plain error text.
func revertDataFrom(err error) []byte {
	if err == nil {
		return nil
	}
	var de interface{ ErrorData() interface{} }
	if !errors.As(err, &de) {
		return nil
	}
	switch v := de.ErrorData().(type) {
	case string:
		return hexBytes(v)
	case []byte:
		return v
	default:
		return nil
	}
}

// hexBytes decodes an optionally-0x-prefixed hex string, returning nil if it
// isn't hex at all (some nodes put a message in "data").
func hexBytes(s string) []byte {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" || len(s)%2 != 0 || !isHex(s) {
		return nil
	}
	return common.Hex2Bytes(s)
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
