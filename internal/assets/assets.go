package assets

import (
	"context"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/chain"
	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// Kind distinguishes the native currency from ERC-20 tokens.
type Kind int

const (
	Native Kind = iota
	Token
)

// Asset is one holding of an account: the native currency or an ERC-20 token,
// with balance in base units and the metadata needed to display it.
type Asset struct {
	Kind     Kind
	Contract common.Address // zero for Native
	Name     string
	Symbol   string
	Decimals uint8
	Balance  *big.Int
	LogoURI  string
}

// HumanBalance returns the balance formatted per the asset's decimals.
func (a Asset) HumanBalance() string {
	return FormatUnits(a.Balance, a.Decimals)
}

// Service loads balances for an account on a specific chain. Token metadata is
// cached (it is immutable) so repeated refreshes only re-read balances.
type Service struct {
	client  rpc.Client
	chainID uint64

	mu    sync.Mutex
	cache map[common.Address]erc20Metadata
}

// NewService builds a Service for the given client/chain.
func NewService(client rpc.Client, chainID uint64) *Service {
	return &Service{client: client, chainID: chainID, cache: map[common.Address]erc20Metadata{}}
}

// Load returns the account's assets: the native currency first (always), then one
// entry per token in the curated list for this chain plus any user-supplied
// tokens (deduplicated). A token whose balance or metadata read fails is skipped
// rather than failing the whole load, so one bad contract can't hide the rest.
func (s *Service) Load(ctx context.Context, account common.Address, userTokens []common.Address) ([]Asset, error) {
	info, _ := chain.Lookup(s.chainID)

	// Native asset first (hard requirement: ether/native shown first).
	nativeBal, err := s.client.BalanceAt(ctx, account, nil)
	if err != nil {
		return nil, err
	}
	out := []Asset{{
		Kind:     Native,
		Name:     info.Native.Name,
		Symbol:   info.Native.Symbol,
		Decimals: info.Native.Decimals,
		Balance:  nativeBal,
	}}

	for _, token := range s.tokenSet(userTokens) {
		asset, ok := s.loadToken(ctx, account, token)
		if ok {
			out = append(out, asset)
		}
	}
	return out, nil
}

// tokenSet merges curated + user tokens for this chain, deduplicated, preserving
// curated-then-user order.
func (s *Service) tokenSet(userTokens []common.Address) []common.Address {
	seen := map[common.Address]bool{}
	var set []common.Address
	add := func(a common.Address) {
		if !seen[a] {
			seen[a] = true
			set = append(set, a)
		}
	}
	for _, c := range curatedFor(s.chainID) {
		add(common.HexToAddress(c.Address))
	}
	for _, t := range userTokens {
		add(t)
	}
	return set
}

// loadToken reads one token's metadata (cached) and balance, returning ok=false
// if either read fails.
func (s *Service) loadToken(ctx context.Context, account, token common.Address) (Asset, bool) {
	meta, ok := s.metadata(ctx, token)
	if !ok {
		return Asset{}, false
	}
	bal, err := BalanceOf(ctx, s.client, token, account)
	if err != nil {
		return Asset{}, false
	}
	return Asset{
		Kind:     Token,
		Contract: token,
		Name:     meta.Name,
		Symbol:   meta.Symbol,
		Decimals: meta.Decimals,
		Balance:  bal,
		LogoURI:  logoFor(s.chainID, token),
	}, true
}

// metadata returns cached token metadata, fetching and caching on first use.
func (s *Service) metadata(ctx context.Context, token common.Address) (erc20Metadata, bool) {
	s.mu.Lock()
	if m, ok := s.cache[token]; ok {
		s.mu.Unlock()
		return m, true
	}
	s.mu.Unlock()

	m, err := Metadata(ctx, s.client, token)
	if err != nil {
		return erc20Metadata{}, false
	}
	s.mu.Lock()
	s.cache[token] = m
	s.mu.Unlock()
	return m, true
}
