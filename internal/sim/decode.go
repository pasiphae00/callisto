package sim

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Event signatures and sentinels below are deliberately recomputed here rather
// than imported from internal/approvals: they're fixed, well-known constants
// (a hash of a fixed string, or a canonical deployment address), so duplication
// carries no drift risk, and it keeps this package decoupled from the
// getLogs-scan-shaped approvals code it doesn't otherwise need. See
// docs/transaction-simulation.md's "Computing human-readable changes" section.
var (
	transferSig        = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	approvalSig        = crypto.Keccak256Hash([]byte("Approval(address,address,uint256)"))
	permit2ApprovalSig = crypto.Keccak256Hash([]byte("Approval(address,address,address,uint160,uint48)"))
	permit2Address     = common.HexToAddress("0x000000000022D473030F116dDEE9F6B43aC78BA3")

	// unlimitedThreshold: a direct ERC-20 allowance at or above 2^255 is
	// treated as effectively unlimited (same threshold internal/approvals
	// uses for the same reason: dapps commonly request max-uint256 or a huge
	// round number, not literally the max value).
	unlimitedThreshold = new(big.Int).Lsh(big.NewInt(1), 255)
	// maxUint160 is Permit2's actual "infinite allowance" sentinel
	// (type(uint160).max) -- exact, not a threshold.
	maxUint160 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 160), big.NewInt(1))

	permit2DataArgs = abi.Arguments{
		{Type: mustType("uint160")},
		{Type: mustType("uint48")},
	}
)

func mustType(t string) abi.Type {
	typ, err := abi.NewType(t, "", nil)
	if err != nil {
		panic("sim: bad abi type " + t + ": " + err.Error())
	}
	return typ
}

// decodeTransfer reports the signed delta for `account` from an ERC-20
// Transfer log, or ok=false if the log isn't a well-formed Transfer touching
// account.
func decodeTransfer(lg types.Log, account common.Address) (token common.Address, delta *big.Int, ok bool) {
	if lg.Topics[0] != transferSig || len(lg.Topics) < 3 || len(lg.Data) < 32 {
		return common.Address{}, nil, false
	}
	from := common.BytesToAddress(lg.Topics[1].Bytes())
	to := common.BytesToAddress(lg.Topics[2].Bytes())
	value := new(big.Int).SetBytes(lg.Data[:32])
	switch account {
	case from:
		return lg.Address, new(big.Int).Neg(value), true
	case to:
		return lg.Address, value, true
	default:
		return common.Address{}, nil, false
	}
}

// decodeApproval reports an ERC-20 Approval log as an ApprovalChange, or
// ok=false if malformed.
func decodeApproval(lg types.Log) (owner common.Address, ac ApprovalChange, ok bool) {
	if lg.Topics[0] != approvalSig || len(lg.Topics) < 3 || len(lg.Data) < 32 {
		return common.Address{}, ApprovalChange{}, false
	}
	owner = common.BytesToAddress(lg.Topics[1].Bytes())
	spender := common.BytesToAddress(lg.Topics[2].Bytes())
	amount := new(big.Int).SetBytes(lg.Data[:32])
	return owner, ApprovalChange{
		Token:     lg.Address,
		Spender:   spender,
		Unlimited: amount.Cmp(unlimitedThreshold) >= 0,
		Amount:    amount,
	}, true
}

// decodePermit2Approval reports a Permit2 Approval log (owner/token/spender
// indexed, amount+expiration in data) as an ApprovalChange, or ok=false if
// malformed or not from the canonical Permit2 contract.
func decodePermit2Approval(lg types.Log) (owner common.Address, ac ApprovalChange, ok bool) {
	if lg.Address != permit2Address || lg.Topics[0] != permit2ApprovalSig || len(lg.Topics) < 4 {
		return common.Address{}, ApprovalChange{}, false
	}
	owner = common.BytesToAddress(lg.Topics[1].Bytes())
	token := common.BytesToAddress(lg.Topics[2].Bytes())
	spender := common.BytesToAddress(lg.Topics[3].Bytes())
	vals, err := permit2DataArgs.Unpack(lg.Data)
	if err != nil || len(vals) < 1 {
		return common.Address{}, ApprovalChange{}, false
	}
	amount, ok := vals[0].(*big.Int)
	if !ok {
		return common.Address{}, ApprovalChange{}, false
	}
	return owner, ApprovalChange{
		Token:     token,
		Spender:   spender,
		Unlimited: amount.Cmp(maxUint160) == 0,
		Amount:    amount,
	}, true
}

