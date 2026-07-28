// Package sim implements pre-sign transaction simulation against current chain
// state, for both EOA and Safe transactions (see docs/transaction-simulation.md
// for the full design). Two features from one simulation call: a universal
// revert warning (works on any RPC, eth_call) and, on a capable endpoint, an
// asset-change preview (actual ETH/token/approval deltas the tx would produce
// right now). No third-party simulation service — everything runs through the
// user's own configured RPC, per PRINCIPLES.md.
//
// This package is RPC-only, no UI dependencies, mirroring internal/tx and
// internal/assets.
package sim

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// Tier is the best simulation capability available on a connection, in
// increasing order of fidelity. A higher tier is a strict superset of what a
// lower tier can do.
type Tier int

const (
	// TierCallOnly supports only eth_call: revert/success and return data, no
	// logs, no state diff. Universal.
	TierCallOnly Tier = iota
	// TierSimulate supports eth_simulateV1 (traceTransfers, per-call logs) --
	// enough to decode ETH/token/approval deltas from events.
	TierSimulate
	// TierDebug supports debug_traceCall (callTracer + prestateTracer
	// diffMode) -- full call tree and state diff, needs the debug namespace.
	TierDebug
)

// String names the RPC method a tier represents, for display in the UI's
// "revert check only — connect an X endpoint for full asset changes" message.
func (t Tier) String() string {
	switch t {
	case TierDebug:
		return "debug_traceCall"
	case TierSimulate:
		return "eth_simulateV1"
	default:
		return "eth_call"
	}
}

// Request describes a single call to simulate, from the perspective of the
// account whose asset deltas we want (an EOA, or a Safe).
type Request struct {
	From  common.Address
	To    common.Address
	Value *big.Int // nil treated as 0
	Data  []byte
}

// Status is the outcome of a simulation.
type Status int

const (
	StatusUnknown Status = iota
	// StatusOK means the call succeeded (would not revert).
	StatusOK
	// StatusRevert means the call would revert; RevertReason may be set.
	StatusRevert
	// StatusUnavailable means simulation could not be performed at all (e.g.
	// the RPC rejected even a plain eth_call, or the request was malformed).
	StatusUnavailable
)

// TokenDelta is a signed ERC-20 balance change for the account of interest.
type TokenDelta struct {
	Token    common.Address
	Symbol   string
	Decimals uint8
	Delta    *big.Int // signed: negative = sent, positive = received
}

// NFTDelta is a single ERC-721/1155 transfer in or out (decoded in a later
// phase — see docs/transaction-simulation.md P3c; the type is defined now so
// Result's shape is stable).
type NFTDelta struct {
	Token   common.Address
	TokenID *big.Int
	In      bool
	Amount  *big.Int // 1155 amount; 1 for a 721
}

// ApprovalChange is a newly granted (or changed) ERC-20/Permit2 allowance seen
// in the simulated logs.
type ApprovalChange struct {
	Token     common.Address
	Symbol    string
	Spender   common.Address
	Unlimited bool
	Amount    *big.Int // meaningless when Unlimited
}

// Result is the outcome of simulating one transaction for the account of
// interest.
type Result struct {
	Status       Status
	RevertReason string
	GasUsed      uint64
	ETHDelta     *big.Int // signed; nil if not determined at this tier
	Tokens       []TokenDelta
	NFTs         []NFTDelta
	Approvals    []ApprovalChange
	Tier         Tier
	// Note explains a limitation of this result, e.g. "asset preview needs an
	// eth_simulateV1/archive endpoint — revert check only" or (Safe
	// DelegateCall) "full asset diff needs signature-bypass simulation, not
	// yet available — revert check only".
	Note string
}
