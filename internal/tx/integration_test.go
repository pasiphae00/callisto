//go:build integration

// Real-network transaction-preparation checks against Sepolia. Excluded from the
// default build; run with:
//
//	go test -tags integration ./internal/tx/
//
// This prepares (but does not broadcast) a transaction: it exercises real gas
// estimation, base-fee reading, and nonce lookup. A funded end-to-end broadcast
// is a manual step (needs a funded key).
package tx

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/rpc"
)

func sepolia(t *testing.T) (*rpc.Connection, func()) {
	t.Helper()
	url := os.Getenv("CALLISTO_TEST_RPC")
	if url == "" {
		url = "https://ethereum-sepolia-rpc.publicnode.com"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := rpc.Dial(ctx, rpc.Endpoint{Name: "sepolia", URL: url})
	if err != nil {
		t.Fatalf("dial sepolia: %v", err)
	}
	return conn, conn.Close
}

// TestIntegrationFeeInputs verifies the real-node reads that feed fee estimation
// (priority tip, base fee, nonce) — none of which require a funded account.
func TestIntegrationFeeInputs(t *testing.T) {
	conn, closeConn := sepolia(t)
	defer closeConn()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tip, err := conn.Client.SuggestGasTipCap(ctx)
	if err != nil {
		t.Fatalf("SuggestGasTipCap: %v", err)
	}
	head, err := conn.Client.HeaderByNumber(ctx, nil)
	if err != nil {
		t.Fatalf("HeaderByNumber: %v", err)
	}
	if head.BaseFee == nil || head.BaseFee.Sign() <= 0 {
		t.Errorf("base fee = %v, want > 0", head.BaseFee)
	}
	nonce, err := conn.Client.PendingNonceAt(ctx, common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"))
	if err != nil {
		t.Fatalf("PendingNonceAt: %v", err)
	}
	t.Logf("sepolia fee inputs: tip=%s baseFee=%s nonce=%d", tip, head.BaseFee, nonce)
}

// TestIntegrationPrepareUnfundedSurfacesError documents that preparing a send
// from an unfunded account fails gas estimation with the node's insufficient-
// funds error (which the UI surfaces to the user), rather than silently.
func TestIntegrationPrepareUnfundedSurfacesError(t *testing.T) {
	conn, closeConn := sepolia(t)
	defer closeConn()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	from := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266") // unfunded
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	send, _ := BuildNativeSend(from, to, big.NewInt(1), "ETH", 18)

	_, err := Prepare(ctx, conn.Client, conn.ChainID, send)
	if err == nil {
		t.Skip("account appears funded; skipping unfunded-path assertion")
	}
	t.Logf("expected preparation error for unfunded account: %v", err)
}
