package sim

import (
	"context"
	"sync"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/assets"
	"github.com/pasiphae00/callisto/internal/textsafe"
)

// tokenInfo is the display metadata a delta needs.
type tokenInfo struct {
	Symbol   string
	Decimals uint8
}

// Enrich fills in the symbol and decimals of every token referenced by res, so
// a preview can render "−250.00 USDC" rather than a raw base-unit integer
// against a bare address.
//
// Metadata is read straight from each token contract and cached for the life of
// the Simulator, since re-simulating the same transaction (or simulating
// several transactions touching the same tokens) is the common case.
//
// Symbols are untrusted on-chain strings -- a token can name itself anything,
// including text engineered to impersonate another asset -- so they go through
// textsafe.Display before they reach the UI. A token whose metadata cannot be
// read keeps an empty symbol; callers fall back to showing its address.
func (s *Simulator) Enrich(ctx context.Context, res *Result) {
	if res == nil || s.client == nil {
		return
	}
	for i := range res.Tokens {
		info := s.tokenInfo(ctx, res.Tokens[i].Token)
		res.Tokens[i].Symbol, res.Tokens[i].Decimals = info.Symbol, info.Decimals
	}
	for i := range res.Approvals {
		info := s.tokenInfo(ctx, res.Approvals[i].Token)
		res.Approvals[i].Symbol, res.Approvals[i].Decimals = info.Symbol, info.Decimals
	}
}

func (s *Simulator) tokenInfo(ctx context.Context, token common.Address) tokenInfo {
	s.metaMu.Lock()
	if s.meta == nil {
		s.meta = make(map[common.Address]tokenInfo)
	}
	cached, ok := s.meta[token]
	s.metaMu.Unlock()
	if ok {
		return cached
	}

	var info tokenInfo
	if m, err := assets.Metadata(ctx, s.client, token); err == nil {
		info = tokenInfo{Symbol: textsafe.Display(m.Symbol), Decimals: m.Decimals}
	}

	s.metaMu.Lock()
	s.meta[token] = info
	s.metaMu.Unlock()
	return info
}

// metaCache is embedded in Simulator; declared here to keep the cache's fields
// next to the code that uses them.
type metaCache struct {
	metaMu sync.Mutex
	meta   map[common.Address]tokenInfo
}
