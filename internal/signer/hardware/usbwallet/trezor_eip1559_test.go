package usbwallet

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"google.golang.org/protobuf/encoding/protowire"
)

// decodeFields parses a flat protobuf message into field number -> raw values,
// so the hand-rolled EthereumSignTxEIP1559 encoding can be verified field by
// field without a device. bytes/string fields land in `bytesFields`, varint
// fields in `varintFields` (repeated fields accumulate).
func decodeFields(t *testing.T, b []byte) (bytesFields map[protowire.Number][][]byte, varintFields map[protowire.Number][]uint64) {
	t.Helper()
	bytesFields = map[protowire.Number][][]byte{}
	varintFields = map[protowire.Number][]uint64{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(b)
			if vn < 0 {
				t.Fatalf("bad varint for field %d", num)
			}
			varintFields[num] = append(varintFields[num], v)
			b = b[vn:]
		case protowire.BytesType:
			v, vn := protowire.ConsumeBytes(b)
			if vn < 0 {
				t.Fatalf("bad bytes for field %d", num)
			}
			cp := append([]byte(nil), v...)
			bytesFields[num] = append(bytesFields[num], cp)
			b = b[vn:]
		default:
			t.Fatalf("unexpected wire type %d for field %d", typ, num)
		}
	}
	return bytesFields, varintFields
}

func TestEncodeEthereumSignTxEIP1559(t *testing.T) {
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	chainID := big.NewInt(1)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     7,
		To:        &to,
		Value:     big.NewInt(1_000_000_000_000_000_000), // 1 ETH
		Gas:       21000,
		GasFeeCap: big.NewInt(30_000_000_000), // 30 gwei
		GasTipCap: big.NewInt(1_000_000_000),  // 1 gwei
		Data:      nil,
	})
	path := []uint32{0x80000000 + 44, 0x80000000 + 60, 0x80000000, 0, 3}

	enc := encodeEthereumSignTxEIP1559(path, tx, chainID, tx.Data(), uint32(len(tx.Data())))
	bf, vf := decodeFields(t, enc)

	// 1: address_n (repeated uint32)
	if got := vf[1]; len(got) != len(path) {
		t.Fatalf("address_n count = %d, want %d", len(got), len(path))
	} else {
		for i := range path {
			if got[i] != uint64(path[i]) {
				t.Errorf("address_n[%d] = %d, want %d", i, got[i], path[i])
			}
		}
	}
	// 2: nonce
	assertBytesField(t, bf, 2, uintBytes(7))
	// 3: max_gas_fee = GasFeeCap
	assertBytesField(t, bf, 3, big.NewInt(30_000_000_000).Bytes())
	// 4: max_priority_fee = GasTipCap
	assertBytesField(t, bf, 4, big.NewInt(1_000_000_000).Bytes())
	// 5: gas_limit
	assertBytesField(t, bf, 5, uintBytes(21000))
	// 6: to (string, with 0x checksummed)
	assertBytesField(t, bf, 6, []byte(to.Hex()))
	// 7: value
	assertBytesField(t, bf, 7, big.NewInt(1_000_000_000_000_000_000).Bytes())
	// 8: data_initial_chunk must be ABSENT for empty calldata
	if _, ok := bf[8]; ok {
		t.Error("data_initial_chunk should be omitted when there is no calldata")
	}
	// 9: data_length = 0
	assertSingleVarint(t, vf, 9, 0)
	// 10: chain_id = 1
	assertSingleVarint(t, vf, 10, 1)
}

func TestEncodeEthereumSignTxEIP1559WithData(t *testing.T) {
	to := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	chainID := big.NewInt(11155111)
	// A short ERC-20-transfer-like calldata (fits in the initial chunk).
	data := common.FromHex("a9059cbb0000000000000000000000001111111111111111111111111111111111111111000000000000000000000000000000000000000000000000000000000000000a")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		To:        &to,
		Value:     big.NewInt(0),
		Gas:       60000,
		GasFeeCap: big.NewInt(2_000_000_000),
		GasTipCap: big.NewInt(1_000_000_000),
		Data:      data,
	})

	enc := encodeEthereumSignTxEIP1559(nil, tx, chainID, data, uint32(len(data)))
	bf, vf := decodeFields(t, enc)

	assertBytesField(t, bf, 8, data)                // data_initial_chunk present
	assertSingleVarint(t, vf, 9, uint64(len(data))) // data_length
	assertSingleVarint(t, vf, 10, 11155111)         // chain_id (sepolia)
	// nonce 0 encodes to empty bytes, but the field/tag is still emitted.
	assertBytesField(t, bf, 2, []byte{})
}

func TestLeftPad32(t *testing.T) {
	if got := leftPad32([]byte{0x01}); len(got) != 32 || got[31] != 0x01 || got[0] != 0 {
		t.Errorf("leftPad32 short = %x", got)
	}
	full := bytes.Repeat([]byte{0xaa}, 32)
	if got := leftPad32(full); !bytes.Equal(got, full) {
		t.Errorf("leftPad32 exact = %x", got)
	}
	// Over-long (defensive): take the last 32 bytes.
	over := append([]byte{0xff}, bytes.Repeat([]byte{0xbb}, 32)...)
	if got := leftPad32(over); len(got) != 32 || got[0] != 0xbb {
		t.Errorf("leftPad32 over = %x", got)
	}
}

func assertBytesField(t *testing.T, bf map[protowire.Number][][]byte, num protowire.Number, want []byte) {
	t.Helper()
	vals, ok := bf[num]
	if !ok || len(vals) != 1 {
		t.Fatalf("field %d: got %d values, want 1", num, len(vals))
	}
	if !bytes.Equal(vals[0], want) {
		t.Errorf("field %d = %x, want %x", num, vals[0], want)
	}
}

func assertSingleVarint(t *testing.T, vf map[protowire.Number][]uint64, num protowire.Number, want uint64) {
	t.Helper()
	vals, ok := vf[num]
	if !ok || len(vals) != 1 {
		t.Fatalf("varint field %d: got %d values, want 1", num, len(vals))
	}
	if vals[0] != want {
		t.Errorf("varint field %d = %d, want %d", num, vals[0], want)
	}
}
