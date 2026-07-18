package walletconnect

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

// TestDeriveSymKeySymmetry verifies X25519+HKDF is symmetric (both peers derive
// the same 32-byte session key) — the core correctness property.
func TestDeriveSymKeySymmetry(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	ka, err := a.DeriveSymKey(b.PublicHex())
	if err != nil {
		t.Fatalf("a derive: %v", err)
	}
	kb, err := b.DeriveSymKey(a.PublicHex())
	if err != nil {
		t.Fatalf("b derive: %v", err)
	}
	if len(ka) != symKeyLen {
		t.Fatalf("symKey len = %d, want %d", len(ka), symKeyLen)
	}
	if !bytes.Equal(ka, kb) {
		t.Errorf("derived keys differ:\n a=%x\n b=%x", ka, kb)
	}
}

func TestDeriveSymKeyRejectsBadPeer(t *testing.T) {
	a, _ := GenerateKeyPair()
	if _, err := a.DeriveSymKey("nothex"); err == nil {
		t.Error("expected error for non-hex peer key")
	}
	if _, err := a.DeriveSymKey(hex.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Error("expected error for wrong-length peer key")
	}
}

func TestTopicOfDeterministic(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, symKeyLen)
	t1 := TopicOf(key)
	t2 := TopicOf(key)
	if t1 != t2 {
		t.Error("TopicOf should be deterministic")
	}
	if len(t1) != 64 { // sha256 hex
		t.Errorf("topic len = %d, want 64", len(t1))
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := make([]byte, symKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"id":1,"jsonrpc":"2.0","method":"wc_sessionPing"}`)

	env, err := Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// It must be a base64 type-0 envelope.
	raw, err := base64.StdEncoding.DecodeString(env)
	if err != nil {
		t.Fatalf("envelope not base64: %v", err)
	}
	if raw[0] != envType0 {
		t.Errorf("envelope type = %d, want 0", raw[0])
	}

	got, err := Open(key, env)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip mismatch: %q", got)
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	k1 := bytes.Repeat([]byte{1}, symKeyLen)
	k2 := bytes.Repeat([]byte{2}, symKeyLen)
	env, _ := Seal(k1, []byte("secret"))
	if _, err := Open(k2, env); err == nil {
		t.Error("Open with the wrong key should fail (auth tag)")
	}
}

// TestOpenType1 builds a type-1 envelope (sender pubkey embedded) and confirms the
// receiver derives the key from it and decrypts.
func TestOpenType1(t *testing.T) {
	sender, _ := GenerateKeyPair()
	receiver, _ := GenerateKeyPair()

	symKey, err := sender.DeriveSymKey(receiver.PublicHex())
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("hello type 1")

	aead, _ := chacha20poly1305.New(symKey)
	nonce := make([]byte, nonceLen)
	rand.Read(nonce)
	sealed := aead.Seal(nil, nonce, plaintext, nil)

	senderPub, _ := hex.DecodeString(sender.PublicHex())
	env := []byte{envType1}
	env = append(env, senderPub...)
	env = append(env, nonce...)
	env = append(env, sealed...)
	b64 := base64.StdEncoding.EncodeToString(env)

	got, gotPub, err := receiver.OpenType1(b64)
	if err != nil {
		t.Fatalf("OpenType1: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("type-1 plaintext = %q", got)
	}
	if gotPub != sender.PublicHex() {
		t.Errorf("recovered sender pub = %s, want %s", gotPub, sender.PublicHex())
	}
}
