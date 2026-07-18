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
type Relay struct {
	url       string
	projectID string
	auth      *authKey

	dialer *websocket.Dialer
	conn   *websocket.Conn

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[uint64]chan rpcResponse

	onMessage func(topic, message string, tag int)
	onError   func(error)

	closeOnce sync.Once
	closed    chan struct{}
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
		closed:    make(chan struct{}),
	}, nil
}

// OnMessage sets the handler for inbound relay messages (topic, encrypted message,
// tag). Set before Dial.
func (r *Relay) OnMessage(fn func(topic, message string, tag int)) { r.onMessage = fn }

// OnError sets the handler invoked when the read loop terminates with an error
// (e.g. the socket dropped). Set before Dial.
func (r *Relay) OnError(fn func(error)) { r.onError = fn }

// Dial connects to the relay, authenticating with a fresh JWT, and starts the read
// loop.
func (r *Relay) Dial(ctx context.Context) error {
	u, err := url.Parse(r.url)
	if err != nil {
		return fmt.Errorf("walletconnect: bad relay url: %w", err)
	}
	q := u.Query()
	if r.projectID != "" {
		q.Set("projectId", r.projectID)
	}
	u.RawQuery = q.Encode()

	jwt, err := r.auth.SignRelayJWT(r.url, time.Now())
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+jwt)

	conn, _, err := r.dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return fmt.Errorf("walletconnect: dial relay: %w", err)
	}
	r.conn = conn
	go r.readLoop()
	return nil
}

// Subscribe subscribes to a topic and waits for the relay's acknowledgement.
func (r *Relay) Subscribe(ctx context.Context, topic string) error {
	_, err := r.call(ctx, methodSubscribe, subscribeParams{Topic: topic})
	return err
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

// write serializes and sends a value under the write mutex.
func (r *Relay) write(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.conn.WriteMessage(websocket.TextMessage, data)
}

// readLoop reads frames, correlating responses to pending calls and dispatching
// inbound irn_subscription pushes (which it also acks).
func (r *Relay) readLoop() {
	for {
		_, data, err := r.conn.ReadMessage()
		if err != nil {
			r.fail(err)
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

// fail closes the relay once and notifies onError.
func (r *Relay) fail(err error) {
	r.closeOnce.Do(func() {
		close(r.closed)
		if r.conn != nil {
			_ = r.conn.Close()
		}
		if r.onError != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			r.onError(err)
		}
	})
}

// Close shuts down the relay connection.
func (r *Relay) Close() {
	r.closeOnce.Do(func() {
		close(r.closed)
		if r.conn != nil {
			_ = r.conn.Close()
		}
	})
}
