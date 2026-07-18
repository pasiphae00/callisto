package walletconnect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

type dappMsg struct {
	topic, message string
	tag            int
}

// TestSessionRoundTrip drives the full Sign flow against an in-memory relay: the
// engine (wallet) pairs, receives a proposal, approves it, and the simulated dApp
// derives the matching session key and sends a request the engine surfaces and
// answers.
func TestSessionRoundTrip(t *testing.T) {
	relay := newMockRelay(t)
	defer relay.Close()
	wsURL := relay.wsURL()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// --- Wallet (the engine under test) ---
	client, err := NewClient(wsURL, "proj", Metadata{Name: "Callisto"})
	if err != nil {
		t.Fatal(err)
	}
	proposalCh := make(chan Proposal, 1)
	requestCh := make(chan Request, 1)
	client.OnProposal(func(p Proposal) { proposalCh <- p })
	client.OnRequest(func(r Request) { requestCh <- r })
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("wallet connect: %v", err)
	}
	defer client.Close()

	// --- dApp side: its own key pair + a self-chosen pairing key/URI ---
	dappKeys, _ := GenerateKeyPair()
	pairingSym := make([]byte, 32)
	rand.Read(pairingSym)
	pairingTopic := TopicOf(pairingSym)
	uri := fmt.Sprintf("wc:%s@2?relay-protocol=irn&symKey=%s", pairingTopic, hex.EncodeToString(pairingSym))

	dapp, _ := NewRelay(wsURL, "proj")
	dappMsgs := make(chan dappMsg, 8)
	dapp.OnMessage(func(topic, message string, tag int) { dappMsgs <- dappMsg{topic, message, tag} })
	if err := dapp.Dial(ctx); err != nil {
		t.Fatalf("dapp dial: %v", err)
	}
	defer dapp.Close()
	// dApp listens on the pairing topic for the wallet's proposal response.
	if err := dapp.Subscribe(ctx, pairingTopic); err != nil {
		t.Fatal(err)
	}

	// Wallet pairs (subscribes to the pairing topic).
	if err := client.Pair(ctx, uri); err != nil {
		t.Fatalf("pair: %v", err)
	}

	// dApp publishes wc_sessionPropose on the pairing topic.
	propose := sessionProposeParams{
		Relays: []relayInfo{{Protocol: "irn"}},
		Proposer: proposer{
			PublicKey: dappKeys.PublicHex(),
			Metadata:  Metadata{Name: "TestDapp", URL: "https://test.example"},
		},
		RequiredNamespaces: map[string]Namespace{"eip155": {
			Chains:  []string{"eip155:1"},
			Methods: []string{"eth_sendTransaction", "personal_sign"},
			Events:  []string{"chainChanged"},
		}},
	}
	dappPublish(t, ctx, dapp, pairingSym, pairingTopic, wcSessionPropose, propose, tagSessionProposeReq, ttlProposal)

	// Wallet surfaces the proposal.
	var prop Proposal
	select {
	case prop = <-proposalCh:
	case <-time.After(3 * time.Second):
		t.Fatal("wallet never received the proposal")
	}
	if prop.Proposer.Name != "TestDapp" {
		t.Errorf("proposer = %q", prop.Proposer.Name)
	}

	// Wallet approves, exposing an account on mainnet.
	const account = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	sess, err := client.Approve(ctx, prop.ID, account,
		[]string{"eip155:1"}, []string{"eth_sendTransaction", "personal_sign"}, []string{"chainChanged"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	// dApp receives the proposal response on the pairing topic and derives the
	// session key — it must match the wallet's session topic.
	respPlain := dappWaitOn(t, dappMsgs, pairingSym, pairingTopic)
	var rr rpcResponse
	if err := json.Unmarshal(respPlain, &rr); err != nil {
		t.Fatalf("decode propose response: %v", err)
	}
	var spr sessionProposeResult
	if err := json.Unmarshal(rr.Result, &spr); err != nil {
		t.Fatalf("decode responderPublicKey: %v", err)
	}
	sessionSym, err := dappKeys.DeriveSymKey(spr.ResponderPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := TopicOf(sessionSym); got != sess.Topic {
		t.Fatalf("dApp session topic %s != wallet session topic %s", got, sess.Topic)
	}

	// dApp subscribes to the session topic and sends a personal_sign request.
	if err := dapp.Subscribe(ctx, sess.Topic); err != nil {
		t.Fatal(err)
	}
	reqParams := sessionRequestParams{
		Request: requestPayload{Method: "personal_sign", Params: json.RawMessage(`["0x48656c6c6f","` + account + `"]`)},
		ChainID: "eip155:1",
	}
	dappPublish(t, ctx, dapp, sessionSym, sess.Topic, wcSessionRequest, reqParams, tagSessionRequestReq, ttlRequest)

	// Wallet surfaces the request.
	var req Request
	select {
	case req = <-requestCh:
	case <-time.After(3 * time.Second):
		t.Fatal("wallet never received the request")
	}
	if req.Method != "personal_sign" || req.ChainID != "eip155:1" {
		t.Errorf("request = %+v", req)
	}

	// Wallet answers; dApp receives the result on the session topic.
	if err := client.RespondResult(ctx, req, "0xSIGNATURE"); err != nil {
		t.Fatalf("respond: %v", err)
	}
	ansPlain := dappWaitOn(t, dappMsgs, sessionSym, sess.Topic)
	var ans rpcResponse
	if err := json.Unmarshal(ansPlain, &ans); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	var sig string
	if err := json.Unmarshal(ans.Result, &sig); err != nil || sig != "0xSIGNATURE" {
		t.Errorf("answer result = %q (err %v)", sig, err)
	}

	if len(client.Sessions()) != 1 {
		t.Errorf("expected 1 active session, got %d", len(client.Sessions()))
	}
}

// dappPublish seals a wc_* request under sym and publishes it on topic.
func dappPublish(t *testing.T, ctx context.Context, r *Relay, sym []byte, topic, method string, params interface{}, tag, ttl int) {
	t.Helper()
	req, err := newRequest(method, params)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(req)
	env, err := Seal(sym, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Publish(ctx, topic, env, tag, ttl); err != nil {
		t.Fatal(err)
	}
}

// dappWaitOn waits for the next dApp message on the given topic and decrypts it.
func dappWaitOn(t *testing.T, ch <-chan dappMsg, sym []byte, topic string) []byte {
	t.Helper()
	for {
		select {
		case m := <-ch:
			if m.topic != topic {
				continue
			}
			pt, err := Open(sym, m.message)
			if err != nil {
				t.Fatalf("dApp decrypt on %s: %v", topic, err)
			}
			return pt
		case <-time.After(3 * time.Second):
			t.Fatalf("dApp timed out waiting on topic %s", topic)
			return nil
		}
	}
}
