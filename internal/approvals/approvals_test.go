package approvals

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// fakeClient implements rpc.Client for tests via hook functions; unused methods
// return zero values.
type fakeClient struct {
	head     uint64
	nonceFn  func(block uint64) uint64
	filterFn func(q ethereum.FilterQuery) ([]types.Log, error)
	callFn   func(msg ethereum.CallMsg) ([]byte, error)
}

func (f *fakeClient) BlockNumber(context.Context) (uint64, error) { return f.head, nil }
func (f *fakeClient) NonceAt(_ context.Context, _ common.Address, block *big.Int) (uint64, error) {
	if f.nonceFn == nil {
		return 0, nil
	}
	return f.nonceFn(block.Uint64()), nil
}
func (f *fakeClient) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if f.filterFn == nil {
		return nil, nil
	}
	return f.filterFn(q)
}
func (f *fakeClient) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	if f.callFn == nil {
		return nil, nil
	}
	return f.callFn(msg)
}

// unused rpc.Client surface.
func (f *fakeClient) ChainID(context.Context) (*big.Int, error) { return big.NewInt(1), nil }
func (f *fakeClient) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return &types.Header{}, nil
}
func (f *fakeClient) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (f *fakeClient) PendingNonceAt(context.Context, common.Address) (uint64, error) { return 0, nil }
func (f *fakeClient) SuggestGasTipCap(context.Context) (*big.Int, error)             { return big.NewInt(0), nil }
func (f *fakeClient) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)  { return 0, nil }
func (f *fakeClient) SendTransaction(context.Context, *types.Transaction) error      { return nil }
func (f *fakeClient) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (f *fakeClient) SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}
func (f *fakeClient) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	return nil, nil
}
func (f *fakeClient) RawClient() *gethrpc.Client { return nil }
func (f *fakeClient) Close()                     {}

// stubMeta gives every token a fixed symbol so tests don't hit CallContract for
// metadata.
func stubScanner(f *fakeClient) *Scanner {
	s := NewScanner(f, 1)
	s.meta = func(context.Context, common.Address) (string, uint8, bool) { return "TKN", 18, true }
	return s
}

func approvalLog(token, owner, spender common.Address) types.Log {
	return types.Log{
		Address: token,
		Topics: []common.Hash{
			approvalEventSig,
			common.BytesToHash(owner.Bytes()),
			common.BytesToHash(spender.Bytes()),
		},
	}
}

func TestFirstActiveBlock(t *testing.T) {
	// First tx at block 1000: nonce is 0 below it, 1 at/above.
	f := &fakeClient{head: 20000, nonceFn: func(b uint64) uint64 {
		if b >= 1000 {
			return 1
		}
		return 0
	}}
	if got := firstActiveBlock(context.Background(), f, common.Address{}, f.head); got != 1000 {
		t.Errorf("firstActiveBlock = %d, want 1000", got)
	}

	// Never sent a tx (nonce 0 at head) ⇒ head (empty scan).
	f2 := &fakeClient{head: 5000, nonceFn: func(uint64) uint64 { return 0 }}
	if got := firstActiveBlock(context.Background(), f2, common.Address{}, f2.head); got != 5000 {
		t.Errorf("firstActiveBlock (inactive) = %d, want 5000", got)
	}
}

func TestScanDirect(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	tokenA := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	spender1 := common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D") // Uniswap V2 (labeled)
	spender2 := common.HexToAddress("0xBBBB000000000000000000000000000000000002")

	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	allowances := map[common.Address]*big.Int{
		spender1: maxUint256,    // unlimited, live
		spender2: big.NewInt(0), // revoked ⇒ dropped
	}

	f := &fakeClient{
		head:    2000,
		nonceFn: func(uint64) uint64 { return 1 },
		filterFn: func(ethereum.FilterQuery) ([]types.Log, error) {
			return []types.Log{
				approvalLog(tokenA, owner, spender1),
				approvalLog(tokenA, owner, spender1), // duplicate pair ⇒ deduped
				approvalLog(tokenA, owner, spender2),
			}, nil
		},
		callFn: func(msg ethereum.CallMsg) ([]byte, error) {
			// decode allowance(owner, spender): last 20 bytes of the 2nd arg word.
			spender := common.BytesToAddress(msg.Data[len(msg.Data)-20:])
			amt := allowances[spender]
			if amt == nil {
				amt = big.NewInt(0)
			}
			return erc20ABI.Methods["allowance"].Outputs.Pack(amt)
		},
	}

	got, err := stubScanner(f).scanDirect(context.Background(), owner, 0, f.head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 live approval, got %d: %+v", len(got), got)
	}
	a := got[0]
	if a.Spender != spender1 || !a.Unlimited || a.Layer != LayerDirect {
		t.Errorf("unexpected approval: %+v", a)
	}
	if a.SpenderLabel != "Uniswap V2 Router" {
		t.Errorf("spender label = %q", a.SpenderLabel)
	}
}

func TestScanPermit2ExpiryFilter(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	liveSpender := common.HexToAddress("0x3fC91A3afd70395Cd496C647d5a6CC9D4B2b7FAD")
	expiredSpender := common.HexToAddress("0xCCCC000000000000000000000000000000000003")

	permit2Log := func(sp common.Address) types.Log {
		return types.Log{Address: permit2Address, Topics: []common.Hash{
			permit2ApprovalSig,
			common.BytesToHash(owner.Bytes()),
			common.BytesToHash(token.Bytes()),
			common.BytesToHash(sp.Bytes()),
		}}
	}
	f := &fakeClient{
		head:    100,
		nonceFn: func(uint64) uint64 { return 1 },
		filterFn: func(ethereum.FilterQuery) ([]types.Log, error) {
			return []types.Log{permit2Log(liveSpender), permit2Log(expiredSpender)}, nil
		},
		callFn: func(msg ethereum.CallMsg) ([]byte, error) {
			sp := common.BytesToAddress(msg.Data[len(msg.Data)-20:])
			exp := big.NewInt(1) // long expired
			if sp == liveSpender {
				exp = new(big.Int).SetInt64(1 << 40) // far future
			}
			return permit2ABI.Methods["allowance"].Outputs.Pack(maxUint160, exp, big.NewInt(0))
		},
	}
	got, err := stubScanner(f).scanPermit2(context.Background(), owner, 0, f.head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Spender != liveSpender {
		t.Fatalf("expiry filter failed: %+v", got)
	}
	if !got[0].Unlimited || got[0].Layer != LayerPermit2 {
		t.Errorf("permit2 approval flags wrong: %+v", got[0])
	}
}

func TestRevokeCall(t *testing.T) {
	token := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	spender := common.HexToAddress("0xBBBB000000000000000000000000000000000002")

	// Direct: approve(spender, 0) to the token.
	d := Approval{Layer: LayerDirect, Token: token, Spender: spender}
	to, data, err := d.RevokeCall()
	if err != nil || to != token {
		t.Fatalf("direct revoke to=%s err=%v", to.Hex(), err)
	}
	args, err := erc20ABI.Methods["approve"].Inputs.Unpack(data[4:])
	if err != nil || args[0].(common.Address) != spender || args[1].(*big.Int).Sign() != 0 {
		t.Errorf("approve calldata wrong: %v (err %v)", args, err)
	}

	// Permit2: lockdown([(token, spender)]) to the Permit2 contract.
	p := Approval{Layer: LayerPermit2, Token: token, Spender: spender}
	to, data, err = p.RevokeCall()
	if err != nil || to != permit2Address {
		t.Fatalf("permit2 revoke to=%s err=%v", to.Hex(), err)
	}
	if len(data) < 4 {
		t.Fatal("empty lockdown calldata")
	}
}

func TestDisplayHelpers(t *testing.T) {
	a := Approval{Token: common.HexToAddress("0xAAAA000000000000000000000000000000000001"), Spender: permit2Address, SpenderLabel: "Uniswap Permit2", TokenSymbol: "USDC"}
	if a.DisplayToken() != "USDC" {
		t.Errorf("DisplayToken = %q", a.DisplayToken())
	}
	if got := a.DisplaySpender(); got == "" || got[:15] != "Uniswap Permit2" {
		t.Errorf("DisplaySpender = %q", got)
	}
	// Unknown token ⇒ short address.
	b := Approval{Token: common.HexToAddress("0xAAAA000000000000000000000000000000000001")}
	if b.DisplayToken() != Short(b.Token) {
		t.Errorf("DisplayToken (no symbol) = %q", b.DisplayToken())
	}
}

func TestRefreshIncremental(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	spenderA := common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D") // cached, now revoked
	spenderB := common.HexToAddress("0xBBBB000000000000000000000000000000000002") // new, live

	f := &fakeClient{
		head:    2000,
		nonceFn: func(uint64) uint64 { return 1 },
		filterFn: func(q ethereum.FilterQuery) ([]types.Log, error) {
			if len(q.Addresses) > 0 { // Permit2 pass: nothing changed
				return nil, nil
			}
			return []types.Log{ // direct pass: A changed (revoked), B is new
				approvalLog(token, owner, spenderA),
				approvalLog(token, owner, spenderB),
			}, nil
		},
		callFn: func(msg ethereum.CallMsg) ([]byte, error) {
			sp := common.BytesToAddress(msg.Data[len(msg.Data)-20:])
			amt := big.NewInt(0) // spenderA → 0 (revoked)
			if sp == spenderB {
				amt = big.NewInt(500)
			}
			return erc20ABI.Methods["allowance"].Outputs.Pack(amt)
		},
	}
	cached := []Approval{{Layer: LayerDirect, Token: token, Spender: spenderA, Amount: big.NewInt(999)}}

	got, wm, err := stubScanner(f).Refresh(context.Background(), owner, 1000, cached, nil)
	if err != nil {
		t.Fatal(err)
	}
	if wm != 2000 {
		t.Errorf("new watermark = %d, want 2000", wm)
	}
	// A is dropped (allowance 0), B is kept (live 500).
	if len(got) != 1 || got[0].Spender != spenderB || got[0].Amount.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("incremental refresh result: %+v", got)
	}
}

func TestParseBlockRangeLimit(t *testing.T) {
	cases := map[string]uint64{
		"query block range exceeds server limit, narrow your filter: 1000": 1000,
		"eth_getLogs range too large, max 10000 blocks":                    10000,
		"no numbers here":             0,
		"from 500 to 999, limit 2000": 2000, // last integer wins
	}
	for msg, want := range cases {
		if got := parseBlockRangeLimit(msg); got != want {
			t.Errorf("parseBlockRangeLimit(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestScanHonorsServerBlockLimit(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	spender := common.HexToAddress("0xBBBB000000000000000000000000000000000002")

	const cap = uint64(1000)
	f := &fakeClient{
		head:    2500,
		nonceFn: func(uint64) uint64 { return 1 },
		filterFn: func(q ethereum.FilterQuery) ([]types.Log, error) {
			if span := q.ToBlock.Uint64() - q.FromBlock.Uint64(); span > cap {
				return nil, fmt.Errorf("query block range exceeds server limit, narrow your filter: %d", cap)
			}
			return []types.Log{approvalLog(token, owner, spender)}, nil
		},
		callFn: func(ethereum.CallMsg) ([]byte, error) {
			return erc20ABI.Methods["allowance"].Outputs.Pack(big.NewInt(500))
		},
	}
	got, err := stubScanner(f).scanDirect(context.Background(), owner, 0, f.head, nil)
	if err != nil {
		t.Fatalf("scan should honor the server cap and complete, got %v", err)
	}
	if len(got) != 1 || got[0].Amount.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("unexpected result under capped scan: %+v", got)
	}
}

func TestSpenderLabel(t *testing.T) {
	if spenderLabel(1, permit2Address) != "Uniswap Permit2" {
		t.Error("Permit2 should be labeled on any chain")
	}
	if spenderLabel(1, common.HexToAddress("0xC92E8bdf79f0507f65a392b0ab4667716BFE0110")) != "CoW Protocol (GPv2 Vault Relayer)" {
		t.Error("CoW relayer label missing")
	}
	if spenderLabel(1, common.HexToAddress("0xdeadbeef00000000000000000000000000000000")) != "" {
		t.Error("unknown spender should have no label")
	}
}
