package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// withDial swaps the package dialer to return the given client, restoring it
// after the test.
func withDial(t *testing.T, c Client, dialErr error) {
	t.Helper()
	orig := dialFunc
	dialFunc = func(ctx context.Context, rawURL, authToken string) (Client, error) {
		if dialErr != nil {
			return nil, dialErr
		}
		return c, nil
	}
	t.Cleanup(func() { dialFunc = orig })
}

func TestDialSendsBearerAuthForAuthRef(t *testing.T) {
	origResolve := ResolveAuthToken
	ResolveAuthToken = func(ref string) string {
		if ref == "ganymede" {
			return "secret-tok"
		}
		return ""
	}
	t.Cleanup(func() { ResolveAuthToken = origResolve })

	mc := newMockClient(1)
	var gotToken string
	orig := dialFunc
	dialFunc = func(ctx context.Context, rawURL, authToken string) (Client, error) {
		gotToken = authToken
		return mc, nil
	}
	t.Cleanup(func() { dialFunc = orig })

	// AuthRef set → token forwarded to the dialer.
	if _, err := Dial(context.Background(), Endpoint{Name: "g", URL: "wss://x", AuthRef: "ganymede"}); err != nil {
		t.Fatal(err)
	}
	if gotToken != "secret-tok" {
		t.Errorf("authed dial token = %q, want secret-tok", gotToken)
	}

	// No AuthRef → no token.
	gotToken = "sentinel"
	if _, err := Dial(context.Background(), Endpoint{Name: "f", URL: "https://y"}); err != nil {
		t.Fatal(err)
	}
	if gotToken != "" {
		t.Errorf("unauthed dial token = %q, want empty", gotToken)
	}
}

func TestDialVerifiesChainID(t *testing.T) {
	mc := newMockClient(1) // mainnet
	withDial(t, mc, nil)

	conn, err := Dial(context.Background(), Endpoint{Name: "m", URL: "https://x"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if conn.ChainID.Uint64() != 1 || !conn.Known || conn.ChainInfo.Native.Symbol != "ETH" {
		t.Errorf("unexpected connection: id=%v known=%v info=%+v", conn.ChainID, conn.Known, conn.ChainInfo)
	}
}

func TestDialClosesClientOnChainIDError(t *testing.T) {
	mc := newMockClient(1)
	mc.chainErr = errors.New("node down")
	withDial(t, mc, nil)

	_, err := Dial(context.Background(), Endpoint{Name: "m", URL: "https://x"})
	if err == nil {
		t.Fatal("expected error when ChainID fails")
	}
	if !mc.isClosed() {
		t.Error("client should be closed when verification fails")
	}
}

func TestDialUnknownChainStillConnects(t *testing.T) {
	mc := newMockClient(424242)
	withDial(t, mc, nil)
	conn, err := Dial(context.Background(), Endpoint{Name: "x", URL: "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.Known {
		t.Error("unknown chain should report Known=false")
	}
	if conn.ChainInfo.Native.Decimals != 18 {
		t.Error("unknown chain should still have a usable native asset fallback")
	}
}

// waitForSub spins until the mock has registered a subscription channel.
func waitForSub(t *testing.T, m *mockClient) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		ok := m.subChan != nil
		m.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("subscription was never established")
}

func TestManagerConnectAndSubscribeHeads(t *testing.T) {
	mc := newMockClient(1)
	withDial(t, mc, nil)
	mgr := NewManager()

	got := make(chan uint64, 4)
	unsub := mgr.OnNewHead(func(h *types.Header) { got <- h.Number.Uint64() })
	defer unsub()

	if _, err := mgr.Connect(context.Background(), Endpoint{Name: "ws", URL: "wss://x/ws"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer mgr.Disconnect()

	waitForSub(t, mc)
	mc.pushHead(42)

	select {
	case n := <-got:
		if n != 42 {
			t.Errorf("head number = %d, want 42", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive head from subscription")
	}
}

func TestManagerConnectReplacesAndClosesOld(t *testing.T) {
	old := newMockClient(1)
	withDial(t, old, nil)
	mgr := NewManager()
	if _, err := mgr.Connect(context.Background(), Endpoint{Name: "a", URL: "wss://a/ws"}); err != nil {
		t.Fatal(err)
	}

	// Swap the dialer to a fresh client and reconnect.
	fresh := newMockClient(1)
	dialFunc = func(ctx context.Context, rawURL, authToken string) (Client, error) { return fresh, nil }
	if _, err := mgr.Connect(context.Background(), Endpoint{Name: "b", URL: "wss://b/ws"}); err != nil {
		t.Fatal(err)
	}
	defer mgr.Disconnect()

	if !old.isClosed() {
		t.Error("previous connection should be closed after reconnect")
	}
	conn, ok := mgr.Active()
	if !ok || conn.Endpoint.Name != "b" {
		t.Errorf("active endpoint = %+v, want b", conn)
	}
}

func TestManagerDisconnectClosesConnection(t *testing.T) {
	mc := newMockClient(1)
	withDial(t, mc, nil)
	mgr := NewManager()
	if _, err := mgr.Connect(context.Background(), Endpoint{Name: "a", URL: "wss://a/ws"}); err != nil {
		t.Fatal(err)
	}
	mgr.Disconnect()
	if !mc.isClosed() {
		t.Error("Disconnect should close the connection")
	}
	if _, ok := mgr.Active(); ok {
		t.Error("no connection should be active after Disconnect")
	}
}

func TestManagerPollFallbackForHTTP(t *testing.T) {
	mc := newMockClient(1)
	mc.headNum = 5 // initial poll emits since 5 > 0
	withDial(t, mc, nil)
	mgr := NewManager()

	got := make(chan uint64, 4)
	mgr.OnNewHead(func(h *types.Header) { got <- h.Number.Uint64() })

	if _, err := mgr.Connect(context.Background(), Endpoint{Name: "http", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	defer mgr.Disconnect()

	select {
	case n := <-got:
		if n != 5 {
			t.Errorf("polled head = %d, want 5", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP endpoint should have polled an initial head")
	}
}
