package sim

import (
	"context"
	"errors"
	"strings"
	"sync"

	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/pasiphae00/callisto/internal/rpc"
)

// Caps records which simulation methods an endpoint actually serves. Both can
// be true at once -- an archive node running a recent geth (Ganymede) offers
// the debug namespace *and* eth_simulateV1.
type Caps struct {
	SimulateV1 bool
	DebugTrace bool
}

// Rich reports whether the endpoint can produce an asset-change preview at all.
// Without one of these two methods we can only ask "would it revert?".
func (c Caps) Rich() bool { return c.SimulateV1 || c.DebugTrace }

// Tier reports the best fidelity the endpoint offers, for the UI's
// "connect an X endpoint for full asset changes" message.
//
// Note this is the best *available* method, which is not always the one a
// simulation uses: we prefer eth_simulateV1 even where debug is also present
// (standardized, cheaper, and its traceTransfers gives native-ETH moves
// directly). Result.Tier records the method a given simulation actually used.
func (c Caps) Tier() Tier {
	switch {
	case c.DebugTrace:
		return TierDebug
	case c.SimulateV1:
		return TierSimulate
	default:
		return TierCallOnly
	}
}

// Prober determines once per connection which simulation methods an endpoint
// serves, and caches the answer. Probing costs two cheap RPC round-trips, so it
// is done lazily on the first simulation rather than at connect time.
//
// A Prober is bound to one rpc.Client; build a new one when the connection is
// replaced (endpoint switch, chain switch, failover).
type Prober struct {
	client rpc.Client

	mu     sync.Mutex
	caps   Caps
	probed bool
}

// NewProber returns a Prober for client. A nil client (or one whose RawClient
// is nil, as test doubles are) probes as TierCallOnly.
func NewProber(client rpc.Client) *Prober { return &Prober{client: client} }

// Caps probes the endpoint on first call and returns the cached result after.
func (p *Prober) Caps(ctx context.Context) Caps {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.probed {
		return p.caps
	}
	p.caps = p.probe(ctx)
	p.probed = true
	return p.caps
}

// Tier is shorthand for Caps(ctx).Tier().
func (p *Prober) Tier(ctx context.Context) Tier { return p.Caps(ctx).Tier() }

func (p *Prober) probe(ctx context.Context) Caps {
	if p.client == nil {
		return Caps{}
	}
	raw := p.client.RawClient()
	if raw == nil {
		return Caps{}
	}

	// Both probes ask for the cheapest possible piece of work. We care only
	// about *whether the method is served*: any error other than
	// method-not-found (bad params, a revert, a gas cap) still proves the
	// endpoint implements it, and the real call below will surface any genuine
	// failure. Errors that aren't method-not-found but are transport failures
	// therefore probe as "supported" -- harmless, because simulate() falls
	// through to the next strategy when its chosen method is rejected.
	var out []simBlockResult
	errSim := raw.CallContext(ctx, &out, "eth_simulateV1", simPayload{
		BlockStateCalls: []simBlockStateCall{},
	}, "latest")

	var frame callFrame
	errDbg := raw.CallContext(ctx, &frame, "debug_traceCall",
		map[string]string{"to": zeroAddress.Hex()}, "latest",
		traceConfig{Tracer: "callTracer"})

	return Caps{
		SimulateV1: !methodUnavailable(errSim),
		DebugTrace: !methodUnavailable(errDbg),
	}
}

// methodUnavailable reports whether err means "this endpoint will not serve
// that method" -- either it doesn't implement it (JSON-RPC -32601) or it
// refuses us access to it (public providers commonly gate the debug namespace
// behind a paid plan and answer with an authorization error rather than
// -32601). Both are "don't use this method" as far as we're concerned.
func methodUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr gethrpc.Error
	if errors.As(err, &rpcErr) && rpcErr.ErrorCode() == -32601 {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"method not found",
		"method does not exist",
		"does not exist/is not available",
		"not available",
		"not supported",
		"unsupported method",
		"method handler crashed",
		"unauthorized",
		"forbidden",
		"insufficient permissions",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
