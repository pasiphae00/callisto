package approvals

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/store"
)

func TestCacheRoundTrip(t *testing.T) {
	st, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := NewCache(st)

	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	spender := common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D") // Uniswap V2

	a := Approval{Layer: LayerDirect, Token: token, Spender: spender, TokenSymbol: "USDC", TokenDecimals: 6, Amount: big.NewInt(1000)}
	if err := c.Save(1, owner, []Approval{a}, 500); err != nil {
		t.Fatal(err)
	}

	got, err := c.List(1, owner)
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %v (err %v)", got, err)
	}
	if got[0].Amount.Cmp(big.NewInt(1000)) != 0 || got[0].TokenSymbol != "USDC" {
		t.Errorf("bad row: %+v", got[0])
	}
	if got[0].SpenderLabel != "Uniswap V2 Router" { // re-derived on load
		t.Errorf("spender label = %q", got[0].SpenderLabel)
	}
	if wm, ok, _ := c.Watermark(1, owner); !ok || wm != 500 {
		t.Errorf("watermark = %d (ok %v)", wm, ok)
	}

	// Live upsert then delete.
	a.Amount = big.NewInt(2000)
	if err := c.Upsert(1, owner, a, 600); err != nil {
		t.Fatal(err)
	}
	if got, _ = c.List(1, owner); len(got) != 1 || got[0].Amount.Cmp(big.NewInt(2000)) != 0 {
		t.Errorf("upsert failed: %+v", got)
	}
	if err := c.Delete(1, owner, a); err != nil {
		t.Fatal(err)
	}
	if got, _ = c.List(1, owner); len(got) != 0 {
		t.Errorf("delete failed: %+v", got)
	}

	// Unlimited + Permit2 + expiration round-trip; Save replaces prior rows.
	u := Approval{Layer: LayerPermit2, Token: token, Spender: spender, Amount: maxUint160, Unlimited: true, Expiration: 123}
	if err := c.Save(1, owner, []Approval{u}, 700); err != nil {
		t.Fatal(err)
	}
	got, _ = c.List(1, owner)
	if len(got) != 1 || !got[0].Unlimited || got[0].Expiration != 123 || got[0].Layer != LayerPermit2 {
		t.Errorf("permit2 round-trip: %+v", got)
	}
	if wm, _, _ := c.Watermark(1, owner); wm != 700 {
		t.Errorf("watermark not advanced: %d", wm)
	}
}
