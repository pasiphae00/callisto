package updater

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// releasePubkeyHex is the maintainer's ed25519 public key (hex), committed to the
// repo and embedded into the binary. The in-app updater verifies every release's
// SHA256SUMS against it. Generate/replace it with `make gen-release-key`.
//
//go:embed release_pubkey.ed25519
var releasePubkeyHex string

// ErrUpdatesNotConfigured is returned when no real maintainer key is embedded (the
// placeholder all-zero key), so updates cannot be cryptographically verified.
var ErrUpdatesNotConfigured = errors.New("updates are not configured: no maintainer signing key is embedded in this build")

// releasePubkey returns the embedded maintainer public key, or
// ErrUpdatesNotConfigured if it is the all-zero placeholder.
func releasePubkey() (ed25519.PublicKey, error) {
	return parsePubkeyHex(releasePubkeyHex)
}

// parsePubkeyHex decodes a hex-encoded ed25519 public key, rejecting the all-zero
// placeholder with ErrUpdatesNotConfigured.
func parsePubkeyHex(s string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("embedded public key is not valid hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("embedded public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	if allZero(raw) {
		return nil, ErrUpdatesNotConfigured
	}
	return ed25519.PublicKey(raw), nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// verifySums checks the ed25519 signature (hex) over the raw SHA256SUMS bytes
// against the maintainer public key.
func verifySums(sums, sigHex []byte, pub ed25519.PublicKey) error {
	sig, err := hex.DecodeString(strings.TrimSpace(string(sigHex)))
	if err != nil {
		return fmt.Errorf("signature is not valid hex: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, sums, sig) {
		return errors.New("SHA256SUMS signature does not verify against the maintainer key")
	}
	return nil
}

// expectedSum returns the hex SHA-256 recorded for filename in a SHA256SUMS body
// (shasum format: "<hex>  <filename>", basename-matched).
func expectedSum(sums []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == filename || strings.TrimPrefix(fields[1], "*") == filename {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", filename)
}

// verifyFileSum checks that the file at path hashes to the SHA-256 recorded for
// filename in sums.
func verifyFileSum(path, filename string, sums []byte) error {
	want, err := expectedSum(sums, filename)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !bytes.Equal([]byte(got), []byte(want)) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filename, got, want)
	}
	return nil
}
