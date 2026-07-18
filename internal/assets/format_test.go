package assets

import (
	"errors"
	"math/big"
	"testing"
)

func bigStr(s string) *big.Int {
	v, _ := new(big.Int).SetString(s, 10)
	return v
}

func TestFormatUnits(t *testing.T) {
	cases := []struct {
		amount   string
		decimals uint8
		want     string
	}{
		{"0", 18, "0"},
		{"1000000000000000000", 18, "1"},          // 1 ETH
		{"1500000000000000000", 18, "1.5"},        // 1.5 ETH
		{"1", 18, "0.000000000000000001"},         // 1 wei
		{"1234567", 6, "1.234567"},                // USDC style
		{"1500000", 6, "1.5"},                     // trailing zeros trimmed
		{"100000000", 8, "1"},                     // 8-decimal token
		{"250000000", 8, "2.5"},                   // 8-decimal token
		{"42", 0, "42"},                           // zero-decimal token
		{"1000000000000000001", 18, "1.000000000000000001"},
		{"-1500000", 6, "-1.5"},
	}
	for _, c := range cases {
		if got := FormatUnits(bigStr(c.amount), c.decimals); got != c.want {
			t.Errorf("FormatUnits(%s, %d) = %q, want %q", c.amount, c.decimals, got, c.want)
		}
	}
	if got := FormatUnits(nil, 18); got != "0" {
		t.Errorf("FormatUnits(nil) = %q, want 0", got)
	}
}

func TestParseUnits(t *testing.T) {
	cases := []struct {
		human    string
		decimals uint8
		want     string
	}{
		{"1", 18, "1000000000000000000"},
		{"1.5", 18, "1500000000000000000"},
		{"0.000000000000000001", 18, "1"},
		{"1.234567", 6, "1234567"},
		{"1.5", 6, "1500000"},
		{"42", 0, "42"},
		{".5", 18, "500000000000000000"},
		{"5.", 18, "5000000000000000000"},
		{"0", 18, "0"},
		{"  2.5  ", 8, "250000000"},
		{"+3", 6, "3000000"},
	}
	for _, c := range cases {
		got, err := ParseUnits(c.human, c.decimals)
		if err != nil {
			t.Errorf("ParseUnits(%q, %d) error: %v", c.human, c.decimals, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseUnits(%q, %d) = %s, want %s", c.human, c.decimals, got, c.want)
		}
	}
}

func TestParseUnitsErrors(t *testing.T) {
	if _, err := ParseUnits("1.2345678", 6); !errors.Is(err, ErrTooManyDecimals) {
		t.Errorf("too many decimals err = %v, want ErrTooManyDecimals", err)
	}
	for _, bad := range []string{"", "  ", "abc", "1.2.3", "1e18", "0x10", ".", "-", "1,5", "1 5"} {
		if _, err := ParseUnits(bad, 18); err == nil {
			t.Errorf("ParseUnits(%q) should error", bad)
		}
	}
}

func TestParseFormatRoundTrip(t *testing.T) {
	// Parsing then formatting returns the canonical (trailing-zero-trimmed) form.
	cases := []struct{ in, canonical string }{
		{"1.500", "1.5"},
		{"0001.2500", "1.25"},
		{"1000000", "1000000"},
	}
	for _, c := range cases {
		v, err := ParseUnits(c.in, 18)
		if err != nil {
			t.Fatalf("ParseUnits(%q): %v", c.in, err)
		}
		if got := FormatUnits(v, 18); got != c.canonical {
			t.Errorf("round-trip %q -> %q, want %q", c.in, got, c.canonical)
		}
	}
}
