package textsafe

import "testing"

func TestDisplay(t *testing.T) {
	rlo := string(rune(0x202e))  // right-to-left override
	zwsp := string(rune(0x200b)) // zero-width space
	bom := string(rune(0xfeff))  // BOM / ZWNBSP
	lrm := string(rune(0x200e))  // left-to-right mark
	rlm := string(rune(0x200f))  // right-to-left mark
	zwj := string(rune(0x200d))  // zero-width joiner

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "USDC", "USDC"},
		{"trim+collapse", "  USD   Coin  ", "USD Coin"},
		{"newline injection", "USDC\nfake row", "USDC fake row"},
		{"tabs", "a\tb", "a b"},
		{"rtl override", "USD" + rlo + "CBA", "USDCBA"},
		{"zero width space", "US" + zwsp + "DC", "USDC"},
		{"bom/zwnbsp", bom + "USDC", "USDC"},
		{"ltr/rtl marks", lrm + "USDC" + rlm, "USDC"},
		{"zwj", "US" + zwj + "DC", "USDC"},
		{"control chars", "US\x00\x07DC", "USDC"},
		{"non-latin kept", "东京", "东京"},
		{"empty", "", ""},
		{"only junk", rlo + zwsp + "\x00", ""},
	}
	for _, c := range cases {
		if got := Display(c.in); got != c.want {
			t.Errorf("%s: Display(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestDisplayCapsLength(t *testing.T) {
	long := make([]byte, 0, 200)
	for i := 0; i < 200; i++ {
		long = append(long, 'a')
	}
	got := Display(string(long))
	if r := []rune(got); len(r) != maxDisplayRunes+1 || r[len(r)-1] != '…' {
		t.Errorf("cap: got %d runes (want %d + ellipsis)", len(r), maxDisplayRunes)
	}
}

func TestDisplayDropsInvalidUTF8(t *testing.T) {
	if got := Display("USD\xffC"); got != "USDC" {
		t.Errorf("invalid utf8: got %q, want USDC", got)
	}
}
