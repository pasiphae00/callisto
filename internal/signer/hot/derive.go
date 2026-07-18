// Package hot implements the in-memory, seed-derived ("hot wallet") signer.
//
// HD derivation (BIP-32 / BIP-44) is implemented here directly on the secp256k1
// scalar primitives that go-ethereum already vendors (decred/dcrd secp256k1),
// rather than pulling in btcutil/hdkeychain — which would drag a personal-fork
// transitive dependency into the signing path. See PRINCIPLES.md.
package hot

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// hardenedOffset marks a hardened derivation index (BIP-32).
const hardenedOffset uint32 = 0x80000000

var (
	// errInvalidSeed means the master key derived from the seed is not a valid
	// secp256k1 scalar (astronomically unlikely with a real seed).
	errInvalidSeed = errors.New("hot: invalid seed (degenerate master key)")
	// errInvalidChild means a child index produced an invalid key; per BIP-32 the
	// caller should proceed to the next index (probability ~2^-127).
	errInvalidChild = errors.New("hot: invalid child key at index")
)

// extKey is a BIP-32 extended private key: the 32-byte key plus its chain code.
// Only the private material is retained (Callisto never needs the xpub).
type extKey struct {
	key       [32]byte
	chainCode [32]byte
}

// wipe zeroes the secret material of an extended key.
func (k *extKey) wipe() {
	zero(k.key[:])
	zero(k.chainCode[:])
}

// masterKey derives the BIP-32 master extended key from a BIP-39 seed.
func masterKey(seed []byte) (*extKey, error) {
	sum := hmac512([]byte("Bitcoin seed"), seed)
	var k extKey
	copy(k.key[:], sum[:32])
	copy(k.chainCode[:], sum[32:])
	zero(sum)

	// The master private key must be a valid scalar in [1, n-1].
	var s secp256k1.ModNScalar
	if s.SetByteSlice(k.key[:]) || s.IsZero() { // overflow (>= n) or zero
		k.wipe()
		return nil, errInvalidSeed
	}
	s.Zero()
	return &k, nil
}

// child derives the child extended key at the given index (>= hardenedOffset for
// hardened derivation), per BIP-32 CKDpriv.
func (k *extKey) child(index uint32) (*extKey, error) {
	var data []byte
	if index >= hardenedOffset {
		// Hardened: 0x00 || ser256(kpar) || ser32(i)
		data = make([]byte, 0, 1+32+4)
		data = append(data, 0x00)
		data = append(data, k.key[:]...)
	} else {
		// Normal: serP(point(kpar)) || ser32(i) — compressed parent public key.
		priv := secp256k1.PrivKeyFromBytes(k.key[:])
		data = append([]byte{}, priv.PubKey().SerializeCompressed()...)
		priv.Zero()
	}
	data = append(data, ser32(index)...)

	I := hmac512(k.chainCode[:], data)
	zero(data)
	defer zero(I)

	// IL must be a valid scalar; ki = (IL + kpar) mod n, and must be non-zero.
	var il secp256k1.ModNScalar
	if il.SetByteSlice(I[:32]) { // IL >= n -> invalid child
		return nil, errInvalidChild
	}
	var kpar secp256k1.ModNScalar
	kpar.SetByteSlice(k.key[:])
	il.Add(&kpar)
	kpar.Zero()
	if il.IsZero() {
		il.Zero()
		return nil, errInvalidChild
	}

	var child extKey
	child.key = il.Bytes()
	copy(child.chainCode[:], I[32:])
	il.Zero()
	return &child, nil
}

// derivePath derives the extended key at a full BIP-32 path (as parsed indices)
// from a seed. The returned key's secret material must be wiped by the caller.
func derivePath(seed []byte, indices []uint32) (*extKey, error) {
	k, err := masterKey(seed)
	if err != nil {
		return nil, err
	}
	for _, idx := range indices {
		next, err := k.child(idx)
		k.wipe() // wipe the parent as we descend
		if err != nil {
			return nil, err
		}
		k = next
	}
	return k, nil
}

// parsePath parses a BIP-32 path like "m/44'/60'/0'/0/0" into indices. A trailing
// apostrophe or "h" marks a hardened component.
func parsePath(path string) ([]uint32, error) {
	parts := strings.Split(strings.TrimSpace(path), "/")
	if len(parts) == 0 || parts[0] != "m" {
		return nil, fmt.Errorf("hot: path must start with \"m\": %q", path)
	}
	indices := make([]uint32, 0, len(parts)-1)
	for _, p := range parts[1:] {
		if p == "" {
			return nil, fmt.Errorf("hot: empty path component in %q", path)
		}
		hardened := false
		if last := p[len(p)-1]; last == '\'' || last == 'h' || last == 'H' {
			hardened = true
			p = p[:len(p)-1]
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("hot: bad path component %q: %w", p, err)
		}
		if n >= uint64(hardenedOffset) {
			return nil, fmt.Errorf("hot: path index %d out of range", n)
		}
		idx := uint32(n)
		if hardened {
			idx += hardenedOffset
		}
		indices = append(indices, idx)
	}
	return indices, nil
}

// DefaultPath returns the standard Ethereum BIP-44 path for account index i:
// m/44'/60'/0'/0/i.
func DefaultPath(index uint32) string {
	return fmt.Sprintf("m/44'/60'/0'/0/%d", index)
}

// ser32 serializes a uint32 as 4 big-endian bytes.
func ser32(i uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, i)
	return b
}

// hmac512 computes HMAC-SHA512(key, data).
func hmac512(key, data []byte) []byte {
	h := hmac.New(sha512.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// zero overwrites a byte slice with zeros (best-effort key-material hygiene).
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
