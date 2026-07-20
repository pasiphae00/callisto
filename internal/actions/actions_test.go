package actions

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func inETH(wei string) Inputs {
	v, _ := new(big.Int).SetString(wei, 10)
	return Inputs{Amounts: map[string]*big.Int{"amount": v}}
}

func selector(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	return common.Bytes2Hex(data[:4])
}

func TestWrapUnwrapStakeCalldata(t *testing.T) {
	const oneEth = "1000000000000000000" // 1e18

	cases := []struct {
		id       string
		wantSel  string // 4-byte selector hex
		wantVal  string // expected msg.value (wei)
		dataLen  int    // total calldata length
		contract common.Address
	}{
		{"weth.wrap", "d0e30db0", oneEth, 4, common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")},
		{"weth.unwrap", "2e1a7d4d", "0", 4 + 32, common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")},
		{"lido.stake", "a1903eab", oneEth, 4 + 32, common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84")},
	}
	for _, c := range cases {
		a, ok := ByID(c.id)
		if !ok {
			t.Fatalf("action %s not in registry", c.id)
		}
		if !a.AvailableOn(1) {
			t.Fatalf("%s not available on mainnet", c.id)
		}
		p, err := a.Build(1, inETH(oneEth))
		if err != nil {
			t.Fatalf("%s Build: %v", c.id, err)
		}
		if p.Call.To != c.contract {
			t.Errorf("%s To = %s, want %s", c.id, p.Call.To, c.contract)
		}
		if p.Call.Value.String() != c.wantVal {
			t.Errorf("%s Value = %s, want %s", c.id, p.Call.Value, c.wantVal)
		}
		if got := selector(p.Call.Data); got != c.wantSel {
			t.Errorf("%s selector = %s, want %s", c.id, got, c.wantSel)
		}
		if len(p.Call.Data) != c.dataLen {
			t.Errorf("%s data len = %d, want %d", c.id, len(p.Call.Data), c.dataLen)
		}
		if p.Summary == "" || len(p.Review) == 0 {
			t.Errorf("%s missing summary/review", c.id)
		}
	}
}

func TestBuildRejectsNonPositive(t *testing.T) {
	a, _ := ByID("weth.wrap")
	if _, err := a.Build(1, inETH("0")); err == nil {
		t.Error("expected error for zero amount")
	}
	if _, err := a.Build(10, inETH("1000000000000000000")); err == nil {
		t.Error("expected error for unsupported chain")
	}
}
