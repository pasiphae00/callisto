package config

import (
	"os"
	"testing"
)

// TestMain redirects the user-config lookup at a temporary directory for every
// test in this package, so no test can write over a developer's real config
// (wallet registry, endpoints, Safes). See internal/ui/main_test.go for the
// incident that motivated this.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "callisto-config-test")
	if err != nil {
		panic("test setup: " + err.Error())
	}
	defer os.RemoveAll(dir)

	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("HOME", dir)

	return m.Run()
}
