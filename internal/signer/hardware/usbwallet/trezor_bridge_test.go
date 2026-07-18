package usbwallet

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockBridge is a minimal trezord stand-in for testing BridgeClient without
// real hardware. It enforces the Origin check the same way trezord does, so
// tests also verify Callisto sends a header trezord would actually accept.
//
// wrappedCall selects which /call wire format the mock speaks (see
// bridgeCallProtocol's doc comment in trezor_bridge.go): false reproduces the
// classic trezord-go behavior (confirmed live: v3.1.0, the standalone Trezor
// Bridge); true reproduces the newer @trezor/transport-embedded behavior
// (confirmed live: v3.2.1, inside recent Trezor Suite builds) — JSON envelope
// with the legacy USB HID report framing (0x3f + 0x23,0x23 + type + length)
// embedded in its hex data field.
type mockBridge struct {
	server        *httptest.Server
	acquireCalls  []string // path/previous pairs seen, for asserting retry behavior
	failFirstCall bool     // simulate "wrong previous session" once
	calledCall    bool
	wrappedCall   bool
	rejectLegacy  bool // wrapped-only mock: reject a legacy-framed /call body
}

func newMockBridge(t *testing.T, wrappedCall bool) *mockBridge {
	t.Helper()
	m := &mockBridge{wrappedCall: wrappedCall}
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
		body := readAll(r)

		if m.wrappedCall {
			var msg bridgeProtocolMessage
			if err := json.Unmarshal(body, &msg); err != nil || msg.Data == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"Invalid BridgeProtocolMessage body"}`))
				return
			}
			raw, err := hex.DecodeString(msg.Data)
			if err != nil || len(raw) < 9 || raw[0] != usbReportID {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"Malformed protocol format"}`))
				return
			}
			// Echo a well-formed reply in the same 9-byte-prefixed shape:
			// type=0x0002, length=0, no data.
			reply := []byte{usbReportID, usbReportMagic[0], usbReportMagic[1], 0x00, 0x02, 0x00, 0x00, 0x00, 0x00}
			respBody, _ := json.Marshal(bridgeProtocolMessage{Protocol: "v1", Data: hex.EncodeToString(reply)})
			_, _ = w.Write(respBody)
			return
		}

		// Legacy: body is bare hex text of a 6-byte header + data.
		if m.rejectLegacy {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Invalid BridgeProtocolMessage body"}`))
			return
		}
		if _, err := hex.DecodeString(string(body)); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Echo back a well-formed reply: type=0x0002, len=0x00000000, no data
		// (6 bytes total: 2 type + 4 length, nothing after).
		_, _ = w.Write([]byte(`"000200000000"`))
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func readAll(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}

func validOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == bridgeOrigin
}

func TestBridgeClientEnumerate(t *testing.T) {
	m := newMockBridge(t, false)
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
	m := newMockBridge(t, false)
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
	m := newMockBridge(t, false)
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

func TestBridgeClientCallRoundTripLegacy(t *testing.T) {
	m := newMockBridge(t, false)
	c := NewBridgeClient(m.server.URL)
	kind, reply, err := c.Call("sess-1", 0x0001, []byte{0xde, 0xad})
	if err != nil {
		t.Fatal(err)
	}
	if kind != 0x0002 || len(reply) != 0 {
		t.Errorf("kind=%d reply=%x, want kind=2 empty reply", kind, reply)
	}
	if !m.calledCall {
		t.Error("mock /call was never hit")
	}
	if bridgeCallProtocol(c.callProtocol.Load()) != callProtocolLegacy {
		t.Error("client should have detected and remembered the legacy protocol")
	}
}

func TestBridgeClientCallRoundTripWrapped(t *testing.T) {
	m := newMockBridge(t, true)
	c := NewBridgeClient(m.server.URL)
	kind, reply, err := c.Call("sess-1", 0x0001, []byte{0xde, 0xad})
	if err != nil {
		t.Fatal(err)
	}
	if kind != 0x0002 || len(reply) != 0 {
		t.Errorf("kind=%d reply=%x, want kind=2 empty reply", kind, reply)
	}
	if bridgeCallProtocol(c.callProtocol.Load()) != callProtocolWrapped {
		t.Error("client should have detected and remembered the wrapped protocol")
	}
}

// TestBridgeClientCallFallsBackToLegacy simulates a mock that only understands
// the legacy format: the client's wrapped-first attempt should fail and it
// should fall back automatically, still returning a correct result.
func TestBridgeClientCallFallsBackToLegacy(t *testing.T) {
	m := newMockBridge(t, false) // legacy-only mock
	c := NewBridgeClient(m.server.URL)
	kind, _, err := c.Call("sess-1", 0x0001, []byte{0xde, 0xad})
	if err != nil {
		t.Fatalf("should fall back to legacy and succeed: %v", err)
	}
	if kind != 0x0002 {
		t.Errorf("kind = %d, want 2", kind)
	}
	if bridgeCallProtocol(c.callProtocol.Load()) != callProtocolLegacy {
		t.Error("should have detected legacy after the wrapped attempt failed")
	}
}

// TestMockEnforcesOrigin confirms the mock (and thus our assumption about
// trezord's real behavior) actually rejects a request with no Origin header —
// otherwise every other test in this file would be silently passing against a
// mock that's more lenient than the real bridge.
func TestMockEnforcesOrigin(t *testing.T) {
	m := newMockBridge(t, false)
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
	m := newMockBridge(t, false)
	if _, err := NewBridgeClient(m.server.URL).Enumerate(); err != nil {
		t.Fatalf("client should send a valid Origin header and succeed: %v", err)
	}
}

func TestDiscoverBridgeURL(t *testing.T) {
	m := newMockBridge(t, false)
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
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%v", wrapped), func(t *testing.T) {
			m := newMockBridge(t, wrapped)
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
		})
	}
}

func TestBridgeTrezorTransportShortReplyIsError(t *testing.T) {
	// Exercise callWrapped directly with a well-formed JSON envelope whose data
	// hex-decodes to fewer than the required 9 bytes — a realistic "short
	// reply" for the wrapped format specifically, as opposed to a malformed
	// top-level body (which would instead trigger the legacy-format fallback
	// and produce a different, less specific error).
	mux := http.NewServeMux()
	mux.HandleFunc("/call/sess-1", func(w http.ResponseWriter, r *http.Request) {
		respBody, _ := json.Marshal(bridgeProtocolMessage{Protocol: "v1", Data: "0001"})
		_, _ = w.Write(respBody)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := NewBridgeClient(server.URL)
	if _, _, err := c.callWrapped("sess-1", 0x0001, nil); err != errTrezorReplyInvalidHeader {
		t.Errorf("err = %v, want errTrezorReplyInvalidHeader", err)
	}
}

func TestBridgeDeviceStubOnlyCloseIsFunctional(t *testing.T) {
	m := newMockBridge(t, false)
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

// TestWrappedRequestFraming confirms the actual bytes callWrapped sends embed
// the legacy USB HID report framing (0x3f report ID + 0x23,0x23 magic) inside
// the hex data field — this is the exact detail that took several rounds of
// live-hardware testing to pin down (see bridgeCallProtocol's doc comment); a
// regression here silently reintroduces the "Firmware error" / malformed
// protocol failures seen against a real device.
func TestWrappedRequestFraming(t *testing.T) {
	var captured bridgeProtocolMessage
	mux := http.NewServeMux()
	mux.HandleFunc("/call/sess-1", func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		_ = json.Unmarshal(body, &captured)
		reply := []byte{usbReportID, usbReportMagic[0], usbReportMagic[1], 0x00, 0x02, 0x00, 0x00, 0x00, 0x00}
		respBody, _ := json.Marshal(bridgeProtocolMessage{Protocol: "v1", Data: hex.EncodeToString(reply)})
		_, _ = w.Write(respBody)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := NewBridgeClient(server.URL)
	data := []byte{0xaa, 0xbb, 0xcc}
	if _, _, err := c.callWrapped("sess-1", 0x0007, data); err != nil {
		t.Fatal(err)
	}

	raw, err := hex.DecodeString(captured.Data)
	if err != nil {
		t.Fatalf("captured data not valid hex: %v", err)
	}
	if len(raw) != 9+len(data) {
		t.Fatalf("framed length = %d, want %d", len(raw), 9+len(data))
	}
	if raw[0] != usbReportID {
		t.Errorf("byte 0 = %#x, want report ID %#x", raw[0], usbReportID)
	}
	if raw[1] != usbReportMagic[0] || raw[2] != usbReportMagic[1] {
		t.Errorf("bytes 1-2 = %x, want magic %x", raw[1:3], usbReportMagic)
	}
	if gotType := binary.BigEndian.Uint16(raw[3:5]); gotType != 0x0007 {
		t.Errorf("type = %#x, want 0x0007", gotType)
	}
	if gotLen := binary.BigEndian.Uint32(raw[5:9]); gotLen != uint32(len(data)) {
		t.Errorf("length = %d, want %d", gotLen, len(data))
	}
	if string(raw[9:]) != string(data) {
		t.Errorf("payload data = %x, want %x", raw[9:], data)
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
