//go:build integration

// These tests hit real public RPC endpoints and are excluded from the default
// build:
//
//	go test -tags integration -v -run TestIntegrationProbe ./internal/sim/
//
// They exist to answer the one question unit tests against a fake node cannot:
// does the capability probe report what these endpoints *actually* serve? A
// false positive is the failure that matters — claiming eth_simulateV1 or the
// debug namespace on an endpoint that will not serve it means the UI offers an
// asset preview that then comes back empty.
//
// Every chain in config.ChainCatalog() is probed, then each capability the probe
// claimed is exercised with a real call and checked for a method-unavailable
// rejection. Endpoints needing bearer auth (Ganymede) are skipped unless a token
// is embedded in the build.
package sim

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/pasiphae00/callisto/internal/buildsecrets"
	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/rpc"
)

// probeTimeout is per endpoint. Public endpoints are rate-limited and
// occasionally slow; this is long enough that a timeout means something real.
const probeTimeout = 45 * time.Second

func TestIntegrationProbeCatalogEndpoints(t *testing.T) {
	// Only cmd/callisto wires this up, so a test binary would otherwise fail
	// bearer-auth endpoints with a 401 and skip the one archive node we most
	// want covered. Empty (no token injected via -ldflags) still skips.
	rpc.ResolveAuthToken = buildsecrets.Token

	for _, chain := range config.ChainCatalog() {
		for _, ep := range chain.Endpoints {
			t.Run(ep.Name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
				defer cancel()

				conn, err := rpc.Dial(ctx, ep)
				if err != nil {
					// A bearer-auth endpoint with no token embedded in this
					// build fails here, as does a rate-limited public one.
					// Neither is a probe defect.
					t.Skipf("dial %s: %v (unreachable, rate-limited, or unauthenticated)", ep.URL, err)
				}
				defer conn.Client.Close()

				caps := NewProber(conn.Client).Caps(ctx)
				t.Logf("chain %d %-28s simulateV1=%-5v debug=%-5v tier=%s",
					chain.ChainID, ep.Name, caps.SimulateV1, caps.DebugTrace, caps.Tier())

				verifyClaimedCaps(ctx, t, conn.Client, caps)
				verifySimulateRuns(ctx, t, conn.Client, caps)
			})
		}
	}
}

// verifyClaimedCaps re-issues each method the probe claimed is available and
// fails if the endpoint rejects it as unavailable — that is the false positive
// the probe must never produce. Any *other* error is fine and expected: these
// are deliberately minimal calls, so a bad-params or gas-cap complaint still
// proves the method is served, which is all the probe asserts.
func verifyClaimedCaps(ctx context.Context, t *testing.T, conn rpc.Client, caps Caps) {
	t.Helper()
	raw := conn.RawClient()
	if raw == nil {
		if caps.Rich() {
			t.Errorf("probe claimed %v with no raw client available", caps)
		}
		return
	}

	if caps.SimulateV1 {
		var out []simBlockResult
		err := raw.CallContext(ctx, &out, "eth_simulateV1", simPayload{
			BlockStateCalls: []simBlockStateCall{{Calls: []simCallPayload{}}},
			TraceTransfers:  true,
		}, "latest")
		if methodUnavailable(err) {
			t.Errorf("FALSE POSITIVE: probe claimed eth_simulateV1, real call rejected it: %v", err)
		} else if err != nil {
			t.Logf("eth_simulateV1 served, returned a non-fatal error: %v", err)
		}
	}

	if caps.DebugTrace {
		var frame callFrame
		err := raw.CallContext(ctx, &frame, "debug_traceCall",
			map[string]string{"to": zeroAddress.Hex()}, "latest",
			traceConfig{Tracer: "callTracer", TracerConfig: &callTracerConfig{WithLog: true}})
		if methodUnavailable(err) {
			t.Errorf("FALSE POSITIVE: probe claimed debug_traceCall, real call rejected it: %v", err)
		} else if err != nil {
			t.Logf("debug_traceCall served, returned a non-fatal error: %v", err)
		}
	}
}

// verifySimulateRuns drives the full strategy fall-through with a harmless
// zero-value self-call. Whatever the endpoint's tier, a simulation must come
// back with a usable verdict rather than an error — a Tier-0 endpoint should
// degrade to the eth_call revert check, not fail.
func verifySimulateRuns(ctx context.Context, t *testing.T, conn rpc.Client, caps Caps) {
	t.Helper()

	res, err := NewSimulator(conn).SimulateEOA(ctx, Request{
		From:  zeroAddress,
		To:    zeroAddress,
		Value: big.NewInt(0),
	})
	if err != nil {
		t.Fatalf("SimulateEOA on a zero-value self-call: %v", err)
	}
	t.Logf("simulate: status=%v tier=%s note=%q", res.Status, res.Tier, res.Note)

	// An endpoint that serves no simulation method at all (Flashbots Protect
	// whitelists only submission methods) is entitled to report Unavailable --
	// but never silently. The contract is that the user always gets either a
	// verdict or a stated reason there isn't one.
	if res.Status == StatusUnavailable && res.Note == "" {
		t.Error("simulation unavailable with no explanation for the user")
	}

	// The tier a simulation actually used may be lower than the best available
	// (a method can be served but reject this particular call), never higher.
	if res.Tier > caps.Tier() {
		t.Errorf("result tier %s exceeds probed capability %s", res.Tier, caps.Tier())
	}

	// Tier-0 must say so rather than silently showing an empty asset preview.
	if !caps.Rich() && res.Note == "" {
		t.Error("call-only endpoint produced no explanatory note")
	}
}
