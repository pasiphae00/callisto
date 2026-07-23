package ui

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/store"
)

func TestTokenCacheRoundTrip(t *testing.T) {
	s, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	defer s.Close()
	c := newTokenCache(s)

	account := common.HexToAddress("0x1111111111111111111111111111111111111111")
	usdc := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	dai := common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")

	// No watermark before any scan.
	if _, ok, err := c.watermark(1, account); err != nil || ok {
		t.Fatalf("watermark before scan: ok=%v err=%v, want ok=false", ok, err)
	}

	// First scan: two tokens, watermark 1000.
	if err := c.add(1, account, []common.Address{usdc, dai}, 1000); err != nil {
		t.Fatalf("add: %v", err)
	}
	toks, err := c.list(1, account)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(toks) != 2 {
		t.Fatalf("got %d tokens, want 2", len(toks))
	}
	if wm, ok, _ := c.watermark(1, account); !ok || wm != 1000 {
		t.Fatalf("watermark = %d ok=%v, want 1000 true", wm, ok)
	}

	// Incremental scan: one already-known token + advanced watermark. No dupes.
	if err := c.add(1, account, []common.Address{usdc}, 2000); err != nil {
		t.Fatalf("add incremental: %v", err)
	}
	toks, _ = c.list(1, account)
	if len(toks) != 2 {
		t.Errorf("after re-add got %d tokens, want 2 (no dupes)", len(toks))
	}
	if wm, _, _ := c.watermark(1, account); wm != 2000 {
		t.Errorf("watermark = %d, want 2000", wm)
	}

	// Empty scan still advances the watermark.
	if err := c.add(1, account, nil, 3000); err != nil {
		t.Fatalf("add empty: %v", err)
	}
	if wm, _, _ := c.watermark(1, account); wm != 3000 {
		t.Errorf("watermark after empty add = %d, want 3000", wm)
	}

	// A different account is isolated.
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")
	if toks, _ := c.list(1, other); len(toks) != 0 {
		t.Errorf("other account has %d tokens, want 0", len(toks))
	}
}

func TestTokenCacheHidden(t *testing.T) {
	s, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	defer s.Close()
	c := newTokenCache(s)

	account := common.HexToAddress("0x1111111111111111111111111111111111111111")
	spam := common.HexToAddress("0xdeAD00000000000000000000000000000000BeEf")

	if hid, _ := c.hiddenList(1, account); len(hid) != 0 {
		t.Fatalf("hidden before any hide = %d, want 0", len(hid))
	}
	if err := c.hide(1, account, spam); err != nil {
		t.Fatalf("hide: %v", err)
	}
	// Hiding twice is idempotent (no dupes, no error).
	if err := c.hide(1, account, spam); err != nil {
		t.Fatalf("hide again: %v", err)
	}
	hid, _ := c.hiddenList(1, account)
	if len(hid) != 1 || hid[0] != spam {
		t.Fatalf("hiddenList = %v, want [%s]", hid, spam)
	}
	if err := c.unhide(1, account, spam); err != nil {
		t.Fatalf("unhide: %v", err)
	}
	if hid, _ := c.hiddenList(1, account); len(hid) != 0 {
		t.Errorf("hidden after unhide = %d, want 0", len(hid))
	}
}
