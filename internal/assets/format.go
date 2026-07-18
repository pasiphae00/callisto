// Package assets handles detection and display of the assets held by an account:
// the native currency (ETH and its per-chain equivalents) and ERC-20 tokens,
// including metadata parsing and correct human<->base-unit amount conversion.
//
// Amount conversion is done with big.Int string arithmetic (never floats) so no
// precision is lost regardless of a token's decimals.
package assets

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrTooManyDecimals means a human amount has more fractional digits than the
// asset's decimals allow — rejected rather than silently truncated, since this is
// money.
var ErrTooManyDecimals = errors.New("amount has more decimal places than the asset supports")

// ErrInvalidAmount means the human amount is not a well-formed decimal number.
var ErrInvalidAmount = errors.New("invalid amount")

// FormatUnits renders a base-unit integer amount as a human decimal string using
// the given number of decimals, trimming trailing fractional zeros. E.g.
// FormatUnits(1_500_000, 6) == "1.5"; FormatUnits(10^18, 18) == "1".
func FormatUnits(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	neg := amount.Sign() < 0
	digits := new(big.Int).Abs(amount).String()

	var out string
	if decimals == 0 {
		out = digits
	} else {
		d := int(decimals)
		if len(digits) <= d {
			// Pad with leading zeros so there is a whole fractional field.
			digits = strings.Repeat("0", d-len(digits)+1) + digits
		}
		split := len(digits) - d
		intPart := digits[:split]
		fracPart := strings.TrimRight(digits[split:], "0")
		if fracPart == "" {
			out = intPart
		} else {
			out = intPart + "." + fracPart
		}
	}
	if neg && out != "0" {
		out = "-" + out
	}
	return out
}

// ParseUnits converts a human decimal string into a base-unit integer using the
// given decimals. It rejects malformed input and amounts with too many fractional
// digits (rather than truncating). E.g. ParseUnits("1.5", 6) == 1_500_000.
func ParseUnits(human string, decimals uint8) (*big.Int, error) {
	s := strings.TrimSpace(human)
	if s == "" {
		return nil, ErrInvalidAmount
	}
	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	if s == "" {
		return nil, ErrInvalidAmount
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart = s[:i]
		fracPart = s[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return nil, ErrInvalidAmount
		}
	}
	if intPart == "" && fracPart == "" {
		return nil, ErrInvalidAmount
	}
	if !isDigits(intPart) || !isDigits(fracPart) {
		return nil, ErrInvalidAmount
	}
	if len(fracPart) > int(decimals) {
		return nil, ErrTooManyDecimals
	}

	// Right-pad the fraction to exactly `decimals` digits, then concatenate.
	fracPart += strings.Repeat("0", int(decimals)-len(fracPart))
	combined := intPart + fracPart
	combined = strings.TrimLeft(combined, "0")
	if combined == "" {
		combined = "0"
	}

	value, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrInvalidAmount, human)
	}
	if neg {
		value.Neg(value)
	}
	return value, nil
}

// DisplayDecimals is the maximum number of fractional digits shown for token
// amounts in the UI. Exact values (for signing) always use FormatUnits/ParseUnits.
const DisplayDecimals = 5

// DustFraction is the default threshold below which a token balance is treated as
// dust and hidden from the overview: 0.00005 of the token.
const DustFraction = "0.00005"

// FormatDisplay renders amount with at most maxDecimals fractional digits
// (truncated, not rounded — never overstates a balance), trimming trailing
// zeros. Use for display only.
func FormatDisplay(amount *big.Int, decimals, maxDecimals uint8) string {
	full := FormatUnits(amount, decimals)
	dot := strings.IndexByte(full, '.')
	if dot < 0 {
		return full
	}
	frac := full[dot+1:]
	if uint8(len(frac)) <= maxDecimals {
		return full
	}
	frac = strings.TrimRight(frac[:maxDecimals], "0")
	if frac == "" {
		return full[:dot]
	}
	return full[:dot+1] + frac
}

// IsDust reports whether a balance is zero or below the dust threshold
// (DustFraction of one token). For tokens with fewer than 5 decimals the
// fractional threshold isn't representable, so only a zero balance counts as dust.
func IsDust(amount *big.Int, decimals uint8) bool {
	if amount == nil || amount.Sign() <= 0 {
		return true
	}
	if decimals < 5 {
		return false // only exact zero is dust for low-decimal tokens
	}
	// threshold = 0.00005 * 10^decimals = 5 * 10^(decimals-5)
	threshold := new(big.Int).Mul(big.NewInt(5), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)-5), nil))
	return amount.Cmp(threshold) < 0
}

// isDigits reports whether s is empty or all ASCII digits.
func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
