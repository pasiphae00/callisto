package usbwallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockBridge is a minimal trezord stand-in for testing BridgeClient without
// real hardware. It enforces the Origin check the same way trezord does, so
// tests also verify Callisto sends a header trezord would actually accept.
type mockBridge struct {
	server        *httptest.Server
	acquireCalls  []string // path/previous pairs seen, for asserting retry behavior
	failFirstCall bool     // simulate "wrong previous session" once
	calledCall    bool
}

func newMockBridge(t *testing.T) *mockBridge {
	t.Helper()
	m := &mockBridge{}
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"3.1.0","protocolMessages":true}`))
	})
	mux.HandleFunc("/enumerate", func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"path": "1", "session": nil},
		})
	})
	mux.HandleFunc("/acquire/1/", func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		m.acquireCalls = append(m.acquireCalls, r.URL.Path)
		if m.failFirstCall && len(m.acquireCalls) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"wrong previous session"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"session": "sess-1"})
	})
	mux.HandleFunc("/release/sess-1", func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/call/sess-1", func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		m.calledCall = true
		// Echo back a well-formed reply: type=0x0002, len=0x00000000, no data
		// (6 bytes total: 2 type + 4 length, nothing after).
		_, _ = w.Write([]byte(`"000200000000"`))
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func validOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == bridgeOrigin
}

func TestBridgeClientEnumerate(t *testing.T) {
	m := newMockBridge(t)
	c := NewBridgeClient(m.server.URL)
	devices, err := c.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Path != "1" || devices[0].Session != nil {
		t.Errorf("devices = %+v", devices)
	}
}

func TestBridgeClientAcquireRelease(t *testing.T) {
	m := newMockBridge(t)
	c := NewBridgeClient(m.server.URL)
	session, err := c.Acquire("1", "")
	if err != nil {
		t.Fatal(err)
	}
	if session != "sess-1" {
		t.Errorf("session = %q, want sess-1", session)
	}
	if err := c.Release(session); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeClientAcquireSelfHealsWrongPreviousSession(t *testing.T) {
	m := newMockBridge(t)
	m.failFirstCall = true
	c := NewBridgeClient(m.server.URL)

	session, err := c.Acquire("1", "some-stale-session")
	if err != nil {
		t.Fatalf("Acquire should self-heal via re-enumerate, got: %v", err)
	}
	if session != "sess-1" {
		t.Errorf("session = %q, want sess-1", session)
	}
	if len(m.acquireCalls) != 2 {
		t.Errorf("expected 2 acquire attempts (fail then retry), got %d", len(m.acquireCalls))
	}
}

func TestBridgeClientCallRoundTrip(t *testing.T) {
	m := newMockBridge(t)
	c := NewBridgeClient(m.server.URL)
	resp, err := c.Call("sess-1", []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("000200000000")
	if hex.EncodeToString(resp) != hex.EncodeToString(want) {
		t.Errorf("resp = %x, want %x", resp, want)
	}
	if !m.calledCall {
		t.Error("mock /call was never hit")
	}
}

// TestMockEnforcesOrigin confirms the mock (and thus our assumption about
// trezord's real behavior) actually rejects a request with no Origin header —
// otherwise every other test in this file would be silently passing against a
// mock that's more lenient than the real bridge.
func TestMockEnforcesOrigin(t *testing.T) {
	m := newMockBridge(t)
	resp, err := http.Post(m.server.URL+"/enumerate", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("no-Origin request status = %d, want 403", resp.StatusCode)
	}
}

// TestBridgeClientSetsOrigin confirms the real client always sends the header
// the mock (and real trezord) requires — this is what every other test in this
// file implicitly relies on succeeding.
func TestBridgeClientSetsOrigin(t *testing.T) {
	m := newMockBridge(t)
	if _, err := NewBridgeClient(m.server.URL).Enumerate(); err != nil {
		t.Fatalf("client should send a valid Origin header and succeed: %v", err)
	}
}

func TestDiscoverBridgeURL(t *testing.T) {
	m := newMockBridge(t)
	// Point discovery at a single-port "range" matching the mock by
	// temporarily using NewBridgeClient directly against the known URL, since
	// DiscoverBridgeURL scans fixed ports we can't control in a unit test —
	// verify the underlying Available() check it relies on instead.
	if !NewBridgeClient(m.server.URL).Available() {
		t.Error("Available() should be true for a reachable mock bridge")
	}
	if NewBridgeClient("http://localhost:1").Available() {
		t.Error("Available() should be false for an unreachable endpoint")
	}
}

func TestBridgeTrezorTransportFraming(t *testing.T) {
	m := newMockBridge(t)
	transport := &bridgeTrezorTransport{client: NewBridgeClient(m.server.URL), session: "sess-1"}
	kind, reply, err := transport.exchange(0x0001, []byte{0xde, 0xad})
	if err != nil {
		t.Fatal(err)
	}
	if kind != 0x0002 {
		t.Errorf("kind = %d, want 2 (from mock reply)", kind)
	}
	if len(reply) != 0 {
		t.Errorf("reply = %x, want empty", reply)
	}
}

func TestBridgeTrezorTransportShortReplyIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/call/sess-1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"0001"`)) // too short to contain a valid header
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	transport := &bridgeTrezorTransport{client: NewBridgeClient(server.URL), session: "sess-1"}
	if _, _, err := transport.exchange(0x0001, nil); err != errTrezorReplyInvalidHeader {
		t.Errorf("err = %v, want errTrezorReplyInvalidHeader", err)
	}
}

func TestBridgeDeviceStubOnlyCloseIsFunctional(t *testing.T) {
	m := newMockBridge(t)
	sess := &bridgeSession{client: NewBridgeClient(m.server.URL), path: "1", id: "sess-1"}
	stub := bridgeDeviceStub{session: sess}

	if _, err := stub.Write(nil); err != errBridgeStubUnused {
		t.Errorf("Write err = %v", err)
	}
	if _, err := stub.Read(nil); err != errBridgeStubUnused {
		t.Errorf("Read err = %v", err)
	}
	if err := stub.Close(); err != nil {
		t.Errorf("Close (release) failed: %v", err)
	}
	if sess.id != "" {
		t.Error("Close should clear the session id after releasing")
	}
}

func TestAcquireGivesUpIfEnumerateAlsoFails(t *testing.T) {
	// A client pointed at nothing: Acquire's self-heal re-enumerate will also
	// fail, and the ORIGINAL acquire error should be returned, not a confusing
	// enumerate error.
	c := NewBridgeClient("http://localhost:1")
	_, err := c.Acquire("1", "stale")
	if err == nil {
		t.Fatal("expected an error against an unreachable bridge")
	}
}

func init() {
	// Sanity: bridgeOrigin must satisfy trezord's own regex
	// (^https?://localhost:[58]\d{3}$) or every live test in this file would be
	// silently testing against a bridge that would reject the real client too.
	if want := "http://localhost:5000"; bridgeOrigin != want {
		panic(fmt.Sprintf("bridgeOrigin changed to %q; update trezord's allowed pattern check", bridgeOrigin))
	}
}
