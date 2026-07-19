package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// pollInterval is how often HTTP endpoints (no subscriptions) are polled for a
// new head. WebSocket endpoints get pushed heads and ignore this.
const pollInterval = 12 * time.Second

// Manager owns the single active connection and a head-watching goroutine that
// fans new block headers out to registered listeners (e.g. to refresh balances
// and track pending-transaction inclusion).
//
// Listeners are invoked from the watcher goroutine; a GUI listener must marshal
// its work onto the UI thread itself (Callisto does this via fyne.Do).
type Manager struct {
	mu   sync.RWMutex
	conn *Connection

	watchCancel context.CancelFunc
	watchDone   chan struct{}

	listenerMu sync.Mutex
	listeners  map[int]func(*types.Header)
	nextID     int

	lostMu sync.Mutex
	onLost func() // invoked when the active connection is lost mid-session
}

// SetOnConnectionLost registers a callback fired when the active connection stops
// responding (sustained head-poll failures), so the app can fail over. The
// callback runs on its own goroutine.
func (m *Manager) SetOnConnectionLost(fn func()) {
	m.lostMu.Lock()
	m.onLost = fn
	m.lostMu.Unlock()
}

func (m *Manager) fireConnectionLost() {
	m.lostMu.Lock()
	fn := m.onLost
	m.lostMu.Unlock()
	if fn != nil {
		go fn()
	}
}

// NewManager returns an unconnected manager.
func NewManager() *Manager {
	return &Manager{listeners: make(map[int]func(*types.Header))}
}

// Connect dials the endpoint, replaces any existing connection, and starts
// watching for new heads. The previous connection (if any) is closed. On dial
// failure the existing connection is left untouched.
func (m *Manager) Connect(ctx context.Context, e Endpoint) (*Connection, error) {
	conn, err := Dial(ctx, e)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	old := m.conn
	oldCancel := m.watchCancel
	oldDone := m.watchDone

	m.conn = conn
	watchCtx, cancel := context.WithCancel(context.Background())
	m.watchCancel = cancel
	done := make(chan struct{})
	m.watchDone = done
	m.mu.Unlock()

	// Tear down the previous watcher/connection outside the lock.
	if oldCancel != nil {
		oldCancel()
	}
	if oldDone != nil {
		<-oldDone
	}
	old.Close()

	go m.watchHeads(watchCtx, conn, done)
	return conn, nil
}

// Disconnect stops the head watcher and closes the active connection.
func (m *Manager) Disconnect() {
	m.mu.Lock()
	conn := m.conn
	cancel := m.watchCancel
	done := m.watchDone
	m.conn = nil
	m.watchCancel = nil
	m.watchDone = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	conn.Close()
}

// Active returns the current connection and whether one exists.
func (m *Manager) Active() (*Connection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.conn, m.conn != nil
}

// Client returns the active low-level client and whether one exists.
func (m *Manager) Client() (Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.conn == nil {
		return nil, false
	}
	return m.conn.Client, true
}

// OnNewHead registers a listener for new block headers and returns a function
// that unregisters it. Listeners fire on the watcher goroutine.
func (m *Manager) OnNewHead(fn func(*types.Header)) (cancel func()) {
	m.listenerMu.Lock()
	id := m.nextID
	m.nextID++
	m.listeners[id] = fn
	m.listenerMu.Unlock()
	return func() {
		m.listenerMu.Lock()
		delete(m.listeners, id)
		m.listenerMu.Unlock()
	}
}

// emit delivers a header to all current listeners.
func (m *Manager) emit(h *types.Header) {
	m.listenerMu.Lock()
	fns := make([]func(*types.Header), 0, len(m.listeners))
	for _, fn := range m.listeners {
		fns = append(fns, fn)
	}
	m.listenerMu.Unlock()
	for _, fn := range fns {
		fn(h)
	}
}

// watchHeads pushes new block headers to listeners for the lifetime of ctx. It
// prefers WebSocket subscriptions and falls back to polling — either because the
// endpoint is HTTP, or because a subscription could not be established.
func (m *Manager) watchHeads(ctx context.Context, conn *Connection, done chan struct{}) {
	defer close(done)

	if conn.Endpoint.SupportsSubscriptions() {
		if m.subscribeHeads(ctx, conn) {
			return // subscription ran until ctx was cancelled
		}
		// Subscription failed to establish; fall through to polling.
	}
	m.pollHeads(ctx, conn)
}

// subscribeHeads runs a live eth_subscribe loop. It returns true if it exited due
// to context cancellation (normal shutdown), or false if it could not establish a
// subscription at all (caller should fall back to polling).
func (m *Manager) subscribeHeads(ctx context.Context, conn *Connection) bool {
	headers := make(chan *types.Header, 16)
	sub, err := conn.Client.SubscribeNewHead(ctx, headers)
	if err != nil {
		return false
	}
	defer sub.Unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return true
		case err := <-sub.Err():
			if err == nil || ctx.Err() != nil {
				return true
			}
			// Lost the subscription unexpectedly; degrade to polling.
			m.pollHeads(ctx, conn)
			return true
		case h := <-headers:
			m.emit(h)
		}
	}
}

// maxPollFailures is how many consecutive head-poll failures mark the connection
// as lost (≈ maxPollFailures × pollInterval of unresponsiveness), triggering the
// connection-lost hook so the app can fail over.
const maxPollFailures = 5

// pollHeads polls the latest header on a fixed interval until ctx is cancelled,
// emitting only when the head advances. After maxPollFailures consecutive failures
// it fires the connection-lost hook and stops (the app reconnects/fails over).
func (m *Manager) pollHeads(ctx context.Context, conn *Connection) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastNum uint64
	poll := func() bool {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		h, err := conn.Client.HeaderByNumber(reqCtx, nil)
		if err != nil || h == nil {
			return false
		}
		if n := h.Number.Uint64(); n > lastNum {
			lastNum = n
			m.emit(h)
		}
		return true
	}
	poll() // emit an initial head promptly
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if poll() {
				failures = 0
				continue
			}
			if failures++; failures >= maxPollFailures {
				m.fireConnectionLost()
				return
			}
		}
	}
}
