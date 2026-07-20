package assets

import (
	"context"
	"math/big"
	"reflect"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// mockClient implements rpc.Client; only BalanceAt and CallContract are used by
// most tests. head/logs let the discovery tests drive BlockNumber and FilterLogs.
// noMulticall forces the aggregate3 batch to be unavailable so the per-call fallback
// path is exercised.
type mockClient struct {
	native      *big.Int
	handler     func(to common.Address, sel [4]byte) ([]byte, error)
	head        uint64
	logs        []types.Log
	noMulticall bool
}

func (m *mockClient) BalanceAt(ctx context.Context, account common.Address, block *big.Int) (*big.Int, error) {
	return m.native, nil
}
func (m *mockClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	// Return the configured logs whose block falls within the queried window; the
	// topic pre-filtering is the node's job, and DiscoverTokens does the ERC-20 vs
	// ERC-721 and dedup filtering in Go (what these tests exercise).
	var out []types.Log
	for _, lg := range m.logs {
		if q.FromBlock != nil && lg.BlockNumber < q.FromBlock.Uint64() {
			continue
		}
		if q.ToBlock != nil && lg.BlockNumber > q.ToBlock.Uint64() {
			continue
		}
		out = append(out, lg)
	}
	return out, nil
}

func (m *mockClient) CallContract(ctx context.Context, msg ethereum.CallMsg, block *big.Int) ([]byte, error) {
	// Act as a Multicall3 node: decode aggregate3, dispatch each inner call to the
	// per-selector handler (getEthBalance → native), and return packed Result[].
	if !m.noMulticall && msg.To != nil && *msg.To == multicall3 {
		var s4 [4]byte
		copy(s4[:], msg.Data[:4])
		if s4 == mcSel("aggregate3") {
			return m.aggregate3(msg.Data)
		}
	}
	var sel [4]byte
	copy(sel[:], msg.Data[:4])
	return m.handler(*msg.To, sel)
}

// aggregate3 mimics Multicall3.aggregate3: unpack the Call3[] blob, run each inner call
// through the same handler the per-call path uses (with getEthBalance answered from
// native), and pack the Result[] outputs.
func (m *mockClient) aggregate3(data []byte) ([]byte, error) {
	vals, err := multicall3ABI.Methods["aggregate3"].Inputs.Unpack(data[4:])
	if err != nil {
		return nil, err
	}
	arr := reflect.ValueOf(vals[0]) // []struct{ Target; AllowFailure; CallData }
	results := make([]mc3Result, arr.Len())
	getEth := mcSel("getEthBalance")
	for i := 0; i < arr.Len(); i++ {
		e := arr.Index(i)
		target := e.Field(0).Interface().(common.Address)
		callData := e.Field(2).Interface().([]byte)
		var s4 [4]byte
		copy(s4[:], callData[:4])

		var out []byte
		if target == multicall3 && s4 == getEth {
			out, _ = multicall3ABI.Methods["getEthBalance"].Outputs.Pack(m.native)
		} else {
			out, _ = m.handler(target, s4)
		}
		results[i] = mc3Result{Success: len(out) > 0, ReturnData: out}
	}
	return multicall3ABI.Methods["aggregate3"].Outputs.Pack(results)
}

func mcSel(method string) [4]byte {
	var s [4]byte
	copy(s[:], multicall3ABI.Methods[method].ID)
	return s
}

// unused interface methods
func (m *mockClient) ChainID(context.Context) (*big.Int, error)   { return big.NewInt(1), nil }
func (m *mockClient) BlockNumber(context.Context) (uint64, error) { return m.head, nil }
func (m *mockClient) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, nil
}
func (m *mockClient) NonceAt(context.Context, common.Address, *big.Int) (uint64, error) {
	return 0, nil
}
func (m *mockClient) PendingNonceAt(context.Context, common.Address) (uint64, error) { return 0, nil }
func (m *mockClient) SuggestGasTipCap(context.Context) (*big.Int, error)             { return big.NewInt(0), nil }
func (m *mockClient) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)  { return 0, nil }
func (m *mockClient) SendTransaction(context.Context, *types.Transaction) error      { return nil }
func (m *mockClient) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (m *mockClient) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	return nil, nil
}
func (m *mockClient) SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}
func (m *mockClient) Close() {}

func sel(method string) [4]byte {
	var s [4]byte
	copy(s[:], erc20ABI.Methods[method].ID)
	return s
}

func TestServiceLoadNativeFirstThenToken(t *testing.T) {
	selDecimals := sel("decimals")
	selSymbol := sel("symbol")
	selName := sel("name")
	selBalance := sel("balanceOf")

	client := &mockClient{
		native: bigStr("2500000000000000000"), // 2.5 ETH
		handler: func(to common.Address, s [4]byte) ([]byte, error) {
			switch s {
			case selDecimals:
				out, _ := erc20ABI.Methods["decimals"].Outputs.Pack(uint8(6))
				return out, nil
			case selSymbol:
				out, _ := erc20ABI.Methods["symbol"].Outputs.Pack("USDC")
				return out, nil
			case selName:
				out, _ := erc20ABI.Methods["name"].Outputs.Pack("USD Coin")
				return out, nil
			case selBalance:
				out, _ := erc20ABI.Methods["balanceOf"].Outputs.Pack(bigStr("1234560")) // 1.23456 USDC
				return out, nil
			}
			return nil, nil
		},
	}

	svc := NewService(client, 1)
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	got, err := svc.Load(context.Background(), common.HexToAddress("0x1111111111111111111111111111111111111111"), []common.Address{token})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Native must be first.
	if got[0].Kind != Native || got[0].Symbol != "ETH" {
		t.Fatalf("first asset = %+v, want native ETH", got[0])
	}
	if got[0].HumanBalance() != "2.5" {
		t.Errorf("native balance = %s, want 2.5", got[0].HumanBalance())
	}

	// The mainnet curated list also includes this USDC address, so it appears
	// once (deduped) — find the USDC token entry.
	var usdc *Asset
	for i := range got {
		if got[i].Kind == Token && got[i].Symbol == "USDC" {
			usdc = &got[i]
		}
	}
	if usdc == nil {
		t.Fatal("USDC token asset not found")
	}
	if usdc.Decimals != 6 || usdc.Name != "USD Coin" {
		t.Errorf("usdc meta = %+v", usdc)
	}
	if usdc.HumanBalance() != "1.23456" {
		t.Errorf("usdc balance = %s, want 1.23456", usdc.HumanBalance())
	}
}

// TestServiceLoadSerialFallback runs the same load with Multicall3 disabled, verifying
// the per-call fallback path produces identical results (native + decoded USDC).
func TestServiceLoadSerialFallback(t *testing.T) {
	selDecimals := sel("decimals")
	selSymbol := sel("symbol")
	selName := sel("name")
	selBalance := sel("balanceOf")
	client := &mockClient{
		native:      bigStr("2500000000000000000"),
		noMulticall: true, // force the fallback path
		handler: func(to common.Address, s [4]byte) ([]byte, error) {
			switch s {
			case selDecimals:
				out, _ := erc20ABI.Methods["decimals"].Outputs.Pack(uint8(6))
				return out, nil
			case selSymbol:
				out, _ := erc20ABI.Methods["symbol"].Outputs.Pack("USDC")
				return out, nil
			case selName:
				out, _ := erc20ABI.Methods["name"].Outputs.Pack("USD Coin")
				return out, nil
			case selBalance:
				out, _ := erc20ABI.Methods["balanceOf"].Outputs.Pack(bigStr("1234560"))
				return out, nil
			}
			return nil, nil
		},
	}
	svc := NewService(client, 1)
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	got, err := svc.Load(context.Background(), common.HexToAddress("0x1111111111111111111111111111111111111111"), []common.Address{token})
	if err != nil {
		t.Fatalf("Load (fallback): %v", err)
	}
	if got[0].Kind != Native || got[0].HumanBalance() != "2.5" {
		t.Errorf("native = %+v, want 2.5 ETH", got[0])
	}
	var usdc *Asset
	for i := range got {
		if got[i].Symbol == "USDC" {
			usdc = &got[i]
		}
	}
	if usdc == nil || usdc.HumanBalance() != "1.23456" || usdc.Decimals != 6 {
		t.Errorf("usdc via fallback = %+v", usdc)
	}
}

func TestServiceSkipsBadToken(t *testing.T) {
	// A token whose decimals() reverts (nil return) is skipped, not fatal.
	client := &mockClient{
		native: big.NewInt(0),
		handler: func(to common.Address, s [4]byte) ([]byte, error) {
			return nil, context.DeadlineExceeded // every call fails
		},
	}
	svc := NewService(client, 1) // curated mainnet tokens will all fail
	got, err := svc.Load(context.Background(), common.HexToAddress("0x2222222222222222222222222222222222222222"), nil)
	if err != nil {
		t.Fatalf("Load should not fail on bad tokens: %v", err)
	}
	if len(got) != 1 || got[0].Kind != Native {
		t.Errorf("only native should remain, got %d assets", len(got))
	}
}

func TestBytes32ToString(t *testing.T) {
	var b [32]byte
	copy(b[:], "MKR")
	if got := bytes32ToString(b); got != "MKR" {
		t.Errorf("bytes32ToString = %q, want MKR", got)
	}
}
