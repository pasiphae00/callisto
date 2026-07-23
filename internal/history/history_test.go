package history

import (
	"testing"

	"github.com/pasiphae00/callisto/internal/store"
)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	s, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func TestInsertAndList(t *testing.T) {
	r := newRepo(t)
	id, err := r.Insert(Record{
		ChainID:       11155111,
		WalletAddress: "0xabc",
		Kind:          "send-eth",
		ToAddress:     "0xdef",
		ValueWei:      "1000000000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected a row id")
	}

	list, err := r.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d records, want 1", len(list))
	}
	rec := list[0]
	if rec.Status != StatusPrepared || rec.Kind != "send-eth" || rec.ValueWei != "1000000000000000000" {
		t.Errorf("record = %+v", rec)
	}
	if rec.PreparedAt == 0 {
		t.Error("prepared_at should be auto-set")
	}
}

func TestLifecycleTransitions(t *testing.T) {
	r := newRepo(t)
	id, _ := r.Insert(Record{ChainID: 1, WalletAddress: "0xabc", Kind: "send-erc20"})

	if err := r.MarkSubmitted(id, "0xhash"); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkIncluded(id, 12345, 1_700_000_000, true); err != nil {
		t.Fatal(err)
	}

	list, _ := r.List(10)
	rec := list[0]
	if rec.Status != StatusIncluded || rec.TxHash != "0xhash" || rec.BlockNumber != 12345 {
		t.Errorf("after inclusion: %+v", rec)
	}
}

func TestMarkIncludedFailure(t *testing.T) {
	r := newRepo(t)
	id, _ := r.Insert(Record{ChainID: 1, WalletAddress: "0xabc", Kind: "send-eth"})
	_ = r.MarkSubmitted(id, "0xhash")
	if err := r.MarkIncluded(id, 999, 1_700_000_000, false); err != nil {
		t.Fatal(err)
	}
	list, _ := r.List(10)
	if list[0].Status != StatusFailed {
		t.Errorf("reverted tx should be Failed, got %s", list[0].Status)
	}
}

func TestMarkError(t *testing.T) {
	r := newRepo(t)
	id, _ := r.Insert(Record{ChainID: 1, WalletAddress: "0xabc", Kind: "send-eth"})
	if err := r.MarkError(id, "insufficient funds"); err != nil {
		t.Fatal(err)
	}
	list, _ := r.List(10)
	if list[0].Status != StatusFailed || list[0].Error != "insufficient funds" {
		t.Errorf("record = %+v", list[0])
	}
}
