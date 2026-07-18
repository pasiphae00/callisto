package walletconnect

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Ethereum request methods a dApp may send over a session.
const (
	MethodSendTransaction = "eth_sendTransaction"
	MethodSignTransaction = "eth_signTransaction"
	MethodPersonalSign    = "personal_sign"
	MethodSign            = "eth_sign"
	MethodSignTypedData   = "eth_signTypedData"
	MethodSignTypedDataV4 = "eth_signTypedData_v4"
	MethodSwitchEthChain  = "wallet_switchEthereumChain"
)

// TxParams is a decoded eth_sendTransaction/eth_signTransaction request. Unset
// numeric fields are nil / 0 so the caller can fill them in (estimate gas/fees,
// read the nonce) via the normal transaction pipeline.
type TxParams struct {
	From                 common.Address
	To                   *common.Address // nil = contract creation
	Value                *big.Int
	Data                 []byte
	Gas                  uint64
	Nonce                *uint64
	GasPrice             *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
}

// DecodeTxParams decodes the [ {…} ] params of eth_sendTransaction / eth_signTransaction.
func DecodeTxParams(params json.RawMessage) (TxParams, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(params, &arr); err != nil || len(arr) == 0 {
		return TxParams{}, fmt.Errorf("walletconnect: transaction params must be a non-empty array")
	}
	var raw struct {
		From                 string  `json:"from"`
		To                   *string `json:"to"`
		Gas                  string  `json:"gas"`
		GasPrice             string  `json:"gasPrice"`
		MaxFeePerGas         string  `json:"maxFeePerGas"`
		MaxPriorityFeePerGas string  `json:"maxPriorityFeePerGas"`
		Value                string  `json:"value"`
		Data                 string  `json:"data"`
		Input                string  `json:"input"`
		Nonce                string  `json:"nonce"`
	}
	if err := json.Unmarshal(arr[0], &raw); err != nil {
		return TxParams{}, fmt.Errorf("walletconnect: bad transaction object: %w", err)
	}

	tp := TxParams{From: common.HexToAddress(raw.From), Value: big.NewInt(0)}
	if raw.To != nil && *raw.To != "" {
		a := common.HexToAddress(*raw.To)
		tp.To = &a
	}
	dataHex := raw.Data
	if dataHex == "" {
		dataHex = raw.Input
	}
	if dataHex != "" && dataHex != "0x" {
		b, err := hexutil.Decode(dataHex)
		if err != nil {
			return TxParams{}, fmt.Errorf("walletconnect: bad calldata: %w", err)
		}
		tp.Data = b
	}
	var err error
	if tp.Value, err = optBig(raw.Value, big.NewInt(0)); err != nil {
		return TxParams{}, fmt.Errorf("walletconnect: bad value: %w", err)
	}
	if raw.Gas != "" {
		if tp.Gas, err = hexutil.DecodeUint64(raw.Gas); err != nil {
			return TxParams{}, fmt.Errorf("walletconnect: bad gas: %w", err)
		}
	}
	if raw.Nonce != "" {
		n, nerr := hexutil.DecodeUint64(raw.Nonce)
		if nerr != nil {
			return TxParams{}, fmt.Errorf("walletconnect: bad nonce: %w", nerr)
		}
		tp.Nonce = &n
	}
	if tp.GasPrice, err = optBig(raw.GasPrice, nil); err != nil {
		return TxParams{}, err
	}
	if tp.MaxFeePerGas, err = optBig(raw.MaxFeePerGas, nil); err != nil {
		return TxParams{}, err
	}
	if tp.MaxPriorityFeePerGas, err = optBig(raw.MaxPriorityFeePerGas, nil); err != nil {
		return TxParams{}, err
	}
	return tp, nil
}

func optBig(s string, def *big.Int) (*big.Int, error) {
	if s == "" {
		return def, nil
	}
	return hexutil.DecodeBig(s)
}

// DecodePersonalSign decodes personal_sign / eth_sign params, returning the raw
// message bytes and the signing address. It tolerates either argument order
// (personal_sign is [message, address]; eth_sign is [address, message]) by
// detecting which argument is a 20-byte address.
func DecodePersonalSign(params json.RawMessage) (message []byte, address common.Address, err error) {
	var arr []string
	if err := json.Unmarshal(params, &arr); err != nil || len(arr) < 2 {
		return nil, common.Address{}, fmt.Errorf("walletconnect: personal_sign needs [message, address]")
	}
	a, b := arr[0], arr[1]
	switch {
	case isAddress(b):
		return decodeMessage(a), common.HexToAddress(b), nil
	case isAddress(a):
		return decodeMessage(b), common.HexToAddress(a), nil
	default:
		return nil, common.Address{}, fmt.Errorf("walletconnect: personal_sign has no address argument")
	}
}

// DecodeTypedData decodes eth_signTypedData[_v4] params ([address, typedData]),
// returning the signing address and the raw typed-data JSON. The typed data may be
// a JSON string or an inline object.
func DecodeTypedData(params json.RawMessage) (address common.Address, typedDataJSON []byte, err error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(params, &arr); err != nil || len(arr) < 2 {
		return common.Address{}, nil, fmt.Errorf("walletconnect: signTypedData needs [address, typedData]")
	}
	var addrStr string
	if err := json.Unmarshal(arr[0], &addrStr); err != nil || !isAddress(addrStr) {
		return common.Address{}, nil, fmt.Errorf("walletconnect: signTypedData first arg must be an address")
	}
	// The typed data is either a quoted JSON string or an inline object.
	var asString string
	if err := json.Unmarshal(arr[1], &asString); err == nil {
		return common.HexToAddress(addrStr), []byte(asString), nil
	}
	return common.HexToAddress(addrStr), arr[1], nil
}

func isAddress(s string) bool {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return false
	}
	b, err := hexutil.Decode(s)
	return err == nil && len(b) == 20
}

// decodeMessage returns the raw bytes of a personal-sign message: hex if
// 0x-prefixed, otherwise the UTF-8 bytes of the string.
func decodeMessage(s string) []byte {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if b, err := hexutil.Decode(s); err == nil {
			return b
		}
	}
	return []byte(s)
}
