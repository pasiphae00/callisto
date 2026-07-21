package walletconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultRelayURL is WalletConnect's public relay. The projectId is appended as a
// query parameter and the Ed25519 auth JWT is sent in the Authorization header.
const DefaultRelayURL = "wss://relay.walletconnect.org"

// relay JSON-RPC methods (client → server).
const (
	methodPublish   = "irn_publish"
	methodSubscribe = "irn_subscribe"
	// methodSubscription is the server → client push carrying an inbound message.
	methodSubscription = "irn_subscription"
)

// maxReconnectBackoff caps the exponential backoff between relay reconnect attempts.
const maxReconnectBackoff = 30 * time.Second

// publishParams / subscribeParams / subscriptionData are the irn_* param shapes.
type publishParams struct {
	Topic   string `json:"topic"`
	Message string `json:"message"`
	TTL     int    `json:"ttl"`
	Tag     int    `json:"tag"`
}

type subscribeParams struct {
	Topic string `json:"topic"`
}

type subscriptionData struct {
	Topic   string `json:"topic"`
	Message string `json:"message"`
	Tag     int    `json:"tag"`
}

type subscriptionParams struct {
	ID   string           `json:"id"`
	Data subscriptionData `json:"data"`
}

// Relay is a WalletConnect relay WebSocket client. It handles the JSON-RPC framing
// (request/response correlation and acking inbound irn_subscription pushes) and
// hands decrypted-topic-scoped messages to onMessage. It knows nothing about the
// Sign protocol — the engine layers that on top.
//
// The relay reconnects automatically: WalletConnect's relay routinely closes idle
// sockets for load balancing (close code 4010), so a dropped socket is treated as
// transient — it re-dials with backoff and re-subscribes to the tracked topics rather
// than tearing down the client.
type Relay struct {
	url       string
	projectID string
	auth      *authKey

	dialer *websocket.Dialer

	writeMu sync.Mutex
	conn    *websocket.Conn // current socket; swapped on reconnect (guarded by writeMu)

	mu      sync.Mutex
	pending map[uint64]chan rpcResponse
	topics  map[string]struct{} // active subscriptions, restored on reconnect

	onMessage      func(topic, message string, tag int)
	onError        func(error)
	onReconnecting func()
	onReconnected  func()

	closeOnce sync.Once
	closed    chan struct{} // closed only by Close() — the "shut down for good" signal
}

// NewRelay builds a relay client for the given relay URL and projectId. Pass
// DefaultRelayURL for the public relay.
func NewRelay(relayURL, projectID string) (*Relay, error) {
	auth, err := newAuthKey()
	if err != nil {
		return nil, err
	}
	return &Relay{
		url:       relayURL,
		projectID: projectID,
		auth:      auth,
		dialer:    websocket.DefaultDialer,
		pending:   make(map[uint64]chan rpcResponse),
		topics:    make(map[string]struct{}),
		closed:    make(chan struct{}),
	}, nil
}

// OnMessage sets the handler for inbound relay messages (topic, encrypted message,
// tag). Set before Dial.
func (r *Relay) OnMessage(fn func(topic, message string, tag int)) { r.onMessage = fn }

// OnError sets the handler invoked on a fatal relay error. With auto-reconnect this is
// rare — transient socket drops (including the relay's 4010 load-balancing close) are
// recovered silently and surfaced via OnReconnecting/OnReconnected instead. Set before Dial.
func (r *Relay) OnError(fn func(error)) { r.onError = fn }

// OnReconnecting / OnReconnected report transient relay reconnects (for UI status).
func (r *Relay) OnReconnecting(fn func()) { r.onReconnecting = fn }
func (r *Relay) OnReconnected(fn func())  { r.onReconnected = fn }

// Dial connects to the relay, authenticating with a fresh JWT, and starts the read
// loop.
func (r *Relay) Dial(ctx context.Context) error {
	conn, err := r.dial(ctx)
	if err != nil {
		return err
	}
	go r.readLoop(conn)
	return nil
}

// dial establishes a new authenticated relay socket and installs it as the current
// connection. It does not start a read loop (Dial / reconnect do).
func (r *Relay) dial(ctx context.Context) (*websocket.Conn, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: bad relay url: %w", err)
	}
	q := u.Query()
	if r.projectID != "" {
		q.Set("projectId", r.projectID)
	}
	u.RawQuery = q.Encode()

	jwt, err := r.auth.SignRelayJWT(r.url, time.Now())
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+jwt)

	conn, _, err := r.dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: dial relay: %w", err)
	}
	r.writeMu.Lock()
	r.conn = conn
	r.writeMu.Unlock()
	return conn, nil
}

// Subscribe subscribes to a topic, waits for the relay's acknowledgement, and records
// the topic so it is restored if the socket reconnects.
func (r *Relay) Subscribe(ctx context.Context, topic string) error {
	if _, err := r.call(ctx, methodSubscribe, subscribeParams{Topic: topic}); err != nil {
		return err
	}
	r.mu.Lock()
	r.topics[topic] = struct{}{}
	r.mu.Unlock()
	return nil
}

// Publish sends an encrypted message on a topic with the given WalletConnect tag
// and ttl (seconds), waiting for the relay's acknowledgement.
func (r *Relay) Publish(ctx context.Context, topic, message string, tag, ttl int) error {
	_, err := r.call(ctx, methodPublish, publishParams{Topic: topic, Message: message, TTL: ttl, Tag: tag})
	return err
}

// call sends a JSON-RPC request and waits for its matching response.
func (r *Relay) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	req, err := newRequest(method, params)
	if err != nil {
		return nil, err
	}
	ch := make(chan rpcResponse, 1)
	r.mu.Lock()
	r.pending[req.ID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, req.ID)
		r.mu.Unlock()
	}()

	if err := r.write(req); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.closed:
		return nil, fmt.Errorf("walletconnect: relay closed")
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("walletconnect: relay %s error: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// write serializes and sends a value under the write mutex, on the current connection.
func (r *Relay) write(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("walletconnect: relay not connected")
	}
	return r.conn.WriteMessage(websocket.TextMessage, data)
}

// readLoop reads frames off conn, correlating responses to pending calls and
// dispatching inbound irn_subscription pushes (which it also acks). On a read error it
// triggers a reconnect (unless the relay was closed for good).
func (r *Relay) readLoop(conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !r.isClosed() {
				go r.reconnect() // transient drop (e.g. relay 4010) — recover
			}
			return
		}
		// A message with a method is a request (irn_subscription); otherwise it is
		// a response to one of our calls.
		var probe struct {
			ID     uint64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			continue // ignore malformed frames
		}
		if probe.Method == methodSubscription {
			r.handleSubscription(probe.ID, probe.Params)
			continue
		}
		// Response: deliver to the waiting caller.
		r.mu.Lock()
		ch := r.pending[probe.ID]
		r.mu.Unlock()
		if ch != nil {
			ch <- rpcResponse{ID: probe.ID, Result: probe.Result, Error: probe.Error}
		}
	}
}

// reconnect re-dials the relay with exponential backoff and re-subscribes to every
// tracked topic, then resumes the read loop. It runs until it succeeds or the relay is
// closed. Exactly one reconnect runs at a time: it is only started by a read loop that
// has already returned, and it starts the next read loop only on success.
func (r *Relay) reconnect() {
	notify(r.onReconnecting)
	backoff := time.Second
	for {
		if r.isClosed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		conn, err := r.dial(ctx)
		cancel()
		if err == nil {
			go r.readLoop(conn)
			r.resubscribeAll()
			notify(r.onReconnected)
			return
		}
		select {
		case <-r.closed:
			return
		case <-time.After(backoff):
		}
		if backoff < maxReconnectBackoff {
			if backoff *= 2; backoff > maxReconnectBackoff {
				backoff = maxReconnectBackoff
			}
		}
	}
}

// resubscribeAll re-subscribes to every tracked topic after a reconnect. Best-effort:
// the read loop is already running to ack these, and any transient failure will surface
// as another read error and trigger a further reconnect.
func (r *Relay) resubscribeAll() {
	r.mu.Lock()
	topics := make([]string, 0, len(r.topics))
	for t := range r.topics {
		topics = append(topics, t)
	}
	r.mu.Unlock()
	for _, t := range topics {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = r.call(ctx, methodSubscribe, subscribeParams{Topic: t})
		cancel()
	}
}

// handleSubscription acks an inbound push and forwards it to onMessage.
func (r *Relay) handleSubscription(reqID uint64, params json.RawMessage) {
	// Ack the relay so it doesn't redeliver.
	_ = r.write(rpcResponse{ID: reqID, JSONRPC: "2.0", Result: json.RawMessage("true")})

	var p subscriptionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if r.onMessage != nil {
		r.onMessage(p.Data.Topic, p.Data.Message, p.Data.Tag)
	}
}

// isClosed reports whether Close() has been called (a permanent shutdown, distinct from
// a transient socket drop).
func (r *Relay) isClosed() bool {
	select {
	case <-r.closed:
		return true
	default:
		return false
	}
}

// Close shuts down the relay connection for good (stops reconnecting).
func (r *Relay) Close() {
	r.closeOnce.Do(func() {
		close(r.closed)
		r.writeMu.Lock()
		if r.conn != nil {
			_ = r.conn.Close()
		}
		r.writeMu.Unlock()
	})
}

func notify(fn func()) {
	if fn != nil {
		fn()
	}
}
