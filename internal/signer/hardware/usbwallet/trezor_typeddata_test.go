package usbwallet

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestEncodeEthereumSignTypedHash(t *testing.T) {
	path := []uint32{0x80000000 + 44, 0x80000000 + 60, 0x80000000, 0, 0}
	domainHash := bytes.Repeat([]byte{0xa1}, 32)
	messageHash := bytes.Repeat([]byte{0xb2}, 32)

	enc := encodeEthereumSignTypedHash(path, domainHash, messageHash)
	bf, vf := decodeFields(t, enc)

	// 1: address_n (repeated uint32)
	if got := vf[1]; len(got) != len(path) {
		t.Fatalf("address_n count = %d, want %d", len(got), len(path))
	} else {
		for i := range path {
			if got[i] != uint64(path[i]) {
				t.Errorf("address_n[%d] = %d, want %d", i, got[i], path[i])
			}
		}
	}
	// 2: domain_separator_hash
	assertBytesField(t, bf, 2, domainHash)
	// 3: message_hash
	assertBytesField(t, bf, 3, messageHash)
}

func TestDecodeTypedDataSignature(t *testing.T) {
	sig := bytes.Repeat([]byte{0x7c}, 65)
	// Build an EthereumTypedDataSignature: field 1 = signature (bytes), field 2 =
	// address (string) — which must be skipped.
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendBytes(b, sig)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, "0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	got, err := decodeTypedDataSignature(b)
	if err != nil {
		t.Fatalf("decodeTypedDataSignature: %v", err)
	}
	if !bytes.Equal(got, sig) {
		t.Errorf("signature mismatch: %x", got)
	}
}

func TestDecodeTypedDataSignatureMissing(t *testing.T) {
	// Only an address field, no signature → error.
	var b []byte
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, "0xabc")
	if _, err := decodeTypedDataSignature(b); err == nil {
		t.Error("expected an error when the signature field is missing")
	}
}
