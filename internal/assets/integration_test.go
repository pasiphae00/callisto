//go:build integration

// Real-network ERC-20 decode checks against Ethereum mainnet. Excluded from the
// default build; run with:
//
//	go test -tags integration ./internal/assets/
//
// Override the endpoint with CALLISTO_TEST_MAINNET_RPC.
package assets

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

func mainnetClient(t *testing.T) rpc.Client {
	t.Helper()
	url := os.Getenv("CALLISTO_TEST_MAINNET_RPC")
	if url == "" {
		url = "https://ethereum-rpc.publicnode.com"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := rpc.Dial(ctx, rpc.Endpoint{Name: "mainnet", URL: url})
	if err != nil {
		t.Fatalf("dial mainnet: %v", err)
	}
	t.Cleanup(conn.Close)
	return conn.Client
}

func TestIntegrationERC20Metadata(t *testing.T) {
	client := mainnetClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	usdc := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	m, err := Metadata(ctx, client, usdc)
	if err != nil {
		t.Fatalf("Metadata(USDC): %v", err)
	}
	if m.Decimals != 6 || m.Symbol != "USDC" {
		t.Errorf("USDC metadata = %+v, want decimals=6 symbol=USDC", m)
	}
	t.Logf("USDC: name=%q symbol=%q decimals=%d", m.Name, m.Symbol, m.Decimals)
}

func TestIntegrationServiceLoad(t *testing.T) {
	client := mainnetClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A well-known address with a nonzero ETH balance.
	account := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045") // vitalik.eth
	svc := NewService(client, 1)
	got, err := svc.Load(ctx, account, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) == 0 || got[0].Kind != Native {
		t.Fatal("native asset should be first")
	}
	t.Logf("loaded %d assets; native balance = %s %s", len(got), got[0].HumanBalance(), got[0].Symbol)
}
