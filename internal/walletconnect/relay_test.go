package walletconnect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type helloInfo struct {
	auth    string
	project string
}

// TestRelayEndToEnd runs the relay client against a mock WebSocket relay: it checks
// the Bearer JWT + projectId reach the server, subscribe/publish are acked, an
// inbound irn_subscription push is dispatched to OnMessage, and the client acks it.
func TestRelayEndToEnd(t *testing.T) {
	helloCh := make(chan helloInfo, 1)
	acked := make(chan uint64, 1)
	const pushID = uint64(999999)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		helloCh <- helloInfo{auth: req.Header.Get("Authorization"), project: req.URL.Query().Get("projectId")}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			id, _ := m["id"].(float64)
			switch m["method"] {
			case "irn_subscribe":
				_ = conn.WriteJSON(map[string]interface{}{"id": id, "jsonrpc": "2.0", "result": "sub-1"})
				// Push an inbound message on the subscribed topic.
				_ = conn.WriteJSON(map[string]interface{}{
					"id": pushID, "jsonrpc": "2.0", "method": "irn_subscription",
					"params": map[string]interface{}{
						"id": "sub-1",
						"data": map[string]interface{}{
							"topic": "topic-1", "message": "cipher-blob", "tag": 1108,
						},
					},
				})
			case "irn_publish":
				_ = conn.WriteJSON(map[string]interface{}{"id": id, "jsonrpc": "2.0", "result": true})
			case nil:
				// A response (no method) — this is the client's ack of our push.
				acked <- uint64(id)
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	r, err := NewRelay(wsURL, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	msgs := make(chan subscriptionData, 1)
	r.OnMessage(func(topic, message string, tag int) {
		msgs <- subscriptionData{Topic: topic, Message: message, Tag: tag}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Dial(ctx); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer r.Close()

	// The server saw our auth header + projectId.
	select {
	case h := <-helloCh:
		if !strings.HasPrefix(h.auth, "Bearer ") {
			t.Errorf("Authorization = %q, want Bearer …", h.auth)
		}
		if h.project != "test-project" {
			t.Errorf("projectId = %q", h.project)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the connection")
	}

	if err := r.Subscribe(ctx, "topic-1"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// The pushed message was dispatched.
	select {
	case m := <-msgs:
		if m.Topic != "topic-1" || m.Message != "cipher-blob" || m.Tag != 1108 {
			t.Errorf("dispatched = %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inbound message not dispatched")
	}
	// And we acked it.
	select {
	case gotID := <-acked:
		if gotID != pushID {
			t.Errorf("acked id = %d, want %d", gotID, pushID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not ack the subscription push")
	}

	if err := r.Publish(ctx, "topic-1", "outgoing-cipher", 1108, 300); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}
