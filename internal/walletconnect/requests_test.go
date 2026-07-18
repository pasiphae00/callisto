package walletconnect

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDecodeTxParams(t *testing.T) {
	params := json.RawMessage(`[{
		"from":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		"to":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		"value":"0xde0b6b3a7640000",
		"data":"0xa9059cbb",
		"gas":"0x5208",
		"nonce":"0x3"
	}]`)
	tp, err := DecodeTxParams(params)
	if err != nil {
		t.Fatalf("DecodeTxParams: %v", err)
	}
	if tp.From != common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8") {
		t.Errorf("from = %s", tp.From.Hex())
	}
	if tp.To == nil || *tp.To != common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48") {
		t.Errorf("to = %v", tp.To)
	}
	if tp.Value.Cmp(big.NewInt(1_000_000_000_000_000_000)) != 0 {
		t.Errorf("value = %s", tp.Value)
	}
	if len(tp.Data) != 4 || tp.Gas != 21000 {
		t.Errorf("data/gas = %x / %d", tp.Data, tp.Gas)
	}
	if tp.Nonce == nil || *tp.Nonce != 3 {
		t.Errorf("nonce = %v", tp.Nonce)
	}
}

func TestDecodeTxParamsMinimalAndInput(t *testing.T) {
	// "input" instead of "data"; no value/gas/nonce.
	tp, err := DecodeTxParams(json.RawMessage(`[{"from":"0x1","to":"0x2","input":"0xdeadbeef"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if tp.Value.Sign() != 0 || tp.Gas != 0 || tp.Nonce != nil {
		t.Errorf("unset fields not zeroed: %+v", tp)
	}
	if len(tp.Data) != 4 {
		t.Errorf("input not decoded: %x", tp.Data)
	}
}

func TestDecodePersonalSignBothOrders(t *testing.T) {
	addr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	// personal_sign order: [message, address].
	msg, got, err := DecodePersonalSign(json.RawMessage(`["0x48656c6c6f","` + addr + `"]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "Hello" || got != common.HexToAddress(addr) {
		t.Errorf("personal_sign decode: msg=%q addr=%s", msg, got.Hex())
	}
	// eth_sign order: [address, message].
	msg2, got2, err := DecodePersonalSign(json.RawMessage(`["` + addr + `","0x48656c6c6f"]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(msg2) != "Hello" || got2 != common.HexToAddress(addr) {
		t.Errorf("eth_sign order decode: msg=%q addr=%s", msg2, got2.Hex())
	}
}

func TestDecodeTypedDataStringAndObject(t *testing.T) {
	addr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	inner := `{"types":{"EIP712Domain":[]},"primaryType":"X","domain":{},"message":{}}`

	// As a JSON string argument.
	strJSON, _ := json.Marshal(inner)
	gotAddr, td, err := DecodeTypedData(json.RawMessage(`["` + addr + `",` + string(strJSON) + `]`))
	if err != nil {
		t.Fatal(err)
	}
	if gotAddr != common.HexToAddress(addr) || string(td) != inner {
		t.Errorf("typed-data (string) decode: addr=%s td=%s", gotAddr.Hex(), td)
	}

	// As an inline object argument.
	_, td2, err := DecodeTypedData(json.RawMessage(`["` + addr + `",` + inner + `]`))
	if err != nil {
		t.Fatal(err)
	}
	var check map[string]interface{}
	if err := json.Unmarshal(td2, &check); err != nil {
		t.Errorf("typed-data (object) not valid JSON: %v", err)
	}
}

func TestDecodersReject(t *testing.T) {
	if _, err := DecodeTxParams(json.RawMessage(`[]`)); err == nil {
		t.Error("empty tx params should error")
	}
	if _, _, err := DecodePersonalSign(json.RawMessage(`["0x48656c6c6f"]`)); err == nil {
		t.Error("personal_sign with one arg should error")
	}
	if _, _, err := DecodeTypedData(json.RawMessage(`["not-an-address","{}"]`)); err == nil {
		t.Error("typed data with non-address should error")
	}
}
