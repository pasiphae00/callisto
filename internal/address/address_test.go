package address

import (
	"errors"
	"testing"
)

// Canonical EIP-55 test vector from the spec.
const (
	checksummed = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	lowercase   = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	uppercase   = "0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED"
	// Same address but with one character's case flipped -> invalid checksum.
	badChecksum = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"
)

func TestParseChecksummed(t *testing.T) {
	a, err := Parse(checksummed)
	if err != nil {
		t.Fatalf("valid checksummed rejected: %v", err)
	}
	if Format(a) != checksummed {
		t.Errorf("Format = %q, want %q", Format(a), checksummed)
	}
}

func TestParseAllLowerAndUpperAccepted(t *testing.T) {
	for _, s := range []string{lowercase, uppercase} {
		a, err := Parse(s)
		if err != nil {
			t.Errorf("case-uniform address %q rejected: %v", s, err)
			continue
		}
		// Normalized output is always the canonical checksummed form.
		if Format(a) != checksummed {
			t.Errorf("Parse(%q) formatted to %q, want %q", s, Format(a), checksummed)
		}
	}
}

func TestParseBadChecksumRejected(t *testing.T) {
	_, err := Parse(badChecksum)
	if !errors.Is(err, ErrBadChecksum) {
		t.Errorf("bad checksum should return ErrBadChecksum, got %v", err)
	}
}

func TestParseInvalidFormat(t *testing.T) {
	for _, s := range []string{
		"",
		"0x123", // too short
		"5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe",    // 39 hex, no 0x, wrong len
		"0xZZAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", // non-hex
		"not an address",
	} {
		if _, err := Parse(s); !errors.Is(err, ErrInvalidFormat) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalidFormat", s, err)
		}
	}
}

func TestParseTrimsWhitespace(t *testing.T) {
	if _, err := Parse("  " + checksummed + "\n"); err != nil {
		t.Errorf("surrounding whitespace should be tolerated: %v", err)
	}
}

func TestValid(t *testing.T) {
	if !Valid(lowercase) {
		t.Error("lowercase should be valid")
	}
	if Valid(badChecksum) {
		t.Error("bad checksum should be invalid")
	}
}

func TestShort(t *testing.T) {
	a, _ := Parse(checksummed)
	// First 6 chars ("0x5aAe") + ellipsis + last 4 ("eAed").
	if got := Short(a); got != "0x5aAe…eAed" {
		t.Errorf("Short = %q, want %q", got, "0x5aAe…eAed")
	}
}
