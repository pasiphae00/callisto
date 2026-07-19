package keystore

import "errors"

// ErrSecretUnavailable is returned by a SecretStore when the OS secure store can't
// be used on this platform / build.
var ErrSecretUnavailable = errors.New("keystore: OS secret store unavailable")

// ErrSecretNotFound is returned by Get when no secret exists for the reference.
var ErrSecretNotFound = errors.New("keystore: secret not found in the OS store")

// SecretStore stores a small secret (Callisto uses it for a keystore's derived AES
// key) in the operating system's secure store, gated by user presence (Touch ID or
// the device passcode) on read. It lets a wallet unlock biometrically without the
// passphrase, while the passphrase-encrypted keystore file remains the source of
// truth and always-available fallback.
type SecretStore interface {
	// Available reports whether the store can be used on this platform/build.
	Available() bool
	// Set stores value under ref (read is user-presence-gated), replacing any
	// existing item.
	Set(ref string, value []byte) error
	// Get retrieves the value for ref, prompting for Touch ID / passcode. Returns
	// ErrSecretNotFound if absent.
	Get(ref string) ([]byte, error)
	// Delete removes ref (no error if absent).
	Delete(ref string) error
}

// OSSecretStore returns the platform's secret store — a macOS Keychain (Touch ID)
// backend, or an unavailable stub elsewhere.
func OSSecretStore() SecretStore { return osSecretStore() }
