package assets

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// transferTopic is keccak256("Transfer(address,address,uint256)"), the topic0 of
// both ERC-20 and ERC-721 Transfer events. ERC-20 indexes only from+to (3 topics,
// value in data); ERC-721 also indexes tokenId (4 topics), which we filter out.
var transferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// DiscoverTokens finds the ERC-20 contracts that have ever transferred a token to
// account, by scanning Transfer(→account) logs over [fromBlock, head]. It returns
// the unique candidate token addresses and the head block scanned (so callers can
// watermark and re-scan only new blocks next time). The candidates still need a
// metadata/balance read (via Service.Load) to confirm they are live ERC-20s with a
// non-dust balance — a token sold down to zero is discovered but shown as dust.
//
// This is the mechanism behind automatic balances: a wallet holds a token only if
// it received it, so incoming Transfer logs enumerate the full holding set without
// any hardcoded token list. It requires an eth_getLogs-capable endpoint with enough
// history (the default archive node); on a relay-only endpoint it returns an error
// and the caller falls back to the curated + user token list.
func DiscoverTokens(ctx context.Context, client rpc.Client, account common.Address, fromBlock uint64) ([]common.Address, uint64, error) {
	head, err := client.BlockNumber(ctx)
	if err != nil {
		return nil, 0, err
	}
	if fromBlock > head {
		return nil, head, nil // nothing new since the last scan
	}
	accountTopic := common.BytesToHash(account.Bytes())
	// topic0 = Transfer, topic1 (from) = any, topic2 (to) = account.
	topics := [][]common.Hash{{transferTopic}, {}, {accountTopic}}
	logs, err := scanLogs(ctx, client, topics, fromBlock, head)
	if err != nil {
		return nil, 0, err
	}
	seen := map[common.Address]bool{}
	var tokens []common.Address
	for _, lg := range logs {
		// ERC-20 Transfer has exactly 3 topics; ERC-721 (tokenId indexed) has 4.
		// Skipping 4-topic logs keeps NFTs out of the fungible-balance list.
		if len(lg.Topics) != 3 {
			continue
		}
		if !seen[lg.Address] {
			seen[lg.Address] = true
			tokens = append(tokens, lg.Address)
		}
	}
	return tokens, head, nil
}

// scanLogs runs eth_getLogs over [from, head] in windows, shrinking the window when
// the node rejects the block range (nodes cap the span per query). It mirrors the
// approval scanner's adaptive strategy: start wide (cheap for topic-filtered scans)
// and back off geometrically, honoring any limit the server states in its error.
func scanLogs(ctx context.Context, client rpc.Client, topics [][]common.Hash, from, head uint64) ([]types.Log, error) {
	const (
		scanWindow = 100_000
		minWindow  = 64
	)
	window := uint64(scanWindow)
	var out []types.Log
	start := from
	for start <= head {
		end := start + window - 1
		if end > head {
			end = head
		}
		q := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
			Topics:    topics,
		}
		logs, err := client.FilterLogs(ctx, q)
		if err != nil {
			next := window / 4
			if lim := parseRangeLimit(err.Error()); lim > 0 && lim < next {
				next = lim
			}
			if next < minWindow {
				return nil, fmt.Errorf("scan logs: %w", err)
			}
			window = next
			continue
		}
		out = append(out, logs...)
		start = end + 1
	}
	return out, nil
}

// parseRangeLimit returns the last integer in a getLogs error message (for
// "block range exceeds server limit … N" style errors, the node's per-query cap).
// Returns 0 if none is found.
func parseRangeLimit(msg string) uint64 {
	var best uint64
	for i := 0; i < len(msg); {
		if msg[i] < '0' || msg[i] > '9' {
			i++
			continue
		}
		var n uint64
		for i < len(msg) && msg[i] >= '0' && msg[i] <= '9' {
			n = n*10 + uint64(msg[i]-'0')
			i++
		}
		best = n
	}
	return best
}
