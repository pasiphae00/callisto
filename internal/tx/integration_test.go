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

	_, err := Prepare(ctx, conn.Client, conn.ChainID, send, DefaultPriority)
	if err == nil {
		t.Skip("account appears funded; skipping unfunded-path assertion")
	}
	t.Logf("expected preparation error for unfunded account: %v", err)
}

// TestIntegrationTierSanityOnMainnet reports what each tier actually bids
// against live mainnet, alongside the node's own marginal-price suggestion and
// the raw percentiles the blend is derived from. It exists because the tiers
// were once ~187x-2700x above the marginal price without anyone noticing: the
// numbers need to be looked at on a real fee market, not just a fake one.
//
// Assertions are the invariants only (ordering, and never below the floor) —
// the absolute values are logged for a human to sanity-check, since what is
// "reasonable" depends on the day's congestion.
func TestIntegrationTierSanityOnMainnet(t *testing.T) {
	url := os.Getenv("CALLISTO_TEST_MAINNET_RPC")
	if url == "" {
		url = "https://ethereum-rpc.publicnode.com"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := rpc.Dial(ctx, rpc.Endpoint{Name: "mainnet", URL: url})
	if err != nil {
		t.Skipf("dial mainnet: %v", err)
	}
	defer conn.Close()

	suggestion, err := conn.Client.SuggestGasTipCap(ctx)
	if err != nil {
		t.Fatalf("SuggestGasTipCap: %v", err)
	}
	t.Logf("node marginal price (eth_maxPriorityFeePerGas): %s gwei", inGwei(suggestion))

	var prev *big.Int
	for _, tier := range Priorities() {
		tip, err := SuggestTip(ctx, conn.Client, tier)
		if err != nil {
			t.Fatalf("%v: %v", tier, err)
		}
		// A second read, so the head may have advanced by a block or two since
		// the bid was computed: these are context for a human, not the exact
		// inputs. The blend arithmetic itself is pinned by the unit tests.
		target, congestion, ok := sampleFeeHistory(ctx, conn.Client, tier)
		if ok {
			t.Logf("%-8s bid %8s gwei   (~p%.0f %s gwei, blocks ~%.0f%% full)",
				tier.Label(), inGwei(tip), tier.rewardPercentile(), inGwei(target),
				float64(congestion)/congestionScale*100)
		}

		num, den := tier.floorScale()
		floor := new(big.Int).Div(new(big.Int).Mul(suggestion, big.NewInt(num)), big.NewInt(den))
		if tip.Cmp(floor) < 0 {
			t.Errorf("%v bids %v, below its %v floor — inclusion is not guaranteed", tier, tip, floor)
		}
		if prev != nil && tip.Cmp(prev) < 0 {
			t.Errorf("%v bids %v, less than the tier below it (%v)", tier, tip, prev)
		}
		prev = tip
	}
}

// inGwei renders wei as a gwei string for log output.
func inGwei(wei *big.Int) string {
	f := new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e9))
	return f.Text('f', 5)
}
