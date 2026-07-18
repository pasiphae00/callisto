package safe

import (
	"fmt"
	"strings"
)

// OwnerLabel is a Safe owner address plus an optional client-side label. Labels
// are Callisto-local (never on-chain); the address is the source of truth.
type OwnerLabel struct {
	Address string `json:"address"` // EIP-55 checksummed
	Label   string `json:"label"`
}

// Descriptor is persisted, non-secret metadata for one imported Safe. The owners,
// threshold, and version are cached so the Safe can be listed and labeled while
// offline; they are refreshed from chain whenever a connection is available. Like
// wallet.Descriptor, it holds no key material.
type Descriptor struct {
	ID        string       `json:"id"`        // stable local identifier
	Label     string       `json:"label"`     // user-facing name
	Address   string       `json:"address"`   // EIP-55 checksummed Safe address
	ChainID   uint64       `json:"chain_id"`  // the chain the Safe lives on
	Threshold uint64       `json:"threshold"` // cached signatures-required
	Version   string       `json:"version"`   // cached contract version
	Owners    []OwnerLabel `json:"owners"`    // cached owners + client-side labels
}

// Validate checks structural completeness for persistence (not on-chain validity).
func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("safe id is required")
	}
	if strings.TrimSpace(d.Address) == "" {
		return fmt.Errorf("safe address is required")
	}
	if d.ChainID == 0 {
		return fmt.Errorf("safe chain id is required")
	}
	return nil
}

// OwnerLabelFor returns the client-side label for an owner address (case-tolerant
// on the "0x" hex), or "" if none is set.
func (d Descriptor) OwnerLabelFor(address string) string {
	want := strings.ToLower(strings.TrimSpace(address))
	for _, o := range d.Owners {
		if strings.ToLower(o.Address) == want {
			return o.Label
		}
	}
	return ""
}
