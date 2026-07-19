package walletconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// sessionTTL is how long a settled session is advertised as valid (7 days, the
// WalletConnect default).
const sessionTTL = 7 * 24 * 60 * 60

// Proposal is a pending session proposal surfaced to the UI for approval.
type Proposal struct {
	ID                 uint64
	PairingTopic       string
	Proposer           Metadata
	ProposerPubKey     string
	RequiredNamespaces map[string]Namespace
	OptionalNamespaces map[string]Namespace
}

// Session is an established WalletConnect session.
type Session struct {
	Topic   string
	Peer    Metadata
	Account string   // the exposed EOA (0x…)
	Chains  []string // CAIP-2 chains, e.g. "eip155:1"
}

// Request is an inbound signing/transaction request bound to a session.
type Request struct {
	ID           uint64
	SessionTopic string
	Method       string
	Params       json.RawMessage
	ChainID      string // CAIP-2, e.g. "eip155:1"
}

// Client is the wallet-side WalletConnect Sign engine: it owns the relay, the
// pairing/session key material, and dispatches proposals/requests to UI callbacks.
type Client struct {
	relay    *Relay
	metadata Metadata

	mu        sync.Mutex
	keys      map[string][]byte    // topic → symKey (pairing and session topics)
	proposals map[uint64]*Proposal // proposal id → context (awaiting approve/reject)
	sessions  map[string]*Session  // session topic → session

	onProposal func(Proposal)
	onRequest  func(Request)
	onDelete   func(topic string)
}

// NewClient builds a Sign client for the given relay URL, projectId, and the
// wallet metadata shown to dApps.
func NewClient(relayURL, projectID string, metadata Metadata) (*Client, error) {
	relay, err := NewRelay(relayURL, projectID)
	if err != nil {
		return nil, err
	}
	c := &Client{
		relay:     relay,
		metadata:  metadata,
		keys:      make(map[string][]byte),
		proposals: make(map[uint64]*Proposal),
		sessions:  make(map[string]*Session),
	}
	relay.OnMessage(c.handleMessage)
	return c, nil
}

// OnProposal/OnRequest/OnSessionDelete register UI callbacks. They fire on the
// relay read-loop goroutine, so UI code must marshal onto its own thread.
func (c *Client) OnProposal(fn func(Proposal))          { c.onProposal = fn }
func (c *Client) OnRequest(fn func(Request))            { c.onRequest = fn }
func (c *Client) OnSessionDelete(fn func(topic string)) { c.onDelete = fn }

// OnError registers a handler for a fatal relay error (socket dropped).
func (c *Client) OnError(fn func(error)) { c.relay.OnError(fn) }

// Connect dials the relay.
func (c *Client) Connect(ctx context.Context) error { return c.relay.Dial(ctx) }

// Close tears down the relay connection.
func (c *Client) Close() { c.relay.Close() }

// Sessions returns a snapshot of the active sessions.
func (c *Client) Sessions() []Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Session, 0, len(c.sessions))
	for _, s := range c.sessions {
		out = append(out, *s)
	}
	return out
}

// Session returns the session for a topic, if present.
func (c *Client) Session(topic string) (Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sessions[topic]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// Pair consumes a wc: URI: it stores the pairing key and subscribes to the pairing
// topic, on which the dApp's wc_sessionPropose will arrive (→ OnProposal).
func (c *Client) Pair(ctx context.Context, rawURI string) error {
	uri, err := ParseURI(rawURI)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.keys[uri.Topic] = uri.SymKey
	c.mu.Unlock()
	return c.relay.Subscribe(ctx, uri.Topic)
}

// Approve accepts a proposal, exposing account on the given eip155 chains. It
// responds to the proposal, derives the session key, subscribes to the session
// topic, and settles the session.
func (c *Client) Approve(ctx context.Context, proposalID uint64, account string, chains, methods, events []string) (Session, error) {
	c.mu.Lock()
	prop := c.proposals[proposalID]
	pairingSym := c.keys[proposalOf(prop)]
	c.mu.Unlock()
	if prop == nil || pairingSym == nil {
		return Session{}, fmt.Errorf("walletconnect: unknown proposal %d", proposalID)
	}

	self, err := GenerateKeyPair()
	if err != nil {
		return Session{}, err
	}
	// 1) Respond to the proposal on the pairing topic with our public key.
	res := sessionProposeResult{Relay: relayInfo{Protocol: "irn"}, ResponderPublicKey: self.PublicHex()}
	if err := c.publishResponse(ctx, prop.PairingTopic, pairingSym, proposalID, res, tagSessionProposeRes, ttlResponse); err != nil {
		return Session{}, err
	}

	// 2) Derive the session key/topic and subscribe.
	sessionSym, err := self.DeriveSymKey(prop.ProposerPubKey)
	if err != nil {
		return Session{}, err
	}
	sessionTopic := TopicOf(sessionSym)
	c.mu.Lock()
	c.keys[sessionTopic] = sessionSym
	c.mu.Unlock()
	if err := c.relay.Subscribe(ctx, sessionTopic); err != nil {
		return Session{}, err
	}

	// 3) Settle the session on the session topic.
	accounts := make([]string, 0, len(chains))
	for _, ch := range chains {
		accounts = append(accounts, ch+":"+account)
	}
	namespaces := map[string]Namespace{
		"eip155": {Chains: chains, Methods: methods, Events: events, Accounts: accounts},
	}
	settle := sessionSettleParams{
		Relay:      relayInfo{Protocol: "irn"},
		Controller: controller{PublicKey: self.PublicHex(), Metadata: c.metadata},
		Namespaces: namespaces,
		Expiry:     time.Now().Unix() + sessionTTL,
	}
	if _, err := c.publishRequest(ctx, sessionTopic, sessionSym, wcSessionSettle, settle, tagSessionSettleReq, ttlSettle); err != nil {
		return Session{}, err
	}

	sess := &Session{Topic: sessionTopic, Peer: prop.Proposer, Account: account, Chains: chains}
	c.mu.Lock()
	c.sessions[sessionTopic] = sess
	delete(c.proposals, proposalID)
	c.mu.Unlock()
	return *sess, nil
}

// Reject declines a proposal.
func (c *Client) Reject(ctx context.Context, proposalID uint64) error {
	c.mu.Lock()
	prop := c.proposals[proposalID]
	pairingSym := c.keys[proposalOf(prop)]
	delete(c.proposals, proposalID)
	c.mu.Unlock()
	if prop == nil {
		return nil
	}
	return c.publishError(ctx, prop.PairingTopic, pairingSym, proposalID, 5000, "User rejected", tagSessionProposeReject, ttlResponse)
}

// RespondResult sends a successful response to a session request.
func (c *Client) RespondResult(ctx context.Context, req Request, result interface{}) error {
	sym := c.symFor(req.SessionTopic)
	if sym == nil {
		return fmt.Errorf("walletconnect: no session for topic")
	}
	return c.publishResponse(ctx, req.SessionTopic, sym, req.ID, result, tagSessionRequestRes, ttlResponse)
}

// RespondError sends an error response to a session request.
func (c *Client) RespondError(ctx context.Context, req Request, code int, message string) error {
	sym := c.symFor(req.SessionTopic)
	if sym == nil {
		return fmt.Errorf("walletconnect: no session for topic")
	}
	return c.publishError(ctx, req.SessionTopic, sym, req.ID, code, message, tagSessionRequestRes, ttlResponse)
}

// DisconnectAll notifies every connected dApp with wc_sessionDelete and clears the
// sessions. Best-effort (errors are ignored) — used at shutdown, before Close, so
// dApps see a clean disconnect instead of a dropped socket.
func (c *Client) DisconnectAll(ctx context.Context) {
	c.mu.Lock()
	topics := make([]string, 0, len(c.sessions))
	for t := range c.sessions {
		topics = append(topics, t)
	}
	c.mu.Unlock()
	for _, t := range topics {
		_ = c.Disconnect(ctx, t)
	}
}

// Disconnect deletes a session (notifying the dApp).
func (c *Client) Disconnect(ctx context.Context, sessionTopic string) error {
	sym := c.symFor(sessionTopic)
	c.mu.Lock()
	delete(c.sessions, sessionTopic)
	delete(c.keys, sessionTopic)
	c.mu.Unlock()
	if sym == nil {
		return nil
	}
	_, err := c.publishRequest(ctx, sessionTopic, sym, wcSessionDelete,
		sessionDeleteParams{Code: 6000, Message: "User disconnected"}, tagSessionDeleteReq, ttlDelete)
	return err
}

// --- inbound routing --------------------------------------------------------

func (c *Client) handleMessage(topic, message string, tag int) {
	sym := c.symFor(topic)
	if sym == nil {
		return // unknown topic
	}
	plaintext, err := Open(sym, message)
	if err != nil {
		return
	}
	var msg rpcRequest
	if err := json.Unmarshal(plaintext, &msg); err != nil || msg.Method == "" {
		return // a response (ack) or malformed — nothing to route
	}
	switch msg.Method {
	case wcSessionPropose:
		c.handlePropose(topic, msg)
	case wcSessionRequest:
		c.handleRequest(topic, msg)
	case wcSessionDelete:
		c.handleDelete(topic, msg)
	case wcSessionPing:
		_ = c.publishResponse(context.Background(), topic, sym, msg.ID, true, tagSessionPingRes, ttlPing)
	}
}

func (c *Client) handlePropose(topic string, msg rpcRequest) {
	var p sessionProposeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	prop := &Proposal{
		ID:                 msg.ID,
		PairingTopic:       topic,
		Proposer:           p.Proposer.Metadata,
		ProposerPubKey:     p.Proposer.PublicKey,
		RequiredNamespaces: p.RequiredNamespaces,
		OptionalNamespaces: p.OptionalNamespaces,
	}
	c.mu.Lock()
	c.proposals[msg.ID] = prop
	c.mu.Unlock()
	if c.onProposal != nil {
		c.onProposal(*prop)
	}
}

func (c *Client) handleRequest(topic string, msg rpcRequest) {
	var p sessionRequestParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	req := Request{
		ID:           msg.ID,
		SessionTopic: topic,
		Method:       p.Request.Method,
		Params:       p.Request.Params,
		ChainID:      p.ChainID,
	}
	if c.onRequest != nil {
		c.onRequest(req)
	}
}

func (c *Client) handleDelete(topic string, msg rpcRequest) {
	c.mu.Lock()
	delete(c.sessions, topic)
	c.mu.Unlock()
	// Best-effort ack.
	if sym := c.symFor(topic); sym != nil {
		_ = c.publishResponse(context.Background(), topic, sym, msg.ID, true, tagSessionDeleteRes, ttlDelete)
	}
	c.mu.Lock()
	delete(c.keys, topic)
	c.mu.Unlock()
	if c.onDelete != nil {
		c.onDelete(topic)
	}
}

// --- publish helpers --------------------------------------------------------

func (c *Client) publishRequest(ctx context.Context, topic string, sym []byte, method string, params interface{}, tag, ttl int) (uint64, error) {
	req, err := newRequest(method, params)
	if err != nil {
		return 0, err
	}
	if err := c.sealAndPublish(ctx, topic, sym, req, tag, ttl); err != nil {
		return 0, err
	}
	return req.ID, nil
}

func (c *Client) publishResponse(ctx context.Context, topic string, sym []byte, id uint64, result interface{}, tag, ttl int) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.sealAndPublish(ctx, topic, sym, rpcResponse{ID: id, JSONRPC: "2.0", Result: raw}, tag, ttl)
}

func (c *Client) publishError(ctx context.Context, topic string, sym []byte, id uint64, code int, message string, tag, ttl int) error {
	return c.sealAndPublish(ctx, topic, sym, rpcResponse{ID: id, JSONRPC: "2.0", Error: &rpcError{Code: code, Message: message}}, tag, ttl)
}

func (c *Client) sealAndPublish(ctx context.Context, topic string, sym []byte, payload interface{}, tag, ttl int) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env, err := Seal(sym, data)
	if err != nil {
		return err
	}
	return c.relay.Publish(ctx, topic, env, tag, ttl)
}

func (c *Client) symFor(topic string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.keys[topic]
}

// proposalOf returns the pairing topic of a proposal (nil-safe key lookup helper).
func proposalOf(p *Proposal) string {
	if p == nil {
		return ""
	}
	return p.PairingTopic
}
