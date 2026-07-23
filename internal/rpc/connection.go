package rpc

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/pasiphae00/callisto/internal/chain"
)

// ResolveAuthToken maps an Endpoint.AuthRef to its bearer token, or "" if none.
// It is set at startup (by main, from internal/buildsecrets) so this package stays
// free of embedded secrets; nil/"" means the endpoint dials without auth.
var ResolveAuthToken func(ref string) string

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

// dialFunc is the dialer used to establish connections; overridable in tests. When
// authToken is non-empty an Authorization: Bearer header is sent — on the request
// for HTTP, or on the upgrade handshake for WS.
var dialFunc = func(ctx context.Context, rawURL, authToken string) (Client, error) {
	if authToken != "" {
		rc, err := gethrpc.DialOptions(ctx, rawURL, gethrpc.WithHeader("Authorization", "Bearer "+authToken))
		if err != nil {
			return nil, err
		}
		return ethclient.NewClient(rc), nil
	}
	// ethclient.DialContext handles http(s) and ws(s) transparently and returns
	// a *Client that satisfies our Client interface.
	c, err := ethclient.DialContext(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// authTokenFor resolves an endpoint's bearer token (empty if none / unconfigured).
func authTokenFor(e Endpoint) string {
	if e.AuthRef == "" || ResolveAuthToken == nil {
		return ""
	}
	return ResolveAuthToken(e.AuthRef)
}

// Dial connects to an endpoint and verifies it by fetching the chain ID. A
// successful Dial means the node is reachable and responsive; the caller owns the
// returned Connection and must Close it.
func Dial(ctx context.Context, e Endpoint) (*Connection, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	client, err := dialFunc(ctx, e.URL, authTokenFor(e))
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
