package ui

import (
	"math/big"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/pasiphae00/callisto/internal/assets"
	"github.com/pasiphae00/callisto/internal/config"
)

func TestSendPanePrepareState(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil)
	p := newSendPane(a)
	_ = p.build() // no wallet/connection -> unavailable, but widgets exist

	// Inject a loaded native asset directly.
	p.items = []assets.Asset{{
		Kind:     assets.Native,
		Symbol:   "ETH",
		Decimals: 18,
		Balance:  big.NewInt(1_000_000_000_000_000_000), // 1 ETH
	}}
	p.assetSelect.Options = []string{"ETH (1)"}
	p.assetSelect.SetSelected("ETH (1)")

	// Nothing entered -> Prepare disabled.
	p.updatePrepareState()
	if !p.prepareBtn.Disabled() {
		t.Fatal("Prepare should be disabled without recipient/amount")
	}

	// Valid recipient + positive amount -> enabled.
	p.recipient.entry.SetText("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	p.amount.SetText("0.5")
	p.updatePrepareState()
	if p.prepareBtn.Disabled() {
		t.Fatal("Prepare should be enabled with valid recipient and amount")
	}

	// Zero amount -> disabled.
	p.amount.SetText("0")
	p.updatePrepareState()
	if !p.prepareBtn.Disabled() {
		t.Error("zero amount should disable Prepare")
	}

	// Over-precise amount for 18 decimals is fine; too many decimals is not.
	p.amount.SetText("0.0000000000000000001") // 19 dp > 18
	p.updatePrepareState()
	if !p.prepareBtn.Disabled() {
		t.Error("over-precise amount should disable Prepare")
	}
}

func TestSendPaneFillMax(t *testing.T) {
	test.NewApp()
	a := New(&config.Config{}, nil)
	p := newSendPane(a)
	_ = p.build()

	p.items = []assets.Asset{{
		Kind:     assets.Token,
		Symbol:   "USDC",
		Decimals: 6,
		Balance:  big.NewInt(1_234_560), // 1.23456
	}}
	p.assetSelect.Options = []string{"USDC (1.23456)"}
	p.assetSelect.SetSelected("USDC (1.23456)")

	p.fillMax()
	if p.amount.Text != "1.23456" {
		t.Errorf("Max set amount to %q, want 1.23456", p.amount.Text)
	}
}
