package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pasiphae00/callisto/internal/assets"
	"github.com/pasiphae00/callisto/internal/chain"
	"github.com/pasiphae00/callisto/internal/rpc"
	"github.com/pasiphae00/callisto/internal/safe"
	"github.com/pasiphae00/callisto/internal/wallet"
)

func TestChainCatalog(t *testing.T) {
	cat := ChainCatalog()
	if len(cat) == 0 {
		t.Fatal("empty catalog")
	}
	if cat[0].ChainID != 1 {
		t.Errorf("first catalog entry = chain %d, want Ethereum (1)", cat[0].ChainID)
	}
	seen := map[uint64]bool{}
	for _, opt := range cat {
		if seen[opt.ChainID] {
			t.Errorf("duplicate chain %d in catalog", opt.ChainID)
		}
		seen[opt.ChainID] = true
		if len(opt.Endpoints) == 0 {
			t.Errorf("%s (chain %d) has no endpoints", opt.Label, opt.ChainID)
		}
		for _, e := range opt.Endpoints {
			if err := e.Validate(); err != nil {
				t.Errorf("%s endpoint %q invalid: %v", opt.Label, e.Name, err)
			}
		}
		// ConnectTarget must return one of the listed endpoints.
		tgt := opt.ConnectTarget()
		found := false
		for _, e := range opt.Endpoints {
			if e.Name == tgt.Name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s ConnectTarget %q not in its endpoint list", opt.Label, tgt.Name)
		}
		// Every catalog chain should have real metadata (native symbol, explorer).
		if info, known := chain.Lookup(opt.ChainID); !known {
			t.Errorf("%s (chain %d) missing from chain registry", opt.Label, opt.ChainID)
		} else if info.ExplorerURL == "" {
			t.Errorf("%s (chain %d) has no explorer URL", opt.Label, opt.ChainID)
		}
	}
	// No embedded token in tests → Ethereum dials the no-auth (Flashbots) endpoint.
	if tgt := cat[0].ConnectTarget(); tgt.AuthRef != "" {
		t.Errorf("Ethereum ConnectTarget in a token-less build = %q (auth %q), want the no-auth fallback", tgt.Name, tgt.AuthRef)
	}
}

func TestSafeRegistry(t *testing.T) {
	c := &Config{}
	d := safe.Descriptor{ID: "s1", Label: "Treasury", Address: "0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1", ChainID: 1, Threshold: 2}
	if err := c.UpsertSafe(d); err != nil {
		t.Fatalf("UpsertSafe: %v", err)
	}
	c.ActiveSafe = "s1"

	// Upsert replaces by ID.
	d.Label = "Main Treasury"
	_ = c.UpsertSafe(d)
	if len(c.Safes) != 1 || c.Safes[0].Label != "Main Treasury" {
		t.Errorf("upsert should replace by id, got %+v", c.Safes)
	}
	if got, ok := c.ActiveSafeDescriptor(); !ok || got.ID != "s1" {
		t.Error("ActiveSafeDescriptor should return the active safe")
	}

	// Remove clears the active selection.
	if !c.RemoveSafe("s1") {
		t.Fatal("RemoveSafe should report true")
	}
	if c.ActiveSafe != "" {
		t.Error("removing the active Safe should clear ActiveSafe")
	}
}

func TestKeystoreDir(t *testing.T) {
	isolate(t)
	dir, err := KeystoreDir()
	if err != nil {
		t.Fatalf("KeystoreDir: %v", err)
	}
	if filepath.Base(dir) != "keystores" {
		t.Errorf("keystore dir = %q, want .../keystores", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("keystore dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("keystore path is not a directory")
	}
}

func TestUpsertSafeRejectsInvalid(t *testing.T) {
	c := &Config{}
	if err := c.UpsertSafe(safe.Descriptor{Label: "no id"}); err == nil {
		t.Error("expected validation error for a Safe with no id/address/chain")
	}
}

// isolate points os.UserConfigDir at a temp location for the duration of a test.
// os.UserConfigDir derives from HOME on darwin and XDG_CONFIG_HOME on linux, so
// we set both to keep the test hermetic across platforms.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

func TestDefaultSecuritySettings(t *testing.T) {
	c := defaultConfig()
	if c.Security.AutoLockMinutes != 15 || !c.Security.LockOnSleep {
		t.Errorf("fresh installs should get gentle auto-lock defaults, got %+v", c.Security)
	}
}

func TestConnectCandidatesOrder(t *testing.T) {
	// Ganymede auto-connect primary + Flashbots fallback: candidates are primary
	// first, then the fallback.
	c := &Config{
		Endpoints: []rpc.Endpoint{
			{Name: GanymedeEndpointName, URL: GanymedeEndpointURL, AuthRef: GanymedeAuthRef, AutoConnect: true},
			{Name: DefaultEndpointName, URL: DefaultEndpointURL},
		},
	}
	cands := c.ConnectCandidates()
	if len(cands) != 2 || cands[0].Name != GanymedeEndpointName || cands[1].Name != DefaultEndpointName {
		t.Fatalf("candidates = %+v", cands)
	}
	if fb, ok := c.FallbackEndpoint(); !ok || fb.Name != DefaultEndpointName {
		t.Errorf("fallback = %+v (ok %v)", fb, ok)
	}

	// When Flashbots is itself the auto-connect endpoint, it appears once (no dup).
	c2 := &Config{Endpoints: []rpc.Endpoint{{Name: DefaultEndpointName, URL: DefaultEndpointURL, AutoConnect: true}}}
	if cands := c2.ConnectCandidates(); len(cands) != 1 || cands[0].Name != DefaultEndpointName {
		t.Errorf("single-endpoint candidates = %+v", cands)
	}
}

func TestLoadMissingSeedsDefaultEndpoint(t *testing.T) {
	isolate(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load on fresh home: %v", err)
	}
	// First run seeds two working default RPCs: Ganymede (archive, primary) and
	// Flashbots Protect (fallback). In a token-less test build Ganymede can't
	// authenticate, so Flashbots is the auto-connect default.
	if len(c.Endpoints) != 2 {
		t.Fatalf("fresh config should seed two endpoints, got %+v", c.Endpoints)
	}
	names := map[string]rpc.Endpoint{}
	for _, e := range c.Endpoints {
		names[e.Name] = e
		if err := e.Validate(); err != nil {
			t.Errorf("seeded endpoint %q invalid: %v", e.Name, err)
		}
	}
	gany, ok := names[GanymedeEndpointName]
	if !ok || gany.URL != GanymedeEndpointURL || gany.AuthRef != GanymedeAuthRef {
		t.Errorf("Ganymede endpoint = %+v", gany)
	}
	fb, ok := names[DefaultEndpointName]
	if !ok || fb.URL != DefaultEndpointURL {
		t.Errorf("Flashbots endpoint = %+v", fb)
	}
	// Without an embedded token, Flashbots is the auto-connect default.
	if c.ActiveEndpoint != DefaultEndpointName {
		t.Errorf("token-less build should default-active Flashbots, got %q", c.ActiveEndpoint)
	}
	if got, ok := c.AutoConnectEndpoint(); !ok || got.Name != DefaultEndpointName {
		t.Error("Flashbots should be the auto-connect endpoint in a token-less build")
	}
	if len(c.Wallets) != 0 {
		t.Errorf("fresh config should have no wallets, got %+v", c.Wallets)
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

func TestAutoConnectExclusive(t *testing.T) {
	c := &Config{}
	_ = c.UpsertEndpoint(rpc.Endpoint{Name: "a", URL: "http://a:8545"})
	_ = c.UpsertEndpoint(rpc.Endpoint{Name: "b", URL: "http://b:8545"})

	if _, ok := c.AutoConnectEndpoint(); ok {
		t.Error("no default expected initially")
	}
	c.SetAutoConnect("a")
	e, ok := c.AutoConnectEndpoint()
	if !ok || e.Name != "a" {
		t.Errorf("default = %+v, want a", e)
	}
	// Setting another default clears the first (exclusive).
	c.SetAutoConnect("b")
	e, _ = c.AutoConnectEndpoint()
	if e.Name != "b" {
		t.Errorf("default = %s, want b", e.Name)
	}
	if got, _ := c.EndpointByName("a"); got.AutoConnect {
		t.Error("endpoint a should no longer be the default")
	}
	// Clearing.
	c.SetAutoConnect("")
	if _, ok := c.AutoConnectEndpoint(); ok {
		t.Error("default should be cleared")
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
