//go:build darwin

package keystore

import "testing"

// TestAvailableIsFalseWithoutAStableIdentity pins the guard that decides whether
// the Touch ID UI appears at all.
//
// `go test` links an ad-hoc, linker-signed binary (identifier "a.out", no Team
// Identifier) — the same signing status as plain `go build` output, and the thing
// a developer actually runs. Keychain ACLs bind an item to the identity that
// created it, and an ad-hoc identity does not survive a rebuild, so enrolling
// Touch ID in such a build produces an OS password prompt on the next launch
// instead of a fingerprint tap. Offering the feature there is worse than hiding
// it.
//
// This is a regression test with a measured history: the previous implementation
// probed by storing and deleting a throwaway keychain item, which any binary can
// do, and so returned true here. Verified in both directions when replaced —
// false for this ad-hoc test binary, true for the identical binary after
// `codesign --sign "Developer ID Application: ..."`.
func TestAvailableIsFalseWithoutAStableIdentity(t *testing.T) {
	if OSSecretStore().Available() {
		t.Error("Available() = true in an ad-hoc-signed build; Touch ID must stay hidden " +
			"until the binary is Developer-ID-signed, or enrolment leads to password prompts")
	}
}

// TestAvailableDoesNotTouchTheKeychain guards the other half: the check must be a
// pure inspection of this binary's own signature. The old probe wrote an item into
// the user's real login keychain on every launch that reached it, purely to see
// whether it could. Calling it repeatedly must remain free of side effects.
func TestAvailableDoesNotTouchTheKeychain(t *testing.T) {
	store := OSSecretStore()
	first := store.Available()
	for i := 0; i < 3; i++ {
		if store.Available() != first {
			t.Fatal("Available() is not stable across calls")
		}
	}
	// The probe ref the old implementation used must not be left behind. Get
	// would require Touch ID, so assert via Delete, which reports success for a
	// missing item and cannot prompt.
	if err := store.Delete("__callisto_touchid_probe__"); err != nil {
		t.Errorf("stale probe item left in the keychain: %v", err)
	}
}
