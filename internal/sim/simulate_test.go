package sim

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/pasiphae00/callisto/internal/rpc"
)

// ---------------------------------------------------------------------------
// A fake JSON-RPC endpoint.
//
// The simulation strategies talk to the node through raw CallContext with
// hand-rolled request and response types, so the interesting failure modes are
// in the JSON on the wire -- field names, hex encodings, the shape geth returns
// for a reverted call. Testing against a real *gethrpc.Client over HTTP
// exercises all of that, which a hand-stubbed Go-level double would skip.
// ---------------------------------------------------------------------------

// handlerFunc answers one JSON-RPC method. Returning an error produces a
// JSON-RPC error response; the errCode/errData fields let a test reproduce
// geth's "execution reverted" shape exactly.
type rpcError struct {
	code int
	msg  string
	data string
}

func (e *rpcError) Error() string { return e.msg }

type fakeNode struct {
	t        *testing.T
	handlers map[string]func(params []json.RawMessage) (interface{}, error)
	calls    map[string]int
	server   *httptest.Server
}

func newFakeNode(t *testing.T) *fakeNode {
	t.Helper()
	n := &fakeNode{
		t:        t,
		handlers: make(map[string]func([]json.RawMessage) (interface{}, error)),
		calls:    make(map[string]int),
	}
	n.server = httptest.NewServer(http.HandlerFunc(n.serve))
	t.Cleanup(n.server.Close)
	return n
}

func (n *fakeNode) on(method string, h func(params []json.RawMessage) (interface{}, error)) *fakeNode {
	n.handlers[method] = h
	return n
}

// ok registers a method that always returns a fixed result.
func (n *fakeNode) ok(method string, result interface{}) *fakeNode {
	return n.on(method, func([]json.RawMessage) (interface{}, error) { return result, nil })
}

func (n *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		n.t.Errorf("fake node: bad request: %v", err)
		return
	}
	n.calls[req.Method]++

	resp := map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(req.ID)}
	h, known := n.handlers[req.Method]
	if !known {
		// Exactly what a node answers for a namespace it does not serve.
		resp["error"] = map[string]interface{}{
			"code":    -32601,
			"message": "the method " + req.Method + " does not exist/is not available",
		}
	} else if result, err := h(req.Params); err != nil {
		e := map[string]interface{}{"code": -32000, "message": err.Error()}
		if re, isRPC := err.(*rpcError); isRPC {
			e["code"] = re.code
			if re.data != "" {
				e["data"] = re.data
			}
		}
		resp["error"] = e
	} else {
		resp["result"] = result
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		n.t.Errorf("fake node: encode response: %v", err)
	}
}

// client wires the fake node into an rpc.Client. CallContract is implemented
// directly (rather than via ethclient) so a test can control the eth_call
// error shape precisely.
func (n *fakeNode) client(t *testing.T) *fakeClient {
	t.Helper()
	raw, err := gethrpc.Dial(n.server.URL)
	if err != nil {
		t.Fatalf("dial fake node: %v", err)
	}
	t.Cleanup(raw.Close)
	return &fakeClient{node: n, raw: raw}
}

type fakeClient struct {
	rpc.Client // unimplemented methods panic if a test reaches them
	node       *fakeNode
	raw        *gethrpc.Client
}

func (c *fakeClient) RawClient() *gethrpc.Client { return c.raw }

func (c *fakeClient) CallContract(ctx context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	var out string
	err := c.raw.CallContext(ctx, &out, "eth_call", toCallArg(msg), "latest")
	if err != nil {
		return nil, err
	}
	return common.FromHex(out), nil
}

func toCallArg(msg ethereum.CallMsg) map[string]interface{} {
	arg := map[string]interface{}{"from": msg.From}
	if msg.To != nil {
		arg["to"] = *msg.To
	}
	if len(msg.Data) > 0 {
		arg["input"] = common.Bytes2Hex(msg.Data)
	}
	return arg
}

// ---------------------------------------------------------------------------
// helpers for building simulation responses
// ---------------------------------------------------------------------------

func hexQty(v uint64) string { return "0x" + big.NewInt(int64(v)).Text(16) }

// logJSON renders a log the way a node does on the wire.
func logJSON(address common.Address, topics []common.Hash, data []byte) map[string]interface{} {
	tp := make([]string, len(topics))
	for i, t := range topics {
		tp[i] = t.Hex()
	}
	return map[string]interface{}{
		"address":     address.Hex(),
		"topics":      tp,
		"data":        "0x" + common.Bytes2Hex(data),
		"blockNumber": "0x1",
		"logIndex":    "0x0",
	}
}

func simOK(gasUsed uint64, logs ...map[string]interface{}) []interface{} {
	if logs == nil {
		logs = []map[string]interface{}{}
	}
	return []interface{}{map[string]interface{}{
		"calls": []interface{}{map[string]interface{}{
			"returnData": "0x",
			"logs":       logs,
			"gasUsed":    hexQty(gasUsed),
			"status":     "0x1",
		}},
	}}
}

// errorStringPayload builds the ABI-encoded Error(string) revert data a
// require() with a message produces.
func errorStringPayload(t *testing.T, msg string) []byte {
	t.Helper()
	packed, err := revertStringArgs.Pack(msg)
	if err != nil {
		t.Fatalf("pack revert string: %v", err)
	}
	return append(append([]byte{}, errorStringSelector...), packed...)
}

// ---------------------------------------------------------------------------
// capability probing
// ---------------------------------------------------------------------------

func TestCapsDetectsBothMethods(t *testing.T) {
	node := newFakeNode(t).
		ok("eth_simulateV1", []interface{}{}).
		ok("debug_traceCall", map[string]interface{}{"type": "CALL", "gasUsed": "0x0"})

	caps := NewProber(node.client(t)).Caps(context.Background())
	if !caps.SimulateV1 || !caps.DebugTrace {
		t.Fatalf("Caps = %+v; want both supported", caps)
	}
	if got := caps.Tier(); got != TierDebug {
		t.Errorf("Tier = %v; want TierDebug (best available)", got)
	}
}

func TestCapsBareEndpointIsCallOnly(t *testing.T) {
	// A node serving neither method answers -32601 to both probes.
	caps := NewProber(newFakeNode(t).client(t)).Caps(context.Background())
	if caps.SimulateV1 || caps.DebugTrace || caps.Rich() {
		t.Fatalf("Caps = %+v; want nothing supported", caps)
	}
	if got := caps.Tier(); got != TierCallOnly {
		t.Errorf("Tier = %v; want TierCallOnly", got)
	}
}

func TestCapsTreatsGatedDebugNamespaceAsUnavailable(t *testing.T) {
	// Public providers commonly gate debug behind a plan and answer with an
	// authorization error rather than method-not-found.
	node := newFakeNode(t).
		ok("eth_simulateV1", []interface{}{}).
		on("debug_traceCall", func([]json.RawMessage) (interface{}, error) {
			return nil, &rpcError{code: -32000, msg: "insufficient permissions for method debug_traceCall"}
		})

	caps := NewProber(node.client(t)).Caps(context.Background())
	if caps.DebugTrace {
		t.Error("a gated debug namespace should not count as available")
	}
	if !caps.SimulateV1 {
		t.Error("eth_simulateV1 should still be available")
	}
}

func TestCapsProbesOnceAndCaches(t *testing.T) {
	node := newFakeNode(t).ok("eth_simulateV1", []interface{}{})
	p := NewProber(node.client(t))
	for i := 0; i < 3; i++ {
		p.Caps(context.Background())
	}
	if n := node.calls["eth_simulateV1"]; n != 1 {
		t.Errorf("probed eth_simulateV1 %d times; want 1 (cached after the first)", n)
	}
}

// ---------------------------------------------------------------------------
// eth_simulateV1
// ---------------------------------------------------------------------------

func TestSimulateEOAViaSimulateV1DecodesDeltas(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	pool := common.HexToAddress("0x2222222222222222222222222222222222222b")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")

	oneETH := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	tokensOut := big.NewInt(3_500_000_000)

	node := newFakeNode(t).ok("eth_simulateV1", simOK(120_000,
		// traceTransfers surfaces the native leg as a log from 0x0.
		logJSON(zeroAddress, []common.Hash{transferSig, topicOf(wallet), topicOf(pool)}, word(oneETH)),
		// and the pool pays out tokens.
		logJSON(token, []common.Hash{transferSig, topicOf(pool), topicOf(wallet)}, word(tokensOut)),
	))

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{
		From: wallet, To: pool, Value: oneETH,
	})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("Status = %v (%q); want StatusOK", res.Status, res.RevertReason)
	}
	if res.Tier != TierSimulate {
		t.Errorf("Tier = %v; want TierSimulate", res.Tier)
	}
	if res.GasUsed != 120_000 {
		t.Errorf("GasUsed = %d; want 120000", res.GasUsed)
	}
	if res.ETHDelta.Cmp(new(big.Int).Neg(oneETH)) != 0 {
		t.Errorf("ETHDelta = %v; want -%v", res.ETHDelta, oneETH)
	}
	if len(res.Tokens) != 1 || res.Tokens[0].Token != token || res.Tokens[0].Delta.Cmp(tokensOut) != 0 {
		t.Fatalf("Tokens = %+v; want +%v of %s", res.Tokens, tokensOut, token.Hex())
	}
}

func TestSimulateEOAViaSimulateV1ReportsRevertReason(t *testing.T) {
	payload := errorStringPayload(t, "ERC20: transfer amount exceeds balance")

	node := newFakeNode(t).ok("eth_simulateV1", []interface{}{map[string]interface{}{
		"calls": []interface{}{map[string]interface{}{
			"returnData": "0x" + common.Bytes2Hex(payload),
			"logs":       []interface{}{},
			"gasUsed":    hexQty(21_000),
			"status":     "0x0",
			"error":      map[string]interface{}{"code": 3, "message": "execution reverted"},
		}},
	}})

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if res.Status != StatusRevert {
		t.Fatalf("Status = %v; want StatusRevert", res.Status)
	}
	if res.RevertReason != "ERC20: transfer amount exceeds balance" {
		t.Errorf("RevertReason = %q; want the decoded require message", res.RevertReason)
	}
}

// ---------------------------------------------------------------------------
// debug_traceCall fallback
// ---------------------------------------------------------------------------

func TestSimulateEOAFallsBackToDebugTrace(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	recipient := common.HexToAddress("0x2222222222222222222222222222222222222b")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	amount := big.NewInt(500)
	sent := big.NewInt(7_000)

	// No eth_simulateV1 here: the node only serves debug.
	node := newFakeNode(t).ok("debug_traceCall", map[string]interface{}{
		"type":    "CALL",
		"from":    wallet.Hex(),
		"to":      recipient.Hex(),
		"value":   "0x" + sent.Text(16),
		"gasUsed": hexQty(45_000),
		"logs": []interface{}{map[string]interface{}{
			"address": token.Hex(),
			"topics":  []string{transferSig.Hex(), topicOf(wallet).Hex(), topicOf(recipient).Hex()},
			"data":    "0x" + common.Bytes2Hex(word(amount)),
		}},
	})

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{From: wallet, To: recipient})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if res.Tier != TierDebug || res.Status != StatusOK {
		t.Fatalf("Tier/Status = %v/%v; want TierDebug/StatusOK", res.Tier, res.Status)
	}
	// The tracer has no traceTransfers, so ETH comes off the frame's value.
	if res.ETHDelta.Cmp(new(big.Int).Neg(sent)) != 0 {
		t.Errorf("ETHDelta = %v; want -%v from the call frame's value", res.ETHDelta, sent)
	}
	if len(res.Tokens) != 1 || res.Tokens[0].Delta.Cmp(new(big.Int).Neg(amount)) != 0 {
		t.Errorf("Tokens = %+v; want -%v", res.Tokens, amount)
	}
}

func TestDebugTraceSkipsRevertedSubtree(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	other := common.HexToAddress("0x2222222222222222222222222222222222222b")

	// A try/catch pattern: the inner call reverts and is caught, so its
	// Transfer never happened and must not appear in the preview.
	node := newFakeNode(t).ok("debug_traceCall", map[string]interface{}{
		"type": "CALL", "from": wallet.Hex(), "to": other.Hex(), "gasUsed": hexQty(30_000),
		"calls": []interface{}{map[string]interface{}{
			"type": "CALL", "from": other.Hex(), "to": token.Hex(), "error": "execution reverted",
			"logs": []interface{}{map[string]interface{}{
				"address": token.Hex(),
				"topics":  []string{transferSig.Hex(), topicOf(other).Hex(), topicOf(wallet).Hex()},
				"data":    "0x" + common.Bytes2Hex(word(big.NewInt(999))),
			}},
		}},
	})

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{From: wallet, To: other})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if len(res.Tokens) != 0 {
		t.Fatalf("Tokens = %+v; a reverted subcall's logs must be discarded", res.Tokens)
	}
}

// ---------------------------------------------------------------------------
// eth_call fallback
// ---------------------------------------------------------------------------

func TestSimulateEOAFallsBackToCallOnly(t *testing.T) {
	node := newFakeNode(t).ok("eth_call", "0x")

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if res.Status != StatusOK || res.Tier != TierCallOnly {
		t.Fatalf("Status/Tier = %v/%v; want StatusOK/TierCallOnly", res.Status, res.Tier)
	}
	if res.Note == "" {
		t.Error("a call-only result must carry a note explaining the missing asset preview")
	}
}

func TestSimulateEOACallOnlyReportsRevert(t *testing.T) {
	payload := errorStringPayload(t, "insufficient allowance")
	node := newFakeNode(t).on("eth_call", func([]json.RawMessage) (interface{}, error) {
		return nil, &rpcError{code: 3, msg: "execution reverted", data: "0x" + common.Bytes2Hex(payload)}
	})

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{})
	if err != nil {
		t.Fatalf("SimulateEOA: %v", err)
	}
	if res.Status != StatusRevert || res.RevertReason != "insufficient allowance" {
		t.Fatalf("= %v %q; want StatusRevert with the decoded reason", res.Status, res.RevertReason)
	}
}

func TestSimulateEOASurfacesNonRevertRPCFailure(t *testing.T) {
	node := newFakeNode(t).on("eth_call", func([]json.RawMessage) (interface{}, error) {
		return nil, &rpcError{code: -32005, msg: "rate limit exceeded"}
	})

	res, err := NewSimulator(node.client(t)).SimulateEOA(context.Background(), Request{})
	if err == nil {
		t.Fatal("a rate-limit failure must be an error, not a silent StatusOK")
	}
	if res.Status != StatusUnavailable {
		t.Errorf("Status = %v; want StatusUnavailable", res.Status)
	}
}
