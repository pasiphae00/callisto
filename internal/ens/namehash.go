package ens

import (
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// NameHash computes the EIP-137 namehash of an ENS name. The empty name hashes
// to all zeros; each label (right-to-left) is folded in as
// keccak256(parentNode || keccak256(label)).
func NameHash(name string) [32]byte {
	var node [32]byte // zero hash for the root
	name = strings.TrimSpace(name)
	if name == "" {
		return node
	}
	labels := strings.Split(name, ".")
	for i := len(labels) - 1; i >= 0; i-- {
		labelHash := crypto.Keccak256([]byte(labels[i]))
		node = crypto.Keccak256Hash(node[:], labelHash)
	}
	return node
}

// ethCallMsg builds a read-only call message to a contract.
func ethCallMsg(to common.Address, data []byte) ethereum.CallMsg {
	return ethereum.CallMsg{To: &to, Data: data}
}
