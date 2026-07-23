//go:build integration

// These tests hit a real Ethereum node and are excluded from the default build:
//
//	go test -tags integration ./internal/safe/
//
// They require a mainnet (or any chain) RPC in CALLISTO_TEST_MAINNET_RPC and the
// address of a real Safe to read in CALLISTO_TEST_SAFE. The core assertion is that
// the locally-computed EIP-712 safeTxHash equals the Safe's own getTransactionHash
// — proving Callisto signs the same digest the contract will verify. LocalHash
// assumes the v1.3.0+ domain (chainId-bound); the test skips older Safes.
package safe

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/rpc"
)

func mainnetEndpoint() rpc.Endpoint {
	url := os.Getenv("CALLISTO_TEST_MAINNET_RPC")
	if url == "" {
		url = "https://ethereum-rpc.publicnode.com"
	}
	return rpc.Endpoint{Name: "integration", URL: url}
}

func TestIntegrationReadSafeAndHash(t *testing.T) {
	safeHex := os.Getenv("CALLISTO_TEST_SAFE")
	if safeHex == "" {
		t.Skip("set CALLISTO_TEST_SAFE to a real Safe address to run this test")
	}
	safeAddr := common.HexToAddress(safeHex)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := rpc.Dial(ctx, mainnetEndpoint())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	info, err := ReadInfo(ctx, conn.Client, safeAddr)
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	t.Logf("Safe %s: version=%s threshold=%d nonce=%d owners=%d",
		safeAddr.Hex(), info.Version, info.Threshold, info.Nonce, len(info.Owners))

	if info.Version != "" && strings.HasPrefix(info.Version, "1.1") {
		t.Skipf("Safe version %s predates the chainId-bound domain LocalHash assumes", info.Version)
	}

	// A representative transfer SafeTx at the Safe's current nonce.
	to := info.Owners[0]
	tx := NewSafeTx(to, big.NewInt(1), nil, info.Nonce)

	onchain, err := tx.OnChainHash(ctx, conn.Client, safeAddr)
	if err != nil {
		t.Fatalf("OnChainHash: %v", err)
	}
	local := tx.LocalHash(conn.ChainID, safeAddr)
	if onchain != local {
		t.Errorf("safeTxHash mismatch:\n on-chain %s\n local    %s", onchain.Hex(), local.Hex())
	}
}
