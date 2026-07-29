package tx

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestParsePriorityRoundTrip(t *testing.T) {
	for _, p := range Priorities() {
		if got := ParsePriority(p.String()); got != p {
			t.Errorf("ParsePriority(%q) = %v; want %v", p.String(), got, p)
		}
	}
}

func TestParsePriorityFallsBackToDefault(t *testing.T) {
	// The empty string is what a config written before this setting existed has;
	// it must land on the default rather than on tier zero by accident.
	for _, in := range []string{"", "  ", "turbo", "FAST!"} {
		if got := ParsePriority(in); got != DefaultPriority {
			t.Errorf("ParsePriority(%q) = %v; want the default %v", in, got, DefaultPriority)
		}
	}
	if DefaultPriority != PriorityFast {
		t.Errorf("DefaultPriority = %v; the product decision is Fast", DefaultPriority)
	}
}

func TestParsePriorityIsCaseInsensitive(t *testing.T) {
	if got := ParsePriority("RAPID"); got != PriorityRapid {
		t.Errorf("ParsePriority(\"RAPID\") = %v; want PriorityRapid", got)
	}
}

func TestPercentilesAreOrdered(t *testing.T) {
	// The whole feature rests on Standard < Fast < Rapid.
	std, fast, rapid := PriorityStandard.rewardPercentile(), PriorityFast.rewardPercentile(), PriorityRapid.rewardPercentile()
	if !(std < fast && fast < rapid) {
		t.Fatalf("percentiles = %v/%v/%v; want strictly ascending", std, fast, rapid)
	}
}

// --- eth_feeHistory ---------------------------------------------------------

// feeHistoryNode serves eth_feeHistory (and eth_maxPriorityFeePerGas) so the
// derivation can be tested over the real JSON-RPC wire encoding.
type feeHistoryNode struct {
	rewards       [][]string // per block, per requested percentile, as hex wei
	suggestion    string
	serveHistory  bool
	sawPercentile float64
}

func (n *feeHistoryNode) client(t *testing.T) *feeHistoryClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(req.ID)}

		switch {
		case req.Method == "eth_feeHistory" && n.serveHistory:
			var pcts []float64
			if len(req.Params) > 2 {
				_ = json.Unmarshal(req.Params[2], &pcts)
			}
			if len(pcts) > 0 {
				n.sawPercentile = pcts[0]
			}
			resp["result"] = map[string]interface{}{"reward": n.rewards}
		default:
			resp["error"] = map[string]interface{}{
				"code": -32601, "message": "the method " + req.Method + " does not exist/is not available",
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	raw, err := gethrpc.Dial(srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(raw.Close)
	return &feeHistoryClient{txMock: txMock{tip: hexToBig(t, n.suggestion)}, raw: raw}
}

type feeHistoryClient struct {
	txMock
	raw *gethrpc.Client
}

func (c *feeHistoryClient) RawClient() *gethrpc.Client { return c.raw }

func hexToBig(t *testing.T, s string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(s[2:], 16)
	if !ok {
		t.Fatalf("bad hex %q", s)
	}
	return v
}

func gwei(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000))
}

func TestSuggestTipUsesFeeHistoryMedian(t *testing.T) {
	// Five blocks; the median of the sampled rewards is 3 gwei. The outlier is
	// deliberately enormous — a mean would be dragged far above what inclusion
	// actually costs.
	node := &feeHistoryNode{
		serveHistory: true,
		suggestion:   "0x1",
		rewards: [][]string{
			{"0x" + gwei(1).Text(16)},
			{"0x" + gwei(2).Text(16)},
			{"0x" + gwei(3).Text(16)},
			{"0x" + gwei(4).Text(16)},
			{"0x" + gwei(1000).Text(16)},
		},
	}

	got, err := SuggestTip(context.Background(), node.client(t), PriorityFast)
	if err != nil {
		t.Fatalf("SuggestTip: %v", err)
	}
	if got.Cmp(gwei(3)) != 0 {
		t.Errorf("tip = %v; want the median 3 gwei, not a mean skewed by the outlier", got)
	}
}

func TestSuggestTipRequestsTheTiersPercentile(t *testing.T) {
	node := &feeHistoryNode{serveHistory: true, suggestion: "0x1", rewards: [][]string{{"0x" + gwei(1).Text(16)}}}
	client := node.client(t)

	if _, err := SuggestTip(context.Background(), client, PriorityRapid); err != nil {
		t.Fatal(err)
	}
	if node.sawPercentile != PriorityRapid.rewardPercentile() {
		t.Errorf("requested percentile %v; want Rapid's %v", node.sawPercentile, PriorityRapid.rewardPercentile())
	}
}

func TestSuggestTipHonoursALowStandardBid(t *testing.T) {
	// Bidding low is the point of Standard: a positive-but-small percentile must
	// be used as-is, not clamped up to the node's own suggestion.
	node := &feeHistoryNode{
		serveHistory: true,
		suggestion:   "0x" + gwei(50).Text(16),
		rewards:      [][]string{{"0x" + big.NewInt(1000).Text(16)}},
	}

	got, err := SuggestTip(context.Background(), node.client(t), PriorityStandard)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("tip = %v; want the observed 1000 wei, not the node's much larger suggestion", got)
	}
}

func TestSuggestTipTreatsZeroAsNoAnswer(t *testing.T) {
	// A zero tip is valid post-merge but many builders won't include it, so a
	// zero percentile falls back to scaling the node's suggestion.
	node := &feeHistoryNode{
		serveHistory: true,
		suggestion:   "0x" + gwei(10).Text(16),
		rewards:      [][]string{{"0x0"}, {"0x0"}, {"0x0"}},
	}

	got, err := SuggestTip(context.Background(), node.client(t), PriorityStandard)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sign() == 0 {
		t.Fatal("tip = 0; a zero percentile must fall back, not prepare an unmineable transaction")
	}
	if got.Cmp(gwei(5)) != 0 {
		t.Errorf("tip = %v; want half the 10 gwei suggestion", got)
	}
}

func TestSuggestTipFallsBackWhenFeeHistoryIsUnavailable(t *testing.T) {
	node := &feeHistoryNode{serveHistory: false, suggestion: "0x" + gwei(10).Text(16)}
	client := node.client(t)

	cases := []struct {
		tier Priority
		want *big.Int
	}{
		{PriorityStandard, gwei(5)},
		{PriorityFast, gwei(10)},
		{PriorityRapid, gwei(20)},
	}
	for _, tc := range cases {
		got, err := SuggestTip(context.Background(), client, tc.tier)
		if err != nil {
			t.Fatalf("%v: %v", tc.tier, err)
		}
		if got.Cmp(tc.want) != 0 {
			t.Errorf("%v tip = %v; want %v (scaled from the node suggestion)", tc.tier, got, tc.want)
		}
	}
}

func TestSuggestTipWithoutARawClientStillWorks(t *testing.T) {
	// Every existing test double returns a nil raw client; the fallback path must
	// carry them rather than erroring.
	m := &txMock{tip: gwei(10)}
	got, err := SuggestTip(context.Background(), m, PriorityRapid)
	if err != nil {
		t.Fatalf("SuggestTip: %v", err)
	}
	if got.Cmp(gwei(20)) != 0 {
		t.Errorf("tip = %v; want 20 gwei", got)
	}
}

func TestEstimateFeesAppliesTheTier(t *testing.T) {
	m := &txMock{gasEstimate: 21_000, tip: gwei(10), baseFee: gwei(30)}

	std, err := EstimateFees(context.Background(), m, testAddr, testCall, PriorityStandard)
	if err != nil {
		t.Fatal(err)
	}
	rapid, err := EstimateFees(context.Background(), m, testAddr, testCall, PriorityRapid)
	if err != nil {
		t.Fatal(err)
	}

	if std.GasTipCap.Cmp(rapid.GasTipCap) >= 0 {
		t.Errorf("standard tip %v is not below rapid %v", std.GasTipCap, rapid.GasTipCap)
	}
	// The base fee is the protocol's, identical regardless of tier.
	if std.BaseFee.Cmp(rapid.BaseFee) != 0 {
		t.Errorf("base fee differs between tiers (%v vs %v); it must not", std.BaseFee, rapid.BaseFee)
	}
	// A higher tip must raise the fee cap, or the bid would not actually be paid.
	if std.GasFeeCap.Cmp(rapid.GasFeeCap) >= 0 {
		t.Errorf("standard max fee %v is not below rapid %v", std.GasFeeCap, rapid.GasFeeCap)
	}
	if rapid.Priority != PriorityRapid {
		t.Errorf("Fees.Priority = %v; want the tier it was estimated for", rapid.Priority)
	}
}

var (
	testAddr = common.HexToAddress("0x1111111111111111111111111111111111111a")
	testCall = Call{To: common.HexToAddress("0x2222222222222222222222222222222222222b"), Value: big.NewInt(1)}
)
