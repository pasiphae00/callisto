package tx

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// ErrNoBaseFee means the chain's latest block has no base fee, so EIP-1559
// dynamic-fee transactions aren't supported. Callisto's basic send is 1559-only
// for now (all its target chains support it); legacy pricing is a future add.
var ErrNoBaseFee = errors.New("chain does not report an EIP-1559 base fee")

// gasLimitBufferNum/Den apply a headroom multiplier to the estimated gas limit so
// small state changes between estimation and inclusion don't cause out-of-gas.
// Under EIP-1559 you pay for gas actually used (not the limit), so headroom on the
// limit is safe; only the max-fee reservation scales with it.
const (
	gasLimitBufferNum = 6 // 6/5 = +20%
	gasLimitBufferDen = 5
)

// Fees holds the EIP-1559 fee parameters for a transaction.
type Fees struct {
	GasLimit  uint64
	GasTipCap *big.Int // maxPriorityFeePerGas
	GasFeeCap *big.Int // maxFeePerGas
	BaseFee   *big.Int // observed base fee (for display)
}

// MaxFeeWei is the maximum total fee the transaction could pay: gasLimit * maxFee.
func (f Fees) MaxFeeWei() *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(f.GasLimit), f.GasFeeCap)
}

// EstimateFees derives EIP-1559 fee parameters for a call: gas limit (estimated +
// buffer), priority tip (node suggestion), and a max fee that tolerates several
// blocks of base-fee growth (2*baseFee + tip).
func EstimateFees(ctx context.Context, client rpc.Client, from common.Address, call Call) (Fees, error) {
	gasEstimate, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:  from,
		To:    &call.To,
		Value: call.Value,
		Data:  call.Data,
	})
	if err != nil {
		return Fees{}, fmt.Errorf("estimate gas: %w", err)
	}
	gasLimit := gasEstimate * gasLimitBufferNum / gasLimitBufferDen

	tip, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return Fees{}, fmt.Errorf("suggest tip: %w", err)
	}

	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return Fees{}, fmt.Errorf("latest header: %w", err)
	}
	if head.BaseFee == nil {
		return Fees{}, ErrNoBaseFee
	}

	// maxFee = 2*baseFee + tip (covers ~6 blocks of max base-fee increase).
	maxFee := new(big.Int).Mul(head.BaseFee, big.NewInt(2))
	maxFee.Add(maxFee, tip)

	return Fees{
		GasLimit:  gasLimit,
		GasTipCap: tip,
		GasFeeCap: maxFee,
		BaseFee:   head.BaseFee,
	}, nil
}

// Prepared is a fully-assembled, unsigned transaction ready for review and
// signing, bundling the human intent (Send), the fee parameters, and the nonce.
type Prepared struct {
	Send    Send
	Fees    Fees
	Nonce   uint64
	ChainID *big.Int
	Tx      *types.Transaction
}

// Prepare turns a Send into a signed-ready transaction: it estimates fees, reads
// the pending nonce, and assembles an EIP-1559 transaction. It performs no
// signing or broadcast.
func Prepare(ctx context.Context, client rpc.Client, chainID *big.Int, send Send) (Prepared, error) {
	fees, err := EstimateFees(ctx, client, send.From, send.Call)
	if err != nil {
		return Prepared{}, err
	}
	nonce, err := client.PendingNonceAt(ctx, send.From)
	if err != nil {
		return Prepared{}, fmt.Errorf("pending nonce: %w", err)
	}

	to := send.Call.To
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &to,
		Value:     send.Call.Value,
		Gas:       fees.GasLimit,
		GasTipCap: fees.GasTipCap,
		GasFeeCap: fees.GasFeeCap,
		Data:      send.Call.Data,
	})

	return Prepared{
		Send:    send,
		Fees:    fees,
		Nonce:   nonce,
		ChainID: chainID,
		Tx:      tx,
	}, nil
}
