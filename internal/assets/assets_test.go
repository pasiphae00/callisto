package assets

import (
	"context"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// mockClient implements rpc.Client; only BalanceAt and CallContract are used.
type mockClient struct {
	native  *big.Int
	handler func(to common.Address, sel [4]byte) ([]byte, error)
}

func (m *mockClient) BalanceAt(ctx context.Context, account common.Address, block *big.Int) (*big.Int, error) {
	return m.native, nil
}
func (m *mockClient) CallContract(ctx context.Context, msg ethereum.CallMsg, block *big.Int) ([]byte, error) {
	var sel [4]byte
	copy(sel[:], msg.Data[:4])
	return m.handler(*msg.To, sel)
}

// unused interface methods
func (m *mockClient) ChainID(context.Context) (*big.Int, error)   { return big.NewInt(1), nil }
func (m *mockClient) BlockNumber(context.Context) (uint64, error) { return 0, nil }
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
