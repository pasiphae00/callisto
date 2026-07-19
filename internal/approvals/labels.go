package approvals

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// knownSpenders maps well-known spender contracts to a human name, per chain, so
// the Approvals pane can show "Uniswap Universal Router" instead of a bare address.
// The address is still shown alongside for verification. Extend as needed —
// unknown spenders simply render without a label.
//
// Permit2 lives at the same address on every chain, so it's added to every chain's
// set in spenderLabel rather than duplicated here.
var knownSpenders = map[uint64]map[common.Address]string{
	1: { // Ethereum mainnet
		common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"): "Uniswap V2 Router",
		common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564"): "Uniswap V3 Router",
		common.HexToAddress("0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45"): "Uniswap V3 Router 2",
		common.HexToAddress("0x3fC91A3afd70395Cd496C647d5a6CC9D4B2b7FAD"): "Uniswap Universal Router",
		common.HexToAddress("0xEf1c6E67703c7BD7107eed8303Fbe6EC2554BF6B"): "Uniswap Universal Router (old)",
		common.HexToAddress("0xDef1C0ded9bec7F1a1670819833240f027b25EfF"): "0x Exchange Proxy",
		common.HexToAddress("0x1111111254EEB25477B68fb85Ed929f73A960582"): "1inch Aggregation Router V5",
		common.HexToAddress("0x111111125421cA6dc452d289314280a0f8842A65"): "1inch Aggregation Router V6",
		common.HexToAddress("0xC92E8bdf79f0507f65a392b0ab4667716BFE0110"): "CoW Protocol (GPv2 Vault Relayer)",
		common.HexToAddress("0x40A50cf069e992AA4536211B23F286eF88752187"): "PancakeSwap Universal Router",
	},
}

// spenderLabel returns a human name for a known spender, or "" if unknown.
func spenderLabel(chainID uint64, spender common.Address) string {
	if spender == permit2Address {
		return "Uniswap Permit2"
	}
	if m, ok := knownSpenders[chainID]; ok {
		if name, ok := m[spender]; ok {
			return name
		}
	}
	return ""
}

// Short returns a shortened checksummed address (0xABCD…1234) for display.
func Short(a common.Address) string {
	s := a.Hex()
	if len(s) < 10 {
		return s
	}
	return s[:6] + "…" + s[len(s)-4:]
}

// DisplaySpender returns "<label> (0xABCD…1234)" when the spender is known, else
// just the short address.
func (a Approval) DisplaySpender() string {
	short := Short(a.Spender)
	if a.SpenderLabel == "" {
		return short
	}
	return a.SpenderLabel + " (" + short + ")"
}

// DisplayToken returns the token symbol, or its short address if the symbol is
// unknown.
func (a Approval) DisplayToken() string {
	if strings.TrimSpace(a.TokenSymbol) != "" {
		return a.TokenSymbol
	}
	return Short(a.Token)
}
