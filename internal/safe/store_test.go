package safe_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/safe"
	"codeberg.org/pasiphae/callisto/internal/store"
)

func newRepo(t *testing.T) *safe.ProposalRepo {
	t.Helper()
	s, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return safe.NewProposalRepo(s.DB())
}

func sampleProposal() safe.Proposal {
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	tx := safe.NewSafeTx(to, big.NewInt(1_000_000_000_000_000_000), []byte{0xab, 0xcd}, 3)
	return safe.Proposal{
		SafeAddress: safeAddr,
		ChainID:     1,
		To:          tx.To,
		Value:       tx.Value,
		Data:        tx.Data,
		Operation:   tx.Operation,
		SafeNonce:   3,
		SafeTxHash:  common.HexToHash("0xdeadbeef"),
		Kind:        safe.KindTransfer,
		Description: "Send 1 ETH",
	}
}

func TestProposalRoundTrip(t *testing.T) {
	r := newRepo(t)
	id, err := r.Insert(sampleProposal())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := r.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != safe.StatusCollecting {
		t.Errorf("status = %q, want collecting", got.Status)
	}
	if got.Value.Cmp(big.NewInt(1_000_000_000_000_000_000)) != 0 {
		t.Errorf("value = %s", got.Value)
	}
	if !bytes.Equal(got.Data, []byte{0xab, 0xcd}) {
		t.Errorf("data = %x", got.Data)
	}
	if got.SafeNonce != 3 || got.Kind != safe.KindTransfer {
		t.Errorf("nonce/kind = %d/%s", got.SafeNonce, got.Kind)
	}
	// Reconstructed SafeTx should carry zeroed gas/refund fields.
	stx := got.SafeTx()
	if stx.SafeTxGas.Sign() != 0 || stx.GasPrice.Sign() != 0 || stx.RefundReceiver != (common.Address{}) {
		t.Error("reconstructed SafeTx not zeroed on gas/refund fields")
	}
}

func TestSignatureCollectionAndUpsert(t *testing.T) {
	r := newRepo(t)
	id, err := r.Insert(sampleProposal())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	owner := common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")
	sig1 := bytes.Repeat([]byte{0x11}, 65)
	sig2 := bytes.Repeat([]byte{0x22}, 65)

	if err := r.AddSignature(id, owner, sig1); err != nil {
		t.Fatalf("AddSignature: %v", err)
	}
	// Re-signing the same owner replaces, not duplicates.
	if err := r.AddSignature(id, owner, sig2); err != nil {
		t.Fatalf("AddSignature (replace): %v", err)
	}

	got, err := r.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Signatures) != 1 {
		t.Fatalf("signature count = %d, want 1 (upsert)", len(got.Signatures))
	}
	if !got.SignedBy(owner) {
		t.Error("SignedBy(owner) = false")
	}
	if !bytes.Equal(got.SignatureMap()[owner], sig2) {
		t.Error("stored signature was not replaced with the newer one")
	}
}

func TestListBySafeAndStatus(t *testing.T) {
	r := newRepo(t)
	id, err := r.Insert(sampleProposal())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	list, err := r.ListBySafe(safeAddr, 1)
	if err != nil {
		t.Fatalf("ListBySafe: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	if err := r.SetStatus(id, safe.StatusExecuted, "0xabc", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, _ := r.Get(id)
	if got.Status != safe.StatusExecuted || got.ExecutedTxHash != "0xabc" {
		t.Errorf("after SetStatus: status=%s hash=%s", got.Status, got.ExecutedTxHash)
	}

	// A different Safe address returns nothing.
	other, err := r.ListBySafe(common.HexToAddress("0xffff"), 1)
	if err != nil {
		t.Fatalf("ListBySafe(other): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("expected no proposals for a different Safe, got %d", len(other))
	}
}

func TestMarkRejectedByNonce(t *testing.T) {
	r := newRepo(t)
	orig, _ := r.Insert(sampleProposal())
	rejection, _ := r.Insert(sampleProposal()) // same nonce/safe

	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	if err := r.MarkRejectedByNonce(safeAddr, 1, 3, rejection); err != nil {
		t.Fatalf("MarkRejectedByNonce: %v", err)
	}

	got, _ := r.Get(orig)
	if got.Status != safe.StatusRejected {
		t.Errorf("original status = %s, want rejected", got.Status)
	}
	keep, _ := r.Get(rejection)
	if keep.Status == safe.StatusRejected {
		t.Error("the executing rejection proposal should not be marked rejected")
	}
}
