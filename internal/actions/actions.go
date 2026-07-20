// Package actions is the curated registry of single-step transaction "actions" that
// Callisto can prepare from a user intent (e.g. "wrap 10 ETH", "deposit 5 ETH to
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
	// FieldAmount18 is an 18-decimal token amount (ETH, WETH, stETH, wstETH),
	// entered in human units in the UI and parsed to base units.
	FieldAmount18 FieldKind = iota
)

// Field describes one action input.
type Field struct {
	Key   string
	Label string
	Kind  FieldKind
	Hint  string
}

// Inputs carries parsed field values plus the acting account (owner/recipient for
// actions that need it). Amounts are already in base units.
type Inputs struct {
	Amounts map[string]*big.Int
	Account common.Address
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
	Summary string // one line, e.g. "Wrap 10 ETH -> WETH"
	Review  []ReviewRow
	// Note, when set, is a caveat shown prominently in the review (e.g. a required
	// token approval, or that a withdrawal is claimed later).
	Note string
}

// Action is one curated, single-step action.
type Action struct {
	ID          string
	Name        string
	Description string
	Fields      []Field
	// contracts maps chainID -> target contract address (also gates availability).
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

func amountField(label, hint string) Field {
	return Field{Key: "amount", Label: label, Kind: FieldAmount18, Hint: hint}
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
	wstethWrapABI   = mustABI(`[{"name":"wrap","type":"function","stateMutability":"nonpayable","inputs":[{"name":"_stETHAmount","type":"uint256"}],"outputs":[{"name":"","type":"uint256"}]}]`)
	wstethUnwrapABI = mustABI(`[{"name":"unwrap","type":"function","stateMutability":"nonpayable","inputs":[{"name":"_wstETHAmount","type":"uint256"}],"outputs":[{"name":"","type":"uint256"}]}]`)
	lidoWithdrawABI = mustABI(`[{"name":"requestWithdrawals","type":"function","stateMutability":"nonpayable","inputs":[{"name":"_amounts","type":"uint256[]"},{"name":"_owner","type":"address"}],"outputs":[{"name":"","type":"uint256[]"}]}]`)
)

// Canonical mainnet contract addresses (verified). Add chains by adding vetted
// entries (code review), never by trusting an external source at runtime.
var (
	wethMainnet    = common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	stethMainnet   = common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84")
	wstethMainnet  = common.HexToAddress("0x7f39C581F595B53c5cb19bD0b3f8dA6c935E2Ca0")
	lidoUnstMainnet = common.HexToAddress("0x889edC2eDab5f40e902b864aD4d7AdE8E412F9B1") // Withdrawal Queue
)

// eth18 formats an 18-decimal base-unit amount for display.
func eth18(v *big.Int) string { return assets.FormatUnits(v, 18) }

var registry = []Action{
	{
		ID:          "weth.wrap",
		Name:        "Wrap ETH (ETH -> WETH)",
		Description: "Deposit ETH into the WETH contract, receiving WETH 1:1.",
		Fields:      []Field{amountField("Amount (ETH)", "ETH to wrap")},
		contracts:   map[uint64]common.Address{1: wethMainnet},
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
				Summary: fmt.Sprintf("Wrap %s ETH -> WETH", eth18(amt)),
				Review: []ReviewRow{
					{"Action", "Wrap ETH to WETH"},
					{"Contract", "WETH · " + to.Hex()},
					{"Function", "deposit()"},
					{"Value", eth18(amt) + " ETH"},
				},
			}, nil
		},
	},
	{
		ID:          "weth.unwrap",
		Name:        "Unwrap WETH (WETH -> ETH)",
		Description: "Withdraw ETH from the WETH contract, burning WETH 1:1.",
		Fields:      []Field{amountField("Amount (WETH)", "WETH to unwrap")},
		contracts:   map[uint64]common.Address{1: wethMainnet},
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
				Summary: fmt.Sprintf("Unwrap %s WETH -> ETH", eth18(amt)),
				Review: []ReviewRow{
					{"Action", "Unwrap WETH to ETH"},
					{"Contract", "WETH · " + to.Hex()},
					{"Function", "withdraw(uint256)"},
					{"Amount", eth18(amt) + " WETH"},
				},
			}, nil
		},
	},
	{
		ID:          "lido.deposit",
		Name:        "Deposit ETH to Lido (ETH -> stETH)",
		Description: "Submit ETH to Lido, receiving stETH 1:1 (a liquid staking token).",
		Fields:      []Field{amountField("Amount (ETH)", "ETH to deposit")},
		contracts:   map[uint64]common.Address{1: stethMainnet},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			data, err := lidoSubmitABI.Pack("submit", common.Address{})
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: amt, Data: data},
				Summary: fmt.Sprintf("Deposit %s ETH to Lido -> stETH", eth18(amt)),
				Review: []ReviewRow{
					{"Action", "Deposit ETH to Lido"},
					{"Contract", "Lido (stETH) · " + to.Hex()},
					{"Function", "submit(address referral)"},
					{"Referral", "none (0x0)"},
					{"Value", eth18(amt) + " ETH"},
				},
			}, nil
		},
	},
	{
		ID:          "lido.withdraw",
		Name:        "Withdraw ETH from Lido (stETH -> ETH)",
		Description: "Request a Lido withdrawal of stETH. This creates a withdrawal request (an NFT) that is claimed for ETH later, once processed.",
		Fields:      []Field{amountField("Amount (stETH)", "stETH to withdraw")},
		contracts:   map[uint64]common.Address{1: lidoUnstMainnet},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			data, err := lidoWithdrawABI.Pack("requestWithdrawals", []*big.Int{amt}, in.Account)
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: big.NewInt(0), Data: data},
				Summary: fmt.Sprintf("Request Lido withdrawal of %s stETH", eth18(amt)),
				Note:    "Requires a prior stETH approval to the Lido Withdrawal Queue. Creates a withdrawal request; claim the ETH later once processed.",
				Review: []ReviewRow{
					{"Action", "Request Lido withdrawal (stETH -> ETH)"},
					{"Contract", "Lido Withdrawal Queue · " + to.Hex()},
					{"Function", "requestWithdrawals(uint256[], address)"},
					{"Amount", eth18(amt) + " stETH"},
					{"Owner", in.Account.Hex()},
				},
			}, nil
		},
	},
	{
		ID:          "wsteth.wrap",
		Name:        "Wrap stETH (stETH -> wstETH)",
		Description: "Wrap stETH into wstETH (a non-rebasing wrapped version).",
		Fields:      []Field{amountField("Amount (stETH)", "stETH to wrap")},
		contracts:   map[uint64]common.Address{1: wstethMainnet},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			data, err := wstethWrapABI.Pack("wrap", amt)
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: big.NewInt(0), Data: data},
				Summary: fmt.Sprintf("Wrap %s stETH -> wstETH", eth18(amt)),
				Note:    "Requires a prior stETH approval to the wstETH contract.",
				Review: []ReviewRow{
					{"Action", "Wrap stETH to wstETH"},
					{"Contract", "wstETH · " + to.Hex()},
					{"Function", "wrap(uint256)"},
					{"Amount", eth18(amt) + " stETH"},
				},
			}, nil
		},
	},
	{
		ID:          "wsteth.unwrap",
		Name:        "Unwrap wstETH (wstETH -> stETH)",
		Description: "Unwrap wstETH back into stETH.",
		Fields:      []Field{amountField("Amount (wstETH)", "wstETH to unwrap")},
		contracts:   map[uint64]common.Address{1: wstethMainnet},
		build: func(a Action, chainID uint64, in Inputs) (Prepared, error) {
			amt, err := in.amount("amount")
			if err != nil {
				return Prepared{}, err
			}
			to := a.contracts[chainID]
			data, err := wstethUnwrapABI.Pack("unwrap", amt)
			if err != nil {
				return Prepared{}, err
			}
			return Prepared{
				Call:    tx.Call{To: to, Value: big.NewInt(0), Data: data},
				Summary: fmt.Sprintf("Unwrap %s wstETH -> stETH", eth18(amt)),
				Review: []ReviewRow{
					{"Action", "Unwrap wstETH to stETH"},
					{"Contract", "wstETH · " + to.Hex()},
					{"Function", "unwrap(uint256)"},
					{"Amount", eth18(amt) + " wstETH"},
				},
			}, nil
		},
	},
}
