package tx

import (
	"context"
	"errors"
	"fmt"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// receiptPollInterval is how often inclusion is polled when the endpoint can't
// push (HTTP) or as a backstop.
const receiptPollInterval = 3 * time.Second

// Broadcast submits a signed transaction and returns its hash. A nil error means
// the node accepted the transaction into its pool (not that it is mined).
func Broadcast(ctx context.Context, client rpc.Client, signed *types.Transaction) (common.Hash, error) {
	if err := client.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("broadcast: %w", err)
	}
	return signed.Hash(), nil
}

// WaitForReceipt polls for a transaction receipt until it is available or ctx is
// cancelled. A missing receipt (not yet mined) is retried; other errors are
// returned. The caller controls the overall timeout via ctx.
func WaitForReceipt(ctx context.Context, client rpc.Client, hash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(receiptPollInterval)
	defer ticker.Stop()
	for {
		receipt, err := client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			return receipt, nil
		}
		if err != nil && !errors.Is(err, ethereum.NotFound) {
			// A real error (not "not yet mined") — surface it.
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Succeeded reports whether a receipt indicates successful execution.
func Succeeded(receipt *types.Receipt) bool {
	return receipt != nil && receipt.Status == types.ReceiptStatusSuccessful
}
