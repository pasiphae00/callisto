//go:build integration

// These tests hit a real Ethereum node and are excluded from the default build.
// Run them explicitly:
//
//	go test -tags integration ./internal/rpc/
//
// Override the endpoint with CALLISTO_TEST_RPC (http(s) or ws(s)); it defaults to
// a public Sepolia node.
package rpc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

func testEndpoint() Endpoint {
	url := os.Getenv("CALLISTO_TEST_RPC")
	if url == "" {
		url = "https://ethereum-sepolia-rpc.publicnode.com"
	}
	return Endpoint{Name: "integration", URL: url}
}

func TestIntegrationDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := Dial(ctx, testEndpoint())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if conn.ChainID == nil || conn.ChainID.Sign() == 0 {
		t.Fatalf("chain ID not returned: %v", conn.ChainID)
	}
	t.Logf("connected: chain=%d known=%v native=%s", conn.ChainID, conn.Known, conn.ChainInfo.Native.Symbol)

	h, err := conn.Client.HeaderByNumber(ctx, nil)
	if err != nil {
		t.Fatalf("HeaderByNumber: %v", err)
	}
	if h.Number == nil || h.Number.Sign() == 0 {
		t.Fatalf("bad head: %+v", h)
	}
	t.Logf("latest head #%d", h.Number)
}

func TestIntegrationManagerWatchesHeads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	mgr := NewManager()
	heads := make(chan *types.Header, 4)
	mgr.OnNewHead(func(h *types.Header) { heads <- h })

	if _, err := mgr.Connect(ctx, testEndpoint()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer mgr.Disconnect()

	select {
	case h := <-heads:
		t.Logf("watcher delivered head #%d", h.Number)
	case <-ctx.Done():
		t.Fatal("timed out waiting for a head from the watcher")
	}
}
