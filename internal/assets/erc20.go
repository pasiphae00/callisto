package assets

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

// Two ABIs: the standard one with string-typed name/symbol, and a fallback with
// bytes32-typed name/symbol for older tokens (e.g. MKR) that predate the string
// return convention. Selectors depend only on name+inputs, so both decode the
// same call — we just try each output shape.
var (
	erc20ABI = mustABI(`[
      {"name":"balanceOf","type":"function","stateMutability":"view","inputs":[{"name":"","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
      {"name":"decimals","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint8"}]},
      {"name":"symbol","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},
      {"name":"name","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},
      {"name":"allowance","type":"function","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
      {"name":"approve","type":"function","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
    ]`)
	erc20BytesABI = mustABI(`[
      {"name":"symbol","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"bytes32"}]},
      {"name":"name","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"bytes32"}]}
    ]`)
)

func mustABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("assets: bad built-in ABI: " + err.Error())
	}
	return a
}

// erc20Metadata is the parsed metadata for an ERC-20 token.
type erc20Metadata struct {
	Name     string
	Symbol   string
	Decimals uint8
}

// callView packs a view method and eth_calls it at the latest block.
func callView(ctx context.Context, client rpc.Client, to common.Address, a abi.ABI, method string, args ...interface{}) ([]byte, error) {
	input, err := a.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: input}, nil)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BalanceOf returns the ERC-20 balance of account for the given token.
func BalanceOf(ctx context.Context, client rpc.Client, token, account common.Address) (*big.Int, error) {
	out, err := callView(ctx, client, token, erc20ABI, "balanceOf", account)
	if err != nil {
		return nil, err
	}
	var bal *big.Int
	if err := erc20ABI.UnpackIntoInterface(&bal, "balanceOf", out); err != nil {
		return nil, fmt.Errorf("decode balanceOf: %w", err)
	}
	return bal, nil
}

// Allowance returns how much of token owner has approved spender to move.
func Allowance(ctx context.Context, client rpc.Client, token, owner, spender common.Address) (*big.Int, error) {
	out, err := callView(ctx, client, token, erc20ABI, "allowance", owner, spender)
	if err != nil {
		return nil, err
	}
	var v *big.Int
	if err := erc20ABI.UnpackIntoInterface(&v, "allowance", out); err != nil {
		return nil, fmt.Errorf("decode allowance: %w", err)
	}
	return v, nil
}

// EncodeApprove builds approve(spender, amount) calldata for an ERC-20 token.
func EncodeApprove(spender common.Address, amount *big.Int) ([]byte, error) {
	return erc20ABI.Pack("approve", spender, amount)
}

// Metadata reads name, symbol, and decimals for a token, tolerating tokens that
// return bytes32 (rather than string) for name/symbol.
func Metadata(ctx context.Context, client rpc.Client, token common.Address) (erc20Metadata, error) {
	var m erc20Metadata

	// decimals is required; if it fails, this is not a usable ERC-20.
	decOut, err := callView(ctx, client, token, erc20ABI, "decimals")
	if err != nil {
		return m, fmt.Errorf("decimals: %w", err)
	}
	if err := erc20ABI.UnpackIntoInterface(&m.Decimals, "decimals", decOut); err != nil {
		return m, fmt.Errorf("decode decimals: %w", err)
	}

	m.Symbol = readStringOrBytes32(ctx, client, token, "symbol")
	m.Name = readStringOrBytes32(ctx, client, token, "name")
	return m, nil
}

// readStringOrBytes32 reads a name/symbol-style field, trying the string ABI
// first and falling back to bytes32. Returns "" if both fail.
func readStringOrBytes32(ctx context.Context, client rpc.Client, token common.Address, method string) string {
	out, err := callView(ctx, client, token, erc20ABI, method)
	if err != nil || len(out) == 0 {
		return ""
	}
	var s string
	if err := erc20ABI.UnpackIntoInterface(&s, method, out); err == nil {
		return strings.TrimSpace(s)
	}
	var b [32]byte
	if err := erc20BytesABI.UnpackIntoInterface(&b, method, out); err == nil {
		return bytes32ToString(b)
	}
	return ""
}

// bytes32ToString interprets a bytes32 field as a null-padded ASCII/UTF-8 string.
func bytes32ToString(b [32]byte) string {
	n := len(b)
	for i, c := range b {
		if c == 0 {
			n = i
			break
		}
	}
	return strings.TrimSpace(string(b[:n]))
}
