package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/pasiphae00/callisto/internal/sim"
)

// simTimeout bounds a simulation so a slow or wedged endpoint can't leave the
// review dialog spinning. Generous, because debug_traceCall on a busy archive
// node is not fast.
const simTimeout = 30 * time.Second

// simRun performs one simulation. Callers supply the closure because only they
// know whether the transaction under review is an EOA send, a WalletConnect
// request, or a Safe proposal; `rich` selects the asset-change preview over the
// cheap universal revert check.
type simRun func(ctx context.Context, s *sim.Simulator, rich bool) (sim.Result, error)

// simSection is the "Simulation" block shared by every pre-sign review dialog.
//
// It exists so the four bespoke review dialogs (Send, WalletConnect, Safe
// Build, Safe Proposals) show an identical, identically-worded simulation --
// wording that hedges correctly is a safety feature, and four copies of it
// would drift.
//
// Behaviour follows the trigger decision in docs/transaction-simulation.md:
//
//   - The revert check runs automatically on open. It is one eth_call, works on
//     every endpoint, and warns about a transaction that would burn gas doing
//     nothing.
//   - The asset-change preview is explicit, behind "Simulate…". It costs a
//     heavier call and only some endpoints serve it, so the user asks for it.
//
// A simulation never blocks signing. It is a snapshot of current state, not a
// guarantee about the block the transaction actually lands in, so it informs
// the decision rather than gating it.
type simSection struct {
	app *App
	run simRun

	status  *widget.Label
	detail  *widget.Label
	rows    *fyne.Container
	button  *widget.Button
	spinner *widget.ProgressBarInfinite
	box     *fyne.Container

	// buttonRich is the mode s.button will run in when tapped.
	buttonRich bool

	mu      sync.Mutex
	running bool
	chainID uint64
}

// newSimSection builds the section and kicks off the automatic revert check.
func newSimSection(app *App, run simRun) *simSection {
	s := &simSection{
		app:     app,
		run:     run,
		status:  widget.NewLabel(""),
		detail:  widget.NewLabel(""),
		rows:    container.NewVBox(),
		spinner: widget.NewProgressBarInfinite(),
	}
	s.status.TextStyle = fyne.TextStyle{Bold: true}
	s.detail.Wrapping = fyne.TextWrapWord

	// The action's mode is rebound each time it is offered (see showAction):
	// retrying a failed revert check must redo the revert check, not silently
	// upgrade to the heavier asset preview.
	s.button = widget.NewButton("Simulate…", func() { s.start(s.buttonRich) })
	s.button.Hide()
	s.spinner.Hide()

	s.box = container.NewVBox(
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Simulation", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		s.status,
		s.spinner,
		s.rows,
		s.detail,
		container.NewHBox(s.button),
	)

	s.start(false)
	return s
}

// object returns the section for embedding in a review dialog.
func (s *simSection) object() fyne.CanvasObject { return s.box }

// start runs a simulation off the UI thread. Concurrent runs are dropped rather
// than queued: the second result would overwrite the first for the same
// transaction, so there is nothing to gain by running both.
func (s *simSection) start(rich bool) {
	simulator, chainID, ok := s.app.simulator()
	if !ok {
		s.setStatus("Not connected", "Connect to an RPC endpoint to simulate this transaction.")
		return
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.chainID = chainID
	s.mu.Unlock()

	s.beginRun(rich)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), simTimeout)
		defer cancel()

		res, err := s.run(ctx, simulator, rich)
		if err == nil && rich {
			// Resolve token symbols/decimals before rendering, so amounts are
			// human units rather than raw base-unit integers.
			simulator.Enrich(ctx, &res)
		}
		caps := simulator.Caps(ctx)

		fyne.Do(func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			s.finishRun(res, err, rich, caps)
		})
	}()
}

// beginRun puts the section into its in-progress state.
func (s *simSection) beginRun(rich bool) {
	s.rows.RemoveAll()
	s.detail.SetText("")
	if rich {
		s.status.SetText("Simulating…")
	} else {
		s.status.SetText("Checking for revert…")
	}
	s.button.Hide()
	s.spinner.Show()
	s.spinner.Start()
}

// finishRun renders the outcome.
func (s *simSection) finishRun(res sim.Result, err error, rich bool, caps sim.Caps) {
	s.spinner.Stop()
	s.spinner.Hide()

	if err != nil {
		// A failed simulation is not a verdict on the transaction. Say what
		// went wrong and let the user decide, rather than implying either
		// safety or danger.
		s.setStatus("Simulation unavailable", "Could not simulate: "+err.Error()+
			"\nThis says nothing about whether the transaction is safe — review the details above carefully.")
		s.offerRetry(rich)
		return
	}

	switch res.Status {
	case sim.StatusRevert:
		reason := res.RevertReason
		if reason == "" {
			reason = "the contract gave no reason"
		}
		s.status.SetText("⚠ This transaction would REVERT")
		s.rows.RemoveAll()
		s.rows.Add(dangerBox("Simulated against current chain state, this transaction fails: " + reason +
			"\n\nSigning it would spend gas and change nothing. Check the amounts, allowances, and recipient before continuing."))
		s.rows.Refresh()
		s.detail.SetText(simFooter(res))
		s.offerRetry(rich)

	case sim.StatusOK:
		s.renderSuccess(res, rich, caps)

	default:
		note := res.Note
		if note == "" {
			note = "This endpoint could not simulate the transaction."
		}
		s.setStatus("Simulation unavailable", note)
		s.offerRetry(rich)
	}
}

func (s *simSection) renderSuccess(res sim.Result, rich bool, caps sim.Caps) {
	s.rows.RemoveAll()

	if !rich {
		s.status.SetText("● No revert — the transaction should execute")
		if caps.Rich() {
			s.detail.SetText("This checked only that the transaction succeeds. Simulate to preview the balance and allowance changes it would make.")
			s.showAction("Simulate…", true)
		} else {
			s.detail.SetText(noteOr(res.Note, "This endpoint supports a revert check only."))
			s.button.Hide()
		}
		s.rows.Refresh()
		return
	}

	rows := res.Rows(s.chainID)
	switch {
	case len(rows) > 0:
		s.status.SetText("● Simulated changes to your account")
		for _, r := range rows {
			s.rows.Add(simRowLabel(r))
		}
	case res.Note != "":
		s.status.SetText("● No revert — the transaction should execute")
	default:
		s.status.SetText("● No revert — but no balance changes for your account")
		s.rows.Add(widget.NewLabel(
			"The transaction succeeds without moving any of your ETH, tokens, or allowances. " +
				"That is expected for a contract call that only changes settings."))
	}
	s.rows.Refresh()

	s.detail.SetText(joinNotes(res.Note, simFooter(res)))
	s.showAction("Re-simulate", true)
}

// offerRetry keeps a retry action available after a failure, since the common
// causes (a rate-limited public endpoint, a transient timeout) clear on their
// own. It retries the mode that failed rather than escalating: a revert check
// that timed out should be retried as a revert check.
func (s *simSection) offerRetry(rich bool) {
	if rich {
		s.showAction("Try again", true)
	} else {
		s.showAction("Check again", false)
	}
}

// showAction labels the button and binds the mode it will run in.
func (s *simSection) showAction(label string, rich bool) {
	s.buttonRich = rich
	s.button.SetText(label)
	s.button.Show()
}

func (s *simSection) setStatus(status, detail string) {
	s.status.SetText(status)
	s.rows.RemoveAll()
	s.rows.Refresh()
	s.detail.SetText(detail)
}

// simRowLabel renders one asset-change row. Unlimited approvals get the danger
// treatment; everything else is plain monospace so amounts line up.
func simRowLabel(r sim.Row) fyne.CanvasObject {
	if r.Warn {
		return dangerBox("⚠ " + r.Text +
			"\nAn unlimited allowance lets the spender move that token from your account at any time, forever.")
	}
	return monoLabel(r.Text)
}

// simFooter states the caveat that matters most: a simulation describes the
// chain as it is now, not as it will be when the transaction is mined.
func simFooter(res sim.Result) string {
	base := "Simulated against current chain state — the real result can differ if the chain changes first."
	if res.GasUsed > 0 {
		return fmt.Sprintf("%s Estimated gas used: %d.", base, res.GasUsed)
	}
	return base
}

func joinNotes(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += p
	}
	return out
}

func noteOr(note, fallback string) string {
	if note != "" {
		return note
	}
	return fallback
}
