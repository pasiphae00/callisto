// Package safe implements Gnosis Safe (Safe{Core}) multisignature support: reading
// a Safe's on-chain configuration (owners, threshold, nonce, version), building and
// hashing Safe transactions, encoding owner-management and execution calldata, and
// packing collected owner signatures for execution.
//
// A Safe is a smart-contract account, not a signer: Callisto proposes a Safe
// transaction, collects signatures from individual owner EOAs (each a signer.Signer)
// switching in and out until the threshold is met, then executes it as an ordinary
// EOA transaction from one owner. This package is deliberately the pure, on-chain
// core — persistence (proposals/signatures) and the UI flow layer on top.
package safe

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// SentinelOwner is the head/tail sentinel of the Safe's owner linked list
// (address(0x1)). It is the predecessor of the first real owner.
var SentinelOwner = common.HexToAddress("0x1")

// safeABI is the minimal subset of the Safe singleton ABI Callisto uses: the
// configuration reads, getTransactionHash (canonical safeTxHash), execTransaction,
// and the owner/threshold management methods (which are called on the Safe itself
// via a Safe transaction, never as direct EOA calls).
var safeABI = mustABI(`[
  {"name":"getOwners","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address[]"}]},
  {"name":"getThreshold","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"name":"nonce","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"name":"VERSION","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},
  {"name":"isOwner","type":"function","stateMutability":"view","inputs":[{"name":"owner","type":"address"}],"outputs":[{"name":"","type":"bool"}]},
  {"name":"getTransactionHash","type":"function","stateMutability":"view","inputs":[
    {"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"},
    {"name":"operation","type":"uint8"},{"name":"safeTxGas","type":"uint256"},{"name":"baseGas","type":"uint256"},
    {"name":"gasPrice","type":"uint256"},{"name":"gasToken","type":"address"},{"name":"refundReceiver","type":"address"},
    {"name":"_nonce","type":"uint256"}],"outputs":[{"name":"","type":"bytes32"}]},
  {"name":"execTransaction","type":"function","stateMutability":"payable","inputs":[
    {"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"},
    {"name":"operation","type":"uint8"},{"name":"safeTxGas","type":"uint256"},{"name":"baseGas","type":"uint256"},
    {"name":"gasPrice","type":"uint256"},{"name":"gasToken","type":"address"},{"name":"refundReceiver","type":"address"},
    {"name":"signatures","type":"bytes"}],"outputs":[{"name":"success","type":"bool"}]},
  {"name":"addOwnerWithThreshold","type":"function","stateMutability":"nonpayable","inputs":[
    {"name":"owner","type":"address"},{"name":"_threshold","type":"uint256"}],"outputs":[]},
  {"name":"removeOwner","type":"function","stateMutability":"nonpayable","inputs":[
    {"name":"prevOwner","type":"address"},{"name":"owner","type":"address"},{"name":"_threshold","type":"uint256"}],"outputs":[]},
  {"name":"swapOwner","type":"function","stateMutability":"nonpayable","inputs":[
    {"name":"prevOwner","type":"address"},{"name":"oldOwner","type":"address"},{"name":"newOwner","type":"address"}],"outputs":[]},
  {"name":"changeThreshold","type":"function","stateMutability":"nonpayable","inputs":[
    {"name":"_threshold","type":"uint256"}],"outputs":[]}
]`)

func mustABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("safe: bad built-in ABI: " + err.Error())
	}
	return a
}

// Info is a Safe's on-chain configuration at the time it was read.
type Info struct {
	Address   common.Address
	Version   string
	Owners    []common.Address
	Threshold uint64
	Nonce     uint64
}

// callView packs a view method and eth_calls it at the latest block.
func callView(ctx context.Context, client rpc.Client, to common.Address, method string, args ...interface{}) ([]byte, error) {
	input, err := safeABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: input}, nil)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", method, err)
	}
	return out, nil
}

// ReadInfo reads a Safe's owners, threshold, nonce, and version. It also serves as
// a validity check: a non-Safe address fails these calls.
func ReadInfo(ctx context.Context, client rpc.Client, addr common.Address) (Info, error) {
	info := Info{Address: addr}

	ownersOut, err := callView(ctx, client, addr, "getOwners")
	if err != nil {
		return Info{}, err
	}
	if err := safeABI.UnpackIntoInterface(&info.Owners, "getOwners", ownersOut); err != nil {
		return Info{}, fmt.Errorf("decode getOwners: %w", err)
	}
	if len(info.Owners) == 0 {
		return Info{}, fmt.Errorf("%s is not a Safe (no owners)", addr.Hex())
	}

	thr, err := readUint(ctx, client, addr, "getThreshold")
	if err != nil {
		return Info{}, err
	}
	info.Threshold = thr

	n, err := readUint(ctx, client, addr, "nonce")
	if err != nil {
		return Info{}, err
	}
	info.Nonce = n

	// VERSION is best-effort: some very old Safes may not expose it.
	if verOut, verr := callView(ctx, client, addr, "VERSION"); verr == nil {
		var v string
		if uerr := safeABI.UnpackIntoInterface(&v, "VERSION", verOut); uerr == nil {
			info.Version = strings.TrimSpace(v)
		}
	}
	return info, nil
}

// readUint reads a uint256 view method and returns it as uint64.
func readUint(ctx context.Context, client rpc.Client, addr common.Address, method string) (uint64, error) {
	out, err := callView(ctx, client, addr, method)
	if err != nil {
		return 0, err
	}
	var v *big.Int
	if err := safeABI.UnpackIntoInterface(&v, method, out); err != nil {
		return 0, fmt.Errorf("decode %s: %w", method, err)
	}
	return v.Uint64(), nil
}

// Nonce reads the Safe's current nonce (the next transaction's nonce).
func Nonce(ctx context.Context, client rpc.Client, addr common.Address) (uint64, error) {
	return readUint(ctx, client, addr, "nonce")
}
