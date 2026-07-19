package signer

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// TypedDataHashes parses an EIP-712 typed-data document and returns its domain
// separator hash, message struct hash, and the final signing digest
// (keccak256("\x19\x01" || domainHash || messageHash)). Hot wallets sign the
// digest directly; hardware wallets need the two component hashes for the device.
func TypedDataHashes(typedDataJSON []byte) (domainHash, messageHash, digest []byte, err error) {
	var td apitypes.TypedData
	if err = json.Unmarshal(typedDataJSON, &td); err != nil {
		return nil, nil, nil, fmt.Errorf("signer: parse typed data: %w", err)
	}
	dh, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("signer: hash domain: %w", err)
	}
	mh, err := td.HashStruct(td.PrimaryType, td.Message)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("signer: hash message: %w", err)
	}
	pre := append([]byte{0x19, 0x01}, dh...)
	pre = append(pre, mh...)
	return dh, mh, crypto.Keccak256(pre), nil
}
