package walletconnect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// mockRelay is an in-memory WalletConnect relay for tests: it accepts multiple
// client connections and routes each irn_publish to the other connections
// subscribed to that topic (as an irn_subscription push), mirroring the real relay.
type mockRelay struct {
	server *httptest.Server

	mu   sync.Mutex
	subs map[string]map[*mockConn]bool // topic → set of subscribed conns
}

type mockConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *mockConn) writeJSON(v interface{}) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.WriteJSON(v)
}

func newMockRelay(t *testing.T) *mockRelay {
	t.Helper()
	m := &mockRelay{subs: make(map[string]map[*mockConn]bool)}
	up := websocket.Upgrader{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mc := &mockConn{conn: conn}
		defer m.dropConn(mc)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				ID     float64         `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Method {
			case "irn_subscribe":
				var p struct {
					Topic string `json:"topic"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				m.subscribe(p.Topic, mc)
				mc.writeJSON(map[string]interface{}{"id": msg.ID, "jsonrpc": "2.0", "result": "sub"})
			case "irn_publish":
				var p publishParams
				_ = json.Unmarshal(msg.Params, &p)
				mc.writeJSON(map[string]interface{}{"id": msg.ID, "jsonrpc": "2.0", "result": true})
				m.deliver(p, mc)
			}
		}
	}))
	return m
}

func (m *mockRelay) wsURL() string {
	return "ws" + strings.TrimPrefix(m.server.URL, "http")
}

func (m *mockRelay) Close() { m.server.Close() }

func (m *mockRelay) subscribe(topic string, c *mockConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subs[topic] == nil {
		m.subs[topic] = make(map[*mockConn]bool)
	}
	m.subs[topic][c] = true
}

func (m *mockRelay) dropConn(c *mockConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, set := range m.subs {
		delete(set, c)
	}
	_ = c.conn.Close()
}

// deliver pushes a published message to every subscriber of its topic except the
// publisher (the real relay does not echo to the sender).
func (m *mockRelay) deliver(p publishParams, from *mockConn) {
	m.mu.Lock()
	targets := make([]*mockConn, 0)
	for c := range m.subs[p.Topic] {
		if c != from {
			targets = append(targets, c)
		}
	}
	m.mu.Unlock()

	for _, c := range targets {
		c.writeJSON(map[string]interface{}{
			"id": newPayloadID(), "jsonrpc": "2.0", "method": "irn_subscription",
			"params": map[string]interface{}{
				"id":   "sub",
				"data": map[string]interface{}{"topic": p.Topic, "message": p.Message, "tag": p.Tag},
			},
		})
	}
}
