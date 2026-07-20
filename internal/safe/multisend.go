package safe

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// multiSendCallOnly is the canonical Safe MultiSendCallOnly v1.3.0 deployment. The
// "CallOnly" variant reverts if any bundled sub-call uses DELEGATECALL, so batching
// through it can only ever perform plain Calls — the safe choice for approve+action
// bundles. It has the same address across the chains Safe supports.
var multiSendCallOnly = common.HexToAddress("0x40A2aCCbd92BCA938b02010E17A5b8929b49130D")

var multiSendABI = mustABI(`[{"name":"multiSend","type":"function","stateMutability":"payable","inputs":[{"name":"transactions","type":"bytes"}],"outputs":[]}]`)

// MultiSendCall is one plain Call in a MultiSend batch.
type MultiSendCall struct {
	To    common.Address
	Value *big.Int
	Data  []byte
}

// BuildMultiSend packs calls into a MultiSend "transactions" blob and returns a SafeTx
// that executes them atomically. The SafeTx targets MultiSendCallOnly with
// operation=DelegateCall, so the batched calls originate from the Safe itself and
// either all succeed or all revert.
//
// Each entry is packed as: operation(1) ‖ to(20) ‖ value(32) ‖ dataLength(32) ‖ data,
// with operation fixed to 0 (Call) — MultiSendCallOnly rejects anything else.
func BuildMultiSend(calls []MultiSendCall, nonce uint64) (SafeTx, error) {
	if len(calls) == 0 {
		return SafeTx{}, fmt.Errorf("safe: empty MultiSend batch")
	}
	var packed []byte
	for _, c := range calls {
		value := c.Value
		if value == nil {
			value = big.NewInt(0)
		}
		packed = append(packed, 0) // operation = Call
		packed = append(packed, c.To.Bytes()...)
		packed = append(packed, common.LeftPadBytes(value.Bytes(), 32)...)
		packed = append(packed, common.LeftPadBytes(big.NewInt(int64(len(c.Data))).Bytes(), 32)...)
		packed = append(packed, c.Data...)
	}
	data, err := multiSendABI.Pack("multiSend", packed)
	if err != nil {
		return SafeTx{}, fmt.Errorf("safe: pack multiSend: %w", err)
	}
	tx := NewSafeTx(multiSendCallOnly, big.NewInt(0), data, nonce)
	tx.Operation = DelegateCall
	return tx, nil
}
