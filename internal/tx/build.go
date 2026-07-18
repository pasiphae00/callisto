// Package tx builds transaction intents from user requests. It is deliberately
// chain- and gas-agnostic: a Send captures what the user wants to do (recipient,
// asset, amount) and the concrete on-chain call (to/value/calldata). Gas
// estimation, nonce, fee selection, review, and broadcast are layered on top in
// later stages — this package is the pure, unit-testable core.
package tx

import (
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// ErrNonPositiveAmount means a send amount was zero or negative.
var ErrNonPositiveAmount = errors.New("amount must be greater than zero")

// erc20TransferABI is just the transfer(address,uint256) method used to encode
// ERC-20 sends.
var erc20TransferABI = mustABI(`[
  {"name":"transfer","type":"function","stateMutability":"nonpayable","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
]`)

func mustABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("tx: bad built-in ABI: " + err.Error())
	}
	return a
}

// Call is the chain-agnostic core of a transaction: the destination, the native
// value attached, and the calldata. For a native transfer To is the recipient and
// Data is empty; for an ERC-20 transfer To is the token contract, Value is zero,
// and Data is the encoded transfer.
type Call struct {
	To    common.Address
	Value *big.Int
	Data  []byte
}

// Send is a fully-described basic transfer: the user-facing intent (recipient,
// asset, amount in base units) plus the concrete Call to execute it. It is what
// the review, signing, broadcast, and history stages consume.
type Send struct {
	From      common.Address
	Recipient common.Address // the human recipient (== Call.To for native)
	IsNative  bool
	Token     common.Address // ERC-20 contract (zero for native)
	Symbol    string
	Decimals  uint8
	Amount    *big.Int // base units of the asset being sent
	Call      Call
}

// BuildNativeSend prepares a native-currency (ETH / chain native) transfer.
func BuildNativeSend(from, recipient common.Address, amount *big.Int, symbol string, decimals uint8) (Send, error) {
	if amount == nil || amount.Sign() <= 0 {
		return Send{}, ErrNonPositiveAmount
	}
	return Send{
		From:      from,
		Recipient: recipient,
		IsNative:  true,
		Symbol:    symbol,
		Decimals:  decimals,
		Amount:    new(big.Int).Set(amount),
		Call: Call{
			To:    recipient,
			Value: new(big.Int).Set(amount),
			Data:  nil,
		},
	}, nil
}

// BuildERC20Send prepares an ERC-20 token transfer: the transaction targets the
// token contract with zero value and transfer(recipient, amount) calldata.
func BuildERC20Send(from, token, recipient common.Address, amount *big.Int, symbol string, decimals uint8) (Send, error) {
	if amount == nil || amount.Sign() <= 0 {
		return Send{}, ErrNonPositiveAmount
	}
	data, err := erc20TransferABI.Pack("transfer", recipient, amount)
	if err != nil {
		return Send{}, err
	}
	return Send{
		From:      from,
		Recipient: recipient,
		IsNative:  false,
		Token:     token,
		Symbol:    symbol,
		Decimals:  decimals,
		Amount:    new(big.Int).Set(amount),
		Call: Call{
			To:    token,
			Value: big.NewInt(0),
			Data:  data,
		},
	}, nil
}

// NativeSendAll computes the maximum native amount that can be sent while leaving
// enough balance to pay the maximum fee (gasLimit * maxFeePerGas). It returns
// ErrNonPositiveAmount if the balance cannot even cover the fee.
func NativeSendAll(balance *big.Int, gasLimit uint64, maxFeePerGas *big.Int) (*big.Int, error) {
	fee := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), maxFeePerGas)
	amount := new(big.Int).Sub(balance, fee)
	if amount.Sign() <= 0 {
		return nil, ErrNonPositiveAmount
	}
	return amount, nil
}
