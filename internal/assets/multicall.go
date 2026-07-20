package assets

import (
	"context"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/textsafe"
)

// multicall3 is the canonical Multicall3 deployment, at the same address on every
// chain Callisto ships (Ethereum, Base, Arbitrum, Optimism, Polygon, BSC, zkSync Era,
// …). It lets us read the native balance and every token balance in a single eth_call
// instead of 1+N, which is the difference between "usable" and "rate-limited" on public
// L2 endpoints. Chains without it fall back to the per-call path.
var multicall3 = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

// aggregate3 batches arbitrary calls (each allowed to fail independently) and returns
// per-call (success, returnData); getEthBalance reads the native balance of an address
// via the Multicall3 contract itself, so it can ride in the same batch.
var multicall3ABI = mustABI(`[
  {"name":"aggregate3","type":"function","stateMutability":"payable","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}]}],"outputs":[{"name":"returnData","type":"tuple[]","components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}]}]},
  {"name":"getEthBalance","type":"function","stateMutability":"view","inputs":[{"name":"addr","type":"address"}],"outputs":[{"name":"balance","type":"uint256"}]}
]`)

// mc3Call is one entry in an aggregate3 batch.
type mc3Call struct {
	Target       common.Address `abi:"target"`
	AllowFailure bool           `abi:"allowFailure"`
	CallData     []byte         `abi:"callData"`
}

// mc3Result is one aggregate3 result.
type mc3Result struct {
	Success    bool   `abi:"success"`
	ReturnData []byte `abi:"returnData"`
}

// aggregate3 packs, eth_calls, and unpacks a Multicall3 batch. It returns one result
// per input call (in order); ok is false if the batched call itself failed (no
// Multicall3 on this chain, or the node rejected it) so callers can fall back.
func (s *Service) aggregate3(ctx context.Context, calls []mc3Call) ([]mc3Result, bool) {
	if len(calls) == 0 {
		return nil, true
	}
	input, err := multicall3ABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, false
	}
	to := multicall3
	out, err := s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: input}, nil)
	if err != nil || len(out) == 0 {
		return nil, false
	}
	var results []mc3Result
	if err := multicall3ABI.UnpackIntoInterface(&results, "aggregate3", out); err != nil {
		return nil, false
	}
	if len(results) != len(calls) {
		return nil, false
	}
	return results, true
}

// batchBalances reads the native balance and each token's balanceOf(account) in one
// Multicall3 call. The returned slice is [native, token0, token1, …]; a nil entry means
// that individual balance read failed. ok is false when the batch call is unavailable
// (fall back to the per-call path).
func (s *Service) batchBalances(ctx context.Context, account common.Address, tokens []common.Address) ([]*big.Int, bool) {
	calls := make([]mc3Call, 0, len(tokens)+1)

	nativeData, err := multicall3ABI.Pack("getEthBalance", account)
	if err != nil {
		return nil, false
	}
	calls = append(calls, mc3Call{Target: multicall3, AllowFailure: true, CallData: nativeData})

	for _, token := range tokens {
		data, perr := erc20ABI.Pack("balanceOf", account)
		if perr != nil {
			return nil, false
		}
		calls = append(calls, mc3Call{Target: token, AllowFailure: true, CallData: data})
	}

	results, ok := s.aggregate3(ctx, calls)
	if !ok {
		return nil, false
	}

	out := make([]*big.Int, len(calls))
	for i, r := range results {
		if !r.Success || len(r.ReturnData) == 0 {
			continue // leave nil
		}
		var v *big.Int
		method := "balanceOf"
		if i == 0 {
			method = "getEthBalance"
		}
		abiDef := erc20ABI
		if i == 0 {
			abiDef = multicall3ABI
		}
		if err := abiDef.UnpackIntoInterface(&v, method, r.ReturnData); err == nil {
			out[i] = v
		}
	}
	// The native balance is a hard requirement; if it didn't decode, treat the batch as
	// failed so the caller falls back rather than showing a missing native balance.
	if out[0] == nil {
		return nil, false
	}
	return out, true
}

// batchMetadata reads decimals/symbol/name for many tokens in one Multicall3 call,
// tolerating tokens that return bytes32 for symbol/name. Tokens whose required decimals
// read fails are omitted from the map. ok is false when the batch is unavailable.
func (s *Service) batchMetadata(ctx context.Context, tokens []common.Address) (map[common.Address]erc20Metadata, bool) {
	if len(tokens) == 0 {
		return map[common.Address]erc20Metadata{}, true
	}
	// Three calls per token: decimals, symbol, name (in that order).
	calls := make([]mc3Call, 0, len(tokens)*3)
	pack := func(method string) ([]byte, bool) {
		d, err := erc20ABI.Pack(method)
		return d, err == nil
	}
	decData, ok1 := pack("decimals")
	symData, ok2 := pack("symbol")
	nameData, ok3 := pack("name")
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	for _, token := range tokens {
		calls = append(calls,
			mc3Call{Target: token, AllowFailure: true, CallData: decData},
			mc3Call{Target: token, AllowFailure: true, CallData: symData},
			mc3Call{Target: token, AllowFailure: true, CallData: nameData})
	}

	results, ok := s.aggregate3(ctx, calls)
	if !ok {
		return nil, false
	}

	out := make(map[common.Address]erc20Metadata, len(tokens))
	for i, token := range tokens {
		dec, sym, nam := results[i*3], results[i*3+1], results[i*3+2]
		var decimals uint8
		if !dec.Success || len(dec.ReturnData) == 0 {
			continue // decimals is required; not a usable ERC-20
		}
		if err := erc20ABI.UnpackIntoInterface(&decimals, "decimals", dec.ReturnData); err != nil {
			continue
		}
		out[token] = erc20Metadata{
			Decimals: decimals,
			Symbol:   textsafe.Display(decodeStringOrBytes32(sym)),
			Name:     textsafe.Display(decodeStringOrBytes32(nam)),
		}
	}
	return out, true
}

// decodeStringOrBytes32 decodes a name/symbol result, trying the string ABI first and
// falling back to bytes32 (older tokens like MKR). Returns "" on failure.
func decodeStringOrBytes32(r mc3Result) string {
	if !r.Success || len(r.ReturnData) == 0 {
		return ""
	}
	var str string
	if err := erc20ABI.UnpackIntoInterface(&str, "symbol", r.ReturnData); err == nil {
		return strings.TrimSpace(str)
	}
	var b [32]byte
	if err := erc20BytesABI.UnpackIntoInterface(&b, "symbol", r.ReturnData); err == nil {
		return bytes32ToString(b)
	}
	return ""
}
