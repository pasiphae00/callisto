// Package textsafe sanitizes untrusted strings (on-chain token metadata, ENS names,
// dApp/peer names, imported proposal descriptions) before they are displayed, to
// defeat display-spoofing: Unicode bidi overrides, zero-width/format characters,
// control characters, and layout-breaking whitespace. It is a leaf package with no
// dependencies so any layer can call it at the point untrusted data enters.
package textsafe

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxDisplayRunes caps a sanitized label so a pathological on-chain string can't blow
// out the UI (token symbols/names are short; anything longer is truncated with an
// ellipsis).
const maxDisplayRunes = 64

// Display returns s sanitized for safe display:
//   - invalid UTF-8 is dropped (no U+FFFD injected);
//   - control characters (Cc) and format characters (Cf — bidi overrides/isolates,
//     zero-width joiners, BOM, LRM/RLM, …) are removed;
//   - every run of whitespace (including exotic Unicode spaces) collapses to one ASCII
//     space, and the result is trimmed;
//   - the result is capped to maxDisplayRunes.
//
// This strips the characters used to make a scam token look like "USDC" or to reorder a
// label with a right-to-left override, while leaving ordinary (including non-Latin) text
// intact.
func Display(s string) string {
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	wrote := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = wrote // defer; drop leading/trailing runs
		case unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError:
			// control / format (bidi, zero-width, BOM) — drop entirely.
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
			wrote = true
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) > maxDisplayRunes {
		runes := []rune(out)
		out = strings.TrimRight(string(runes[:maxDisplayRunes]), " ") + "…"
	}
	return out
}
