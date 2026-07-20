package ui

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDecodeDangerousCall(t *testing.T) {
	spender := common.HexToAddress("0x1111111111111111111111111111111111111111")
	maxU := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

	// approve(spender, MAX) -> warn + UNLIMITED
	data, err := dangerDecoderABI.Pack("approve", spender, maxU)
	if err != nil {
		t.Fatal(err)
	}
	sum, warn, ok := decodeDangerousCall(data)
	if !ok || !warn || !contains(sum, "UNLIMITED") || !contains(sum, "Approve") {
		t.Errorf("approve MAX: sum=%q warn=%v ok=%v", sum, warn, ok)
	}

	// approve(spender, 1000) -> warn, specific amount (not unlimited)
	data, _ = dangerDecoderABI.Pack("approve", spender, big.NewInt(1000))
	sum, warn, ok = decodeDangerousCall(data)
	if !ok || !warn || contains(sum, "UNLIMITED") {
		t.Errorf("approve 1000: sum=%q warn=%v ok=%v", sum, warn, ok)
	}

	// transfer -> ok, no warn
	data, _ = dangerDecoderABI.Pack("transfer", spender, big.NewInt(5))
	sum, warn, ok = decodeDangerousCall(data)
	if !ok || warn || !contains(sum, "Transfer") {
		t.Errorf("transfer: sum=%q warn=%v ok=%v", sum, warn, ok)
	}

	// unknown selector -> not ok
	if _, _, ok := decodeDangerousCall([]byte{0xde, 0xad, 0xbe, 0xef, 0x00}); ok {
		t.Error("unknown selector should not decode")
	}
	// too short -> not ok
	if _, _, ok := decodeDangerousCall([]byte{0x01}); ok {
		t.Error("short data should not decode")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
