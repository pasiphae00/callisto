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

// fallbackScale multiplies the node's own tip suggestion when eth_feeHistory is
// unavailable. geth's suggestion sits around the 60th percentile of recent
// blocks, so Fast maps to it unchanged and the other tiers scale from there.
// Expressed as a fraction to stay in integer arithmetic.
func (p Priority) fallbackScale() (num, den int64) {
	switch p {
	case PriorityStandard:
		return 1, 2 // half the suggestion
	case PriorityRapid:
		return 2, 1 // double it
	default:
		return 1, 1
	}
}

// feeHistoryBlocks is how far back to sample. Wide enough to smooth over one
// unusually empty or unusually contested block, short enough to still track a
// genuine change in congestion.
const feeHistoryBlocks = 20

// SuggestTip returns the priority fee to bid for the given tier.
//
// It prefers eth_feeHistory, which reports what transactions in recent blocks
// actually paid, so the tiers are grounded in observed data rather than in a
// multiplier we invented — this is how public gas trackers derive their
// Standard/Fast/Rapid numbers. Where that isn't available (an endpoint that
// doesn't serve it, or a test double with no raw client) it scales the node's own
// eth_maxPriorityFeePerGas suggestion instead.
//
// A zero result is treated as no answer rather than as a bid of zero. A low
// percentile on a quiet chain genuinely can be zero, and while a zero tip is
// valid post-merge, many builders will not include such a transaction — so we
// fall back to scaling the node's suggestion instead of preparing a transaction
// that may never be mined. Note this is *only* a zero-guard: a Standard bid that
// comes back positive-but-low is honoured, because bidding low is the entire
// point of that tier.
func SuggestTip(ctx context.Context, client rpc.Client, p Priority) (*big.Int, error) {
	if tip, ok := tipFromFeeHistory(ctx, client, p); ok && tip.Sign() > 0 {
		return tip, nil
	}

	suggested, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, err
	}
	num, den := p.fallbackScale()
	scaled := new(big.Int).Mul(suggested, big.NewInt(num))
	return scaled.Div(scaled, big.NewInt(den)), nil
}

// feeHistoryResult is eth_feeHistory's response. Only the reward matrix matters
// here; the base fee is read from the head block instead, since that is the value
// the transaction will actually be priced against.
type feeHistoryResult struct {
	Reward [][]hexutil.Big `json:"reward"`
}

// tipFromFeeHistory samples what recent blocks paid at the tier's percentile and
// returns the median across those blocks.
//
// Median, not mean: a single block containing one desperate transaction paying a
// thousandfold tip would drag a mean far above what inclusion actually costs.
func tipFromFeeHistory(ctx context.Context, client rpc.Client, p Priority) (*big.Int, bool) {
	raw := client.RawClient()
	if raw == nil {
		return nil, false
	}

	var out feeHistoryResult
	err := raw.CallContext(ctx, &out, "eth_feeHistory",
		hexutil.Uint64(feeHistoryBlocks), "latest", []float64{p.rewardPercentile()})
	if err != nil || len(out.Reward) == 0 {
		return nil, false
	}

	samples := make([]*big.Int, 0, len(out.Reward))
	for _, block := range out.Reward {
		if len(block) == 0 {
			continue
		}
		samples = append(samples, (*big.Int)(&block[0]))
	}
	if len(samples) == 0 {
		return nil, false
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i].Cmp(samples[j]) < 0 })
	return new(big.Int).Set(samples[len(samples)/2]), true
}
