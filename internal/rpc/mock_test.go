package rpc

import (
	"context"
	"math/big"
	"sync"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// mockClient is a controllable Client for tests. Unset fields return zero values
// / nil errors. It satisfies the Client interface.
type mockClient struct {
	mu sync.Mutex

	chainID   *big.Int
	chainErr  error
	closed    bool
	headNum   uint64
	balances  map[common.Address]*big.Int
	callRet   []byte
	callErr   error
	sentTxs   []*types.Transaction
	receipts  map[common.Hash]*types.Receipt
	subChan   chan<- *types.Header
	subErrCh  chan error
	failSub   bool
	nonce     uint64
	gasTip    *big.Int
	gasEst    uint64
	closeHook func()
}

func newMockClient(chainID uint64) *mockClient {
	return &mockClient{
		chainID:  new(big.Int).SetUint64(chainID),
		balances: map[common.Address]*big.Int{},
		receipts: map[common.Hash]*types.Receipt{},
		subErrCh: make(chan error, 1),
		gasTip:   big.NewInt(1_000_000_000),
	}
}

func (m *mockClient) ChainID(ctx context.Context) (*big.Int, error) {
	if m.chainErr != nil {
		return nil, m.chainErr
	}
	return m.chainID, nil
}

func (m *mockClient) BlockNumber(ctx context.Context) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.headNum, nil
}

func (m *mockClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &types.Header{Number: new(big.Int).SetUint64(m.headNum), Time: 1_700_000_000}, nil
}

func (m *mockClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	if b, ok := m.balances[account]; ok {
		return b, nil
	}
	return big.NewInt(0), nil
}

func (m *mockClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	return m.nonce, nil
}

func (m *mockClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return m.nonce, nil
}

func (m *mockClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}

func (m *mockClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return m.callRet, m.callErr
}

func (m *mockClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return m.gasTip, nil
}

func (m *mockClient) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	return m.gasEst, nil
}

func (m *mockClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentTxs = append(m.sentTxs, tx)
	return nil
}

func (m *mockClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if r, ok := m.receipts[txHash]; ok {
		return r, nil
	}
	return nil, ethereum.NotFound
}

// mockSubscription is a minimal ethereum.Subscription.
type mockSubscription struct {
	errCh chan error
}

func (s *mockSubscription) Unsubscribe()      {}
func (s *mockSubscription) Err() <-chan error { return s.errCh }

func (m *mockClient) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	return &mockSubscription{errCh: m.subErrCh}, nil
}

func (m *mockClient) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	if m.failSub {
		return nil, ethereum.NotFound
	}
	m.mu.Lock()
	m.subChan = ch
	m.mu.Unlock()
	return &mockSubscription{errCh: m.subErrCh}, nil
}

// pushHead simulates a new head arriving over the subscription.
func (m *mockClient) pushHead(n uint64) {
	m.mu.Lock()
	ch := m.subChan
	m.headNum = n
	m.mu.Unlock()
	if ch != nil {
		ch <- &types.Header{Number: new(big.Int).SetUint64(n)}
	}
}

func (m *mockClient) RawClient() *gethrpc.Client { return nil }

func (m *mockClient) Close() {
	m.mu.Lock()
	m.closed = true
	hook := m.closeHook
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (m *mockClient) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}
