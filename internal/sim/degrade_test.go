package sim

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// These cover what the live-endpoint sweep in integration_test.go found: an
// endpoint can serve a method and still reject our particular request, and an
// endpoint can refuse eth_call itself. Neither may cost the user the revert
// check or produce a bare error dialog.

// simV1Params is the decoded first parameter of an eth_simulateV1 request, used
// to tell the capability probe's empty payload from a real simulation and to
// assert what we put on the wire.
type simV1Params struct {
	BlockStateCalls []struct {
		Calls []map[string]json.RawMessage `json:"calls"`
	} `json:"blockStateCalls"`
}

func decodeSimParams(t *testing.T, params []json.RawMessage) simV1Params {
	t.Helper()
	var p simV1Params
	if err := json.Unmarshal(params[0], &p); err != nil {
		t.Fatalf("decode eth_simulateV1 params: %v", err)
	}
	return p
}

// isProbe reports whether this eth_simulateV1 request is the capability probe
// rather than a real simulation (the probe sends no calls).
func isProbe(p simV1Params) bool {
	return len(p.BlockStateCalls) == 0 || len(p.BlockStateCalls[0].Calls) == 0
}

// TestSimulateFallsBackWhenRichMethodRejectsRequest reproduces Optimism: the
// endpoint serves eth_simulateV1, so the probe reports it, but the real request
// is rejected. The user must still get the revert verdict, with the reason the
// preview is missing.
func TestSimulateFallsBackWhenRichMethodRejectsRequest(t *testing.T) {
	node := newFakeNode(t)
	node.on("eth_simulateV1", func(params []json.RawMessage) (interface{}, error) {
		if isProbe(decodeSimParams(t, params)) {
			return []interface{}{}, nil
		}
		return nil, &rpcError{code: -38015, msg: "Block gas limit exceeded by the block's transactions"}
	})
	node.ok("eth_call", "0x")

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if res.Status != StatusOK {
		t.Errorf("status = %v, want StatusOK from the eth_call fallback", res.Status)
	}
	if res.Tier != TierCallOnly {
		t.Errorf("tier = %s, want the downgraded eth_call tier", res.Tier)
	}
	if !strings.Contains(res.Note, "Block gas limit exceeded") {
		t.Errorf("note does not name why the preview failed: %q", res.Note)
	}
}

// TestSimulateUnavailableWhenEndpointRefusesEthCall reproduces Flashbots
// Protect, which whitelists only submission methods. There is no verdict to be
// had, but it must be reported as a stated limitation rather than an error.
func TestSimulateUnavailableWhenEndpointRefusesEthCall(t *testing.T) {
	node := newFakeNode(t) // no handlers: every method answers -32601

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{})
	if err != nil {
		t.Fatalf("SimulateEOA returned an error rather than a stated limitation: %v", err)
	}
	if res.Status != StatusUnavailable {
		t.Errorf("status = %v, want StatusUnavailable", res.Status)
	}
	if res.Note != noteEndpointCannotSimulate {
		t.Errorf("note = %q, want the endpoint-cannot-simulate explanation", res.Note)
	}
}

// TestRevertCheckUnavailableWhenEndpointRefusesEthCall is the same for the
// automatic check every review dialog runs on open -- the path a user actually
// hits after a mainnet failover to Flashbots.
func TestRevertCheckUnavailableWhenEndpointRefusesEthCall(t *testing.T) {
	node := newFakeNode(t)

	res, err := NewSimulator(node.client(t)).RevertCheckEOA(context.Background(), Request{})
	if err != nil {
		t.Fatalf("RevertCheckEOA: %v", err)
	}
	if res.Status != StatusUnavailable || res.Note != noteEndpointCannotSimulate {
		t.Errorf("got status=%v note=%q, want a stated endpoint limitation", res.Status, res.Note)
	}
}

// TestSimulateV1OmitsGas pins the Optimism fix on the wire: sending a gas budget
// above the chain's block gas limit makes eth_simulateV1 reject the whole
// request, so the field must not be sent at all.
func TestSimulateV1OmitsGas(t *testing.T) {
	var sawGas bool
	node := newFakeNode(t)
	node.on("eth_simulateV1", func(params []json.RawMessage) (interface{}, error) {
		p := decodeSimParams(t, params)
		if isProbe(p) {
			return []interface{}{}, nil
		}
		_, sawGas = p.BlockStateCalls[0].Calls[0]["gas"]
		return simOK(21000), nil
	})

	if _, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{}); err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if sawGas {
		t.Error("eth_simulateV1 payload carries a gas field; it must be left to the node's block limit")
	}
}

// TestCallTracerConfigSendsOnlyTopCall pins the zkSync fix: its callTracer
// deserializes tracerConfig strictly and rejects the request when onlyTopCall is
// absent, so the field is emitted even though geth would default it.
func TestCallTracerConfigSendsOnlyTopCall(t *testing.T) {
	cfg, err := json.Marshal(traceConfig{
		Tracer:       "callTracer",
		TracerConfig: &callTracerConfig{WithLog: true},
	})
	if err != nil {
		t.Fatalf("marshal traceConfig: %v", err)
	}
	if !strings.Contains(string(cfg), `"onlyTopCall"`) {
		t.Errorf("tracerConfig omits onlyTopCall: %s", cfg)
	}
}
