package hot

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSignSafeTxHashRecoversToAddress(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Lock()

	hash := common.HexToHash("0x5a6b8c1d2e3f40510a1b2c3d4e5f60718293a4b5c6d7e8f9012345678abcdef01")
	sig, err := w.SignSafeTxHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("SignSafeTxHash: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("signature length = %d, want 65", len(sig))
	}
	if v := sig[64]; v != 27 && v != 28 {
		t.Errorf("v = %d, want 27 or 28 (direct-hash Safe signature)", v)
	}

	// Recover against the hash directly (v normalized back to 0/1).
	rec := make([]byte, 65)
	copy(rec, sig)
	rec[64] -= 27
	pub, err := crypto.SigToPub(hash.Bytes(), rec)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if got := crypto.PubkeyToAddress(*pub); got != w.Address() {
		t.Errorf("recovered %s, want %s", got.Hex(), w.Address().Hex())
	}
}

func TestSignSafeTxHashAfterLock(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Lock()
	if _, err := w.SignSafeTxHash(context.Background(), common.Hash{}); err != ErrLocked {
		t.Errorf("err = %v, want ErrLocked", err)
	}
}
