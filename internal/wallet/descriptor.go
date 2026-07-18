// Package wallet manages the persistent registry of wallet *descriptors* and,
// in later phases, the mapping from a selected descriptor to a live signer.
//
// A Descriptor is inert, persisted metadata — it never contains key material.
// This separation is deliberate (see DESIGN.md / PRINCIPLES.md): a wallet can be
// listed and selected even when its signer is not connected or unlocked, and the
// on-disk config can be inspected freely without exposing secrets.
package wallet

import (
	"fmt"
	"strings"
)

// SignerKind identifies how a wallet's transactions are signed. It mirrors
// signer.Kind but is duplicated here as a plain string type so the persisted
// descriptor schema does not depend on the live signer package.
type SignerKind string

const (
	KindHot     SignerKind = "hot"     // in-memory seed-derived key
	KindLedger  SignerKind = "ledger"  // hardware
	KindTrezor  SignerKind = "trezor"  // hardware
	KindLattice SignerKind = "lattice" // hardware (GridPlus), best-effort
)

// Valid reports whether k is a known signer kind.
func (k SignerKind) Valid() bool {
	switch k {
	case KindHot, KindLedger, KindTrezor, KindLattice:
		return true
	default:
		return false
	}
}

// Descriptor is persisted, non-secret metadata for one wallet.
//
// For HD signers, DerivationPath records which account was selected (e.g.
// "m/44'/60'/0'/0/0"); the seed/private key is never stored. Address is the
// checksummed EOA address so the wallet is identifiable while locked.
type Descriptor struct {
	ID             string     `json:"id"`              // stable local identifier
	Label          string     `json:"label"`           // user-facing name
	Address        string     `json:"address"`         // EIP-55 checksummed EOA address
	Kind           SignerKind `json:"kind"`            // how it signs
	DerivationPath string     `json:"derivation_path"` // for HD/hardware signers
}

// Validate checks that a descriptor is well-formed enough to persist. It does
// not verify checksum correctness (that is the address package's job at the
// point of entry) — only structural completeness.
func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("wallet id is required")
	}
	if strings.TrimSpace(d.Address) == "" {
		return fmt.Errorf("wallet address is required")
	}
	if !d.Kind.Valid() {
		return fmt.Errorf("unknown signer kind %q", d.Kind)
	}
	return nil
}

// IsHardware reports whether this wallet is backed by a hardware signer.
func (d Descriptor) IsHardware() bool {
	return d.Kind == KindLedger || d.Kind == KindTrezor || d.Kind == KindLattice
}
