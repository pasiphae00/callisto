package walletconnect

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseURI(t *testing.T) {
	sym := strings.Repeat("ab", 32) // 32 bytes hex
	raw := "wc:3aff2f57358c58929b49467c7b02ac54@2?expiryTimestamp=1784417051&relay-protocol=irn&symKey=" + sym

	got, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if got.Topic != "3aff2f57358c58929b49467c7b02ac54" {
		t.Errorf("topic = %q", got.Topic)
	}
	if got.Version != "2" || got.RelayProtocol != "irn" {
		t.Errorf("version/relay = %q/%q", got.Version, got.RelayProtocol)
	}
	if got.Expiry != 1784417051 {
		t.Errorf("expiry = %d", got.Expiry)
	}
	wantKey, _ := hex.DecodeString(sym)
	if len(got.SymKey) != 32 || string(got.SymKey) != string(wantKey) {
		t.Errorf("symKey mismatch")
	}
}

func TestParseURIRejects(t *testing.T) {
	sym := strings.Repeat("ab", 32)
	cases := map[string]string{
		"not wc":         "https://example.com",
		"no version":     "wc:topiconly",
		"wrong version":  "wc:topic@1?symKey=" + sym,
		"missing symKey": "wc:topic@2?relay-protocol=irn",
		"short symKey":   "wc:topic@2?symKey=abcd",
		"empty topic":    "wc:@2?symKey=" + sym,
		"bad symKey hex": "wc:topic@2?symKey=" + strings.Repeat("zz", 32),
	}
	for name, raw := range cases {
		if _, err := ParseURI(raw); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
