// Package keystore encrypts a secret (a BIP-39 seed) at rest with a user-provided
// passphrase, so a hot wallet can be imported once and thereafter unlocked with the
// passphrase instead of re-entering the recovery phrase every time.
//
// The scheme is deliberately standard and conservative: scrypt (memory-hard KDF)
// derives a 256-bit key from the passphrase and a random salt, and AES-256-GCM
// (authenticated encryption) seals the secret under a random nonce. GCM's
// authentication tag means a wrong passphrase — or any tampering with the file —
// fails cleanly on Decrypt, so no separate MAC or plaintext oracle is needed.
//
// This package handles only bytes and files; it has no domain dependencies and
// never logs secrets. Callers pass explicit file paths (the config package owns
// the keystore directory), keeping this layer pure and cycle-free.
package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/scrypt"
)

// ErrBadPassphrase is returned by Decrypt when the passphrase is wrong or the
// ciphertext has been tampered with (both surface as a GCM authentication
// failure, deliberately indistinguishable).
var ErrBadPassphrase = errors.New("keystore: wrong passphrase or corrupted keystore")

// Version is the current keystore file format version.
const Version = 1

// scrypt parameters. N is memory-hard cost (2^18, ~256 MB, ~1–2 s to derive) —
// matching go-ethereum's StandardScryptN — chosen for security over unlock speed.
const (
	scryptN     = 1 << 18
	scryptR     = 8
	scryptP     = 1
	scryptDKLen = 32 // 256-bit AES key
	saltLen     = 32
	nonceLen    = 12 // AES-GCM standard nonce size
)

// KDFParams records the scrypt parameters and salt needed to re-derive the key.
type KDFParams struct {
	N     int    `json:"n"`
	R     int    `json:"r"`
	P     int    `json:"p"`
	DKLen int    `json:"dklen"`
	Salt  string `json:"salt"` // hex
}

// Keystore is the encrypted secret plus everything (except the passphrase) needed
// to decrypt it. It is JSON-serializable for on-disk storage.
type Keystore struct {
	Version    int       `json:"version"`
	KDF        string    `json:"kdf"`        // "scrypt"
	KDFParams  KDFParams `json:"kdfparams"`
	Cipher     string    `json:"cipher"`     // "aes-256-gcm"
	Nonce      string    `json:"nonce"`      // hex
	Ciphertext string    `json:"ciphertext"` // hex
	CreatedAt  int64     `json:"created_at"` // unix seconds
	// Secret labels what the encrypted bytes are, for the consumer (the keystore
	// package itself is agnostic): "" / "seed" = a BIP-39 seed (HD wallet),
	// "private-key" = a raw 32-byte account key (single-account import).
	Secret string `json:"secret,omitempty"`
}

// Encrypt seals secret under passphrase, returning a Keystore that can be written
// to disk. A fresh random salt and nonce are generated per call.
func Encrypt(secret []byte, passphrase string) (*Keystore, error) {
	if len(secret) == 0 {
		return nil, errors.New("keystore: refusing to encrypt an empty secret")
	}
	if passphrase == "" {
		return nil, errors.New("keystore: a passphrase is required")
	}

	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("keystore: read salt: %w", err)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return nil, fmt.Errorf("keystore: derive key: %w", err)
	}
	defer zero(key)

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("keystore: read nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, secret, nil)

	return &Keystore{
		Version:    Version,
		KDF:        "scrypt",
		KDFParams:  KDFParams{N: scryptN, R: scryptR, P: scryptP, DKLen: scryptDKLen, Salt: hex.EncodeToString(salt)},
		Cipher:     "aes-256-gcm",
		Nonce:      hex.EncodeToString(nonce),
		Ciphertext: hex.EncodeToString(ciphertext),
		CreatedAt:  time.Now().Unix(),
	}, nil
}

// Decrypt recovers the secret from ks using passphrase. It returns ErrBadPassphrase
// if the passphrase is wrong or the keystore has been tampered with. The caller is
// responsible for zeroing the returned secret when done.
func (ks *Keystore) Decrypt(passphrase string) ([]byte, error) {
	key, err := ks.DeriveKey(passphrase)
	if err != nil {
		return nil, err
	}
	defer zero(key)
	return ks.DecryptWithKey(key)
}

// DeriveKey re-derives the AES key from a passphrase using ks's stored KDF params.
// The caller must zero the returned key. Exposed so an OS-keychain / Touch ID unlock
// can cache the derived key and decrypt later without the passphrase (DecryptWithKey).
func (ks *Keystore) DeriveKey(passphrase string) ([]byte, error) {
	if ks.KDF != "scrypt" {
		return nil, fmt.Errorf("keystore: unsupported kdf %q", ks.KDF)
	}
	salt, err := hex.DecodeString(ks.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("keystore: decode salt: %w", err)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, ks.KDFParams.N, ks.KDFParams.R, ks.KDFParams.P, ks.KDFParams.DKLen)
	if err != nil {
		return nil, fmt.Errorf("keystore: derive key: %w", err)
	}
	return key, nil
}

// DecryptWithKey recovers the secret using a pre-derived AES key (from DeriveKey),
// bypassing the (slow) scrypt step. Returns ErrBadPassphrase on a wrong key or
// tampering. The caller zeroes the returned secret.
func (ks *Keystore) DecryptWithKey(key []byte) ([]byte, error) {
	if ks.Cipher != "aes-256-gcm" {
		return nil, fmt.Errorf("keystore: unsupported cipher %q", ks.Cipher)
	}
	nonce, err := hex.DecodeString(ks.Nonce)
	if err != nil {
		return nil, fmt.Errorf("keystore: decode nonce: %w", err)
	}
	ciphertext, err := hex.DecodeString(ks.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("keystore: decode ciphertext: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, ErrBadPassphrase
	}
	secret, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Wrong key and tampering both land here (auth-tag failure).
		return nil, ErrBadPassphrase
	}
	return secret, nil
}

// Rekey re-encrypts ks's secret under a new passphrase, returning a fresh keystore
// (new salt + nonce). oldPassphrase must be correct (else ErrBadPassphrase). The
// decrypted secret is zeroed before returning.
func Rekey(ks *Keystore, oldPassphrase, newPassphrase string) (*Keystore, error) {
	secret, err := ks.Decrypt(oldPassphrase)
	if err != nil {
		return nil, err
	}
	defer zero(secret)
	return Encrypt(secret, newPassphrase)
}

// newGCM builds an AES-256-GCM AEAD from a 32-byte key.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("keystore: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keystore: new gcm: %w", err)
	}
	return gcm, nil
}

// Save writes ks to path atomically (temp file + rename) with 0600 permissions.
func Save(path string, ks *Keystore) error {
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("keystore: encode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("keystore: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("keystore: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("keystore: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("keystore: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keystore: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("keystore: commit: %w", err)
	}
	return nil
}

// Load reads and parses a keystore file.
func Load(path string) (*Keystore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ks Keystore
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, fmt.Errorf("keystore: parse %s: %w", path, err)
	}
	return &ks, nil
}

// Wipe best-effort overwrites a keystore file with random bytes and removes it.
//
// Caveat: on modern SSDs and copy-on-write filesystems an in-place overwrite is
// not a guaranteed secure erase (the controller/FS may write elsewhere). This
// removes the file and drops the encrypted material from the visible filesystem;
// the user's recovery phrase remains their authoritative backup. A missing file
// is not an error.
func Wipe(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if f, oerr := os.OpenFile(path, os.O_WRONLY, 0o600); oerr == nil {
		buf := make([]byte, info.Size())
		if _, rerr := io.ReadFull(rand.Reader, buf); rerr == nil {
			_, _ = f.WriteAt(buf, 0)
			_ = f.Sync()
		}
		_ = f.Close()
	}
	return os.Remove(path)
}

// zero overwrites a byte slice.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
