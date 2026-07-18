package safe

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
)

// SignatureLen is the byte length of a single Safe owner signature (r||s||v).
const SignatureLen = 65

// EncodeExec builds the execTransaction calldata for a Safe transaction with the
// given packed signatures. The result is sent as a normal EOA transaction to the
// Safe address by the executing owner.
func EncodeExec(t SafeTx, packedSignatures []byte) ([]byte, error) {
	return safeABI.Pack("execTransaction",
		t.To, t.Value, t.Data, uint8(t.Operation), t.SafeTxGas, t.BaseGas,
		t.GasPrice, t.GasToken, t.RefundReceiver, packedSignatures)
}

// EncodeAddOwner builds addOwnerWithThreshold(owner, threshold) calldata. This is
// the inner call of a Safe transaction whose To is the Safe itself.
func EncodeAddOwner(owner common.Address, threshold uint64) ([]byte, error) {
	return safeABI.Pack("addOwnerWithThreshold", owner, new(big.Int).SetUint64(threshold))
}

// EncodeRemoveOwner builds removeOwner(prevOwner, owner, threshold) calldata.
// prevOwner is the owner linked-list predecessor (see PrevOwner).
func EncodeRemoveOwner(prevOwner, owner common.Address, threshold uint64) ([]byte, error) {
	return safeABI.Pack("removeOwner", prevOwner, owner, new(big.Int).SetUint64(threshold))
}

// EncodeSwapOwner builds swapOwner(prevOwner, oldOwner, newOwner) calldata.
func EncodeSwapOwner(prevOwner, oldOwner, newOwner common.Address) ([]byte, error) {
	return safeABI.Pack("swapOwner", prevOwner, oldOwner, newOwner)
}

// EncodeChangeThreshold builds changeThreshold(threshold) calldata.
func EncodeChangeThreshold(threshold uint64) ([]byte, error) {
	return safeABI.Pack("changeThreshold", new(big.Int).SetUint64(threshold))
}

// PrevOwner returns the predecessor of target in the Safe's owner linked list, as
// required by removeOwner and swapOwner. The list is ordered as getOwners returns
// it; the first owner's predecessor is the sentinel (address(0x1)). It returns an
// error if target is not in owners.
func PrevOwner(owners []common.Address, target common.Address) (common.Address, error) {
	for i, o := range owners {
		if o == target {
			if i == 0 {
				return SentinelOwner, nil
			}
			return owners[i-1], nil
		}
	}
	return common.Address{}, fmt.Errorf("owner %s not found in Safe owner list", target.Hex())
}

// PackSignatures concatenates owner signatures in ascending signer-address order,
// as the Safe contract requires when validating a threshold of signatures. Each
// signature must be exactly 65 bytes. Duplicate signers are rejected.
func PackSignatures(sigs map[common.Address][]byte) ([]byte, error) {
	addrs := make([]common.Address, 0, len(sigs))
	for a := range sigs {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i].Bytes(), addrs[j].Bytes()) < 0
	})

	out := make([]byte, 0, len(addrs)*SignatureLen)
	for _, a := range addrs {
		sig := sigs[a]
		if len(sig) != SignatureLen {
			return nil, fmt.Errorf("signature for %s is %d bytes, want %d", a.Hex(), len(sig), SignatureLen)
		}
		out = append(out, sig...)
	}
	return out, nil
}
