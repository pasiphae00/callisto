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
	rewards [][]string // per block, per requested percentile, as hex wei
	// ratios is each block's gasUsedRatio. Nil means the field is omitted
	// entirely, as an endpoint that doesn't report it would.
	ratios        []float64
	suggestion    string
	serveHistory  bool
	sawPercentile float64
}

// fullBlocks marks every block as completely full, isolating the percentile
// derivation from the congestion blend in tests that are about the percentile.
func fullBlocks(n int) []float64 {
	r := make([]float64, n)
	for i := range r {
		r[i] = 1
	}
	return r
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
			result := map[string]interface{}{"reward": n.rewards}
			if n.ratios != nil {
				result["gasUsedRatio"] = n.ratios
			}
			resp["result"] = result
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
		ratios:       fullBlocks(5), // full blocks: bid the percentile outright
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

func TestSuggestTipScalesWithCongestion(t *testing.T) {
	// The core of the design: the same observed percentile is bid in full only
	// when blocks are actually contested. In a half-empty block every
	// transaction is included whatever it paid, so what others chose to pay is
	// no evidence of what inclusion costs.
	//
	// Fast's floor is 2x the 1 gwei suggestion = 2 gwei; the percentile is
	// 10 gwei; so the tip is 2 + congestion*(10-2) gwei.
	cases := []struct {
		name  string
		ratio float64
		want  *big.Int
	}{
		{"empty blocks bid the floor", 0, gwei(2)},
		{"half full interpolates", 0.5, gwei(6)},
		{"full blocks bid the percentile", 1, gwei(10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &feeHistoryNode{
				serveHistory: true,
				suggestion:   "0x" + gwei(1).Text(16),
				ratios:       []float64{tc.ratio, tc.ratio},
				rewards:      [][]string{{"0x" + gwei(10).Text(16)}, {"0x" + gwei(10).Text(16)}},
			}

			got, err := SuggestTip(context.Background(), node.client(t), PriorityFast)
			if err != nil {
				t.Fatal(err)
			}
			if got.Cmp(tc.want) != 0 {
				t.Errorf("at %.0f%% full: tip = %v; want %v", tc.ratio*100, got, tc.want)
			}
		})
	}
}

func TestSuggestTipNeverBidsBelowTheMarginalPrice(t *testing.T) {
	// The node's suggestion is what the cheapest *included* transaction paid, so
	// an observed percentile below it is not a cheaper way in — it is stale or
	// contradictory data. Standard bids low, but not below the price of getting
	// in at all.
	node := &feeHistoryNode{
		serveHistory: true,
		suggestion:   "0x" + gwei(50).Text(16),
		ratios:       fullBlocks(1),
		rewards:      [][]string{{"0x" + big.NewInt(1000).Text(16)}},
	}

	got, err := SuggestTip(context.Background(), node.client(t), PriorityStandard)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(gwei(50)) != 0 {
		t.Errorf("tip = %v; want the 50 gwei marginal price, not the lower observed percentile", got)
	}
}

func TestSuggestTipClampsAZeroPercentileToTheFloor(t *testing.T) {
	// A zero tip is valid post-merge but widely dropped by builders. This used to
	// need a special-cased zero-guard; the floor clamp now subsumes it, since
	// zero is necessarily at or below the floor.
	node := &feeHistoryNode{
		serveHistory: true,
		suggestion:   "0x" + gwei(10).Text(16),
		ratios:       fullBlocks(3),
		rewards:      [][]string{{"0x0"}, {"0x0"}, {"0x0"}},
	}

	got, err := SuggestTip(context.Background(), node.client(t), PriorityStandard)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sign() == 0 {
		t.Fatal("tip = 0; that prepares a transaction builders may never include")
	}
	if got.Cmp(gwei(10)) != 0 {
		t.Errorf("tip = %v; want Standard's 10 gwei floor", got)
	}
}

// TestSuggestTipAgreesAcrossBothPaths pins the bug this design replaced: the
// eth_feeHistory path and the no-feeHistory path used to disagree by ~2700x for
// the same tier, because the fallback was calibrated on the false premise that
// the node's suggestion sits at the 60th percentile. With no congestion the
// blend *is* the floor, so the two paths now agree exactly.
func TestSuggestTipAgreesAcrossBothPaths(t *testing.T) {
	const suggestion = "0x" + "2540be400" // 10 gwei

	for _, tier := range Priorities() {
		quiet := &feeHistoryNode{
			serveHistory: true,
			suggestion:   suggestion,
			ratios:       []float64{0, 0},
			rewards:      [][]string{{"0x" + gwei(500).Text(16)}, {"0x" + gwei(500).Text(16)}},
		}
		bare := &feeHistoryNode{serveHistory: false, suggestion: suggestion}

		withHistory, err := SuggestTip(context.Background(), quiet.client(t), tier)
		if err != nil {
			t.Fatal(err)
		}
		withoutHistory, err := SuggestTip(context.Background(), bare.client(t), tier)
		if err != nil {
			t.Fatal(err)
		}
		if withHistory.Cmp(withoutHistory) != 0 {
			t.Errorf("%v: feeHistory path gives %v, fallback gives %v; they must agree with no congestion",
				tier, withHistory, withoutHistory)
		}
	}
}

func TestSuggestTipFallsBackWhenFeeHistoryIsUnavailable(t *testing.T) {
	node := &feeHistoryNode{serveHistory: false, suggestion: "0x" + gwei(10).Text(16)}
	client := node.client(t)

	cases := []struct {
		tier Priority
		want *big.Int
	}{
		{PriorityStandard, gwei(10)}, // the marginal price of inclusion
		{PriorityFast, gwei(20)},
		{PriorityRapid, gwei(40)},
	}
	for _, tc := range cases {
		got, err := SuggestTip(context.Background(), client, tc.tier)
		if err != nil {
			t.Fatalf("%v: %v", tc.tier, err)
		}
		if got.Cmp(tc.want) != 0 {
			t.Errorf("%v tip = %v; want %v (the tier's floor)", tc.tier, got, tc.want)
		}
	}
}

func TestSuggestTipTreatsMissingGasUsedRatioAsNoCongestion(t *testing.T) {
	// An endpoint that serves rewards but omits gasUsedRatio leaves us no
	// congestion evidence. Falling to the floor is the safe direction: it is the
	// cheaper of the two anchors, and still the price of inclusion.
	node := &feeHistoryNode{
		serveHistory: true,
		suggestion:   "0x" + gwei(1).Text(16),
		rewards:      [][]string{{"0x" + gwei(500).Text(16)}},
	}

	got, err := SuggestTip(context.Background(), node.client(t), PriorityStandard)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(gwei(1)) != 0 {
		t.Errorf("tip = %v; want the 1 gwei floor, not the unweighted percentile", got)
	}
}

func TestSuggestTipWithoutARawClientStillWorks(t *testing.T) {
	// Every existing test double returns a nil raw client; the floor path must
	// carry them rather than erroring.
	m := &txMock{tip: gwei(10)}
	got, err := SuggestTip(context.Background(), m, PriorityRapid)
	if err != nil {
		t.Fatalf("SuggestTip: %v", err)
	}
	if got.Cmp(gwei(40)) != 0 {
		t.Errorf("tip = %v; want Rapid's 40 gwei floor", got)
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
