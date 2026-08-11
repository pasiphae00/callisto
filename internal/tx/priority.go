package tx

import (
	"context"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/pasiphae00/callisto/internal/rpc"
)

// Priority is how aggressively a transaction bids for inclusion. It sets only the
// priority fee (maxPriorityFeePerGas) — the base fee is fixed by the protocol from
// the parent block's gas usage and is identical for every transaction in a block,
// so no setting can change it.
type Priority int

const (
	// PriorityStandard bids near the bottom of recent blocks: cheapest, and fine
	// when there is no hurry.
	PriorityStandard Priority = iota
	// PriorityFast is the default — the middle of what recent blocks actually
	// paid, which is what a wallet should do when the user hasn't said otherwise.
	PriorityFast
	// PriorityRapid bids near the top of recent blocks for time-sensitive
	// transactions (a liquidation, a mint, beating a competing same-nonce tx).
	PriorityRapid
)

// DefaultPriority is what a fresh install and any unrecognized stored value use.
const DefaultPriority = PriorityFast

// String returns the stable identifier persisted in the config file.
func (p Priority) String() string {
	switch p {
	case PriorityStandard:
		return "standard"
	case PriorityRapid:
		return "rapid"
	default:
		return "fast"
	}
}

// Label returns the human name shown in the UI.
func (p Priority) Label() string {
	switch p {
	case PriorityStandard:
		return "Standard"
	case PriorityRapid:
		return "Rapid"
	default:
		return "Fast"
	}
}

// Description explains the trade-off in the Settings picker.
func (p Priority) Description() string {
	switch p {
	case PriorityStandard:
		return "Cheapest. May wait several blocks when the network is busy."
	case PriorityRapid:
		return "Bids high for the next block or two. Costs more."
	default:
		return "Balanced — the middle of what recent blocks paid."
	}
}

// Priorities lists the tiers in ascending order, for building a picker.
func Priorities() []Priority { return []Priority{PriorityStandard, PriorityFast, PriorityRapid} }

// ParsePriority reads a persisted identifier, falling back to the default for
// anything unrecognized (including the empty string a config written before this
// setting existed will have).
func ParsePriority(s string) Priority {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "standard":
		return PriorityStandard
	case "rapid":
		return PriorityRapid
	default:
		return DefaultPriority
	}
}

// rewardPercentile is the position within each recent block's priority fees that
// a tier bids at. Blocks are sorted by tip paid, so the 90th percentile means
// "more than 90% of the transactions in that block paid".
func (p Priority) rewardPercentile() float64 {
	switch p {
	case PriorityStandard:
		return 20
	case PriorityRapid:
		return 90
	default:
		return 60
	}
}

// floorScale multiplies the node's own tip suggestion to give each tier its
// floor — the least it will ever bid, used as-is when there is no congestion to
// bid against (and when eth_feeHistory is unavailable, which is the same thing
// with no data).
//
// The anchor is the *marginal price of inclusion*, not a mid-market rate. geth's
// oracle sorts each block's transactions ascending by effective tip, keeps only
// the cheapest three (`sampleNumber`), pools those across 20 blocks and returns
// the 60th percentile of that pool — so it tracks what the cheapest transaction
// that still got included paid. Measured against mainnet it lands around the 5th
// percentile of all transactions, not the 60th. An earlier version of this file
// claimed the latter and mapped Fast to 1x on that basis, which put the
// eth_feeHistory path and this one ~2700x apart for the same tier.
//
// Standard is therefore exactly the marginal price: the cheapest bid that still
// gets in. Fast and Rapid are urgency multiples of it. Expressed as a fraction to
// stay in integer arithmetic.
func (p Priority) floorScale() (num, den int64) {
	switch p {
	case PriorityStandard:
		return 1, 1 // the marginal price of inclusion
	case PriorityRapid:
		return 4, 1
	default:
		return 2, 1
	}
}

// feeHistoryBlocks is how far back to sample. Wide enough to smooth over one
// unusually empty or unusually contested block, short enough to still track a
// genuine change in congestion.
const feeHistoryBlocks = 20

// congestionScale is the fixed-point denominator for the blend weight. Integer
// arithmetic throughout: these are wei, and float64 cannot hold them exactly.
const congestionScale = 1000

// SuggestTip returns the priority fee to bid for the given tier.
//
// Two different things are measured, and the tier is a blend of them:
//
//   - The node's eth_maxPriorityFeePerGas is the *marginal price of inclusion* —
//     what the cheapest transaction that still got into recent blocks paid (see
//     floorScale). Scaled per tier, this is the floor.
//   - eth_feeHistory's percentiles are *willingness to pay* — what transactions
//     in recent blocks chose to bid, which is what public gas trackers report.
//
// Willingness to pay is the wrong number to bid on its own: in a half-empty block
// every transaction is included whatever it paid, so the p20 transaction paid
// what it did by choice, not by necessity. Bidding it means paying a competitive
// price in a market with no competition — measured on mainnet at ~52% full, that
// was 187x the marginal price for Standard alone.
//
// So the percentile is approached only to the degree blocks are actually
// contested, using the gas-used ratios eth_feeHistory already returns alongside
// the rewards:
//
//	tip = floor + congestion * (percentile - floor)      (never below floor)
//
// Empty blocks bid the floor; full blocks bid the percentile; the common case
// interpolates. This also subsumes what used to be a special-cased zero-guard: a
// percentile of zero (valid post-merge, and common on a quiet chain, but widely
// dropped by builders) now falls below the floor and is clamped away to it,
// rather than being detected and handled separately.
//
// Where eth_feeHistory is unavailable — an endpoint that doesn't serve it, or a
// test double with no raw client — the tier's floor is used alone, which is
// exactly what the blend converges to when there is no congestion. The two paths
// therefore agree rather than diverging by orders of magnitude.
func SuggestTip(ctx context.Context, client rpc.Client, p Priority) (*big.Int, error) {
	suggested, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, err
	}

	num, den := p.floorScale()
	floor := new(big.Int).Mul(suggested, big.NewInt(num))
	floor.Div(floor, big.NewInt(den))

	target, congestion, ok := sampleFeeHistory(ctx, client, p)
	if !ok {
		return floor, nil
	}

	// floor + congestion*(target-floor), in integer arithmetic.
	delta := new(big.Int).Sub(target, floor)
	delta.Mul(delta, big.NewInt(congestion))
	delta.Div(delta, big.NewInt(congestionScale))
	tip := delta.Add(delta, floor)

	if tip.Cmp(floor) < 0 {
		return floor, nil
	}
	return tip, nil
}

// feeHistoryResult is eth_feeHistory's response. The base fee is ignored here —
// it is read from the head block instead, since that is the value the transaction
// will actually be priced against.
type feeHistoryResult struct {
	Reward [][]hexutil.Big `json:"reward"`
	// GasUsedRatio is each block's gas used over its gas limit, in [0,1]. It is
	// the congestion signal: how much competition for space there actually was.
	GasUsedRatio []float64 `json:"gasUsedRatio"`
}

// sampleFeeHistory returns the tier's percentile across recent blocks and how
// contested those blocks were, in one round trip.
//
// The percentile is the median across blocks, not the mean: a single block
// containing one desperate transaction paying a thousandfold tip would drag a
// mean far above what inclusion actually costs. Congestion is the mean, since
// every block's spare capacity counts equally toward how easy inclusion is, and
// it is returned as an integer fraction of congestionScale.
func sampleFeeHistory(ctx context.Context, client rpc.Client, p Priority) (target *big.Int, congestion int64, ok bool) {
	raw := client.RawClient()
	if raw == nil {
		return nil, 0, false
	}

	var out feeHistoryResult
	err := raw.CallContext(ctx, &out, "eth_feeHistory",
		hexutil.Uint64(feeHistoryBlocks), "latest", []float64{p.rewardPercentile()})
	if err != nil || len(out.Reward) == 0 {
		return nil, 0, false
	}

	samples := make([]*big.Int, 0, len(out.Reward))
	for _, block := range out.Reward {
		if len(block) == 0 {
			continue
		}
		samples = append(samples, (*big.Int)(&block[0]))
	}
	if len(samples) == 0 {
		return nil, 0, false
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Cmp(samples[j]) < 0 })

	return new(big.Int).Set(samples[len(samples)/2]), meanCongestion(out.GasUsedRatio), true
}

// meanCongestion averages the per-block gas-used ratios into a weight in
// [0,congestionScale]. An endpoint that omits gasUsedRatio yields zero, which
// makes the blend fall back to the tier floor — the safe direction, since the
// floor is the cheaper of the two anchors.
func meanCongestion(ratios []float64) int64 {
	if len(ratios) == 0 {
		return 0
	}
	var sum float64
	for _, r := range ratios {
		sum += r
	}
	scaled := int64(sum / float64(len(ratios)) * congestionScale)
	if scaled < 0 {
		return 0
	}
	if scaled > congestionScale {
		return congestionScale
	}
	return scaled
}
