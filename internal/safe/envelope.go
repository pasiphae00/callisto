package safe

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"codeberg.org/pasiphae/callisto/internal/textsafe"
)

// EnvelopeVersion is the portable-proposal format version.
const EnvelopeVersion = 1

// envelopeTextPrefix marks a copy-paste (base64) envelope so it is easy to detect
// and paste alongside other text.
const envelopeTextPrefix = "callisto-safe-proposal-v1:"

// Envelope is a self-contained, portable representation of a Safe proposal plus any
// collected owner signatures, for sharing between co-owners on different machines
// (Callisto has no Safe transaction service). It carries the SafeTx *fields* so the
// importer can recompute the safeTxHash from scratch — the SafeTxHash and Signatures
// here are never trusted: the importer re-derives the hash and re-recovers every
// signer from it, keeping only signatures that recover to a current owner.
type Envelope struct {
	Version     int          `json:"version"`
	SafeAddress string       `json:"safe"`         // EIP-55
	ChainID     uint64       `json:"chain_id"`
	To          string       `json:"to"`           // EIP-55
	Value       string       `json:"value"`        // decimal string (wei / base units)
	Data        string       `json:"data"`         // 0x-hex ("0x" if none)
	Operation   uint8        `json:"operation"`    // 0 = Call (only supported value)
	SafeNonce   uint64       `json:"safe_nonce"`
	Kind        ProposalKind `json:"kind"`
	Description string       `json:"description"`
	SafeTxHash  string       `json:"safe_tx_hash"` // informational only; recomputed on import
	Signatures  []EnvSig     `json:"signatures"`
}

// EnvSig is one owner signature in an envelope. The signer is advisory (for display
// before verification); the importer uses the address it recovers from the signature.
type EnvSig struct {
	Signer string `json:"signer"` // EIP-55 (advisory)
	Sig    string `json:"sig"`    // 0x-hex, 65 bytes
}

// ExportEnvelope builds a portable envelope from a proposal and its signatures.
func ExportEnvelope(p Proposal) Envelope {
	value := "0"
	if p.Value != nil {
		value = p.Value.String()
	}
	sigs := make([]EnvSig, 0, len(p.Signatures))
	for _, s := range p.Signatures {
		sigs = append(sigs, EnvSig{Signer: s.Signer.Hex(), Sig: hexutil.Encode(s.Sig)})
	}
	return Envelope{
		Version:     EnvelopeVersion,
		SafeAddress: p.SafeAddress.Hex(),
		ChainID:     p.ChainID,
		To:          p.To.Hex(),
		Value:       value,
		Data:        hexutil.Encode(p.Data),
		Operation:   uint8(p.Operation),
		SafeNonce:   p.SafeNonce,
		Kind:        p.Kind,
		Description: p.Description,
		SafeTxHash:  p.SafeTxHash.Hex(),
		Signatures:  sigs,
	}
}

// EncodeJSON renders the envelope as indented JSON, for a shareable file.
func (e Envelope) EncodeJSON() ([]byte, error) { return json.MarshalIndent(e, "", "  ") }

// EncodeText renders the envelope as a single prefixed base64 line, for copy-paste
// (email/chat). DecodeEnvelope accepts either form.
func (e Envelope) EncodeText() (string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return envelopeTextPrefix + base64.StdEncoding.EncodeToString(b), nil
}

// DecodeEnvelope parses either form: raw JSON (from a file) or the prefixed / bare
// base64 text (from copy-paste). It validates the format version.
func DecodeEnvelope(raw []byte) (Envelope, error) {
	s := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(s, envelopeTextPrefix):
		body := strings.TrimSpace(strings.TrimPrefix(s, envelopeTextPrefix))
		dec, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return Envelope{}, fmt.Errorf("safe: decode envelope base64: %w", err)
		}
		raw = dec
	case !strings.HasPrefix(s, "{"):
		// No prefix and not JSON: try bare base64 before giving up.
		if dec, err := base64.StdEncoding.DecodeString(s); err == nil {
			raw = dec
		}
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return Envelope{}, fmt.Errorf("safe: parse envelope: %w", err)
	}
	if e.Version != EnvelopeVersion {
		return Envelope{}, fmt.Errorf("safe: unsupported envelope version %d (need %d)", e.Version, EnvelopeVersion)
	}
	// The description comes from whoever built the envelope (another machine) — sanitize
	// it before it can reach a dialog or the history record.
	e.Description = textsafe.Display(e.Description)
	return e, nil
}

// SafeAddr returns the envelope's Safe address.
func (e Envelope) SafeAddr() common.Address { return common.HexToAddress(e.SafeAddress) }

// SafeTx reconstructs the SafeTx from the envelope fields (for hashing/execution).
func (e Envelope) SafeTx() (SafeTx, error) {
	value, ok := new(big.Int).SetString(e.Value, 10)
	if !ok {
		return SafeTx{}, fmt.Errorf("safe: bad value %q", e.Value)
	}
	var data []byte
	if e.Data != "" && e.Data != "0x" {
		d, err := hexutil.Decode(e.Data)
		if err != nil {
			return SafeTx{}, fmt.Errorf("safe: bad data: %w", err)
		}
		data = d
	}
	if e.Operation != uint8(Call) {
		return SafeTx{}, fmt.Errorf("safe: unsupported operation %d (only Call)", e.Operation)
	}
	return NewSafeTx(common.HexToAddress(e.To), value, data, e.SafeNonce), nil
}

// rawSignatures parses the envelope's signatures into Signature values (advisory
// signer + bytes), skipping malformed ones. The signer is re-derived during
// verification, so a wrong advisory signer here is harmless.
func (e Envelope) rawSignatures() []Signature {
	out := make([]Signature, 0, len(e.Signatures))
	for _, es := range e.Signatures {
		sb, err := hexutil.Decode(es.Sig)
		if err != nil || len(sb) != 65 {
			continue
		}
		out = append(out, Signature{Signer: common.HexToAddress(es.Signer), Sig: sb})
	}
	return out
}

// Proposal builds a Proposal from the envelope using an authoritative safeTxHash
// (computed by the caller from the reconstructed SafeTx — LocalHash offline, or the
// on-chain hash when connected) and a pre-verified set of owner signatures.
func (e Envelope) Proposal(safeTxHash common.Hash, sigs []Signature) (Proposal, error) {
	tx, err := e.SafeTx()
	if err != nil {
		return Proposal{}, err
	}
	return Proposal{
		SafeAddress: e.SafeAddr(),
		ChainID:     e.ChainID,
		To:          tx.To,
		Value:       tx.Value,
		Data:        tx.Data,
		Operation:   Operation(e.Operation),
		SafeNonce:   e.SafeNonce,
		SafeTxHash:  safeTxHash,
		Kind:        e.Kind,
		Description: e.Description,
		Status:      StatusCollecting,
		Signatures:  sigs,
	}, nil
}

// Verify recovers and owner-checks the envelope's signatures against safeTxHash and
// the current owner set, returning the valid (owner-verified, deduped) signatures
// and the number rejected. safeTxHash must be computed by the caller from the
// reconstructed SafeTx (never taken from the envelope's own SafeTxHash field).
func (e Envelope) Verify(safeTxHash common.Hash, owners []common.Address) (valid []Signature, rejected int) {
	return FilterOwnerSignatures(safeTxHash, e.rawSignatures(), owners)
}

// RecoverSafeSigner recovers the address that produced sig over safeTxHash. It
// handles the direct-hash form (v 27/28, which Callisto produces) and the eth_sign
// form (v 31/32, signed over the personal-message wrap) — the two EOA signature
// types the Safe contract's checkSignatures accepts.
func RecoverSafeSigner(safeTxHash common.Hash, sig []byte) (common.Address, error) {
	if len(sig) != 65 {
		return common.Address{}, fmt.Errorf("safe: signature must be 65 bytes, got %d", len(sig))
	}
	digest := safeTxHash.Bytes()
	v := sig[64]
	switch {
	case v == 27 || v == 28:
		v -= 27
	case v == 31 || v == 32: // eth_sign: signed over the EIP-191 wrap, v + 4
		v -= 31
		digest = accounts.TextHash(safeTxHash.Bytes())
	default:
		return common.Address{}, fmt.Errorf("safe: unsupported signature v=%d", sig[64])
	}
	rsv := make([]byte, 65)
	copy(rsv, sig)
	rsv[64] = v
	pub, err := crypto.SigToPub(digest, rsv)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// FilterOwnerSignatures keeps only the signatures that recover to one of owners over
// safeTxHash, deduplicated by the *recovered* owner (first wins). The signer stored
// is the recovered address, never the advisory one — so a forged signer field can't
// smuggle in a non-owner. Returns the valid signatures and how many were rejected
// (malformed, non-owner, or duplicate).
func FilterOwnerSignatures(safeTxHash common.Hash, sigs []Signature, owners []common.Address) (valid []Signature, rejected int) {
	ownerSet := make(map[common.Address]bool, len(owners))
	for _, o := range owners {
		ownerSet[o] = true
	}
	seen := map[common.Address]bool{}
	for _, s := range sigs {
		rec, err := RecoverSafeSigner(safeTxHash, s.Sig)
		if err != nil || !ownerSet[rec] || seen[rec] {
			rejected++
			continue
		}
		seen[rec] = true
		valid = append(valid, Signature{Signer: rec, Sig: s.Sig, SignedAt: s.SignedAt})
	}
	return valid, rejected
}
