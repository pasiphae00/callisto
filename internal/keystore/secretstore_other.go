//go:build !darwin

package keystore

// osSecretStore returns an unavailable store on non-macOS platforms. Keychain /
// Secret Service / DPAPI backends are a future addition; the passphrase path
// always works meanwhile.
func osSecretStore() SecretStore { return unavailableStore{} }

type unavailableStore struct{}

func (unavailableStore) Available() bool            { return false }
func (unavailableStore) Set(string, []byte) error   { return ErrSecretUnavailable }
func (unavailableStore) Get(string) ([]byte, error) { return nil, ErrSecretUnavailable }
func (unavailableStore) Delete(string) error        { return nil }
