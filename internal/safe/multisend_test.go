package safe

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestBuildMultiSend(t *testing.T) {
	a := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	b := common.HexToAddress("0x7f39C581F595B53c5cb19bD0b3f8dA6c935E2Ca0")
	calls := []MultiSendCall{
		{To: a, Value: big.NewInt(0), Data: []byte{0x01, 0x02}},   // 2-byte data
		{To: b, Value: big.NewInt(5), Data: []byte{0x03, 0x04, 0x05}}, // 3-byte data
	}
	stx, err := BuildMultiSend(calls, 7)
	if err != nil {
		t.Fatalf("BuildMultiSend: %v", err)
	}
	if stx.To != multiSendCallOnly {
		t.Errorf("To = %s, want MultiSendCallOnly %s", stx.To, multiSendCallOnly)
	}
	if stx.Operation != DelegateCall {
		t.Errorf("operation = %d, want DelegateCall (1)", stx.Operation)
	}
	if stx.Nonce.Uint64() != 7 {
		t.Errorf("nonce = %d, want 7", stx.Nonce.Uint64())
	}
	// multiSend(bytes) selector 0x8d80ff0a, then abi-encoded bytes.
	if len(stx.Data) < 4 || common.Bytes2Hex(stx.Data[:4]) != "8d80ff0a" {
		t.Errorf("selector = %x, want 8d80ff0a", stx.Data[:4])
	}

	// Empty batch is rejected.
	if _, err := BuildMultiSend(nil, 0); err == nil {
		t.Error("expected error for empty batch")
	}
}
