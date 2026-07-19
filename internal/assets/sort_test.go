package assets

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestSortNativeFirstThenSymbol(t *testing.T) {
	list := []Asset{
		{Kind: Token, Symbol: "ZRX", Contract: common.HexToAddress("0x03")},
		{Kind: Token, Symbol: "aave", Contract: common.HexToAddress("0x02")},
		{Kind: Native, Symbol: "ETH"},
		{Kind: Token, Symbol: "AAVE", Contract: common.HexToAddress("0x01")}, // same symbol, lower addr
	}
	Sort(list)

	if list[0].Kind != Native {
		t.Fatalf("first asset = %s (%v), want native", list[0].Symbol, list[0].Kind)
	}
	// Case-insensitive by symbol, then by contract address.
	want := []string{"ETH", "0x0000000000000000000000000000000000000001", "0x0000000000000000000000000000000000000002", "0x0000000000000000000000000000000000000003"}
	got := []string{list[0].Symbol, list[1].Contract.Hex(), list[2].Contract.Hex(), list[3].Contract.Hex()}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %s, want %s", i, got[i], want[i])
		}
	}
}
