package safe

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func sampleProposal() Proposal {
	return Proposal{
		SafeAddress: common.HexToAddress("0x0322D57e4369dBB2C612E2E0f0B72668c854E7B9"),
		ChainID:     1,
		To:          common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
		Value:       big.NewInt(0),
		Data:        []byte{0x01, 0x02, 0x03},
		Operation:   Call,
		SafeNonce:   45,
		SafeTxHash:  common.HexToHash("0xdeadbeef"),
		Kind:        KindTransfer,
		Description: "Transfer 0.001 AAVE to 0x0322…E7B9",
		Status:      StatusCollecting,
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	p := sampleProposal()
	env := ExportEnvelope(p)

	for _, form := range []string{"json", "text"} {
		var raw []byte
		if form == "json" {
			b, err := env.EncodeJSON()
			if err != nil {
				t.Fatalf("EncodeJSON: %v", err)
			}
			raw = b
		} else {
			s, err := env.EncodeText()
			if err != nil {
				t.Fatalf("EncodeText: %v", err)
			}
			raw = []byte(s)
		}
		got, err := DecodeEnvelope(raw)
		if err != nil {
			t.Fatalf("DecodeEnvelope(%s): %v", form, err)
		}
		if got.SafeAddress != env.SafeAddress || got.SafeNonce != 45 || got.Value != "0" || got.Description != p.Description {
			t.Errorf("%s round-trip mismatch: %+v", form, got)
		}
		tx, err := got.SafeTx()
		if err != nil {
			t.Fatalf("%s SafeTx: %v", form, err)
		}
		if tx.To != p.To || len(tx.Data) != 3 || tx.Nonce.Uint64() != 45 {
			t.Errorf("%s reconstructed SafeTx wrong: %+v", form, tx)
		}
	}
}

func TestRecoverAndFilterSignatures(t *testing.T) {
	ownerKey, _ := crypto.GenerateKey()
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	strangerKey, _ := crypto.GenerateKey()
	stranger := crypto.PubkeyToAddress(strangerKey.PublicKey)

	hash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	// Direct-hash signature (v 27/28), as Callisto produces.
	sig, err := crypto.Sign(hash.Bytes(), ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27

	rec, err := RecoverSafeSigner(hash, sig)
	if err != nil {
		t.Fatalf("RecoverSafeSigner: %v", err)
	}
	if rec != owner {
		t.Errorf("recovered %s, want owner %s", rec, owner)
	}

	// eth_sign form (v 31/32): signed over the EIP-191 wrap.
	ethSig, err := crypto.Sign(accounts.TextHash(hash.Bytes()), ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	ethSig[64] += 31
	if rec2, err := RecoverSafeSigner(hash, ethSig); err != nil || rec2 != owner {
		t.Errorf("eth_sign recover = %s, %v; want %s", rec2, err, owner)
	}

	// A stranger's signature must be rejected by the owner filter; the owner's kept,
	// and the stored signer is the recovered address (not the advisory one).
	strSig, _ := crypto.Sign(hash.Bytes(), strangerKey)
	strSig[64] += 27
	input := []Signature{
		{Signer: common.Address{}, Sig: sig},    // advisory signer deliberately wrong (zero)
		{Signer: owner, Sig: strSig},             // advisory claims owner, but signed by stranger
	}
	valid, rejected := FilterOwnerSignatures(hash, input, []common.Address{owner})
	if len(valid) != 1 {
		t.Fatalf("valid = %d, want 1", len(valid))
	}
	if valid[0].Signer != owner {
		t.Errorf("stored signer = %s, want recovered owner %s", valid[0].Signer, owner)
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1 (the stranger)", rejected)
	}
	_ = stranger
}
