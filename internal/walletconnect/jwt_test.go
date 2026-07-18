package walletconnect

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDIDKeyFormat(t *testing.T) {
	k, err := newAuthKey()
	if err != nil {
		t.Fatal(err)
	}
	did := k.did()
	// ed25519 did:keys are multibase-z (base58btc) over multicodec 0xed01, which
	// always renders with the "did:key:z6Mk" prefix.
	if !strings.HasPrefix(did, "did:key:z6Mk") {
		t.Errorf("did = %q, want did:key:z6Mk… prefix", did)
	}
}

func TestSignRelayJWTVerifies(t *testing.T) {
	k, err := newAuthKey()
	if err != nil {
		t.Fatal(err)
	}
	aud := "wss://relay.walletconnect.org"
	now := time.Unix(1_700_000_000, 0)

	token, err := k.SignRelayJWT(aud, now)
	if err != nil {
		t.Fatalf("SignRelayJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT should have 3 parts, got %d", len(parts))
	}

	// Header: EdDSA/JWT.
	var hdr jwtHeader
	decodeSeg(t, parts[0], &hdr)
	if hdr.Alg != "EdDSA" || hdr.Typ != "JWT" {
		t.Errorf("header = %+v", hdr)
	}

	// Claims: iss is our did, aud/iat/exp correct.
	var claims jwtClaims
	decodeSeg(t, parts[1], &claims)
	if claims.Iss != k.did() {
		t.Errorf("iss = %q, want %q", claims.Iss, k.did())
	}
	if claims.Aud != aud {
		t.Errorf("aud = %q", claims.Aud)
	}
	if claims.Iat != now.Unix() || claims.Exp != now.Add(clientJWTTTL).Unix() {
		t.Errorf("iat/exp = %d/%d", claims.Iat, claims.Exp)
	}
	if claims.Sub == "" {
		t.Error("sub should be a random nonce")
	}

	// Signature: verifies against the public key with the signing input.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("sig decode: %v", err)
	}
	signingInput := parts[0] + "." + parts[1]
	if !ed25519.Verify(k.pub, []byte(signingInput), sig) {
		t.Error("JWT signature does not verify")
	}
}

func decodeSeg(t *testing.T, seg string, v interface{}) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
}
