package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/assets"
	"github.com/pasiphae00/callisto/internal/rpc"
)

// tokenDiscovery keeps, per (chain, account), the set of ERC-20 tokens the wallet
// has ever received — discovered by scanning Transfer(→account) logs. It drives
// automatic balances: the first scan walks all history, and each subsequent scan
// (triggered on every new head) only covers blocks since the last watermark, so a
// token received live shows up on the next block. Newly discovered tokens trigger
// a balance refresh so they appear without the user pressing anything.
//
// The set is persisted (assets.Cache) and hydrated lazily per key on first use, so
// across launches only blocks since the stored watermark are re-scanned — no full
// history walk each start. The in-memory maps are the live working copy; the DB is
// the durable mirror.
type tokenDiscovery struct {
	app   *App
	cache *tokenCache // nil when there is no store (tests)

	mu         sync.Mutex
	found      map[string]map[common.Address]bool // key → discovered token set
	hidden     map[string]map[common.Address]bool // key → user-hidden ("spam") set
	watermark  map[string]uint64                  // key → last block scanned
	inProgress map[string]bool                    // key → a scan is running
	hydrated   map[string]bool                    // key → loaded from the cache
}

func newTokenDiscovery(a *App) *tokenDiscovery {
	d := &tokenDiscovery{
		app:        a,
		found:      map[string]map[common.Address]bool{},
		hidden:     map[string]map[common.Address]bool{},
		watermark:  map[string]uint64{},
		inProgress: map[string]bool{},
		hydrated:   map[string]bool{},
	}
	if a.store != nil {
		d.cache = newTokenCache(a.store)
	}
	return d
}

// hydrate loads the persisted token set + watermark for key on first use. The
// caller must hold d.mu. It marks the key hydrated unconditionally so a read error
// isn't retried every call; a later launch retries from a clean process.
func (d *tokenDiscovery) hydrate(key string, chainID uint64, account common.Address) {
	if d.hydrated[key] || d.cache == nil {
		return
	}
	d.hydrated[key] = true
	if toks, err := d.cache.list(chainID, account); err == nil && len(toks) > 0 {
		set := make(map[common.Address]bool, len(toks))
		for _, t := range toks {
			set[t] = true
		}
		d.found[key] = set
	}
	if hid, err := d.cache.hiddenList(chainID, account); err == nil && len(hid) > 0 {
		set := make(map[common.Address]bool, len(hid))
		for _, t := range hid {
			set[t] = true
		}
		d.hidden[key] = set
	}
	if wm, ok, err := d.cache.watermark(chainID, account); err == nil && ok {
		d.watermark[key] = wm
	}
}

// isHidden reports whether the user has hidden token for (chain, account).
func (d *tokenDiscovery) isHidden(chainID uint64, account, token common.Address) bool {
	key := discKey(chainID, account)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hydrate(key, chainID, account)
	return d.hidden[key][token]
}

// setHidden hides or unhides token for (chain, account), persisting the decision.
func (d *tokenDiscovery) setHidden(chainID uint64, account, token common.Address, hide bool) {
	key := discKey(chainID, account)
	d.mu.Lock()
	d.hydrate(key, chainID, account)
	if hide {
		if d.hidden[key] == nil {
			d.hidden[key] = map[common.Address]bool{}
		}
		d.hidden[key][token] = true
	} else {
		delete(d.hidden[key], token)
	}
	cache := d.cache
	d.mu.Unlock()

	if cache != nil {
		if hide {
			_ = cache.hide(chainID, account, token)
		} else {
			_ = cache.unhide(chainID, account, token)
		}
	}
}

// hiddenTokens returns the user-hidden tokens for (chain, account).
func (d *tokenDiscovery) hiddenTokens(chainID uint64, account common.Address) []common.Address {
	key := discKey(chainID, account)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hydrate(key, chainID, account)
	set := d.hidden[key]
	out := make([]common.Address, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out
}

func discKey(chainID uint64, account common.Address) string {
	return fmt.Sprintf("%d/%s", chainID, account.Hex())
}

// tokens returns the discovered, non-hidden token addresses for (chain, account),
// hydrating from the cache on first use so persisted tokens are available
// immediately. Hidden ("spam") tokens are excluded so their balances aren't even
// fetched each block.
func (d *tokenDiscovery) tokens(chainID uint64, account common.Address) []common.Address {
	key := discKey(chainID, account)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hydrate(key, chainID, account)
	set := d.found[key]
	hidden := d.hidden[key]
	out := make([]common.Address, 0, len(set))
	for t := range set {
		if !hidden[t] {
			out = append(out, t)
		}
	}
	return out
}

// ensure kicks off (or advances) discovery for (chain, account) in the background:
// a full scan the first time, then incremental scans from the watermark. It is a
// no-op while a scan for that key is already running, so the per-head calls don't
// stack up. When new tokens are found it refreshes the balance panes.
func (d *tokenDiscovery) ensure(chainID uint64, account common.Address, client rpc.Client) {
	key := discKey(chainID, account)
	d.mu.Lock()
	d.hydrate(key, chainID, account)
	if d.inProgress[key] {
		d.mu.Unlock()
		return
	}
	from := uint64(0)
	if wm, ok := d.watermark[key]; ok {
		from = wm + 1
	}
	d.inProgress[key] = true
	d.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		tokens, head, err := assets.DiscoverTokens(ctx, client, account, from)

		d.mu.Lock()
		d.inProgress[key] = false
		if err != nil {
			// Endpoint can't serve logs (relay-only) or the scan failed; leave the
			// watermark as-is so a later connection to a log-capable node retries.
			d.mu.Unlock()
			return
		}
		if d.found[key] == nil {
			d.found[key] = map[common.Address]bool{}
		}
		var added []common.Address
		for _, t := range tokens {
			if !d.found[key][t] {
				d.found[key][t] = true
				added = append(added, t)
			}
		}
		d.watermark[key] = head
		cache := d.cache
		d.mu.Unlock()

		// Persist the new tokens + advanced watermark (outside the lock). Called
		// even when nothing new was found so the watermark still advances and those
		// blocks aren't re-scanned next time.
		if cache != nil {
			_ = cache.add(chainID, account, added, head)
		}
		if len(added) > 0 {
			fyne.Do(d.app.refreshAssets)
		}
	}()
}

// knownTokens merges the user's explicitly-added tokens with the auto-discovered
// (non-hidden) set for (chain, account). The assets Service deduplicates against
// the curated list, so passing both is safe.
func (a *App) knownTokens(chainID uint64, account common.Address) []common.Address {
	out := a.cfg.TokensForChain(chainID)
	if a.disc != nil {
		out = append(out, a.disc.tokens(chainID, account)...)
	}
	return out
}

// displayAssets prepares a loaded asset list for display: it drops any tokens the
// user has hidden (this also catches curated tokens, which bypass knownTokens) and
// sorts the result into a stable order (native first, then by symbol).
func (a *App) displayAssets(chainID uint64, account common.Address, list []assets.Asset) []assets.Asset {
	out := make([]assets.Asset, 0, len(list))
	for _, as := range list {
		if as.Kind == assets.Token && a.disc != nil && a.disc.isHidden(chainID, account, as.Contract) {
			continue
		}
		out = append(out, as)
	}
	assets.Sort(out)
	return out
}

// assetService returns an assets.Service cached per (chain, client) so token
// metadata (immutable) is fetched once and reused across the per-head balance
// reloads instead of re-fetched every block. It is rebuilt when the chain or the
// underlying client changes (e.g. after a reconnect/failover).
func (a *App) assetService(chainID uint64, client rpc.Client) *assets.Service {
	a.svcMu.Lock()
	defer a.svcMu.Unlock()
	key := fmt.Sprintf("%d-%p", chainID, client)
	if a.svc == nil || a.svcKey != key {
		a.svc = assets.NewService(client, chainID)
		a.svcKey = key
	}
	return a.svc
}
