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
