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

// Call sends one wire-protocol message (type(2) + length(4) + protobuf data,
// matching the framing trezorExchange already builds) to the device and returns
// the response in the same shape. Bounded by callTimeout (not adminTimeout): the
// device may be waiting on the user to physically confirm.
func (c *BridgeClient) Call(session string, payload []byte) ([]byte, error) {
	body, err := c.post(callTimeout, "/call/"+session, []byte(hex.EncodeToString(payload)))
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(string(bytes.TrimSpace(trimJSONQuotes(body))))
	if err != nil {
		return nil, fmt.Errorf("trezor bridge: decode call response: %w", err)
	}
	return decoded, nil
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

// bridgeDeviceStub satisfies hid.Device so a bridge-backed *wallet can reuse
// wallet.go's existing nil-checks/lifecycle (w.device != nil, Close, etc.)
// without modifying them — all actual I/O for a bridge wallet goes through
// bridgeTrezorTransport instead; this stub's Write/Read/* are never called by
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
func (b bridgeDeviceStub) ReadTimeout(p []byte, timeout int) (int, error) {
	return 0, errBridgeStubUnused
}
func (b bridgeDeviceStub) GetFeatureReport(p []byte) (int, error)  { return 0, errBridgeStubUnused }
func (b bridgeDeviceStub) SendFeatureReport(p []byte) (int, error) { return 0, errBridgeStubUnused }

var errBridgeStubUnused = errors.New("usbwallet: bridge wallet does not use raw HID I/O")

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
	payload := make([]byte, 6+len(data))
	binary.BigEndian.PutUint16(payload[0:], msgType)
	binary.BigEndian.PutUint32(payload[2:], uint32(len(data)))
	copy(payload[6:], data)

	resp, err := t.client.Call(t.session, payload)
	if err != nil {
		return 0, nil, err
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
