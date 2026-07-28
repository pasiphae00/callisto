// Package buildsecrets resolves build-time-embedded access tokens (currently the
// Ganymede default-RPC bearer token) for the RPC layer.
//
// SECURITY: a token compiled into a publicly distributed, open-source binary is NOT
// secret. The obfuscation here only keeps the plaintext out of a `strings` dump —
// the deobfuscation (below) is public, so a determined user can recover the token.
// Treat any token shipped this way as a SHARED, effectively public access key:
// rate-limit and rotate it on the server, and never embed a credential whose leak
// would be harmful. The token is injected at build time via ldflags from a
// gitignored env file (see the Makefile); a plain `go build` leaves it empty.
package buildsecrets

import (
	"encoding/base64"
	"strings"
)

// ganymedeObf is the obfuscated Ganymede bearer token, set at build time with:
//
//	-ldflags "-X github.com/pasiphae00/callisto/internal/buildsecrets.ganymedeObf=<obf>"
//
// It is empty in a normal `go build` (dev builds ship no token; the RPC layer then
// falls back to the unauthenticated default endpoint).
var ganymedeObf string

// obfKey is the fixed XOR key used to obfuscate/deobfuscate embedded tokens. It is
// intentionally in the repo — this is anti-`strings` scrambling, not encryption
// (see the package doc). Rotating it requires rebuilding, not re-releasing secrets.
const obfKey = "callisto/ganymede/v1-not-a-secret"

// Token returns the embedded token for ref, or "" if none is compiled in.
func Token(ref string) string {
	switch ref {
	case "ganymede":
		return deobfuscate(ganymedeObf)
	default:
		return ""
	}
}

// Obfuscate returns the build-time representation of tok (used by the Makefile via
// `go run` so the obfuscation stays in one place). Round-trips with deobfuscate.
func Obfuscate(tok string) string {
	if tok == "" {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(xor([]byte(tok)))
}

func deobfuscate(obf string) string {
	obf = strings.TrimSpace(obf)
	if obf == "" {
		return ""
	}
	raw, err := base64.RawStdEncoding.DecodeString(obf)
	if err != nil {
		return ""
	}
	return string(xor(raw))
}

// xor applies the repeating-key XOR (its own inverse).
func xor(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ obfKey[i%len(obfKey)]
	}
	return out
}
