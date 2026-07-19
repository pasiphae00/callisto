package usbwallet

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/protobuf/encoding/protowire"
)

var mailTypedData = []byte(`{
  "types": {
    "EIP712Domain": [
      {"name":"name","type":"string"},
      {"name":"version","type":"string"},
      {"name":"chainId","type":"uint256"},
      {"name":"verifyingContract","type":"address"}
    ],
    "Person": [{"name":"name","type":"string"},{"name":"wallet","type":"address"}],
    "Mail": [{"name":"from","type":"Person"},{"name":"to","type":"Person"},{"name":"contents","type":"string"}]
  },
  "primaryType": "Mail",
  "domain": {"name":"Ether Mail","version":"1","chainId":1,"verifyingContract":"0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC"},
  "message": {
    "from": {"name":"Cow","wallet":"0xCD2a3d9F938E13CD947Ec05AbC7FE734Df8DD826"},
    "to": {"name":"Bob","wallet":"0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB"},
    "contents":"Hello, Bob!"
  }
}`)

func TestParseFieldType(t *testing.T) {
	types := map[string][]typedField{"Person": nil}
	cases := []struct {
		in   string
		want fieldType
	}{
		{"address", fieldType{dataType: edtAddress}},
		{"bool", fieldType{dataType: edtBool}},
		{"string", fieldType{dataType: edtString}},
		{"bytes", fieldType{dataType: edtBytes}},
		{"bytes32", fieldType{dataType: edtBytes, size: 32, hasSize: true}},
		{"uint256", fieldType{dataType: edtUint, size: 32, hasSize: true}},
		{"uint160", fieldType{dataType: edtUint, size: 20, hasSize: true}},
		{"int128", fieldType{dataType: edtInt, size: 16, hasSize: true}},
		{"Person", fieldType{dataType: edtStruct, structName: "Person"}},
	}
	for _, c := range cases {
		got, err := parseFieldType(c.in, types)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got.dataType != c.want.dataType || got.size != c.want.size || got.hasSize != c.want.hasSize || got.structName != c.want.structName {
			t.Errorf("%s = %+v, want %+v", c.in, *got, c.want)
		}
	}
	// Arrays.
	arr, err := parseFieldType("Person[]", types)
	if err != nil || arr.dataType != edtArray || arr.hasSize || arr.entryType.structName != "Person" {
		t.Errorf("Person[] = %+v (err %v)", arr, err)
	}
	fixed, err := parseFieldType("uint256[3]", types)
	if err != nil || fixed.dataType != edtArray || fixed.size != 3 || !fixed.hasSize || fixed.entryType.dataType != edtUint {
		t.Errorf("uint256[3] = %+v (err %v)", fixed, err)
	}
}

func TestValueAt(t *testing.T) {
	td, err := parseTypedData(mailTypedData)
	if err != nil {
		t.Fatal(err)
	}

	// Domain: name (string), chainId (uint256 → 32 bytes BE), verifyingContract (address).
	assertVal(t, td, []uint32{0, 0}, []byte("Ether Mail"))
	assertVal(t, td, []uint32{0, 2}, leftPadTo(big.NewInt(1).Bytes(), 32))
	assertVal(t, td, []uint32{0, 3}, common.HexToAddress("0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC").Bytes())

	// Message: contents (string), from.name (nested struct field), from.wallet (address).
	assertVal(t, td, []uint32{1, 2}, []byte("Hello, Bob!"))
	assertVal(t, td, []uint32{1, 0, 0}, []byte("Cow"))
	assertVal(t, td, []uint32{1, 0, 1}, common.HexToAddress("0xCD2a3d9F938E13CD947Ec05AbC7FE734Df8DD826").Bytes())
}

func TestValueAtArrayLength(t *testing.T) {
	// A doc with an array member: the array value request returns the uint16 count.
	doc := []byte(`{
	  "types":{
	    "EIP712Domain":[{"name":"name","type":"string"}],
	    "Order":[{"name":"amounts","type":"uint256[]"}]
	  },
	  "primaryType":"Order",
	  "domain":{"name":"X"},
	  "message":{"amounts":["1","2","3"]}
	}`)
	td, err := parseTypedData(doc)
	if err != nil {
		t.Fatal(err)
	}
	// [1,0] → the "amounts" array → length 3 as uint16.
	assertVal(t, td, []uint32{1, 0}, []byte{0x00, 0x03})
	// [1,0,1] → the 2nd element (uint256 "2") → 32-byte BE.
	assertVal(t, td, []uint32{1, 0, 1}, leftPadTo(big.NewInt(2).Bytes(), 32))
}

func TestEncodeStructAckRoundTrip(t *testing.T) {
	td, _ := parseTypedData(mailTypedData)
	enc, err := encodeStructAck(td.Types["Mail"], td.Types)
	if err != nil {
		t.Fatal(err)
	}
	// Decode the repeated members (field 1, each a message).
	var members [][]byte
	if serr := scanProto(enc, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) {
		if num == 1 && typ == protowire.BytesType {
			members = append(members, append([]byte(nil), v...))
		}
	}); serr != nil {
		t.Fatal(serr)
	}
	if len(members) != 3 {
		t.Fatalf("Mail has 3 members, decoded %d", len(members))
	}
	// Member 0 = {type: {STRUCT, struct_name:"Person"}, name:"from"}.
	name, dataType, structName := decodeMember(t, members[0])
	if name != "from" || dataType != edtStruct || structName != "Person" {
		t.Errorf("member0 = name=%q type=%d struct=%q", name, dataType, structName)
	}
	// Member 2 = {type:{STRING}, name:"contents"}.
	name2, dataType2, _ := decodeMember(t, members[2])
	if name2 != "contents" || dataType2 != edtString {
		t.Errorf("member2 = name=%q type=%d", name2, dataType2)
	}
}

func TestDecodeRequestRoundTrips(t *testing.T) {
	// StructRequest: field 1 = name.
	var sr []byte
	sr = protowire.AppendTag(sr, 1, protowire.BytesType)
	sr = protowire.AppendString(sr, "Person")
	if got, err := decodeStructRequest(sr); err != nil || got != "Person" {
		t.Errorf("decodeStructRequest = %q (err %v)", got, err)
	}

	// ValueRequest: field 1 = member_path (repeated uint32).
	var vr []byte
	for _, n := range []uint32{1, 0, 2} {
		vr = protowire.AppendTag(vr, 1, protowire.VarintType)
		vr = protowire.AppendVarint(vr, uint64(n))
	}
	got, err := decodeValueRequest(vr)
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != 0 || got[2] != 2 {
		t.Errorf("decodeValueRequest = %v (err %v)", got, err)
	}
}

func TestEncodeSignTypedData(t *testing.T) {
	path := []uint32{0x80000000 + 44, 0x80000000 + 60, 0x80000000, 0, 0}
	enc := encodeSignTypedData(path, "Mail")
	bf, vf := decodeFields(t, enc)
	if got := vf[1]; len(got) != len(path) {
		t.Fatalf("address_n count = %d", len(got))
	}
	assertBytesField(t, bf, 2, []byte("Mail"))
	assertSingleVarint(t, vf, 3, 1) // metamask_v4_compat
}

// --- helpers ---------------------------------------------------------------

func assertVal(t *testing.T, td *typedDataDoc, path []uint32, want []byte) {
	t.Helper()
	got, err := td.valueAt(path)
	if err != nil {
		t.Fatalf("valueAt(%v): %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("valueAt(%v) = %x, want %x", path, got, want)
	}
}

// decodeMember decodes an EthereumStructMember {type(1) msg, name(2) string},
// returning the name, the type's data_type, and struct_name.
func decodeMember(t *testing.T, b []byte) (name string, dataType int, structName string) {
	t.Helper()
	err := scanProto(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) {
		switch {
		case num == 2 && typ == protowire.BytesType:
			name = string(v)
		case num == 1 && typ == protowire.BytesType:
			// nested EthereumFieldType: data_type(1) varint, struct_name(4) string.
			_ = scanProto(v, func(n2 protowire.Number, t2 protowire.Type, v2 []byte, vint uint64) {
				if n2 == 1 && t2 == protowire.VarintType {
					dataType = int(vint)
				}
				if n2 == 4 && t2 == protowire.BytesType {
					structName = string(v2)
				}
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return name, dataType, structName
}
