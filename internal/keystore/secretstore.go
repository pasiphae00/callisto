package keystore

import "errors"

// ErrSecretUnavailable is returned by a SecretStore when the OS secure store can't
// be used on this platform / build.
var ErrSecretUnavailable = errors.New("keystore: OS secret store unavailable")

// ErrSecretNotFound is returned by Get when no secret exists for the reference.
var ErrSecretNotFound = errors.New("keystore: secret not found in the OS store")

// ErrSecretForeignIdentity is returned by Get when the item exists but was written
// by a different build of Callisto, so this build cannot read it.
//
// macOS keychain ACLs bind an item to the code identity that created it. A
// Developer-ID-signed release and a local `go build` are different identities, and
// two local builds differ from each other, so an enrolment made in one cannot be
// used by another. Nothing is lost — the wallet still unlocks with its passphrase —
// but the Touch ID enrolment must be redone by whichever build the user runs.
//
// Reads deliberately fail with this rather than letting macOS put up its own
// "enter the login keychain password" dialog: the user proved presence with Touch
// ID moments earlier, and typing a login password is the very thing Touch ID was
// enabled to avoid.
var ErrSecretForeignIdentity = errors.New("keystore: this Touch ID enrolment was created by a different build of Callisto")

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
