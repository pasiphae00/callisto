package buildsecrets

import "testing"

func TestObfuscateRoundTrip(t *testing.T) {
	cases := []string{"abc123", "a-very-long-bearer-token_with.symbols/AND+stuff==", "x"}
	for _, tok := range cases {
		obf := Obfuscate(tok)
		if obf == tok {
			t.Errorf("Obfuscate(%q) returned plaintext", tok)
		}
		if got := deobfuscate(obf); got != tok {
			t.Errorf("round-trip: deobfuscate(Obfuscate(%q)) = %q", tok, got)
		}
	}
}

func TestEmpty(t *testing.T) {
	if Obfuscate("") != "" {
		t.Error("Obfuscate(\"\") should be empty")
	}
	if deobfuscate("") != "" {
		t.Error("deobfuscate(\"\") should be empty")
	}
	if Token("ganymede") != "" {
		t.Error("Token(ganymede) should be empty when no token is embedded")
	}
	if Token("unknown-ref") != "" {
		t.Error("Token(unknown) should be empty")
	}
}
