// Package approvals discovers and helps revoke outstanding ERC-20 token approvals
// for an account — both direct allowances (approve(spender, amount) on the token)
// and Uniswap Permit2 inner allowances. Discovery is done entirely against the
// active RPC by scanning Approval event logs (there is no "list approvals" call),
// bounded below by the account's first active block so it never scans from genesis.
// See permit2.go for the Permit2 layer and labels.go for spender naming.
package approvals

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// Layer distinguishes a direct token allowance from a Permit2 inner allowance.
type Layer int

const (
	LayerDirect Layer = iota
	LayerPermit2
)

func (l Layer) String() string {
	if l == LayerPermit2 {
		return "Permit2"
	}
	return "ERC-20"
}

// Approval is one outstanding allowance held by an owner.
type Approval struct {
	Layer         Layer
	Token         common.Address
	TokenSymbol   string // "" if metadata unavailable
	TokenDecimals uint8
	Spender       common.Address
	SpenderLabel  string   // "" if the spender is not a known contract
	Amount        *big.Int // current allowance (base units)
	Unlimited     bool     // amount is effectively infinite
	Expiration    int64    // Permit2 expiry (unix); 0 for direct / no expiry
}

// erc20 approve/allowance ABI (view + revoke). Kept local so this package is
// self-contained.
var erc20ABI = mustABI(`[
  {"name":"allowance","type":"function","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
  {"name":"approve","type":"function","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
]`)

// approvalEventSig is keccak("Approval(address,address,uint256)") — topic0 of the
// standard ERC-20 Approval event.
var approvalEventSig = crypto.Keccak256Hash([]byte("Approval(address,address,uint256)"))

// unlimitedThreshold: a direct allowance at or above 2^255 is treated as
// "unlimited" (covers MaxUint256 and the near-max sentinels wallets commonly set).
var unlimitedThreshold = new(big.Int).Lsh(big.NewInt(1), 255)

// Scanner discovers approvals against one RPC connection / chain.
type Scanner struct {
	client  rpc.Client
	chainID uint64
	meta    metaResolver
}

// metaResolver resolves a token's symbol/decimals; defaults to on-chain reads.
type metaResolver func(ctx context.Context, token common.Address) (symbol string, decimals uint8, ok bool)

// NewScanner builds a Scanner for the given client and chain.
func NewScanner(client rpc.Client, chainID uint64) *Scanner {
	return &Scanner{
		client:  client,
		chainID: chainID,
		meta: func(ctx context.Context, token common.Address) (string, uint8, bool) {
			m, err := assets.Metadata(ctx, client, token)
			if err != nil {
				return "", 0, false
			}
			return m.Symbol, m.Decimals, true
		},
	}
}

// Progress reports scan advancement to the UI: a human-readable stage and an
// overall completion fraction in [0,1] (from the wallet's first block to head,
// across both the direct and Permit2 passes).
type Progress struct {
	Stage    string
	Fraction float64
}

// Scan returns every outstanding approval (direct + Permit2) for owner, plus the
// head block scanned (the new watermark). progress (may be nil) receives stage +
// fraction updates.
func (s *Scanner) Scan(ctx context.Context, owner common.Address, progress func(Progress)) ([]Approval, uint64, error) {
	emitStage := func(stage string, frac float64) {
		if progress != nil {
			progress(Progress{Stage: stage, Fraction: frac})
		}
	}
	emitStage("Finding first activity…", 0)
	head, err := s.client.BlockNumber(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("head: %w", err)
	}
	from := firstActiveBlock(ctx, s.client, owner, head)

	// The two passes (direct, then Permit2) each cover [from, head]; report a
	// single fraction across both, throttled so we don't flood the UI thread.
	var span uint64 = 1
	if head >= from {
		span = head - from + 1
	}
	total := float64(2 * span)
	lastFrac := -1.0
	emit := func(stage string, base, cur uint64) {
		if progress == nil {
			return
		}
		done := base
		if cur >= from {
			done += cur - from + 1
		}
		frac := float64(done) / total
		if frac > 1 {
			frac = 1
		}
		if frac-lastFrac < 0.004 {
			return // throttle to meaningful advances
		}
		lastFrac = frac
		progress(Progress{Stage: stage, Fraction: frac})
	}

	direct, err := s.scanDirect(ctx, owner, from, head, func(cur uint64) { emit("Scanning token approvals…", 0, cur) })
	if err != nil {
		return nil, 0, err
	}
	permit2, err := s.scanPermit2(ctx, owner, from, head, func(cur uint64) { emit("Scanning Permit2 approvals…", span, cur) })
	if err != nil {
		return nil, 0, err
	}
	emitStage("Done", 1)
	return append(direct, permit2...), head, nil
}

// pair identifies one (layer, token, spender) approval slot.
type pair struct {
	layer   Layer
	token   common.Address
	spender common.Address
}

func (p pair) key() string { return string(rune(p.layer)) + p.token.Hex() + p.spender.Hex() }

// Refresh performs an incremental update: it scans only the blocks after
// sinceBlock for approval changes, unions those (token, spender) pairs with the
// already-known cached ones, and re-reads every pair's live allowance (which also
// catches finite allowances spent down via transferFrom, and revocations made
// elsewhere). It returns the current outstanding set and the new watermark (head).
func (s *Scanner) Refresh(ctx context.Context, owner common.Address, sinceBlock uint64, cached []Approval, progress func(Progress)) ([]Approval, uint64, error) {
	emit := func(stage string, frac float64) {
		if progress != nil {
			progress(Progress{Stage: stage, Fraction: frac})
		}
	}
	emit("Checking for new approvals…", 0)
	head, err := s.client.BlockNumber(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("head: %w", err)
	}

	// Seed the working set with the known pairs, then add any that changed since
	// the watermark.
	set := map[string]pair{}
	for _, a := range cached {
		p := pair{a.Layer, a.Token, a.Spender}
		set[p.key()] = p
	}

	if from := sinceBlock + 1; from <= head {
		ownerTopic := common.BytesToHash(owner.Bytes())
		span := head - from + 1
		onBlk := func(cur uint64) {
			if span > 0 {
				emit("Scanning new blocks…", 0.5*float64(cur-from+1)/float64(span))
			}
		}
		directLogs, err := s.getLogs(ctx, nil, [][]common.Hash{{approvalEventSig}, {ownerTopic}}, from, head, onBlk)
		if err != nil {
			return nil, 0, err
		}
		for _, lg := range directLogs {
			if len(lg.Topics) >= 3 {
				p := pair{LayerDirect, lg.Address, common.BytesToAddress(lg.Topics[2].Bytes())}
				set[p.key()] = p
			}
		}
		p2Logs, err := s.getLogs(ctx, []common.Address{permit2Address}, [][]common.Hash{{permit2ApprovalSig, permit2PermitSig}, {ownerTopic}}, from, head, nil)
		if err != nil {
			return nil, 0, err
		}
		for _, lg := range p2Logs {
			if len(lg.Topics) >= 4 {
				p := pair{LayerPermit2, common.BytesToAddress(lg.Topics[2].Bytes()), common.BytesToAddress(lg.Topics[3].Bytes())}
				set[p.key()] = p
			}
		}
	}

	// Re-read the live allowance for every candidate pair.
	pairs := make([]pair, 0, len(set))
	for _, p := range set {
		pairs = append(pairs, p)
	}
	out := make([]Approval, 0, len(pairs))
	for i, p := range pairs {
		emit("Refreshing allowances…", 0.5+0.5*float64(i)/float64(len(pairs)+1))
		if a, ok := s.resolve(ctx, owner, p); ok {
			out = append(out, a)
		}
	}
	emit("Done", 1)
	return out, head, nil
}

// resolve reads the live allowance for one pair and returns the Approval if it is
// still outstanding (non-zero, and non-expired for Permit2).
func (s *Scanner) resolve(ctx context.Context, owner common.Address, p pair) (Approval, bool) {
	if p.layer == LayerPermit2 {
		amount, expiration, err := s.permit2Allowance(ctx, owner, p.token, p.spender)
		if err != nil || amount == nil || amount.Sign() == 0 {
			return Approval{}, false
		}
		if expiration != 0 && expiration <= time.Now().Unix() {
			return Approval{}, false
		}
		return s.build(ctx, LayerPermit2, p.token, p.spender, amount, maxUint160, expiration), true
	}
	amount, err := s.allowance(ctx, p.token, owner, p.spender)
	if err != nil || amount == nil || amount.Sign() == 0 {
		return Approval{}, false
	}
	return s.build(ctx, LayerDirect, p.token, p.spender, amount, unlimitedThreshold, 0), true
}

// Watch subscribes to live approval changes for owner (WSS endpoints only) and
// invokes onChange for each event with the re-read Approval, whether it is still
// outstanding, and the block it changed at, until ctx is cancelled or the
// subscription drops. Non-outstanding (revoked/zero) changes carry a zero-amount
// Approval carrying only the identity (layer/token/spender) for removal.
func (s *Scanner) Watch(ctx context.Context, owner common.Address, onChange func(a Approval, outstanding bool, block uint64)) error {
	ownerTopic := common.BytesToHash(owner.Bytes())
	ch := make(chan types.Log, 64)
	subD, err := s.client.SubscribeFilterLogs(ctx,
		ethereum.FilterQuery{Topics: [][]common.Hash{{approvalEventSig}, {ownerTopic}}}, ch)
	if err != nil {
		return err
	}
	subP, err := s.client.SubscribeFilterLogs(ctx,
		ethereum.FilterQuery{Addresses: []common.Address{permit2Address}, Topics: [][]common.Hash{{permit2ApprovalSig, permit2PermitSig}, {ownerTopic}}}, ch)
	if err != nil {
		subD.Unsubscribe()
		return err
	}
	go func() {
		defer subD.Unsubscribe()
		defer subP.Unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case <-subD.Err():
				return
			case <-subP.Err():
				return
			case lg := <-ch:
				p, ok := logToPair(lg)
				if !ok {
					continue
				}
				a, outstanding := s.resolve(ctx, owner, p)
				if !outstanding {
					a = Approval{Layer: p.layer, Token: p.token, Spender: p.spender, Amount: big.NewInt(0)}
				}
				onChange(a, outstanding, lg.BlockNumber)
			}
		}
	}()
	return nil
}

// logToPair maps an Approval/Permit log to its (layer, token, spender) pair.
func logToPair(lg types.Log) (pair, bool) {
	if lg.Address == permit2Address {
		if len(lg.Topics) >= 4 {
			return pair{LayerPermit2, common.BytesToAddress(lg.Topics[2].Bytes()), common.BytesToAddress(lg.Topics[3].Bytes())}, true
		}
		return pair{}, false
	}
	if len(lg.Topics) >= 3 && lg.Topics[0] == approvalEventSig {
		return pair{LayerDirect, lg.Address, common.BytesToAddress(lg.Topics[2].Bytes())}, true
	}
	return pair{}, false
}

// scanDirect finds live direct ERC-20 allowances: Approval logs by owner give the
// (token, spender) pairs ever touched; the current allowance() decides which are
// still outstanding.
func (s *Scanner) scanDirect(ctx context.Context, owner common.Address, from, head uint64, onBlock func(uint64)) ([]Approval, error) {
	ownerTopic := common.BytesToHash(owner.Bytes())
	logs, err := s.getLogs(ctx, nil, [][]common.Hash{{approvalEventSig}, {ownerTopic}}, from, head, onBlock)
	if err != nil {
		return nil, err
	}
	var out []Approval
	seen := map[string]bool{}
	for _, lg := range logs {
		if len(lg.Topics) < 3 {
			continue
		}
		token := lg.Address
		spender := common.BytesToAddress(lg.Topics[2].Bytes())
		key := token.Hex() + spender.Hex()
		if seen[key] {
			continue
		}
		seen[key] = true

		amount, err := s.allowance(ctx, token, owner, spender)
		if err != nil || amount == nil || amount.Sign() == 0 {
			continue
		}
		out = append(out, s.build(ctx, LayerDirect, token, spender, amount, unlimitedThreshold, 0))
	}
	return out, nil
}

// allowance reads token.allowance(owner, spender).
func (s *Scanner) allowance(ctx context.Context, token, owner, spender common.Address) (*big.Int, error) {
	out, err := s.call(ctx, token, erc20ABI, "allowance", owner, spender)
	if err != nil {
		return nil, err
	}
	var v *big.Int
	if err := erc20ABI.UnpackIntoInterface(&v, "allowance", out); err != nil {
		return nil, err
	}
	return v, nil
}

// build fills token metadata + spender label and the unlimited flag.
func (s *Scanner) build(ctx context.Context, layer Layer, token, spender common.Address, amount, unlimitedAt *big.Int, expiration int64) Approval {
	a := Approval{
		Layer:        layer,
		Token:        token,
		Spender:      spender,
		Amount:       amount,
		Unlimited:    amount.Cmp(unlimitedAt) >= 0,
		Expiration:   expiration,
		SpenderLabel: spenderLabel(s.chainID, spender),
	}
	if sym, dec, ok := s.meta(ctx, token); ok {
		a.TokenSymbol, a.TokenDecimals = sym, dec
	}
	return a
}

// getLogs runs an eth_getLogs scan over [from, head] in windows, shrinking the
// window when the RPC rejects a range (nodes commonly cap the block span per
// query). It honors a limit the server states in its error (e.g. "…limit… 1000")
// and otherwise backs off geometrically. onBlock (may be nil) is called with the
// last block scanned after each successful window, for progress reporting. A
// failure that persists down to the minimum window (e.g. an endpoint that doesn't
// serve logs at all) is returned so the UI can tell the user to use a full RPC.
func (s *Scanner) getLogs(ctx context.Context, addrs []common.Address, topics [][]common.Hash, from, head uint64, onBlock func(uint64)) ([]types.Log, error) {
	const minWindow = 64
	// Start at logScanWindow and shrink only if the node rejects the range (it
	// parses the node's stated cap from the error). Approval scans are topic-
	// filtered with tiny result sets, so a wide window is cheap on the node; this
	// default matches a comfortable node getLogs block-range cap (see the node's
	// getLogs range setting, e.g. reth --rpc.max-blocks-per-filter).
	const logScanWindow = 100_000
	window := uint64(logScanWindow)
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
			Addresses: addrs,
			Topics:    topics,
		}
		logs, err := s.client.FilterLogs(ctx, q)
		if err != nil {
			next := window / 4
			if lim := parseBlockRangeLimit(err.Error()); lim > 0 && lim < next {
				next = lim // jump straight to the server's stated cap
			}
			if next < minWindow {
				return nil, fmt.Errorf("scan logs: %w", err)
			}
			window = next
			continue
		}
		out = append(out, logs...)
		start = end + 1
		if onBlock != nil {
			onBlock(end)
		}
	}
	return out, nil
}

// parseBlockRangeLimit returns the last integer in a getLogs error message, which
// for "block range exceeds server limit … N" style errors is the node's per-query
// block cap. Returns 0 if none is found.
func parseBlockRangeLimit(msg string) uint64 {
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

// call performs a read-only contract call and returns the raw output.
func (s *Scanner) call(ctx context.Context, to common.Address, a abi.ABI, method string, args ...interface{}) ([]byte, error) {
	data, err := a.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	return s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
}

// firstActiveBlock binary-searches for the first block where owner's nonce is > 0
// (its first outbound tx). Approvals are always sent by the owner, so this is a
// safe lower bound for the log scan. Returns head if the account never sent a tx,
// or 0 if a nonce read fails (fall back to a full scan).
func firstActiveBlock(ctx context.Context, client rpc.Client, owner common.Address, head uint64) uint64 {
	n, err := client.NonceAt(ctx, owner, new(big.Int).SetUint64(head))
	if err != nil {
		return 0
	}
	if n == 0 {
		return head // never sent a transaction ⇒ no approvals to find
	}
	lo, hi := uint64(0), head
	for lo < hi {
		mid := lo + (hi-lo)/2
		nm, err := client.NonceAt(ctx, owner, new(big.Int).SetUint64(mid))
		if err != nil {
			return 0
		}
		if nm == 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// RevokeCall returns the (to, data) for a transaction that revokes a. For a direct
// allowance this is approve(spender, 0) on the token; for Permit2 it is
// lockdown([(token, spender)]) on the Permit2 contract.
func (a Approval) RevokeCall() (to common.Address, data []byte, err error) {
	if a.Layer == LayerPermit2 {
		data, err = permit2LockdownCalldata(a.Token, a.Spender)
		return permit2Address, data, err
	}
	data, err = erc20ABI.Pack("approve", a.Spender, big.NewInt(0))
	return a.Token, data, err
}

func mustABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("approvals: bad built-in ABI: " + err.Error())
	}
	return a
}
