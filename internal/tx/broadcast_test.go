package tx

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestWaitForReceiptPollsThroughTransientErrors verifies that a non-NotFound error
// from the receipt query (as some archive/proxied endpoints return for a pending
// hash) does not abort the wait — the receipt is still returned once it lands.
func TestWaitForReceiptPollsThroughTransientErrors(t *testing.T) {
	old := receiptPollInterval
	receiptPollInterval = time.Millisecond
	defer func() { receiptPollInterval = old }()

	want := &types.Receipt{Status: types.ReceiptStatusSuccessful, BlockNumber: big.NewInt(123)}
	m := &txMock{
		receipt:        want,
		receiptErr:     errors.New("header not found"), // not ethereum.NotFound
		receiptOKAfter: 3,                              // 3 transient errors, then success
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := WaitForReceipt(ctx, m, common.Hash{})
	if err != nil {
		t.Fatalf("WaitForReceipt: %v", err)
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if m.receiptCalls < 4 {
		t.Errorf("expected to poll through the errors, calls = %d", m.receiptCalls)
	}
}

// TestWaitForReceiptTimeoutIncludesLastError verifies that on ctx timeout the last
// underlying error is surfaced for diagnosis.
func TestWaitForReceiptTimeoutIncludesLastError(t *testing.T) {
	old := receiptPollInterval
	receiptPollInterval = time.Millisecond
	defer func() { receiptPollInterval = old }()

	m := &txMock{receiptErr: errors.New("boom"), receiptOKAfter: 1 << 30} // never succeeds
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := WaitForReceipt(ctx, m, common.Hash{})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected last error surfaced, got %v", err)
	}
}
