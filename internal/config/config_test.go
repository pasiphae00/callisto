package config

import (
	"os"
	"path/filepath"
	"testing"

	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/wallet"
)

// isolate points os.UserConfigDir at a temp location for the duration of a test.
// os.UserConfigDir derives from HOME on darwin and XDG_CONFIG_HOME on linux, so
// we set both to keep the test hermetic across platforms.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	isolate(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load on fresh home: %v", err)
	}
	if len(c.Endpoints) != 0 || len(c.Wallets) != 0 {
		t.Errorf("fresh config should be empty, got %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolate(t)
	c := &Config{}
	if err := c.UpsertEndpoint(rpc.Endpoint{Name: "local", URL: "http://localhost:8545"}); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertEndpoint(rpc.Endpoint{Name: "sepolia", URL: "wss://sepolia.example/ws", ChainID: 11155111}); err != nil {
		t.Fatal(err)
	}
	c.ActiveEndpoint = "sepolia"
	if err := c.UpsertWallet(wallet.Descriptor{ID: "w1", Label: "main", Address: "0xAbC", Kind: wallet.KindHot, DerivationPath: "m/44'/60'/0'/0/0"}); err != nil {
		t.Fatal(err)
	}
	c.ActiveWallet = "w1"
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Endpoints) != 2 || got.ActiveEndpoint != "sepolia" {
		t.Errorf("endpoints round-trip mismatch: %+v", got)
	}
	if len(got.Wallets) != 1 || got.ActiveWallet != "w1" || got.Wallets[0].DerivationPath != "m/44'/60'/0'/0/0" {
		t.Errorf("wallets round-trip mismatch: %+v", got)
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	isolate(t)
	c := &Config{}
	_ = c.UpsertEndpoint(rpc.Endpoint{Name: "n", URL: "http://x:8545"})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perms = %o, want 600", perm)
	}
	// No leftover temp files should remain in the config dir.
	dir, _ := Dir()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > len(configFile) && e.Name()[:len(configFile)] == configFile && e.Name() != configFile {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestUpsertReplaces(t *testing.T) {
	c := &Config{}
	_ = c.UpsertEndpoint(rpc.Endpoint{Name: "n", URL: "http://a:8545"})
	_ = c.UpsertEndpoint(rpc.Endpoint{Name: "n", URL: "http://b:8545"})
	if len(c.Endpoints) != 1 || c.Endpoints[0].URL != "http://b:8545" {
		t.Errorf("upsert should replace by name, got %+v", c.Endpoints)
	}
}

func TestRemoveClearsActive(t *testing.T) {
	c := &Config{}
	_ = c.UpsertEndpoint(rpc.Endpoint{Name: "n", URL: "http://a:8545"})
	c.ActiveEndpoint = "n"
	if !c.RemoveEndpoint("n") {
		t.Fatal("remove should report true")
	}
	if c.ActiveEndpoint != "" {
		t.Error("removing active endpoint should clear ActiveEndpoint")
	}
}

func TestTokensPerChainAndDedup(t *testing.T) {
	c := &Config{}
	usdcLower := "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
	if !c.AddToken(assets.TokenRef{ChainID: 1, Address: usdcLower}) {
		t.Fatal("first add should succeed")
	}
	// Same token, different case, same chain -> dedup.
	if c.AddToken(assets.TokenRef{ChainID: 1, Address: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"}) {
		t.Error("duplicate token (case-insensitive) should be rejected")
	}
	// Same address, different chain -> allowed.
	if !c.AddToken(assets.TokenRef{ChainID: 11155111, Address: usdcLower}) {
		t.Error("same address on a different chain should be allowed")
	}
	if got := c.TokensForChain(1); len(got) != 1 {
		t.Errorf("chain 1 tokens = %d, want 1", len(got))
	}
	if got := c.TokensForChain(11155111); len(got) != 1 {
		t.Errorf("sepolia tokens = %d, want 1", len(got))
	}
	// Stored address is checksummed for readability.
	if c.Tokens[0].Address != "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48" {
		t.Errorf("stored address = %s, want checksummed", c.Tokens[0].Address)
	}
}

func TestUpsertRejectsInvalid(t *testing.T) {
	c := &Config{}
	if err := c.UpsertEndpoint(rpc.Endpoint{Name: "", URL: "http://x"}); err == nil {
		t.Error("invalid endpoint should be rejected")
	}
	if err := c.UpsertWallet(wallet.Descriptor{ID: "", Address: "0x", Kind: wallet.KindHot}); err == nil {
		t.Error("invalid wallet should be rejected")
	}
}
