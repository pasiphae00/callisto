// Package signer defines the common signing interface that every wallet type
// (hot, Ledger, Trezor, Lattice) implements. The rest of Callisto prepares and
// reviews transactions against this interface and never touches key material
// directly — it only calls SignTx. This is the seam that lets new signer types be
// added without changes to transaction preparation, review, or broadcast.
package signer

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Kind identifies a signer's backing wallet type.
type Kind string

const (
	KindHot     Kind = "hot"
	KindLedger  Kind = "ledger"
	KindTrezor  Kind = "trezor"
	KindLattice Kind = "lattice"
)

// Signer produces signatures for prepared transactions using the account it
// represents. Implementations must be safe for concurrent use.
type Signer interface {
	// Address is the EOA this signer signs for.
	Address() common.Address
	// SignTx returns a signed copy of tx for the given chain. It must not mutate
	// the input transaction.
	SignTx(ctx context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
	// Kind reports the backing wallet type.
	Kind() Kind
}

// Lockable is implemented by signers holding sensitive in-memory material (hot
// wallets). Lock wipes that material; after Lock the signer must refuse to sign.
// Lock must be idempotent.
type Lockable interface {
	Lock()
}

// SafeHashSigner is implemented by signers that can produce an owner signature
// over a Safe transaction hash (safeTxHash), used to collect signatures on a Safe
// multisig proposal. It is an optional capability (type-asserted, like Lockable),
// so signer kinds that cannot do it — e.g. the Lattice stub — simply don't
// implement it.
//
// The returned signature is 65 bytes (r||s||v) in the format the Safe contract's
// checkSignatures expects. v may be 27/28 when the signer signs the safeTxHash
// digest directly (hot wallets), or 31/32 when it signs via the eth_sign /
// personal-message route (hardware wallets); the Safe contract validates both.
type SafeHashSigner interface {
	SignSafeTxHash(ctx context.Context, safeTxHash common.Hash) ([]byte, error)
}
