package safe

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestPrevOwner(t *testing.T) {
	a := common.HexToAddress("0xaa")
	b := common.HexToAddress("0xbb")
	c := common.HexToAddress("0xcc")
	owners := []common.Address{a, b, c}

	cases := []struct {
		target common.Address
		want   common.Address
	}{
		{a, SentinelOwner}, // first owner -> sentinel
		{b, a},             // middle
		{c, b},             // last
	}
	for _, tc := range cases {
		got, err := PrevOwner(owners, tc.target)
		if err != nil {
			t.Fatalf("PrevOwner(%s): %v", tc.target.Hex(), err)
		}
		if got != tc.want {
			t.Errorf("PrevOwner(%s) = %s, want %s", tc.target.Hex(), got.Hex(), tc.want.Hex())
		}
	}

	if _, err := PrevOwner(owners, common.HexToAddress("0xdd")); err == nil {
		t.Error("PrevOwner of a non-owner should error")
	}
}

func TestPackSignaturesSortsByAddress(t *testing.T) {
	hi := common.HexToAddress("0xff00000000000000000000000000000000000000")
	lo := common.HexToAddress("0x0100000000000000000000000000000000000000")
	mid := common.HexToAddress("0x8000000000000000000000000000000000000000")

	sigHi := bytes.Repeat([]byte{0x11}, SignatureLen)
	sigLo := bytes.Repeat([]byte{0x22}, SignatureLen)
	sigMid := bytes.Repeat([]byte{0x33}, SignatureLen)

	packed, err := PackSignatures(map[common.Address][]byte{hi: sigHi, lo: sigLo, mid: sigMid})
	if err != nil {
		t.Fatalf("PackSignatures: %v", err)
	}
	if len(packed) != 3*SignatureLen {
		t.Fatalf("packed len = %d, want %d", len(packed), 3*SignatureLen)
	}
	// Ascending address order: lo, mid, hi.
	if !bytes.Equal(packed[0:SignatureLen], sigLo) ||
		!bytes.Equal(packed[SignatureLen:2*SignatureLen], sigMid) ||
		!bytes.Equal(packed[2*SignatureLen:], sigHi) {
		t.Error("signatures not concatenated in ascending signer-address order")
	}
}

func TestPackSignaturesRejectsWrongLength(t *testing.T) {
	a := common.HexToAddress("0xaa")
	if _, err := PackSignatures(map[common.Address][]byte{a: {0x01, 0x02}}); err == nil {
		t.Error("expected error for a non-65-byte signature")
	}
}

func TestEncodeAdminCalldataDecodesBack(t *testing.T) {
	owner := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	prev := common.HexToAddress("0x0100000000000000000000000000000000000001")
	newOwner := common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")

	t.Run("addOwnerWithThreshold", func(t *testing.T) {
		data, err := EncodeAddOwner(owner, 2)
		if err != nil {
			t.Fatal(err)
		}
		args := decodeInputs(t, "addOwnerWithThreshold", data)
		if args[0].(common.Address) != owner {
			t.Errorf("owner = %v", args[0])
		}
		if args[1].(*big.Int).Uint64() != 2 {
			t.Errorf("threshold = %v", args[1])
		}
	})

	t.Run("removeOwner", func(t *testing.T) {
		data, err := EncodeRemoveOwner(prev, owner, 1)
		if err != nil {
			t.Fatal(err)
		}
		args := decodeInputs(t, "removeOwner", data)
		if args[0].(common.Address) != prev || args[1].(common.Address) != owner {
			t.Errorf("prev/owner = %v/%v", args[0], args[1])
		}
		if args[2].(*big.Int).Uint64() != 1 {
			t.Errorf("threshold = %v", args[2])
		}
	})

	t.Run("swapOwner", func(t *testing.T) {
		data, err := EncodeSwapOwner(prev, owner, newOwner)
		if err != nil {
			t.Fatal(err)
		}
		args := decodeInputs(t, "swapOwner", data)
		if args[0].(common.Address) != prev || args[1].(common.Address) != owner || args[2].(common.Address) != newOwner {
			t.Errorf("swap args = %v", args)
		}
	})

	t.Run("changeThreshold", func(t *testing.T) {
		data, err := EncodeChangeThreshold(3)
		if err != nil {
			t.Fatal(err)
		}
		args := decodeInputs(t, "changeThreshold", data)
		if args[0].(*big.Int).Uint64() != 3 {
			t.Errorf("threshold = %v", args[0])
		}
	})
}

func TestEncodeExecDecodesBack(t *testing.T) {
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	tx := NewSafeTx(to, big.NewInt(123), []byte{0xde, 0xad}, 7)
	sigs := bytes.Repeat([]byte{0x11}, SignatureLen)

	data, err := EncodeExec(tx, sigs)
	if err != nil {
		t.Fatal(err)
	}
	args := decodeInputs(t, "execTransaction", data)
	if args[0].(common.Address) != to {
		t.Errorf("to = %v", args[0])
	}
	if args[1].(*big.Int).Uint64() != 123 {
		t.Errorf("value = %v", args[1])
	}
	if !bytes.Equal(args[2].([]byte), []byte{0xde, 0xad}) {
		t.Errorf("data = %x", args[2])
	}
	if args[3].(uint8) != uint8(Call) {
		t.Errorf("operation = %v", args[3])
	}
	if !bytes.Equal(args[9].([]byte), sigs) {
		t.Errorf("signatures mismatch")
	}
}

// decodeInputs unpacks a method's calldata (after the 4-byte selector) back into
// its arguments, so encoders are verified without a chain.
func decodeInputs(t *testing.T, method string, data []byte) []interface{} {
	t.Helper()
	m, ok := safeABI.Methods[method]
	if !ok {
		t.Fatalf("unknown method %q", method)
	}
	if len(data) < 4 || !bytes.Equal(data[:4], m.ID) {
		t.Fatalf("calldata selector mismatch for %s", method)
	}
	args, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("unpack %s: %v", method, err)
	}
	return args
}
