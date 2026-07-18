package assets

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// TokenRef is a user-added token reference (persisted in config). Metadata is
// resolved on-chain; only the contract address and chain are stored.
type TokenRef struct {
	ChainID uint64 `json:"chain_id"`
	Address string `json:"address"`
}

// curatedToken is a built-in known token used both as a discovery hint and as a
// source of a logo URI (metadata is still read on-chain for authority).
type curatedToken struct {
	Address string
	Symbol  string
	LogoURI string
}

// curated is a small built-in per-chain token list. It is intentionally minimal;
// users add anything else by contract address, and broader discovery (log scan /
// token-list import) is a future enhancement.
var curated = map[uint64][]curatedToken{
	1: { // Ethereum mainnet
		{"0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", "WETH", "https://assets.trustwalletapp.com/blockchains/ethereum/assets/0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2/logo.png"},
		{"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "https://assets.trustwalletapp.com/blockchains/ethereum/assets/0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48/logo.png"},
		{"0xdAC17F958D2ee523a2206206994597C13D831ec7", "USDT", "https://assets.trustwalletapp.com/blockchains/ethereum/assets/0xdAC17F958D2ee523a2206206994597C13D831ec7/logo.png"},
		{"0x6B175474E89094C44Da98b954EedeAC495271d0F", "DAI", "https://assets.trustwalletapp.com/blockchains/ethereum/assets/0x6B175474E89094C44Da98b954EedeAC495271d0F/logo.png"},
	},
	11155111: { // Sepolia
		{"0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9", "WETH", ""},
		{"0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", "USDC", ""},
	},
}

// curatedFor returns the curated tokens for a chain (may be empty).
func curatedFor(chainID uint64) []curatedToken {
	return curated[chainID]
}

// logoFor returns a curated logo URI for a token address on a chain, or "".
func logoFor(chainID uint64, token common.Address) string {
	for _, t := range curated[chainID] {
		if strings.EqualFold(t.Address, token.Hex()) {
			return t.LogoURI
		}
	}
	return ""
}
