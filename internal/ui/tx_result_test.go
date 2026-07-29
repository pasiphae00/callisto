package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae00/callisto/internal/chain"
	"github.com/pasiphae00/callisto/internal/config"
)

// buttonLabels walks a rendered tree and returns every button's text, which is
// what "the actions are consistent" actually means to a user.
func buttonLabels(o fyne.CanvasObject) []string {
	switch v := o.(type) {
	case *widget.Button:
		return []string{v.Text}
	case *fyne.Container:
		var out []string
		for _, child := range v.Objects {
			out = append(out, buttonLabels(child)...)
		}
		return out
	default:
		return nil
	}
}

func testApp(t *testing.T) *App {
	t.Helper()
	test.NewApp()
	return New(&config.Config{}, nil)
}

func TestTxActionRowOffersCopyAndExplorer(t *testing.T) {
	a := testApp(t)
	info, _ := chain.Lookup(1) // Ethereum: has an explorer

	got := buttonLabels(a.txActionRow("0x4f0aaf7f8bbfd4df5af89efae23ef927d6b5d819ad577f8ef79e875c09be926f", info))
	want := []string{"Copy hash", "View on explorer"}
	if len(got) != len(want) {
		t.Fatalf("buttons = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("button %d = %q; want %q", i, got[i], want[i])
		}
	}
}

func TestTxActionRowOmitsExplorerWithoutOne(t *testing.T) {
	a := testApp(t)
	// An unknown chain has no explorer URL; a dead button would be worse than none.
	info, known := chain.Lookup(999_999)
	if known {
		t.Skip("chain 999999 is now in the registry; pick another unknown id")
	}

	got := buttonLabels(a.txActionRow("0xabc", info))
	if len(got) != 1 || got[0] != "Copy hash" {
		t.Fatalf("buttons = %v; want only [Copy hash] on a chain with no explorer", got)
	}
}

func TestCopyWithFeedbackCopiesAndConfirms(t *testing.T) {
	a := testApp(t)
	hash := "0x4f0aaf7f8bbfd4df5af89efae23ef927d6b5d819ad577f8ef79e875c09be926f"

	btn := widget.NewButton("Copy hash", nil)
	a.copyWithFeedback(btn, "Copy hash", hash)

	if got := fyne.CurrentApp().Clipboard().Content(); got != hash {
		t.Errorf("clipboard = %q; want the full hash %q", got, hash)
	}
	if btn.Text != "Copied ✓" {
		t.Errorf("button text = %q; want it to confirm the copy", btn.Text)
	}
}

func TestCopyToClipboardWithoutAnAppDoesNotPanic(t *testing.T) {
	// Panes are constructed without a Fyne app in unit tests; copying must be a
	// no-op there rather than a nil dereference.
	a := &App{}
	a.copyToClipboard("0xabc")
}
