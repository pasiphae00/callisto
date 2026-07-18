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

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/wallet"
)

// appDir is the subdirectory under os.UserConfigDir used for all Callisto data.
const appDir = "callisto"

// configFile is the settings document name within appDir.
const configFile = "config.json"

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
			return &Config{}, nil
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
