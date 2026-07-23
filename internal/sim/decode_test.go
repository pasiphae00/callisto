package sim

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func topicOf(addr common.Address) common.Hash { return common.BytesToHash(addr.Bytes()) }

func word(v *big.Int) []byte {
	b := make([]byte, 32)
	v.FillBytes(b)
	return b
}

func TestDecodeTransferSentAndReceived(t *testing.T) {
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	alice := common.HexToAddress("0x1111111111111111111111111111111111111a")
	bob := common.HexToAddress("0x2222222222222222222222222222222222222b")
	value := big.NewInt(1_000_000)

	lg := types.Log{
		Address: token,
		Topics:  []common.Hash{transferSig, topicOf(alice), topicOf(bob)},
		Data:    word(value),
	}

	gotToken, delta, ok := decodeTransfer(lg, alice)
	if !ok || gotToken != token || delta.Cmp(new(big.Int).Neg(value)) != 0 {
		t.Fatalf("sender delta = %v, %v (ok=%v); want -%v", gotToken, delta, ok, value)
	}

	gotToken, delta, ok = decodeTransfer(lg, bob)
	if !ok || gotToken != token || delta.Cmp(value) != 0 {
		t.Fatalf("receiver delta = %v, %v (ok=%v); want +%v", gotToken, delta, ok, value)
	}

	_, _, ok = decodeTransfer(lg, common.HexToAddress("0x3333333333333333333333333333333333333c"))
	if ok {
		t.Fatal("decodeTransfer should not match an unrelated account")
	}
}

func TestDecodeApprovalUnlimited(t *testing.T) {
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	owner := common.HexToAddress("0x1111111111111111111111111111111111111a")
	spender := common.HexToAddress("0x2222222222222222222222222222222222222b")

	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	lg := types.Log{
		Address: token,
		Topics:  []common.Hash{approvalSig, topicOf(owner), topicOf(spender)},
		Data:    word(maxUint256),
	}

	gotOwner, ac, ok := decodeApproval(lg)
	if !ok || gotOwner != owner || ac.Spender != spender || !ac.Unlimited {
		t.Fatalf("decodeApproval = %+v owner=%v ok=%v; want unlimited approval", ac, gotOwner, ok)
	}
}

func TestDecodeApprovalFinite(t *testing.T) {
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	owner := common.HexToAddress("0x1111111111111111111111111111111111111a")
	spender := common.HexToAddress("0x2222222222222222222222222222222222222b")
	amount := big.NewInt(500)

	lg := types.Log{
		Address: token,
		Topics:  []common.Hash{approvalSig, topicOf(owner), topicOf(spender)},
		Data:    word(amount),
	}

	_, ac, ok := decodeApproval(lg)
	if !ok || ac.Unlimited || ac.Amount.Cmp(amount) != 0 {
		t.Fatalf("decodeApproval = %+v (ok=%v); want finite %v", ac, ok, amount)
	}
}

func TestDecodePermit2ApprovalUnlimited(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111a")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	spender := common.HexToAddress("0x2222222222222222222222222222222222222b")

	data, err := permit2DataArgs.Pack(maxUint160, big.NewInt(1_800_000_000))
	if err != nil {
		t.Fatalf("pack permit2 data: %v", err)
	}
	lg := types.Log{
		Address: permit2Address,
		Topics:  []common.Hash{permit2ApprovalSig, topicOf(owner), topicOf(token), topicOf(spender)},
		Data:    data,
	}

	gotOwner, ac, ok := decodePermit2Approval(lg)
	if !ok || gotOwner != owner || ac.Token != token || ac.Spender != spender || !ac.Unlimited {
		t.Fatalf("decodePermit2Approval = %+v owner=%v ok=%v; want unlimited", ac, gotOwner, ok)
	}
}

func TestDecodePermit2ApprovalWrongContractRejected(t *testing.T) {
	notPermit2 := common.HexToAddress("0x9999999999999999999999999999999999999d")
	data, _ := permit2DataArgs.Pack(big.NewInt(1), big.NewInt(0))
	lg := types.Log{
		Address: notPermit2,
		Topics: []common.Hash{
			permit2ApprovalSig,
			topicOf(common.Address{}), topicOf(common.Address{}), topicOf(common.Address{}),
		},
		Data: data,
	}
	if _, _, ok := decodePermit2Approval(lg); ok {
		t.Fatal("decodePermit2Approval should reject a log not from the canonical Permit2 address")
	}
}

// sanity-check the abi.Arguments packing helper used by the Permit2 test above
// matches what decodePermit2Approval expects to unpack.
func TestPermit2DataArgsRoundTrip(t *testing.T) {
	args := abi.Arguments{{Type: mustType("uint160")}, {Type: mustType("uint48")}}
	data, err := args.Pack(big.NewInt(42), big.NewInt(7))
	if err != nil {
		t.Fatal(err)
	}
	vals, err := permit2DataArgs.Unpack(data)
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].(*big.Int).Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("round trip mismatch: %v", vals[0])
	}
}
