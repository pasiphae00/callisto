package walletconnect

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// clientJWTTTL is how long a relay-auth JWT is valid. WalletConnect clients use
// one day.
const clientJWTTTL = 24 * time.Hour

// ed25519 multicodec header (varint 0xed 0x01) for did:key encoding.
var ed25519MulticodecHeader = []byte{0xed, 0x01}

// authKey is the Ed25519 key pair a relay client uses to sign its connection JWT.
// It is ephemeral (per client instance) and unrelated to any wallet key.
type authKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newAuthKey() (*authKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("walletconnect: generate auth key: %w", err)
	}
	return &authKey{pub: pub, priv: priv}, nil
}

// did returns the did:key identifier for the auth public key:
// "did:key:z" + base58btc(0xed01 || pub). ed25519 did:keys start with "z6Mk".
func (k *authKey) did() string {
	encoded := append(append([]byte{}, ed25519MulticodecHeader...), k.pub...)
	return "did:key:z" + base58Encode(encoded)
}

// jwtHeader/jwtClaims are the relay-auth JWT parts.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// SignRelayJWT builds and signs the EdDSA JWT the relay expects in the
// Authorization header. aud is the relay URL (e.g. "wss://relay.walletconnect.org").
func (k *authKey) SignRelayJWT(aud string, now time.Time) (string, error) {
	sub := make([]byte, 32)
	if _, err := rand.Read(sub); err != nil {
		return "", fmt.Errorf("walletconnect: jwt sub: %w", err)
	}
	header, err := json.Marshal(jwtHeader{Alg: "EdDSA", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(jwtClaims{
		Iss: k.did(),
		Sub: hex.EncodeToString(sub),
		Aud: aud,
		Iat: now.Unix(),
		Exp: now.Add(clientJWTTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sig := ed25519.Sign(k.priv, []byte(signingInput))
	return signingInput + "." + enc.EncodeToString(sig), nil
}
