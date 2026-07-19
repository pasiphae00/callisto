// Callisto-local addition; see hub.go's package doc comment.
//
// This file adds a Trezor Bridge (trezord) transport as an alternative to
// direct USB access. On some platforms/devices — confirmed for the Trezor Safe
// family on macOS — the device's wallet-protocol USB interface isn't reachable
// through the OS's HID API at all (only a secondary interface, e.g. FIDO2/U2F,
// enumerates); writes succeed but reads never return data. This mirrors the
// underlying cause of go-ethereum#31841 and matches what upstream's own blocked
// fix (PR #32752) is trying to address by swapping to a lower-level USB library.
// Trezor Bridge already solves this correctly on every OS (it's what Trezor
// Suite and the web wallet use), so instead of adding a libusb/CGo dependency,
// Callisto talks to Bridge's local HTTP API and reuses the existing trezorDriver
// protocol logic (Open/Derive/SignTx) via the trezorTransport abstraction —
// only the wire framing differs (a single HTTP round trip per message instead
// of chunked USB HID reports).
//
// API reference: https://github.com/trezor/trezord-go — default port 21325.
package usbwallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/log"
)

// DefaultBridgeURL is Trezor Bridge's default local endpoint.
const DefaultBridgeURL = "http://localhost:21325"

// bridgePortScanStart/End bound the port range DiscoverBridgeURL probes.
// trezord itself scans and binds the first free port starting at 21325 when the
// default is already taken (e.g. another Bridge instance, or — confirmed live —
// a newer Trezor Suite whose embedded bridge landed on 21328 instead of the
// classic default after a relaunch). A fixed DefaultBridgeURL alone is not
// reliable enough to depend on.
const (
	bridgePortScanStart = 21325
	bridgePortScanEnd   = 21335
)

// DiscoverBridgeURL finds the actual local Trezor Bridge endpoint by probing
// bridgePortScanStart..bridgePortScanEnd (trezord's own scan range) and
// returning the first one that answers with the expected version-info
// response. Returns ok=false if none responded (Bridge isn't running).
func DiscoverBridgeURL() (url string, ok bool) {
	for port := bridgePortScanStart; port <= bridgePortScanEnd; port++ {
		candidate := fmt.Sprintf("http://localhost:%d", port)
		if NewBridgeClient(candidate).Available() {
			return candidate, true
		}
	}
	return "", false
}

// bridgeOrigin satisfies trezord's server-side Origin allowlist (it rejects
// requests with a missing/non-matching Origin header with 403 — this is an
// anti-DNS-rebinding check aimed at malicious web pages, not a restriction on
// legitimate local clients like Callisto talking to the user's own bridge for
// the user's own device). Must match trezord's `^https?://localhost:[58]\d{3}$`.
const bridgeOrigin = "http://localhost:5000"

// ErrBridgeUnavailable means Trezor Bridge isn't reachable at the configured
// URL (not installed, or not running).
var ErrBridgeUnavailable = errors.New("trezor bridge: not reachable (is Trezor Bridge or Trezor Suite running?)")

// BridgeDevice describes one device as reported by Trezor Bridge's /enumerate.
type BridgeDevice struct {
	Path    string  // Opaque device path, passed to Acquire
	Session *string // nil if available, a session ID string if already in use
}

// adminTimeout bounds non-interactive Bridge calls (enumerate/acquire/release):
// these never wait on the user, just local-daemon latency, so a short timeout
// gives a fast, clear "Bridge isn't running" failure instead of a long hang.
const adminTimeout = 10 * time.Second

// callTimeout bounds /call — the device-exchange endpoint, which may require
// the user to physically confirm on the device (e.g. exporting an address, or
// signing). Matches deviceReadTimeoutMs (the equivalent budget on the raw-USB
// path in wallet.go) so both transports give a human the same time to react
// before failing with a clear timeout error instead of hanging indefinitely.
const callTimeout = 60 * time.Second

// BridgeClient is a minimal client for Trezor Bridge's local HTTP API.
type BridgeClient struct {
	baseURL string
	http    *http.Client // shared transport; per-call timeout via context, not client.Timeout

	// callProtocol remembers which /call wire format (see bridgeCallProtocol)
	// this bridge instance actually wants, once detected, so subsequent calls
	// don't pay for a doubled round trip. Zero value (callProtocolUnknown) means
	// "not yet detected" — atomic since Call may run from a background goroutine.
	callProtocol atomic.Int32
}

// NewBridgeClient returns a client for the Trezor Bridge instance at url (use
// DefaultBridgeURL for the standard local install).
func NewBridgeClient(url string) *BridgeClient {
	return &BridgeClient{baseURL: url, http: &http.Client{}}
}

// Available reports whether Trezor Bridge is reachable at all.
func (c *BridgeClient) Available() bool {
	_, err := c.post(adminTimeout, "/", []byte("{}"))
	return err == nil
}

// Enumerate lists devices Trezor Bridge currently sees.
func (c *BridgeClient) Enumerate() ([]BridgeDevice, error) {
	body, err := c.post(adminTimeout, "/enumerate", nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Path    string  `json:"path"`
		Session *string `json:"session"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("trezor bridge: decode enumerate response: %w", err)
	}
	out := make([]BridgeDevice, len(raw))
	for i, d := range raw {
		out[i] = BridgeDevice{Path: d.Path, Session: d.Session}
	}
	return out, nil
}

// Acquire claims a session for the device at path. previous should be the
// session ID this client (or a previous Callisto run against the same device)
// last held, or "" if none is known.
//
// trezord rejects the request with "wrong previous session" if previous
// doesn't match what it currently has on record — which happens whenever a
// prior Acquire's matching Release never ran (e.g. a crashed process, or an
// earlier attempt that errored out before reaching Close/Release). Rather than
// surface that as a hard failure, Acquire re-enumerates to find the device's
// actual current session and retries once with that — self-healing a stale
// session instead of requiring the user to physically replug the device.
func (c *BridgeClient) Acquire(path, previous string) (string, error) {
	session, err := c.acquireOnce(path, previous)
	if err == nil {
		return session, nil
	}
	if !strings.Contains(err.Error(), "wrong previous session") {
		return "", err
	}
	devices, enumErr := c.Enumerate()
	if enumErr != nil {
		return "", err // report the original acquire error; enumeration didn't help
	}
	for _, d := range devices {
		if d.Path == path {
			actual := ""
			if d.Session != nil {
				actual = *d.Session
			}
			return c.acquireOnce(path, actual)
		}
	}
	return "", err
}

func (c *BridgeClient) acquireOnce(path, previous string) (string, error) {
	if previous == "" {
		previous = "null"
	}
	body, err := c.post(adminTimeout, fmt.Sprintf("/acquire/%s/%s", path, previous), nil)
	if err != nil {
		return "", err
	}
	var res struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("trezor bridge: decode acquire response: %w", err)
	}
	return res.Session, nil
}

// Release releases a session, freeing the device for other applications.
func (c *BridgeClient) Release(session string) error {
	_, err := c.post(adminTimeout, "/release/"+session, nil)
	return err
}

// bridgeCallProtocol is the /call wire format, of which trezord implementations
// disagree — both the outer HTTP body shape AND the inner message framing:
//
//   - callProtocolLegacy: classic trezord-go (confirmed: v3.1.0, the standalone
//     Trezor Bridge). Request body is bare hex text of `type(2) + length(4) +
//     protobuf data` (6-byte header; no USB report framing — trezord-go strips
//     that at the daemon level). Response body is a JSON string containing hex
//     in the same shape, e.g. `"deadbeef"`.
//   - callProtocolWrapped: the newer @trezor/transport-embedded bridge shipped
//     inside recent Trezor Suite builds (confirmed: v3.2.1). Request/response
//     bodies are a JSON object `{"protocol":"v1","data":"<hex>"}` (source:
//     trezor-suite packages/transport/src/utils/bridgeProtocolMessage.ts,
//     undocumented in the classic trezord-go API reference) — but the `data`
//     hex itself is the *raw legacy USB HID report framing*, unchunked: a
//     single `0x3f` report-ID byte, then `0x23 0x23` magic, then `type(2) +
//     length(4) + protobuf data` (9-byte header total). Confirmed empirically:
//     sending the 6-byte (no-report-framing) form inside the JSON envelope
//     produced a live device-level `Failure{message:"Firmware error"}` reply —
//     the firmware itself chokes on it — while the device's own responses
//     always carry the full 9-byte form.
//
// Neither bridge advertises which format it wants ahead of time (both report a
// similar version-info payload from `/`), so BridgeClient detects it adaptively:
// try wrapped first (what current Suite builds need), falling back to legacy on
// a request-format rejection, and remembering the choice for the rest of this
// client's lifetime to avoid a doubled round trip on every subsequent call.
type bridgeCallProtocol int

const (
	callProtocolUnknown bridgeCallProtocol = iota
	callProtocolWrapped
	callProtocolLegacy
)

// bridgeProtocolMessage mirrors trezor-suite's BridgeProtocolMessage JSON shape
// for the wrapped /call request and response bodies.
type bridgeProtocolMessage struct {
	Protocol string `json:"protocol"`
	Data     string `json:"data"`
}

// usbReportMagic/usbReportID are the legacy USB HID report framing bytes the
// wrapped protocol's `data` field carries (see bridgeCallProtocol's doc
// comment) — reused from the byte layout usbTrezorTransport already builds per
// 64-byte chunk, just assembled once as a single unchunked blob here.
var usbReportMagic = [2]byte{0x23, 0x23}

const usbReportID = 0x3f

// Call sends one wire-protocol message to the device and returns the parsed
// reply's message type and payload. Bounded by callTimeout (not adminTimeout):
// the device may be waiting on the user to physically confirm.
func (c *BridgeClient) Call(session string, msgType uint16, data []byte) (uint16, []byte, error) {
	proto := bridgeCallProtocol(c.callProtocol.Load())

	// Once a format is confirmed for this client, use it directly — no
	// speculative fallback, so a genuine later error (e.g. a real protocol
	// failure) isn't misdiagnosed as a format mismatch and silently retried
	// under the other format.
	switch proto {
	case callProtocolWrapped:
		return c.callWrapped(session, msgType, data)
	case callProtocolLegacy:
		return c.callLegacy(session, msgType, data)
	}

	// Unknown yet: try wrapped first (what current Trezor Suite builds need —
	// see bridgeCallProtocol's doc comment), falling back to legacy trezord-go
	// framing if that attempt fails outright.
	kind, reply, err := c.callWrapped(session, msgType, data)
	if err == nil {
		c.callProtocol.Store(int32(callProtocolWrapped))
		return kind, reply, nil
	}
	kind, reply, legacyErr := c.callLegacy(session, msgType, data)
	if legacyErr != nil {
		// Neither format worked; report the wrapped-format error since it's
		// tried first and is the more likely format for a modern install.
		return 0, nil, err
	}
	c.callProtocol.Store(int32(callProtocolLegacy))
	return kind, reply, nil
}

// callWrapped sends /call using the newer @trezor/transport JSON envelope,
// with the legacy USB HID report framing embedded in its data field (see
// bridgeCallProtocol's doc comment for why).
func (c *BridgeClient) callWrapped(session string, msgType uint16, data []byte) (uint16, []byte, error) {
	payload := make([]byte, 9+len(data))
	payload[0] = usbReportID
	payload[1], payload[2] = usbReportMagic[0], usbReportMagic[1]
	binary.BigEndian.PutUint16(payload[3:], msgType)
	binary.BigEndian.PutUint32(payload[5:], uint32(len(data)))
	copy(payload[9:], data)

	reqBody, err := json.Marshal(bridgeProtocolMessage{Protocol: "v1", Data: hex.EncodeToString(payload)})
	if err != nil {
		return 0, nil, err
	}
	body, err := c.post(callTimeout, "/call/"+session, reqBody)
	if err != nil {
		return 0, nil, err
	}
	var msg bridgeProtocolMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return 0, nil, fmt.Errorf("trezor bridge: decode wrapped call response: %w", err)
	}
	resp, err := hex.DecodeString(msg.Data)
	if err != nil {
		return 0, nil, fmt.Errorf("trezor bridge: decode wrapped call response hex: %w", err)
	}
	if len(resp) < 9 || resp[0] != usbReportID || resp[1] != usbReportMagic[0] || resp[2] != usbReportMagic[1] {
		return 0, nil, errTrezorReplyInvalidHeader
	}
	kind := binary.BigEndian.Uint16(resp[3:5])
	length := binary.BigEndian.Uint32(resp[5:9])
	reply := resp[9:]
	if uint32(len(reply)) != length {
		return 0, nil, errTrezorReplyInvalidHeader
	}
	return kind, reply, nil
}

// callLegacy sends /call using the classic trezord-go bare-hex framing (a
// 6-byte type+length header, no USB report framing).
func (c *BridgeClient) callLegacy(session string, msgType uint16, data []byte) (uint16, []byte, error) {
	payload := make([]byte, 6+len(data))
	binary.BigEndian.PutUint16(payload[0:], msgType)
	binary.BigEndian.PutUint32(payload[2:], uint32(len(data)))
	copy(payload[6:], data)

	body, err := c.post(callTimeout, "/call/"+session, []byte(hex.EncodeToString(payload)))
	if err != nil {
		return 0, nil, err
	}
	resp, err := hex.DecodeString(string(bytes.TrimSpace(trimJSONQuotes(body))))
	if err != nil {
		return 0, nil, fmt.Errorf("trezor bridge: decode call response: %w", err)
	}
	if len(resp) < 6 {
		return 0, nil, errTrezorReplyInvalidHeader
	}
	kind := binary.BigEndian.Uint16(resp[0:2])
	length := binary.BigEndian.Uint32(resp[2:6])
	reply := resp[6:]
	if uint32(len(reply)) != length {
		return 0, nil, errTrezorReplyInvalidHeader
	}
	return kind, reply, nil
}

// post issues a POST to path with an optional raw body, bounded by timeout, and
// returns the response body. Non-2xx responses (notably 403 from the Origin
// check, and connection failures when Bridge isn't running) are turned into
// clear errors.
func (c *BridgeClient) post(timeout time.Duration, path string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", bridgeOrigin)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBridgeUnavailable, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("trezor bridge: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trezor bridge: %s returned %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// trimJSONQuotes strips a leading/trailing JSON string quote, if present —
// trezord's /call response body is a bare JSON string (e.g. "1234abcd").
func trimJSONQuotes(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return b[1 : len(b)-1]
	}
	return b
}

// bridgeSession pins a Trezor Bridge client + device path + active session ID on
// a *wallet (see the bridge field added to the wallet struct in wallet.go). Kept
// separate from bridgeTrezorTransport (which only needs client+session) because
// wallet.Open needs the device path too, to (re-)acquire a session.
type bridgeSession struct {
	client *BridgeClient
	path   string
	id     string // set once Acquire succeeds
}

// bridgeDeviceStub satisfies usb.Device so a bridge-backed *wallet can reuse
// wallet.go's existing nil-checks/lifecycle (w.device != nil, Close, etc.)
// without modifying them — all actual I/O for a bridge wallet goes through
// bridgeTrezorTransport instead; this stub's Write/Read are never called by
// trezorDriver once opened via OpenBridge. Only Close does real work: releasing
// the Bridge session.
type bridgeDeviceStub struct {
	session *bridgeSession
}

func (b bridgeDeviceStub) Close() error {
	if b.session == nil || b.session.id == "" {
		return nil
	}
	err := b.session.client.Release(b.session.id)
	b.session.id = ""
	return err
}

func (b bridgeDeviceStub) Write(p []byte) (int, error) { return 0, errBridgeStubUnused }
func (b bridgeDeviceStub) Read(p []byte) (int, error)  { return 0, errBridgeStubUnused }

var errBridgeStubUnused = errors.New("usbwallet: bridge wallet does not use raw USB I/O")

// bridgeTrezorTransport implements trezorTransport over a Trezor Bridge session
// (single HTTP round trip per message; no USB HID chunking).
type bridgeTrezorTransport struct {
	client  *BridgeClient
	session string
}

// NewBridgeWallet returns an accounts.Wallet for a device Trezor Bridge reports,
// bypassing the USB Hub/enumeration path entirely (no direct USB access is
// involved). The returned wallet's Open acquires a Bridge session and speaks the
// Trezor wire protocol over HTTP via bridgeTrezorTransport.
func NewBridgeWallet(client *BridgeClient, device BridgeDevice) accounts.Wallet {
	url := accounts.URL{Scheme: TrezorScheme + "-bridge", Path: device.Path}
	logger := log.New("url", url)
	return &wallet{
		hub:    &Hub{}, // zero-value Hub: only its mutex/feed are used, both zero-safe
		driver: newTrezorDriver(logger),
		url:    &url,
		log:    logger,
		bridge: &bridgeSession{client: client, path: device.Path},
	}
}

func (t *bridgeTrezorTransport) exchange(msgType uint16, data []byte) (uint16, []byte, error) {
	return t.client.Call(t.session, msgType, data)
}
