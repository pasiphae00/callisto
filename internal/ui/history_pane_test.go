package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/history"
	"github.com/pasiphae00/callisto/internal/signer/hot"
	"github.com/pasiphae00/callisto/internal/store"
)

func TestHistoryPaneListsRecords(t *testing.T) {
	test.NewApp()
	st, err := store.OpenAt(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := New(&config.Config{}, st)
	if a.history == nil {
		t.Fatal("history repo should be created when a store is present")
	}
	_, _ = a.history.Insert(history.Record{
		ChainID:       11155111,
		WalletAddress: "0xabc",
		Kind:          "send-native",
		Instructions:  "Send 1 ETH to 0x70…79C8",
	})

	p := newHistoryPane(a)
	_ = p.build() // build calls reload()
	if len(p.records) != 1 {
		t.Fatalf("history pane shows %d records, want 1", len(p.records))
	}
	if row := historyRow(p.records[0]); row == "" {
		t.Error("empty history row")
	}

	// The post-send hook should be registered and trigger a reload.
	if a.historyReload == nil {
		t.Error("historyReload hook should be registered by the history pane")
	}
}

func TestHistoryPaneNoStore(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil) // no store
	p := newHistoryPane(a)
	_ = p.build()
	if len(p.records) != 0 {
		t.Error("no records expected without a store")
	}
}

func TestSignAvailability(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil)
	p := newSendPane(a)

	w, err := hot.Open(junkMnemonic, "", hot.DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}

	// No signer unlocked yet.
	if ok, _ := p.signAvailability(w.Address()); ok {
		t.Error("should not be able to sign with no wallet unlocked")
	}

	// Unlock the matching wallet.
	a.setSigner("w1", w)
	defer a.clearSigner()
	if ok, _ := p.signAvailability(w.Address()); !ok {
		t.Error("should be able to sign with the matching wallet unlocked")
	}

	// A different sender is not signable with this signer.
	other := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	if ok, _ := p.signAvailability(other); ok {
		t.Error("should not sign for a non-matching sender")
	}
}
