// Package ens resolves Ethereum Name Service names both ways:
//
//   - forward  (name -> address) for address input fields, and
//   - reverse  (address -> name) for display, where any shown address is
//     replaced by its primary ENS name when one is set.
//
// It is implemented directly on top of the rpc.Client CallContract method (no
// third-party ENS dependency, per PRINCIPLES.md) so it is fully mockable and adds
// no new modules. Reverse resolution is always forward-verified: a claimed
// primary name is only trusted if that name resolves back to the same address
// (an unverified reverse record is treated as "no name").
package ens

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/rpc"
	"codeberg.org/pasiphae/callisto/internal/textsafe"
)

// ErrNotFound means the name/address has no (verified) ENS record.
var ErrNotFound = errors.New("no ENS record")

// registryAddress is the ENS registry (ENSRegistryWithFallback), deployed at the
// same address on Ethereum mainnet, Sepolia, Holesky, and Goerli. Chains without
// a registry here simply resolve to ErrNotFound.
var registryAddress = common.HexToAddress("0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e")

// ensABI covers the few methods we call across the registry and resolvers.
const ensABI = `[
  {"name":"resolver","stateMutability":"view","type":"function","inputs":[{"name":"node","type":"bytes32"}],"outputs":[{"name":"","type":"address"}]},
  {"name":"addr","stateMutability":"view","type":"function","inputs":[{"name":"node","type":"bytes32"}],"outputs":[{"name":"","type":"address"}]},
  {"name":"name","stateMutability":"view","type":"function","inputs":[{"name":"node","type":"bytes32"}],"outputs":[{"name":"","type":"string"}]}
]`

// parsedABI is initialized as a var (not in init()) so that any package-level
// vars depending on it — e.g. precomputed method selectors in tests — see a
// populated ABI, since init() runs only after all var initializers.
var parsedABI = mustParseABI(ensABI)

func mustParseABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("ens: bad built-in ABI: " + err.Error())
	}
	return a
}

// Resolver performs ENS lookups over a single RPC client. Construct a new one
// when the active connection changes (see NewResolver). It holds no mutable
// state beyond the client, so it is safe for concurrent use.
type Resolver struct {
	client rpc.Client
}

// NewResolver returns a Resolver bound to the given client.
func NewResolver(client rpc.Client) *Resolver {
	return &Resolver{client: client}
}

// Resolve performs forward resolution (name -> address). It returns ErrNotFound
// if the name has no resolver or no address record.
func (r *Resolver) Resolve(ctx context.Context, name string) (common.Address, error) {
	name = normalize(name)
	if name == "" {
		return common.Address{}, ErrNotFound
	}
	node := NameHash(name)

	resolverAddr, err := r.resolverOf(ctx, node)
	if err != nil {
		return common.Address{}, err
	}

	out, err := r.call(ctx, resolverAddr, "addr", node)
	if err != nil {
		return common.Address{}, ErrNotFound
	}
	var addr common.Address
	if err := parsedABI.UnpackIntoInterface(&addr, "addr", out); err != nil {
		return common.Address{}, ErrNotFound
	}
	if addr == (common.Address{}) {
		return common.Address{}, ErrNotFound
	}
	return addr, nil
}

// ReverseResolve performs reverse resolution (address -> primary name), then
// forward-verifies it. Returns ErrNotFound if there is no name or the reverse
// record does not forward-resolve back to addr.
func (r *Resolver) ReverseResolve(ctx context.Context, addr common.Address) (string, error) {
	if addr == (common.Address{}) {
		return "", ErrNotFound
	}
	reverseName := reverseNode(addr)
	node := NameHash(reverseName)

	resolverAddr, err := r.resolverOf(ctx, node)
	if err != nil {
		return "", err
	}

	out, err := r.call(ctx, resolverAddr, "name", node)
	if err != nil {
		return "", ErrNotFound
	}
	var name string
	if err := parsedABI.UnpackIntoInterface(&name, "name", out); err != nil {
		return "", ErrNotFound
	}
	if name == "" {
		return "", ErrNotFound
	}

	// Forward-verify with the raw name (namehash must use the exact record), then
	// sanitize only the value we return for display — strips bidi/zero-width/control
	// characters a spoofing name could carry.
	fwd, err := r.Resolve(ctx, name)
	if err != nil || fwd != addr {
		return "", ErrNotFound
	}
	return textsafe.Display(name), nil
}

// resolverOf returns the resolver contract for a node, or ErrNotFound if none.
func (r *Resolver) resolverOf(ctx context.Context, node [32]byte) (common.Address, error) {
	out, err := r.call(ctx, registryAddress, "resolver", node)
	if err != nil {
		return common.Address{}, ErrNotFound
	}
	var resolverAddr common.Address
	if err := parsedABI.UnpackIntoInterface(&resolverAddr, "resolver", out); err != nil {
		return common.Address{}, ErrNotFound
	}
	if resolverAddr == (common.Address{}) {
		return common.Address{}, ErrNotFound
	}
	return resolverAddr, nil
}

// call packs and eth_calls a method on a contract at the latest block.
func (r *Resolver) call(ctx context.Context, to common.Address, method string, node [32]byte) ([]byte, error) {
	if r.client == nil {
		return nil, errors.New("ens: no client")
	}
	input, err := parsedABI.Pack(method, node)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}
	msg := ethCallMsg(to, input)
	out, err := r.client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// reverseNode builds the reverse-resolution name for an address:
// "<lowercase-hex-without-0x>.addr.reverse".
func reverseNode(addr common.Address) string {
	return strings.ToLower(addr.Hex()[2:]) + ".addr.reverse"
}

// normalize applies minimal ENS name normalization. Full ENSIP-15 (UTS-46)
// normalization is a future improvement; lowercasing + trimming handles the
// common ASCII case correctly and safely.
func normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// LooksLikeENS reports whether an input string is plausibly an ENS name (has a
// dot and is not a hex address), used by the UI to decide whether to attempt
// forward resolution.
func LooksLikeENS(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return false
	}
	return strings.Contains(s, ".")
}
