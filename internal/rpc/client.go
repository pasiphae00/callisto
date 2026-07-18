package rpc

import (
	"context"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Client is the subset of go-ethereum's ethclient surface that Callisto's domain
// logic depends on. Abstracting it as an interface lets asset population, gas
// estimation, broadcast, and inclusion tracking be unit-tested against a mock
// without a live node, and lets the active endpoint be swapped underneath them.
//
// *ethclient.Client satisfies this interface directly (see connection.go).
type Client interface {
	// Chain / head.
	ChainID(ctx context.Context) (*big.Int, error)
	BlockNumber(ctx context.Context) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)

	// State reads.
	BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error)
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)

	// Gas.
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error)

	// Broadcast / inclusion.
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)

	// Live subscriptions (WebSocket transports only; see Endpoint.Scheme).
	SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error)

	// Close releases the underlying connection.
	Close()
}
