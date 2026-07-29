package sim

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// changes is the net effect of a simulated transaction on one account, built by
// replaying the execution's logs.
type changes struct {
	ETH       *big.Int
	Tokens    []TokenDelta
	Approvals []ApprovalChange
}

// aggregate nets out the logs of a successful simulation into the balance and
// allowance changes seen by `account`.
//
// Netting matters: a swap routed through several pools emits a chain of
// Transfer logs, most of which are between contracts we don't care about, and
// the ones that do touch the account may both debit and credit the same token.
// Only the sum is meaningful to a human, so per-token deltas are summed and
// anything that nets to zero is dropped.
//
// Approvals are the opposite: ERC-20 approve *sets* an allowance rather than
// adding to it, so for a given (token, spender) the last log wins.
//
// Token order follows first appearance in the logs, which keeps the rendered
// preview stable across re-simulations of the same transaction.
func aggregate(logs []*types.Log, account common.Address) changes {
	out := changes{ETH: new(big.Int)}

	tokenIndex := make(map[common.Address]int)
	type approvalKey struct{ token, spender common.Address }
	approvalIndex := make(map[approvalKey]int)

	for _, lg := range logs {
		if lg == nil {
			continue
		}

		// Native currency, reported by eth_simulateV1's traceTransfers as a
		// Transfer log from a sentinel address rather than a real token.
		if isNativeLogAddress(lg.Address) {
			if _, delta, ok := decodeTransfer(*lg, account); ok {
				out.ETH.Add(out.ETH, delta)
			}
			continue
		}

		if token, delta, ok := decodeTransfer(*lg, account); ok {
			if i, seen := tokenIndex[token]; seen {
				out.Tokens[i].Delta.Add(out.Tokens[i].Delta, delta)
			} else {
				tokenIndex[token] = len(out.Tokens)
				out.Tokens = append(out.Tokens, TokenDelta{Token: token, Delta: new(big.Int).Set(delta)})
			}
			continue
		}

		if owner, ac, ok := decodeApproval(*lg); ok && owner == account {
			upsertApproval(&out.Approvals, approvalIndex, approvalKey{ac.Token, ac.Spender}, ac)
			continue
		}
		if owner, ac, ok := decodePermit2Approval(*lg); ok && owner == account {
			upsertApproval(&out.Approvals, approvalIndex, approvalKey{ac.Token, ac.Spender}, ac)
		}
	}

	out.Tokens = dropZeroDeltas(out.Tokens)
	return out
}

func upsertApproval[K comparable](dst *[]ApprovalChange, index map[K]int, key K, ac ApprovalChange) {
	if i, seen := index[key]; seen {
		(*dst)[i] = ac
		return
	}
	index[key] = len(*dst)
	*dst = append(*dst, ac)
}

func dropZeroDeltas(in []TokenDelta) []TokenDelta {
	out := in[:0]
	for _, d := range in {
		if d.Delta.Sign() != 0 {
			out = append(out, d)
		}
	}
	return out
}

// applyETHMoves folds the native transfers recovered from a callTracer trace
// into the account's ETH delta. debug_traceCall has no traceTransfers
// equivalent, so ETH movement is read off the call tree's value fields instead
// of from pseudo-logs.
func applyETHMoves(c *changes, moves []ethMove, account common.Address) {
	for _, m := range moves {
		// Not a switch: a contract paying itself matches both arms and must
		// net to zero, not to a debit.
		if m.From == account {
			c.ETH.Sub(c.ETH, m.Value)
		}
		if m.To == account {
			c.ETH.Add(c.ETH, m.Value)
		}
	}
}
