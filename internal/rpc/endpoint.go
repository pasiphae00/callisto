// Package rpc manages Ethereum JSON-RPC endpoints: the user-curated, persisted
// list of backends and the live connection manager built on go-ethereum's
// ethclient. A default endpoint (Flashbots Protect mainnet) is seeded on first
// run by the config package (see config.defaultConfig); users can replace or
// remove it.
//
// This file defines only the persisted Endpoint descriptor so it can be
// referenced by the config schema; the connection logic lives alongside it.
package rpc

import (
	"fmt"
	"net/url"
	"strings"
)

// Scheme classifies an endpoint transport. Callisto supports both so the chain
// can be monitored live (WebSocket subscriptions) and the full JSON-RPC surface
// is reachable (HTTP).
type Scheme string

const (
	SchemeHTTP Scheme = "http" // http:// or https://
	SchemeWS   Scheme = "ws"   // ws:// or wss://
)

// Endpoint is a persisted, user-configured JSON-RPC backend. ChainID is a cache
// of the last observed chain ID (0 = unknown until first connect); it is a hint
// for display only and is always re-verified on connect.
type Endpoint struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	ChainID uint64 `json:"chain_id,omitempty"`
	// AutoConnect marks this endpoint to be connected automatically at startup.
	// At most one endpoint is the auto-connect default (see Config.SetAutoConnect).
	AutoConnect bool `json:"auto_connect,omitempty"`
}

// SchemeOf reports whether a URL is an HTTP(S) or WS(S) endpoint, validating that
// it is one of the supported transports.
func SchemeOf(rawURL string) (Scheme, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return SchemeHTTP, nil
	case "ws", "wss":
		return SchemeWS, nil
	default:
		return "", fmt.Errorf("unsupported scheme %q (want http(s):// or ws(s)://)", u.Scheme)
	}
}

// Validate checks that an endpoint is well-formed enough to persist.
func (e Endpoint) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("endpoint name is required")
	}
	if strings.TrimSpace(e.URL) == "" {
		return fmt.Errorf("endpoint URL is required")
	}
	if _, err := SchemeOf(e.URL); err != nil {
		return err
	}
	return nil
}

// Scheme returns the endpoint's transport, ignoring any parse error (callers
// that care about validity should use Validate/SchemeOf).
func (e Endpoint) Scheme() Scheme {
	s, _ := SchemeOf(e.URL)
	return s
}

// SupportsSubscriptions reports whether live subscriptions (eth_subscribe) are
// available on this endpoint — only WebSocket transports support them.
func (e Endpoint) SupportsSubscriptions() bool {
	return e.Scheme() == SchemeWS
}
