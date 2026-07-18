package chain

import "testing"

func TestLookupKnown(t *testing.T) {
	info, ok := Lookup(1)
	if !ok {
		t.Fatal("mainnet (1) should be recognized")
	}
	if info.Native.Symbol != "ETH" || info.Native.Decimals != 18 {
		t.Errorf("mainnet native = %+v, want ETH/18", info.Native)
	}
	if info.Testnet {
		t.Error("mainnet must not be flagged as testnet")
	}
}

func TestLookupTestnet(t *testing.T) {
	info, ok := Lookup(11155111)
	if !ok || !info.Testnet {
		t.Errorf("sepolia should be recognized testnet, got ok=%v info=%+v", ok, info)
	}
}

func TestLookupUnknownFallback(t *testing.T) {
	info, ok := Lookup(999999999)
	if ok {
		t.Fatal("chain 999999999 should not be recognized")
	}
	// Fallback must still be usable, not zero-valued.
	if info.ID != 999999999 || info.Native.Decimals != 18 || info.Native.Symbol != "ETH" {
		t.Errorf("fallback = %+v, want id set + ETH/18 native", info)
	}
	if info.TxURL("0xabc") != "" {
		t.Error("fallback has no explorer, TxURL must be empty")
	}
}

func TestExplorerURLs(t *testing.T) {
	info, _ := Lookup(1)
	if got := info.TxURL("0xdeadbeef"); got != "https://etherscan.io/tx/0xdeadbeef" {
		t.Errorf("TxURL = %q", got)
	}
	if got := info.AddressURL("0x1234"); got != "https://etherscan.io/address/0x1234" {
		t.Errorf("AddressURL = %q", got)
	}
}

func TestNativeAssetVaries(t *testing.T) {
	// Polygon's native asset is POL, not ETH — chain-awareness is a hard requirement.
	info, ok := Lookup(137)
	if !ok || info.Native.Symbol != "POL" {
		t.Errorf("polygon native = %+v, want POL", info.Native)
	}
}
