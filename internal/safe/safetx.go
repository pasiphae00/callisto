package safe

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"codeberg.org/pasiphae/callisto/internal/rpc"
)

// Operation is a Safe transaction's call type. Callisto only ever uses Call;
// DelegateCall is defined for completeness (and to reject it explicitly).
type Operation uint8

const (
	Call         Operation = 0
	DelegateCall Operation = 1
)

// SafeTx is a Safe transaction to be hashed, signed by owners, and executed. The
// gas/refund fields are fixed at zero: Callisto pays execution gas as a normal
// EIP-1559 outer transaction from the executing owner, and uses no Safe-level gas
// refunds (safeTxGas=0 lets the Safe use all available gas for the inner call).
type SafeTx struct {
	To             common.Address
	Value          *big.Int
	Data           []byte
	Operation      Operation
	SafeTxGas      *big.Int
	BaseGas        *big.Int
	GasPrice       *big.Int
	GasToken       common.Address
	RefundReceiver common.Address
	Nonce          *big.Int
}

// NewSafeTx builds a SafeTx for a Call with all gas/refund parameters zeroed.
func NewSafeTx(to common.Address, value *big.Int, data []byte, nonce uint64) SafeTx {
	if value == nil {
		value = big.NewInt(0)
	}
	return SafeTx{
		To:             to,
		Value:          value,
		Data:           data,
		Operation:      Call,
		SafeTxGas:      big.NewInt(0),
		BaseGas:        big.NewInt(0),
		GasPrice:       big.NewInt(0),
		GasToken:       common.Address{},
		RefundReceiver: common.Address{},
		Nonce:          new(big.Int).SetUint64(nonce),
	}
}

// EIP-712 type hashes (Safe contracts v1.3.0+, which bind chainId in the domain).
var (
	// keccak256("EIP712Domain(uint256 chainId,address verifyingContract)")
	domainSeparatorTypehash = crypto.Keccak256Hash([]byte("EIP712Domain(uint256 chainId,address verifyingContract)"))
	// keccak256("SafeTx(address to,uint256 value,bytes data,uint8 operation,uint256 safeTxGas,uint256 baseGas,uint256 gasPrice,address gasToken,address refundReceiver,uint256 nonce)")
	safeTxTypehash = crypto.Keccak256Hash([]byte("SafeTx(address to,uint256 value,bytes data,uint8 operation,uint256 safeTxGas,uint256 baseGas,uint256 gasPrice,address gasToken,address refundReceiver,uint256 nonce)"))
)

// domainSeparator computes the EIP-712 domain separator for a Safe on a chain.
func domainSeparator(chainID *big.Int, safe common.Address) common.Hash {
	enc := make([]byte, 0, 3*32)
	enc = append(enc, domainSeparatorTypehash.Bytes()...)
	enc = append(enc, word(chainID)...)
	enc = append(enc, addrWord(safe)...)
	return crypto.Keccak256Hash(enc)
}

// LocalHash computes the safeTxHash locally via EIP-712 (no network). It matches
// the on-chain getTransactionHash for Safe v1.3.0+ contracts, and is used both as
// a cross-check against the on-chain value and for offline display.
func (t SafeTx) LocalHash(chainID *big.Int, safe common.Address) common.Hash {
	dataHash := crypto.Keccak256(t.Data) // keccak256("") is well-defined for empty data

	enc := make([]byte, 0, 11*32)
	enc = append(enc, safeTxTypehash.Bytes()...)
	enc = append(enc, addrWord(t.To)...)
	enc = append(enc, word(t.Value)...)
	enc = append(enc, dataHash...)
	enc = append(enc, uint8Word(uint8(t.Operation))...)
	enc = append(enc, word(t.SafeTxGas)...)
	enc = append(enc, word(t.BaseGas)...)
	enc = append(enc, word(t.GasPrice)...)
	enc = append(enc, addrWord(t.GasToken)...)
	enc = append(enc, addrWord(t.RefundReceiver)...)
	enc = append(enc, word(t.Nonce)...)
	structHash := crypto.Keccak256(enc)

	pre := make([]byte, 0, 2+32+32)
	pre = append(pre, 0x19, 0x01)
	pre = append(pre, domainSeparator(chainID, safe).Bytes()...)
	pre = append(pre, structHash...)
	return crypto.Keccak256Hash(pre)
}

// OnChainHash asks the Safe contract for the canonical safeTxHash via
// getTransactionHash. This is authoritative across Safe versions (older Safes use
// a different domain separator than LocalHash assumes), so it is the value owners
// actually sign.
func (t SafeTx) OnChainHash(ctx context.Context, client rpc.Client, safe common.Address) (common.Hash, error) {
	out, err := callView(ctx, client, safe, "getTransactionHash",
		t.To, t.Value, t.Data, uint8(t.Operation), t.SafeTxGas, t.BaseGas,
		t.GasPrice, t.GasToken, t.RefundReceiver, t.Nonce)
	if err != nil {
		return common.Hash{}, err
	}
	var h [32]byte
	if err := safeABI.UnpackIntoInterface(&h, "getTransactionHash", out); err != nil {
		return common.Hash{}, fmt.Errorf("decode getTransactionHash: %w", err)
	}
	return common.BytesToHash(h[:]), nil
}

// word left-pads a non-negative big.Int to a 32-byte EVM word.
func word(v *big.Int) []byte {
	if v == nil {
		v = big.NewInt(0)
	}
	return common.LeftPadBytes(v.Bytes(), 32)
}

// addrWord left-pads a 20-byte address to a 32-byte EVM word.
func addrWord(a common.Address) []byte {
	return common.LeftPadBytes(a.Bytes(), 32)
}

// uint8Word encodes a uint8 as a 32-byte EVM word.
func uint8Word(v uint8) []byte {
	return common.LeftPadBytes([]byte{v}, 32)
}
