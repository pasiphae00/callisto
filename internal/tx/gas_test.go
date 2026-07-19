package tx

import (
	"context"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// txMock implements rpc.Client for prepare/estimate tests.
type txMock struct {
	gasEstimate uint64
	tip         *big.Int
	baseFee     *big.Int
	nonce       uint64
}

func (m *txMock) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error) {
	return m.gasEstimate, nil
}
func (m *txMock) SuggestGasTipCap(context.Context) (*big.Int, error) { return m.tip, nil }
func (m *txMock) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return &types.Header{Number: big.NewInt(100), BaseFee: m.baseFee}, nil
}
func (m *txMock) PendingNonceAt(context.Context, common.Address) (uint64, error) { return m.nonce, nil }

// unused interface methods
func (m *txMock) ChainID(context.Context) (*big.Int, error)   { return big.NewInt(1), nil }
func (m *txMock) BlockNumber(context.Context) (uint64, error) { return 0, nil }
func (m *txMock) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (m *txMock) NonceAt(context.Context, common.Address, *big.Int) (uint64, error) { return 0, nil }
func (m *txMock) FilterLogs(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}

func (m *txMock) CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	return nil, nil
}
func (m *txMock) SendTransaction(context.Context, *types.Transaction) error { return nil }
func (m *txMock) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (m *txMock) SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}
func (m *txMock) Close() {}

func TestEstimateFees(t *testing.T) {
	m := &txMock{
		gasEstimate: 21000,
		tip:         big.NewInt(1_000_000_000),  // 1 gwei
		baseFee:     big.NewInt(10_000_000_000), // 10 gwei
	}
	from := common.HexToAddress("0x1")
	call := Call{To: common.HexToAddress("0x2"), Value: big.NewInt(1), Data: nil}

	fees, err := EstimateFees(context.Background(), m, from, call)
	if err != nil {
		t.Fatal(err)
	}
	// gas limit = 21000 * 6/5 = 25200
	if fees.GasLimit != 25200 {
		t.Errorf("gas limit = %d, want 25200", fees.GasLimit)
	}
	if fees.GasTipCap.Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("tip = %s", fees.GasTipCap)
	}
	// maxFee = 2*10gwei + 1gwei = 21 gwei
	if fees.GasFeeCap.Cmp(big.NewInt(21_000_000_000)) != 0 {
		t.Errorf("maxFee = %s, want 21e9", fees.GasFeeCap)
	}
	// maxFeeWei = 25200 * 21e9
	wantMax := new(big.Int).Mul(big.NewInt(25200), big.NewInt(21_000_000_000))
	if fees.MaxFeeWei().Cmp(wantMax) != 0 {
		t.Errorf("maxFeeWei = %s, want %s", fees.MaxFeeWei(), wantMax)
	}
}

func TestEstimateFeesNoBaseFee(t *testing.T) {
	m := &txMock{gasEstimate: 21000, tip: big.NewInt(1), baseFee: nil}
	_, err := EstimateFees(context.Background(), m, common.HexToAddress("0x1"),
		Call{To: common.HexToAddress("0x2"), Value: big.NewInt(1)})
	if err != ErrNoBaseFee {
		t.Errorf("err = %v, want ErrNoBaseFee", err)
	}
}

func TestPrepareAssemblesDynamicTx(t *testing.T) {
	m := &txMock{
		gasEstimate: 21000,
		tip:         big.NewInt(1_000_000_000),
		baseFee:     big.NewInt(10_000_000_000),
		nonce:       7,
	}
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	send, _ := BuildNativeSend(from, to, big.NewInt(1_000_000_000_000_000_000), "ETH", 18)

	chainID := big.NewInt(11155111)
	prep, err := Prepare(context.Background(), m, chainID, send)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Nonce != 7 {
		t.Errorf("nonce = %d, want 7", prep.Nonce)
	}
	if prep.Tx.Type() != types.DynamicFeeTxType {
		t.Errorf("tx type = %d, want dynamic-fee", prep.Tx.Type())
	}
	if prep.Tx.Nonce() != 7 || prep.Tx.Gas() != 25200 {
		t.Errorf("tx nonce/gas = %d/%d", prep.Tx.Nonce(), prep.Tx.Gas())
	}
	if got := prep.Tx.To(); got == nil || *got != to {
		t.Errorf("tx.To = %v, want %s", got, to)
	}
	if prep.Tx.Value().Cmp(big.NewInt(1_000_000_000_000_000_000)) != 0 {
		t.Errorf("tx value = %s", prep.Tx.Value())
	}
	if prep.Tx.ChainId().Cmp(chainID) != 0 {
		t.Errorf("chainID = %s", prep.Tx.ChainId())
	}
}
