package sim

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/assets"
	"github.com/pasiphae00/callisto/internal/chain"
)

// Row is one rendered line of the asset-change preview.
//
// Rendering lives here rather than in the Fyne panes so the wording, signing,
// and unit conversion are unit-testable without a display, and so every review
// dialog shows an identical preview.
type Row struct {
	// Text is the line as shown, e.g. "-10 ETH", "+9.998 stETH",
	// "Approve UNLIMITED USDC -> 0x1111...111a".
	Text string
	// Incoming is true for a credit, false for a debit or an approval.
	Incoming bool
	// Warn marks a row the user should look at twice -- today, an unlimited
	// token allowance, the classic drainer setup.
	Warn bool
}

// Rows renders a successful simulation's asset changes, ordered debits and
// credits first (what moved), then approvals (what was granted).
//
// chainID selects the native asset's symbol, so a Polygon simulation reads
// "-10 POL" rather than "-10 ETH". A zero ETH delta produces no row: the
// preview should show what changed, not enumerate what didn't.
func (r Result) Rows(chainID uint64) []Row {
	var rows []Row

	if r.ETHDelta != nil && r.ETHDelta.Sign() != 0 {
		native := chainNative(chainID)
		rows = append(rows, Row{
			Text:     signedAmount(r.ETHDelta, native.Decimals, native.Symbol),
			Incoming: r.ETHDelta.Sign() > 0,
		})
	}

	for _, t := range r.Tokens {
		if t.Delta == nil || t.Delta.Sign() == 0 {
			continue
		}
		rows = append(rows, Row{
			Text:     signedAmount(t.Delta, t.Decimals, symbolOr(t.Symbol, t.Token)),
			Incoming: t.Delta.Sign() > 0,
		})
	}

	for _, a := range r.Approvals {
		rows = append(rows, Row{Text: approvalText(a), Warn: a.Unlimited})
	}

	return rows
}

// HasChanges reports whether a successful simulation found anything to show.
// A transaction can legitimately succeed and move nothing (a state-only call);
// the UI says so explicitly rather than rendering an empty list.
func (r Result) HasChanges() bool { return len(r.Rows(0)) > 0 }

// signedAmount renders a base-unit delta with an explicit sign, so a credit and
// a debit are distinguishable at a glance and never by colour alone.
func signedAmount(delta *big.Int, decimals uint8, symbol string) string {
	text := assets.FormatUnits(delta, decimals)
	if delta.Sign() > 0 {
		text = "+" + text
	}
	return text + " " + symbol
}

func approvalText(a ApprovalChange) string {
	symbol := symbolOr(a.Symbol, a.Token)
	spender := shortAddress(a.Spender)
	if a.Unlimited {
		return fmt.Sprintf("Approve UNLIMITED %s -> %s", symbol, spender)
	}
	// An approval reset to zero is a revocation; naming it as such is clearer
	// than "Approve 0".
	if a.Amount != nil && a.Amount.Sign() == 0 {
		return fmt.Sprintf("Revoke %s allowance for %s", symbol, spender)
	}
	// Decimals is zero when the token's metadata could not be read, which
	// renders the raw base-unit integer -- honest, and better than scaling by
	// a guessed factor.
	return fmt.Sprintf("Approve %s %s -> %s", assets.FormatUnits(a.Amount, a.Decimals), symbol, spender)
}

// symbolOr falls back to a shortened address when a token's symbol could not be
// read (or was stripped as unsafe to display).
func symbolOr(symbol string, token common.Address) string {
	if s := strings.TrimSpace(symbol); s != "" {
		return s
	}
	return shortAddress(token)
}

func shortAddress(a common.Address) string {
	h := a.Hex()
	return h[:6] + "..." + h[len(h)-4:]
}

func chainNative(chainID uint64) chain.NativeAsset {
	info, _ := chain.Lookup(chainID)
	return info.Native
}
