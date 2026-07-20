// Package actions is the curated registry of single-step transaction "actions" that
// Callisto can prepare from a user intent (e.g. "wrap 10 ETH", "stake 5 ETH with
// Lido"). Each action is a known contract/function on supported chains with a
// deterministic calldata builder and a human-readable review.
//
// This is the trust boundary for the Claude-assisted preparation pipeline: an intent
// resolver (Claude, or a manual form) may only *select* an action from this registry
// and supply parameters — it never emits raw calldata. Callisto builds and decodes the
// call here, so only pre-vetted contracts and functions are ever executable, and a
// wrong selection yields a review the user can reject rather than a silent bad call.
// It doubles as the "address book" DESIGN.md calls for.
package actions

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/assets"
	"codeberg.org/pasiphae/callisto/internal/tx"
)

// FieldKind is the type of an action input, so the UI can validate/format it.
type FieldKind int

const (
	FieldAmountETH FieldKind = iota // an amount in ETH (18 decimals), human units in the UI
)

// Field describes one action input.
type Field struct {
	Key   string
	Label string
	Kind  FieldKind
	Hint  string
}

// Inputs carries parsed field values. Amounts are already in base units (wei).
type Inputs struct {
	Amounts map[string]*big.Int
}

func (in Inputs) amount(key string) (*big.Int, error) {
	v := in.Amounts[key]
	if v == nil || v.Sign() <= 0 {
		return nil, fmt.Errorf("enter a positive amount")
	}
	return v, nil
}

// ReviewRow is one decoded line for the pre-sign review (contract, function, params).
type ReviewRow struct{ Key, Value string }

// Prepared is an action's output: the concrete call plus a human-readable review.
type Prepared struct {
	Call    tx.Call
	Summary string // one line, e.g. "Wrap 10 ETH → WETH"
	Review  []ReviewRow
}

// Action is one curated, single-step action.
type Action struct {
	ID          string
	Name        string
	Description string
	Fields      []Field
	// contracts maps chainID → the target contract address (also gates availability).
	contracts map[uint64]common.Address
	build     func(a Action, chainID uint64, in Inputs) (Prepared, error)
}

// AvailableOn reports whether the action has a configured contract on chainID.
func (a Action) AvailableOn(chainID uint64) bool {
	_, ok := a.contracts[chainID]
	return ok
}

// Build produces the call + review for chainID and inputs.
func (a Action) Build(chainID uint64, in Inputs) (Prepared, error) {
	if !a.AvailableOn(chainID) {
		return Prepared{}, fmt.Errorf("%s is not available on chain %d", a.Name, chainID)
	}
	return a.build(a, chainID, in)
}

// All returns the actions available on chainID, in registry order.
func All(chainID uint64) []Action {
	var out []Action
	for _, a := range registry {
		if a.AvailableOn(chainID) {
			out = append(out, a)
		}
	}
	return out
}

// ByID returns the action with the given id.
func ByID(id string) (Action, bool) {
	for _, a := range registry {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

func ethAmountField(hint string) Field {
	return Field{Key: "amount", Label: "Amount (ETH)", Kind: FieldAmountETH, Hint: hint}
}

func mustABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("actions: bad ABI: " + err.Error())
	}
	return a
}

var (
	wethDepositABI  = mustABI(`[{"name":"deposit","type":"function","stateMutability":"payable","inputs":[],"outputs":[]}]`)
	wethWithdrawABI = mustABI(`[{"name":"withdraw","type":"function","stateMutability":"nonpayable","inputs":[{"name":"wad","type":"uint256"}],"outputs":[]}]`)
	lidoSubmitABI   = mustABI(`[{"name":"submit","type":"function","stateMutability":"payable","inputs":[{"name":"_referral","type":"address"}],"outputs":[{"name":"","type":"uint256"}]}]`)
)

// registry is the curated action set. Addresses are canonical, verified mainnet
// contracts; add chains/actions by adding vetted entries (code review), never by
// trusting an external source at runtime.
var registry = []Action{
	{
		ID:          "weth.wrap",
		Name:        "Wrap ETH → WETH",
		Description: "Deposit ETH into the WETH contract, receiving WETH 1:1.",
		Fields:      []Field{ethAmountField("ETH to wrap")},
		contracts: map[uint64]common.Address{
			1: common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"), // mainnet WETH9
		},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			data, err := wethDepositABI.Pack("deposit")
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: amt, Data: data},
				Summary: fmt.Sprintf("Wrap %s ETH → WETH", assets.FormatUnits(amt, 18)),
				Review: []ReviewRow{
					{"Action", "Wrap ETH to WETH"},
					{"Contract", "WETH · " + to.Hex()},
					{"Function", "deposit()"},
					{"Value", assets.FormatUnits(amt, 18) + " ETH"},
				},
			}, nil
		},
	},
	{
		ID:          "weth.unwrap",
		Name:        "Unwrap WETH → ETH",
		Description: "Withdraw ETH from the WETH contract, burning WETH 1:1.",
		Fields:      []Field{ethAmountField("WETH to unwrap")},
		contracts: map[uint64]common.Address{
			1: common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"),
		},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			data, err := wethWithdrawABI.Pack("withdraw", amt)
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: big.NewInt(0), Data: data},
				Summary: fmt.Sprintf("Unwrap %s WETH → ETH", assets.FormatUnits(amt, 18)),
				Review: []ReviewRow{
					{"Action", "Unwrap WETH to ETH"},
					{"Contract", "WETH · " + to.Hex()},
					{"Function", "withdraw(uint256)"},
					{"Amount", assets.FormatUnits(amt, 18) + " WETH"},
				},
			}, nil
		},
	},
	{
		ID:          "lido.stake",
		Name:        "Stake ETH with Lido",
		Description: "Submit ETH to Lido, receiving stETH 1:1 (a liquid staking token).",
		Fields:      []Field{ethAmountField("ETH to stake")},
		contracts: map[uint64]common.Address{
			1: common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"), // mainnet Lido stETH
		},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			// Zero referral address (no referral program).
			data, err := lidoSubmitABI.Pack("submit", common.Address{})
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: amt, Data: data},
				Summary: fmt.Sprintf("Stake %s ETH with Lido → stETH", assets.FormatUnits(amt, 18)),
				Review: []ReviewRow{
					{"Action", "Stake ETH with Lido"},
					{"Contract", "Lido (stETH) · " + to.Hex()},
					{"Function", "submit(address referral)"},
					{"Referral", "none (0x0)"},
					{"Value", assets.FormatUnits(amt, 18) + " ETH"},
				},
			}, nil
		},
	},
}
