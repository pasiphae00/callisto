package ui

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/sim"
)

// collectText walks a rendered tree and returns every label's text, which is how
// these tests assert on what the user is actually told.
func collectText(o fyne.CanvasObject) []string {
	switch v := o.(type) {
	case *widget.Label:
		return []string{v.Text}
	case *fyne.Container:
		var out []string
		for _, child := range v.Objects {
			out = append(out, collectText(child)...)
		}
		return out
	default:
		return nil
	}
}

func sectionText(s *simSection) string {
	return strings.Join(collectText(s.object()), "\n")
}

// newTestSection builds a section without a live connection, then drives its
// rendering directly. simSection.start needs an RPC connection, so the tests
// below exercise finishRun, which is where every wording decision lives.
func newTestSection(t *testing.T) *simSection {
	t.Helper()
	test.NewApp()
	a := New(&config.Config{}, nil)
	return newSimSection(a, func(context.Context, *sim.Simulator, bool) (sim.Result, error) {
		return sim.Result{}, nil
	})
}

func TestSimSectionWithoutConnectionSaysSo(t *testing.T) {
	// newSimSection kicks off the automatic revert check, which cannot run
	// unconnected -- it must say that rather than sit on a spinner forever.
	s := newTestSection(t)
	if got := sectionText(s); !strings.Contains(got, "Not connected") {
		t.Fatalf("section text = %q; want it to report no connection", got)
	}
	if s.spinner.Visible() {
		t.Error("the spinner must not be left running when there is nothing to run")
	}
}

func TestSimSectionRevertShowsWarningAndReason(t *testing.T) {
	s := newTestSection(t)
	s.finishRun(sim.Result{Status: sim.StatusRevert, RevertReason: "ERC20: insufficient allowance"},
		nil, false, sim.Caps{})

	got := sectionText(s)
	if !strings.Contains(got, "REVERT") {
		t.Errorf("section text = %q; want a prominent REVERT warning", got)
	}
	if !strings.Contains(got, "ERC20: insufficient allowance") {
		t.Errorf("section text = %q; want the revert reason surfaced", got)
	}
}

func TestSimSectionRevertWithoutReasonStillWarns(t *testing.T) {
	s := newTestSection(t)
	s.finishRun(sim.Result{Status: sim.StatusRevert}, nil, false, sim.Caps{})

	got := sectionText(s)
	if !strings.Contains(got, "REVERT") || !strings.Contains(got, "no reason") {
		t.Fatalf("section text = %q; want a warning that names the missing reason", got)
	}
}

func TestSimSectionOffersPreviewOnlyOnACapableEndpoint(t *testing.T) {
	capable := newTestSection(t)
	capable.finishRun(sim.Result{Status: sim.StatusOK}, nil, false, sim.Caps{SimulateV1: true})
	if !capable.button.Visible() || capable.button.Text != "Simulate…" {
		t.Errorf("button = %q visible=%v; want an offered Simulate… on a capable endpoint",
			capable.button.Text, capable.button.Visible())
	}

	bare := newTestSection(t)
	bare.finishRun(sim.Result{Status: sim.StatusOK, Note: "Revert check only."}, nil, false, sim.Caps{})
	if bare.button.Visible() {
		t.Error("a bare endpoint must not offer an asset preview it cannot produce")
	}
	if got := sectionText(bare); !strings.Contains(got, "revert check only") &&
		!strings.Contains(strings.ToLower(got), "revert check only") {
		t.Errorf("section text = %q; want the limitation explained", got)
	}
}

func TestSimSectionRetryRepeatsTheModeThatFailed(t *testing.T) {
	// Retrying a failed revert check must not silently escalate to the heavier
	// asset preview -- on a rate-limited endpoint that turns one failed cheap
	// call into a much more expensive one.
	s := newTestSection(t)
	s.finishRun(sim.Result{}, errors.New("timeout"), false, sim.Caps{SimulateV1: true})
	if s.buttonRich {
		t.Error("retry after a failed revert check should re-run the revert check")
	}

	s.finishRun(sim.Result{}, errors.New("timeout"), true, sim.Caps{SimulateV1: true})
	if !s.buttonRich {
		t.Error("retry after a failed asset preview should re-run the asset preview")
	}
}

func TestSimSectionRendersAssetChanges(t *testing.T) {
	s := newTestSection(t)
	s.chainID = 1
	oneETH := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	s.finishRun(sim.Result{
		Status:   sim.StatusOK,
		ETHDelta: new(big.Int).Neg(oneETH),
		Tokens: []sim.TokenDelta{{
			Token:  common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"),
			Symbol: "stETH", Decimals: 18, Delta: big.NewInt(998_000_000_000_000_000),
		}},
	}, nil, true, sim.Caps{SimulateV1: true})

	got := sectionText(s)
	for _, want := range []string{"-1 ETH", "+0.998 stETH"} {
		if !strings.Contains(got, want) {
			t.Errorf("section text = %q; want it to contain %q", got, want)
		}
	}
	if !strings.Contains(got, "current chain state") {
		t.Error("the preview must carry the snapshot caveat")
	}
}

func TestSimSectionFlagsUnlimitedApproval(t *testing.T) {
	s := newTestSection(t)
	s.chainID = 1
	s.finishRun(sim.Result{
		Status: sim.StatusOK,
		Approvals: []sim.ApprovalChange{{
			Token:     common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
			Symbol:    "USDC",
			Spender:   common.HexToAddress("0x2222222222222222222222222222222222222b"),
			Unlimited: true,
		}},
	}, nil, true, sim.Caps{SimulateV1: true})

	got := sectionText(s)
	if !strings.Contains(got, "UNLIMITED") || !strings.Contains(got, "USDC") {
		t.Fatalf("section text = %q; want an unlimited USDC allowance called out", got)
	}
}

func TestSimSectionExplainsAStateOnlyCall(t *testing.T) {
	s := newTestSection(t)
	s.finishRun(sim.Result{Status: sim.StatusOK, ETHDelta: new(big.Int)},
		nil, true, sim.Caps{SimulateV1: true})

	got := sectionText(s)
	if !strings.Contains(got, "without moving any") {
		t.Fatalf("section text = %q; want an empty preview explained rather than left blank", got)
	}
}

func TestSimSectionFailureDoesNotImplySafety(t *testing.T) {
	// The dangerous failure mode: a simulation that errors must never read as
	// "checks passed". It has to say it learned nothing.
	s := newTestSection(t)
	s.finishRun(sim.Result{}, errors.New("rate limit exceeded"), false, sim.Caps{})

	got := sectionText(s)
	if !strings.Contains(got, "unavailable") {
		t.Errorf("section text = %q; want the failure named", got)
	}
	if !strings.Contains(got, "says nothing about whether the transaction is safe") {
		t.Errorf("section text = %q; a failed simulation must not imply the tx is fine", got)
	}
	if !s.button.Visible() {
		t.Error("a transient failure should leave a retry available")
	}
}
