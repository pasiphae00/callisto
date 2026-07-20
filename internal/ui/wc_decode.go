package ui

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/address"
)

// dangerDecoderABI covers the handful of high-risk methods a dApp might slip into an
// eth_sendTransaction whose intent is otherwise invisible in raw calldata — token
// approvals and transfers. Decoding these in the WalletConnect review lets a user catch
// a malicious "approve unlimited to attacker" that would look like opaque hex.
var dangerDecoderABI = mustDecoderABI(`[
  {"name":"approve","type":"function","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}]},
  {"name":"increaseAllowance","type":"function","inputs":[{"name":"spender","type":"address"},{"name":"added","type":"uint256"}]},
  {"name":"transfer","type":"function","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
  {"name":"transferFrom","type":"function","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
  {"name":"setApprovalForAll","type":"function","inputs":[{"name":"operator","type":"address"},{"name":"approved","type":"bool"}]},
  {"name":"permit","type":"function","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"},{"name":"value","type":"uint256"},{"name":"deadline","type":"uint256"},{"name":"v","type":"uint8"},{"name":"r","type":"bytes32"},{"name":"s","type":"bytes32"}]}
]`)

func mustDecoderABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("ui: bad decoder ABI: " + err.Error())
	}
	return a
}

// unlimitedThreshold: an allowance above this (over half the uint256 space) is treated
// as effectively unlimited, which is how "infinite approval" scams are usually encoded.
var unlimitedThreshold = new(big.Int).Lsh(big.NewInt(1), 255)

// decodeDangerousCall returns a human-readable summary of a known high-risk call and
// whether it warrants a warning (an approval / setApprovalForAll). ok is false for
// calldata that isn't one of the recognized methods — the caller then just shows the
// raw hex as before. It never fabricates meaning: only exact selector+ABI matches decode.
func decodeDangerousCall(data []byte) (summary string, warn bool, ok bool) {
	if len(data) < 4 {
		return "", false, false
	}
	m, err := dangerDecoderABI.MethodById(data[:4])
	if err != nil {
		return "", false, false
	}
	args, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		return "", false, false
	}
	amt := func(v interface{}) string {
		n, _ := v.(*big.Int)
		if n == nil {
			return "?"
		}
		if n.Cmp(unlimitedThreshold) >= 0 {
			return "UNLIMITED"
		}
		return n.String()
	}
	switch m.Name {
	case "approve":
		return fmt.Sprintf("Approve %s to spend %s tokens", address.Short(args[0].(common.Address)), amt(args[1])), true, true
	case "increaseAllowance":
		return fmt.Sprintf("Increase %s allowance by %s", address.Short(args[0].(common.Address)), amt(args[1])), true, true
	case "permit":
		return fmt.Sprintf("Permit (gasless approve) %s to spend %s", address.Short(args[1].(common.Address)), amt(args[2])), true, true
	case "setApprovalForAll":
		if args[1].(bool) {
			return fmt.Sprintf("Approve ALL NFTs to operator %s", address.Short(args[0].(common.Address))), true, true
		}
		return fmt.Sprintf("Revoke NFT approval for %s", address.Short(args[0].(common.Address))), false, true
	case "transfer":
		return fmt.Sprintf("Transfer %s tokens to %s", amt(args[1]), address.Short(args[0].(common.Address))), false, true
	case "transferFrom":
		return fmt.Sprintf("Transfer %s tokens from %s to %s", amt(args[2]), address.Short(args[0].(common.Address)), address.Short(args[1].(common.Address))), false, true
	}
	return "", false, false
}
