package ens

import (
	"context"
	"encoding/hex"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// --- NameHash known-answer vectors (EIP-137) --------------------------------

func TestNameHashVectors(t *testing.T) {
	cases := map[string]string{
		"":        "0000000000000000000000000000000000000000000000000000000000000000",
		"eth":     "93cdeb708b7545dc668eb9280176169d1c33cfd8ed6f04690a0bcc88a93fc4ae",
		"foo.eth": "de9b09fd7c5f901e23a3f19fecc54828e9c848539801e86591bd9801b019f84f",
	}
	for name, want := range cases {
		node := NameHash(name)
		got := hex.EncodeToString(node[:])
		if got != want {
			t.Errorf("NameHash(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestLooksLikeENS(t *testing.T) {
	yes := []string{"vitalik.eth", "foo.bar.eth", "a.xyz"}
	no := []string{"", "vitalik", "0xabc.eth", "0X1234", "nodot"}
	for _, s := range yes {
		if !LooksLikeENS(s) {
			t.Errorf("LooksLikeENS(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if LooksLikeENS(s) {
			t.Errorf("LooksLikeENS(%q) = true, want false", s)
		}
	}
}

// --- mock client ------------------------------------------------------------

// mockRPC implements rpc.Client; only CallContract is meaningful.
type mockRPC struct {
	handler func(to common.Address, selector [4]byte, node [32]byte) ([]byte, error)
}

func (m *mockRPC) CallContract(ctx context.Context, msg ethereum.CallMsg, block *big.Int) ([]byte, error) {
	var sel [4]byte
	copy(sel[:], msg.Data[:4])
	var node [32]byte
	copy(node[:], msg.Data[4:36])
	return m.handler(*msg.To, sel, node)
}

func (m *mockRPC) ChainID(context.Context) (*big.Int, error)      { return big.NewInt(1), nil }
func (m *mockRPC) BlockNumber(context.Context) (uint64, error)    { return 0, nil }
func (m *mockRPC) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, nil
}
func (m *mockRPC) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (m *mockRPC) NonceAt(context.Context, common.Address, *big.Int) (uint64, error) { return 0, nil }
func (m *mockRPC) PendingNonceAt(context.Context, common.Address) (uint64, error)    { return 0, nil }
func (m *mockRPC) SuggestGasTipCap(context.Context) (*big.Int, error)                { return big.NewInt(0), nil }
func (m *mockRPC) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)     { return 0, nil }
func (m *mockRPC) SendTransaction(context.Context, *types.Transaction) error         { return nil }
func (m *mockRPC) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (m *mockRPC) SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}
func (m *mockRPC) Close() {}

// selectors for routing in the mock.
var (
	selResolver = mustSelector("resolver")
	selAddr     = mustSelector("addr")
	selName     = mustSelector("name")
)

func mustSelector(method string) [4]byte {
	var s [4]byte
	copy(s[:], parsedABI.Methods[method].ID)
	return s
}

func packAddr(method string, a common.Address) []byte {
	out, err := parsedABI.Methods[method].Outputs.Pack(a)
	if err != nil {
		panic(err)
	}
	return out
}

func packName(name string) []byte {
	out, err := parsedABI.Methods["name"].Outputs.Pack(name)
	if err != nil {
		panic(err)
	}
	return out
}

// --- resolution tests -------------------------------------------------------

func TestResolveForward(t *testing.T) {
	target := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
	resolver := common.HexToAddress("0x0000000000000000000000000000000000001234")

	client := &mockRPC{handler: func(to common.Address, sel [4]byte, node [32]byte) ([]byte, error) {
		switch sel {
		case selResolver:
			return packAddr("resolver", resolver), nil
		case selAddr:
			return packAddr("addr", target), nil
		}
		return nil, nil
	}}

	got, err := NewResolver(client).Resolve(context.Background(), "vitalik.eth")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != target {
		t.Errorf("Resolve = %s, want %s", got.Hex(), target.Hex())
	}
}

func TestResolveNoResolver(t *testing.T) {
	client := &mockRPC{handler: func(to common.Address, sel [4]byte, node [32]byte) ([]byte, error) {
		// resolver() returns the zero address -> not found.
		return packAddr("resolver", common.Address{}), nil
	}}
	_, err := NewResolver(client).Resolve(context.Background(), "nope.eth")
	if err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestReverseResolveVerified(t *testing.T) {
	addr := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
	resolver := common.HexToAddress("0x0000000000000000000000000000000000001234")

	client := &mockRPC{handler: func(to common.Address, sel [4]byte, node [32]byte) ([]byte, error) {
		switch sel {
		case selResolver:
			return packAddr("resolver", resolver), nil
		case selName:
			return packName("vitalik.eth"), nil
		case selAddr:
			// forward-verify resolves back to the same address
			return packAddr("addr", addr), nil
		}
		return nil, nil
	}}

	name, err := NewResolver(client).ReverseResolve(context.Background(), addr)
	if err != nil {
		t.Fatalf("ReverseResolve: %v", err)
	}
	if name != "vitalik.eth" {
		t.Errorf("name = %q, want vitalik.eth", name)
	}
}

func TestReverseResolveUnverifiedRejected(t *testing.T) {
	addr := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
	other := common.HexToAddress("0x1111111111111111111111111111111111111111")
	resolver := common.HexToAddress("0x0000000000000000000000000000000000001234")

	client := &mockRPC{handler: func(to common.Address, sel [4]byte, node [32]byte) ([]byte, error) {
		switch sel {
		case selResolver:
			return packAddr("resolver", resolver), nil
		case selName:
			return packName("imposter.eth"), nil
		case selAddr:
			// forward resolves to a DIFFERENT address -> reverse must be rejected
			return packAddr("addr", other), nil
		}
		return nil, nil
	}}

	_, err := NewResolver(client).ReverseResolve(context.Background(), addr)
	if err != ErrNotFound {
		t.Errorf("unverified reverse record should be rejected, got err=%v", err)
	}
}
