// Package config owns Callisto's on-disk settings document: the persisted list
// of RPC endpoints and wallet descriptors, plus which of each is currently
// selected. It is deliberately the only place that reads/writes the config file,
// and it never stores key material — only inert descriptors (see PRINCIPLES.md).
//
// The file lives under the OS user-config directory (e.g. ~/Library/Application
// Support/callisto/config.json on macOS) and is written atomically so a crash
// mid-save cannot corrupt it.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/buildsecrets"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/safe"
	"codeberg.org/pasiphae/callisto/internal/wallet"
)

// appDir is the subdirectory under os.UserConfigDir used for all Callisto data.
const appDir = "callisto"

// configFile is the settings document name within appDir.
const configFile = "config.json"

// The out-of-the-box default RPCs. Callisto ships with two working Ethereum
// Mainnet endpoints, seeded on first launch (users can replace, remove, or re-order
// them in Settings). This supersedes the original no-default-RPC stance; see
// DESIGN.md.
//
//   - Ganymede: the maintainer's archive node (WSS, bearer auth via a build-embedded
//     token) — the auto-connect default in release builds, so approval scans have
//     full history and live subscriptions work out of the box.
//   - Flashbots Protect (fast): the secondary/fallback (HTTPS, no auth). If Ganymede
//     can't be reached (no embedded token, or it's down), Callisto uses this.
const (
	DefaultEndpointName = "Ethereum Mainnet (via Flashbots Protect)"
	DefaultEndpointURL  = "https://rpc.flashbots.net/fast"

	GanymedeEndpointName = "Ethereum Mainnet (via Ganymede archive)"
	GanymedeEndpointURL  = "wss://ganymede.pasiphae.io"
	GanymedeAuthRef      = "ganymede"
)

// FallbackEndpointName is the endpoint Callisto fails over to when the auto-connect
// endpoint can't be reached.
const FallbackEndpointName = DefaultEndpointName

// defaultConfig is the first-run configuration: Ganymede (primary) + Flashbots
// (fallback). Ganymede auto-connects only when a bearer token is embedded in this
// build; otherwise Flashbots is the auto-connect default so token-less dev builds
// behave sanely.
func defaultConfig() *Config {
	ganymede := rpc.Endpoint{Name: GanymedeEndpointName, URL: GanymedeEndpointURL, AuthRef: GanymedeAuthRef}
	flashbots := rpc.Endpoint{Name: DefaultEndpointName, URL: DefaultEndpointURL}

	active := flashbots.Name
	if buildsecrets.Token(GanymedeAuthRef) != "" {
		ganymede.AutoConnect = true
		active = ganymede.Name
	} else {
		flashbots.AutoConnect = true
	}
	return &Config{
		Endpoints:      []rpc.Endpoint{ganymede, flashbots},
		ActiveEndpoint: active,
		// Gentle defaults for fresh installs: lock after 15 idle minutes and on
		// wake-from-sleep. (Existing configs load with 0 = never, unchanged.)
		Security: SecuritySettings{AutoLockMinutes: 15, LockOnSleep: true},
	}
}

// Config is the full persisted settings document.
type Config struct {
	// Endpoints is the user-curated RPC list; Callisto ships no default.
	Endpoints []rpc.Endpoint `json:"endpoints"`
	// ActiveEndpoint is the Name of the currently selected endpoint ("" = none).
	ActiveEndpoint string `json:"active_endpoint"`
	// Wallets is the persisted wallet registry (descriptors only, no secrets).
	Wallets []wallet.Descriptor `json:"wallets"`
	// ActiveWallet is the ID of the currently selected wallet ("" = none).
	ActiveWallet string `json:"active_wallet"`
	// Tokens is the user-added ERC-20 token list (metadata resolved on-chain).
	Tokens []assets.TokenRef `json:"tokens"`
	// Safes is the persisted registry of imported Safe multisig accounts
	// (descriptors only: address, cached owners/threshold/version, no secrets).
	Safes []safe.Descriptor `json:"safes"`
	// ActiveSafe is the ID of the currently selected Safe ("" = none).
	ActiveSafe string `json:"active_safe"`
	// AutoDetectApprovals enables live approval detection over a WSS endpoint in
	// the Approvals pane (opt-in; off by default to stay resource-mindful).
	AutoDetectApprovals bool `json:"auto_detect_approvals"`
	// Security holds wallet auto-lock preferences.
	Security SecuritySettings `json:"security"`
	// TouchIDKeystores lists the KeystoreIDs enrolled for Touch ID unlock (the
	// derived key is held in the OS keychain, never here). Per keystore, so all
	// accounts sharing it are covered.
	TouchIDKeystores []string `json:"touch_id_keystores,omitempty"`
	// AI holds the optional, default-off Claude integration settings.
	AI AISettings `json:"ai"`
}

// AISettings controls the optional Claude-assisted transaction preparation. Off by
// default; nothing is sent to Anthropic unless Enabled is true and a key is set.
type AISettings struct {
	// Enabled turns AI-assisted preparation on. When false, no AI client is ever
	// constructed and the API key is never read (cold path).
	Enabled bool `json:"enabled"`
	// APIKey is the user's Anthropic API key (bring-your-own). Stored in this 0600
	// config; delete it to fully remove it. It is a billing credential, not wallet
	// material.
	APIKey string `json:"api_key,omitempty"`
}

// AIReady reports whether AI-assisted preparation is enabled and has a key.
func (c *Config) AIReady() bool {
	return c.AI.Enabled && strings.TrimSpace(c.AI.APIKey) != ""
}

// SecuritySettings controls when an unlocked hot wallet is automatically locked.
// The defaults are deliberately gentle (see defaultConfig) so they protect an
// unattended machine without interrupting active use.
type SecuritySettings struct {
	// AutoLockMinutes locks any unlocked wallet after this many minutes of
	// inactivity. 0 = never. Existing configs load as 0 (unchanged behavior); new
	// installs get a gentle default.
	AutoLockMinutes int `json:"auto_lock_minutes"`
	// LockOnSleep locks when the computer wakes from sleep.
	LockOnSleep bool `json:"lock_on_sleep"`
}

// Dir returns the Callisto config directory, creating it if needed.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	dir := filepath.Join(base, appDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// Path returns the absolute path to the config file (creating its dir).
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFile), nil
}

// KeystoreDir returns the directory holding encrypted hot-wallet keystores,
// creating it (0700) if needed. Keystore files are named "<keystore-id>.json".
func KeystoreDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	ks := filepath.Join(dir, "keystores")
	if err := os.MkdirAll(ks, 0o700); err != nil {
		return "", fmt.Errorf("create keystore dir: %w", err)
	}
	return ks, nil
}

// Load reads the config from disk. A missing file is not an error: it returns a
// zero-value Config ready to be populated (first run).
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Genuine first run (no config file yet): seed the default endpoint so
			// Callisto works out of the box. Only happens when the file is absent,
			// so removing all endpoints later is respected (not re-seeded).
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config to disk atomically (write temp + rename) with 0600
// permissions. Although the file holds no secrets, restrictive perms are prudent
// for a wallet's local state.
func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), configFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit config: %w", err)
	}
	return nil
}

// --- convenience accessors -------------------------------------------------

// EndpointByName returns the endpoint with the given name, or false.
func (c *Config) EndpointByName(name string) (rpc.Endpoint, bool) {
	for _, e := range c.Endpoints {
		if e.Name == name {
			return e, true
		}
	}
	return rpc.Endpoint{}, false
}

// ActiveEndpointConfig returns the currently selected endpoint, or false if none.
func (c *Config) ActiveEndpointConfig() (rpc.Endpoint, bool) {
	if c.ActiveEndpoint == "" {
		return rpc.Endpoint{}, false
	}
	return c.EndpointByName(c.ActiveEndpoint)
}

// UpsertEndpoint adds or replaces an endpoint by Name (names are unique).
func (c *Config) UpsertEndpoint(e rpc.Endpoint) error {
	if err := e.Validate(); err != nil {
		return err
	}
	for i := range c.Endpoints {
		if c.Endpoints[i].Name == e.Name {
			c.Endpoints[i] = e
			return nil
		}
	}
	c.Endpoints = append(c.Endpoints, e)
	return nil
}

// RemoveEndpoint deletes an endpoint by name, clearing the active selection if it
// pointed at the removed endpoint. Reports whether anything was removed.
func (c *Config) RemoveEndpoint(name string) bool {
	for i := range c.Endpoints {
		if c.Endpoints[i].Name == name {
			c.Endpoints = append(c.Endpoints[:i], c.Endpoints[i+1:]...)
			if c.ActiveEndpoint == name {
				c.ActiveEndpoint = ""
			}
			return true
		}
	}
	return false
}

// SetAutoConnect makes the named endpoint the sole auto-connect default
// (clearing the flag on all others). Pass "" to clear the default entirely.
func (c *Config) SetAutoConnect(name string) {
	for i := range c.Endpoints {
		c.Endpoints[i].AutoConnect = c.Endpoints[i].Name == name && name != ""
	}
}

// AutoConnectEndpoint returns the endpoint marked for startup auto-connect.
func (c *Config) AutoConnectEndpoint() (rpc.Endpoint, bool) {
	for _, e := range c.Endpoints {
		if e.AutoConnect {
			return e, true
		}
	}
	return rpc.Endpoint{}, false
}

// ConnectCandidates returns the endpoints to try at startup, in order: the
// auto-connect endpoint first, then the named fallback (Flashbots) if present and
// distinct. Used so a failed primary (e.g. Ganymede down) falls over cleanly.
func (c *Config) ConnectCandidates() []rpc.Endpoint {
	var out []rpc.Endpoint
	seen := map[string]bool{}
	add := func(e rpc.Endpoint, ok bool) {
		if ok && !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e)
		}
	}
	add(c.AutoConnectEndpoint())
	add(c.EndpointByName(FallbackEndpointName))
	return out
}

// FallbackEndpoint returns the endpoint Callisto fails over to (Flashbots), if it
// is configured.
func (c *Config) FallbackEndpoint() (rpc.Endpoint, bool) {
	return c.EndpointByName(FallbackEndpointName)
}

// IsTouchIDEnrolled reports whether a keystore is enrolled for Touch ID unlock.
func (c *Config) IsTouchIDEnrolled(keystoreID string) bool {
	for _, k := range c.TouchIDKeystores {
		if k == keystoreID {
			return true
		}
	}
	return false
}

// SetTouchIDEnrolled adds or removes a keystore from the Touch ID enrollment list.
func (c *Config) SetTouchIDEnrolled(keystoreID string, on bool) {
	if keystoreID == "" {
		return
	}
	out := c.TouchIDKeystores[:0]
	for _, k := range c.TouchIDKeystores {
		if k != keystoreID {
			out = append(out, k)
		}
	}
	if on {
		out = append(out, keystoreID)
	}
	c.TouchIDKeystores = out
}

// WalletByID returns the wallet descriptor with the given ID, or false.
func (c *Config) WalletByID(id string) (wallet.Descriptor, bool) {
	for _, w := range c.Wallets {
		if w.ID == id {
			return w, true
		}
	}
	return wallet.Descriptor{}, false
}

// UpsertWallet adds or replaces a wallet descriptor by ID.
func (c *Config) UpsertWallet(w wallet.Descriptor) error {
	if err := w.Validate(); err != nil {
		return err
	}
	for i := range c.Wallets {
		if c.Wallets[i].ID == w.ID {
			c.Wallets[i] = w
			return nil
		}
	}
	c.Wallets = append(c.Wallets, w)
	return nil
}

// RemoveWallet deletes a wallet by ID, clearing the active selection if needed.
// Reports whether anything was removed.
func (c *Config) RemoveWallet(id string) bool {
	for i := range c.Wallets {
		if c.Wallets[i].ID == id {
			c.Wallets = append(c.Wallets[:i], c.Wallets[i+1:]...)
			if c.ActiveWallet == id {
				c.ActiveWallet = ""
			}
			return true
		}
	}
	return false
}

// SafeByID returns the Safe descriptor with the given ID, or false.
func (c *Config) SafeByID(id string) (safe.Descriptor, bool) {
	for _, s := range c.Safes {
		if s.ID == id {
			return s, true
		}
	}
	return safe.Descriptor{}, false
}

// UpsertSafe adds or replaces a Safe descriptor by ID.
func (c *Config) UpsertSafe(s safe.Descriptor) error {
	if err := s.Validate(); err != nil {
		return err
	}
	for i := range c.Safes {
		if c.Safes[i].ID == s.ID {
			c.Safes[i] = s
			return nil
		}
	}
	c.Safes = append(c.Safes, s)
	return nil
}

// RemoveSafe deletes a Safe by ID, clearing the active selection if it pointed at
// the removed Safe. Reports whether anything was removed.
func (c *Config) RemoveSafe(id string) bool {
	for i := range c.Safes {
		if c.Safes[i].ID == id {
			c.Safes = append(c.Safes[:i], c.Safes[i+1:]...)
			if c.ActiveSafe == id {
				c.ActiveSafe = ""
			}
			return true
		}
	}
	return false
}

// ActiveSafeDescriptor returns the currently selected Safe, or false if none.
func (c *Config) ActiveSafeDescriptor() (safe.Descriptor, bool) {
	if c.ActiveSafe == "" {
		return safe.Descriptor{}, false
	}
	return c.SafeByID(c.ActiveSafe)
}

// TokensForChain returns the user-added token contract addresses for a chain.
func (c *Config) TokensForChain(chainID uint64) []common.Address {
	var out []common.Address
	for _, t := range c.Tokens {
		if t.ChainID == chainID {
			out = append(out, common.HexToAddress(t.Address))
		}
	}
	return out
}

// AddToken records a user token for a chain, deduplicated by (chain, address).
// Reports false if it was already present.
func (c *Config) AddToken(ref assets.TokenRef) bool {
	want := common.HexToAddress(ref.Address)
	for _, t := range c.Tokens {
		if t.ChainID == ref.ChainID && common.HexToAddress(t.Address) == want {
			return false
		}
	}
	// Store the checksummed form for readability.
	ref.Address = want.Hex()
	c.Tokens = append(c.Tokens, ref)
	return true
}
