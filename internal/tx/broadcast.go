package tx

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// receiptPollInterval is how often inclusion is polled when the endpoint can't
// push (HTTP) or as a backstop. A var (not const) so tests can shrink it.
var receiptPollInterval = 3 * time.Second

// Broadcast submits a signed transaction and returns its hash. A nil error means
// the node accepted the transaction into its pool (not that it is mined).
func Broadcast(ctx context.Context, client rpc.Client, signed *types.Transaction) (common.Hash, error) {
	if err := client.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("broadcast: %w", err)
	}
	return signed.Hash(), nil
}

// WaitForReceipt polls for a transaction receipt until it is available or ctx is
// cancelled. The caller controls the overall timeout via ctx.
//
// Any error from the receipt query is treated as "not available yet" and retried,
// not surfaced immediately. A not-yet-mined transaction usually shows up as
// ethereum.NotFound, but nodes vary: an archive/erigon node, or one reached through
// a proxy or bearer-auth gateway, can return other transient errors for an
// unknown/pending hash. The hash here came from our own accepted broadcast, so the
// only real outcomes are "eventually mined" or "still pending until ctx expires" —
// aborting on a single transient blip would wrongly report "could not confirm
// inclusion" for a transaction that in fact lands fine.
func WaitForReceipt(ctx context.Context, client rpc.Client, hash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(receiptPollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		receipt, err := client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			return receipt, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("timed out waiting for receipt (last error: %v): %w", lastErr, ctx.Err())
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Succeeded reports whether a receipt indicates successful execution.
func Succeeded(receipt *types.Receipt) bool {
	return receipt != nil && receipt.Status == types.ReceiptStatusSuccessful
}
