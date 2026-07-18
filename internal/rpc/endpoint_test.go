package rpc

import "testing"

func TestSchemeOf(t *testing.T) {
	cases := []struct {
		url  string
		want Scheme
		ok   bool
	}{
		{"https://mainnet.example/v3/key", SchemeHTTP, true},
		{"http://localhost:8545", SchemeHTTP, true},
		{"wss://mainnet.example/ws", SchemeWS, true},
		{"ws://localhost:8546", SchemeWS, true},
		{"ftp://nope", "", false},
		{"not a url at all", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := SchemeOf(c.url)
		if (err == nil) != c.ok {
			t.Errorf("SchemeOf(%q) ok=%v, want %v (err=%v)", c.url, err == nil, c.ok, err)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("SchemeOf(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestEndpointValidate(t *testing.T) {
	if err := (Endpoint{Name: "", URL: "https://x"}).Validate(); err == nil {
		t.Error("empty name should be invalid")
	}
	if err := (Endpoint{Name: "n", URL: ""}).Validate(); err == nil {
		t.Error("empty URL should be invalid")
	}
	if err := (Endpoint{Name: "n", URL: "ftp://x"}).Validate(); err == nil {
		t.Error("bad scheme should be invalid")
	}
	if err := (Endpoint{Name: "Infura", URL: "wss://mainnet.example/ws"}).Validate(); err != nil {
		t.Errorf("valid ws endpoint rejected: %v", err)
	}
}

func TestSupportsSubscriptions(t *testing.T) {
	if !(Endpoint{Name: "n", URL: "wss://x/ws"}).SupportsSubscriptions() {
		t.Error("wss should support subscriptions")
	}
	if (Endpoint{Name: "n", URL: "https://x"}).SupportsSubscriptions() {
		t.Error("https should not support subscriptions")
	}
}
