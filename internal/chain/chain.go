// Package chain holds static, per-network metadata keyed by EIP-155 chain ID:
// the native asset (name/symbol/decimals) and the canonical block explorer.
//
// Callisto has no default RPC (see DESIGN.md), so chain metadata is decoupled
// from any specific endpoint: once a connection reports its chain ID, the UI
// looks up the matching Info here to label the native asset and build explorer
// links. Unknown chains degrade gracefully to a generic "ETH" native asset.
package chain

import "fmt"

// NativeAsset describes a chain's native (non-contract) currency.
type NativeAsset struct {
	Name     string
	Symbol   string
	Decimals uint8
}

// Info is the static metadata Callisto knows about a chain.
type Info struct {
	ID          uint64
	Name        string
	Native      NativeAsset
	ExplorerURL string // base URL, no trailing slash, e.g. "https://etherscan.io"
	Testnet     bool
}

// ether is the near-universal native asset shape (18 decimals). Most EVM chains
// share it even when the symbol differs, so helpers below reuse it.
func ether(symbol string) NativeAsset {
	return NativeAsset{Name: symbol, Symbol: symbol, Decimals: 18}
}

// registry is the built-in set of known chains. It is intentionally small and
// extensible: adding a chain is a single entry, and unknown chains still work
// via Lookup's synthesized fallback.
var registry = map[uint64]Info{
	1: {
		ID:          1,
		Name:        "Ethereum Mainnet",
		Native:      NativeAsset{Name: "Ether", Symbol: "ETH", Decimals: 18},
		ExplorerURL: "https://etherscan.io",
	},
	11155111: {
		ID:          11155111,
		Name:        "Sepolia",
		Native:      NativeAsset{Name: "Sepolia Ether", Symbol: "ETH", Decimals: 18},
		ExplorerURL: "https://sepolia.etherscan.io",
		Testnet:     true,
	},
	17000: {
		ID:          17000,
		Name:        "Holesky",
		Native:      NativeAsset{Name: "Holesky Ether", Symbol: "ETH", Decimals: 18},
		ExplorerURL: "https://holesky.etherscan.io",
		Testnet:     true,
	},
	10: {
		ID:          10,
		Name:        "OP Mainnet",
		Native:      ether("ETH"),
		ExplorerURL: "https://optimistic.etherscan.io",
	},
	8453: {
		ID:          8453,
		Name:        "Base",
		Native:      ether("ETH"),
		ExplorerURL: "https://basescan.org",
	},
	42161: {
		ID:          42161,
		Name:        "Arbitrum One",
		Native:      ether("ETH"),
		ExplorerURL: "https://arbiscan.io",
	},
	137: {
		ID:          137,
		Name:        "Polygon",
		Native:      NativeAsset{Name: "POL", Symbol: "POL", Decimals: 18},
		ExplorerURL: "https://polygonscan.com",
	},
	100: {
		ID:          100,
		Name:        "Gnosis",
		Native:      NativeAsset{Name: "xDAI", Symbol: "xDAI", Decimals: 18},
		ExplorerURL: "https://gnosisscan.io",
	},
}

// Lookup returns metadata for a chain ID. For unknown chains it synthesizes a
// generic entry (18-decimal "ETH", no explorer) so callers never have to nil-check
// — the boolean reports whether the chain was actually recognized.
func Lookup(id uint64) (Info, bool) {
	if info, ok := registry[id]; ok {
		return info, true
	}
	return Info{
		ID:     id,
		Name:   fmt.Sprintf("Chain %d", id),
		Native: NativeAsset{Name: "Ether", Symbol: "ETH", Decimals: 18},
	}, false
}

// TxURL builds an explorer link for a transaction hash, or "" if the chain has
// no known explorer.
func (i Info) TxURL(txHash string) string {
	if i.ExplorerURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/tx/%s", i.ExplorerURL, txHash)
}

// AddressURL builds an explorer link for an address, or "" if none is known.
func (i Info) AddressURL(addr string) string {
	if i.ExplorerURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/address/%s", i.ExplorerURL, addr)
}
