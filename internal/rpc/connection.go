package rpc

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/ethclient"

	"codeberg.org/pasiphae/callisto/internal/chain"
)

// Connection is a live, dialed link to one endpoint. It bundles the low-level
// Client with the verified chain ID and the resolved chain metadata so the UI
// can label the native asset and build explorer links without re-querying.
type Connection struct {
	Endpoint  Endpoint
	Client    Client
	ChainID   *big.Int
	ChainInfo chain.Info
	// Known reports whether ChainID matched a chain Callisto has metadata for.
	Known bool
}

// dialFunc is the dialer used to establish connections; overridable in tests.
var dialFunc = func(ctx context.Context, rawURL string) (Client, error) {
	// ethclient.DialContext handles http(s) and ws(s) transparently and returns
	// a *Client that satisfies our Client interface.
	c, err := ethclient.DialContext(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Dial connects to an endpoint and verifies it by fetching the chain ID. A
// successful Dial means the node is reachable and responsive; the caller owns the
// returned Connection and must Close it.
func Dial(ctx context.Context, e Endpoint) (*Connection, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	client, err := dialFunc(ctx, e.URL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", e.Name, err)
	}
	id, err := client.ChainID(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("verify %s (eth_chainId): %w", e.Name, err)
	}
	info, known := chain.Lookup(id.Uint64())
	// Record the observed chain ID back onto the endpoint copy.
	e.ChainID = id.Uint64()
	return &Connection{
		Endpoint:  e,
		Client:    client,
		ChainID:   id,
		ChainInfo: info,
		Known:     known,
	}, nil
}

// Close releases the underlying client. Safe to call on a nil Connection.
func (c *Connection) Close() {
	if c == nil || c.Client == nil {
		return
	}
	c.Client.Close()
}
