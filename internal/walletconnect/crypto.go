package walletconnect

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	symKeyLen = 32 // ChaCha20-Poly1305 key / X25519 shared-key length
	nonceLen  = 12 // ChaCha20-Poly1305 IETF nonce (the envelope "iv")
	envType0  = 0x00
	envType1  = 0x01
	pubKeyLen = 32 // X25519 public key
)

// KeyPair is an X25519 key pair used to derive a session's symmetric key. The
// public key is shared with the peer; the private key never leaves this process.
type KeyPair struct {
	priv *ecdh.PrivateKey
}

// GenerateKeyPair creates a fresh X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: generate keypair: %w", err)
	}
	return &KeyPair{priv: priv}, nil
}

// PublicHex returns the 32-byte X25519 public key as hex (the form sent in
// session proposal/settle messages).
func (k *KeyPair) PublicHex() string {
	return hex.EncodeToString(k.priv.PublicKey().Bytes())
}

// DeriveSymKey computes the shared symmetric key with a peer's hex public key,
// following the WalletConnect v2 derivation: symKey = HKDF-SHA256(X25519(priv,
// peerPub), salt="", info="", 32 bytes).
func (k *KeyPair) DeriveSymKey(peerPubHex string) ([]byte, error) {
	peerPub, err := hex.DecodeString(peerPubHex)
	if err != nil || len(peerPub) != pubKeyLen {
		return nil, fmt.Errorf("walletconnect: bad peer public key")
	}
	pub, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: peer public key: %w", err)
	}
	shared, err := k.priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: ecdh: %w", err)
	}
	symKey := make([]byte, symKeyLen)
	r := hkdf.New(sha256.New, shared, nil, nil)
	if _, err := io.ReadFull(r, symKey); err != nil {
		return nil, fmt.Errorf("walletconnect: hkdf: %w", err)
	}
	return symKey, nil
}

// TopicOf returns the relay topic for a symmetric key: hex(sha256(symKey)).
func TopicOf(symKey []byte) string {
	sum := sha256.Sum256(symKey)
	return hex.EncodeToString(sum[:])
}

// Seal encrypts plaintext under symKey as a type-0 envelope and base64-encodes it
// for the relay `message` field: base64(0x00 || iv(12) || ChaCha20-Poly1305 seal).
func Seal(symKey, plaintext []byte) (string, error) {
	aead, err := chacha20poly1305.New(symKey)
	if err != nil {
		return "", fmt.Errorf("walletconnect: cipher: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("walletconnect: nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plaintext, nil)
	env := make([]byte, 0, 1+nonceLen+len(sealed))
	env = append(env, envType0)
	env = append(env, nonce...)
	env = append(env, sealed...)
	return base64.StdEncoding.EncodeToString(env), nil
}

// Open decrypts a base64 type-0 envelope with symKey.
func Open(symKey []byte, message string) ([]byte, error) {
	env, err := base64.StdEncoding.DecodeString(message)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: base64: %w", err)
	}
	if len(env) < 1 {
		return nil, errors.New("walletconnect: empty envelope")
	}
	switch env[0] {
	case envType0:
		if len(env) < 1+nonceLen {
			return nil, errors.New("walletconnect: short type-0 envelope")
		}
		return openSealed(symKey, env[1:1+nonceLen], env[1+nonceLen:])
	case envType1:
		return nil, errors.New("walletconnect: type-1 envelope requires OpenType1")
	default:
		return nil, fmt.Errorf("walletconnect: unknown envelope type %d", env[0])
	}
}

// OpenType1 decrypts a type-1 envelope (0x01 || senderPub(32) || iv || sealbox):
// the symmetric key is derived from the sender's public key and our key pair. It
// returns the plaintext and the sender's hex public key.
func (k *KeyPair) OpenType1(message string) (plaintext []byte, senderPubHex string, err error) {
	env, derr := base64.StdEncoding.DecodeString(message)
	if derr != nil {
		return nil, "", fmt.Errorf("walletconnect: base64: %w", derr)
	}
	if len(env) < 1+pubKeyLen+nonceLen || env[0] != envType1 {
		return nil, "", errors.New("walletconnect: not a valid type-1 envelope")
	}
	senderPub := env[1 : 1+pubKeyLen]
	iv := env[1+pubKeyLen : 1+pubKeyLen+nonceLen]
	sealed := env[1+pubKeyLen+nonceLen:]
	senderPubHex = hex.EncodeToString(senderPub)
	symKey, derr := k.DeriveSymKey(senderPubHex)
	if derr != nil {
		return nil, "", derr
	}
	pt, derr := openSealed(symKey, iv, sealed)
	if derr != nil {
		return nil, "", derr
	}
	return pt, senderPubHex, nil
}

func openSealed(symKey, nonce, sealed []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(symKey)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: cipher: %w", err)
	}
	pt, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: decrypt: %w", err)
	}
	return pt, nil
}
