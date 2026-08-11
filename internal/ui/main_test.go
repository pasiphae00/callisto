package ui

import (
	"os"
	"testing"
)

// TestMain redirects the user-config lookup at a temporary directory for every
// test in this package.
//
// This is a safety guard, not a convenience. config.Save() writes to the real
// os.UserConfigDir(), which on a developer's machine holds their actual wallet
// registry, RPC endpoints, and Safe list. Any test that constructs an App and
// touches a control wired to Save() — a Settings checkbox, a picker — will
// silently overwrite that file with whatever empty Config the test built. It
// has happened: a Settings picker test wrote an empty config over a real one,
// erasing the wallet registry. (Key material survived, since keystores are
// separate files, but every descriptor pointing at them was lost.)
//
// Relying on each test to remember t.Setenv is what failed. Doing it once here
// cannot be forgotten by the next test added to this package.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

// runIsolated is separate so the deferred cleanup runs before os.Exit.
func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "callisto-ui-test-config")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	defer os.RemoveAll(dir)

	// os.UserConfigDir reads XDG_CONFIG_HOME on Unix and HOME on macOS
	// (~/Library/Application Support); set both so this holds on either.
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("HOME", dir)

	return m.Run()
}
