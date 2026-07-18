//go:build integration

// Real-network ENS checks against Ethereum mainnet. Excluded from the default
// build; run with:
//
//	go test -tags integration ./internal/ens/
//
// Override the mainnet endpoint with CALLISTO_TEST_MAINNET_RPC.
package ens

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

func TestIntegrationForwardResolve(t *testing.T) {
	r := NewResolver(mainnetClient(t))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// vitalik.eth is a long-stable registration.
	want := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
	got, err := r.Resolve(ctx, "vitalik.eth")
	if err != nil {
		t.Fatalf("Resolve(vitalik.eth): %v", err)
	}
	if got != want {
		t.Errorf("vitalik.eth = %s, want %s", got.Hex(), want.Hex())
	}
	t.Logf("vitalik.eth -> %s", got.Hex())
}

func TestIntegrationForwardResolveMissing(t *testing.T) {
	r := NewResolver(mainnetClient(t))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := r.Resolve(ctx, "this-name-should-not-exist-callisto-xyz.eth")
	if err != ErrNotFound {
		t.Errorf("missing name err = %v, want ErrNotFound", err)
	}
}
