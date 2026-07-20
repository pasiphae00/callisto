// Package buildinfo exposes Callisto's version and commit for display (About
// dialog, logs). Version is bumped by hand alongside CHANGELOG.md at release
// time (see docs/RELEASING.md); Commit is read automatically from the Go
// toolchain's embedded VCS metadata (populated by `go build` in a git checkout,
// no ldflags required).
package buildinfo

import "runtime/debug"

// Version is Callisto's current version. Update at release time.
const Version = "0.11.0"

// ShortCommit returns the short (7-char) git commit the running binary was
// built from, or "unknown" if that information isn't available (e.g. built
// outside a git checkout, or VCS stamping was disabled).
func ShortCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) > 0 {
			if len(s.Value) > 7 {
				return s.Value[:7]
			}
			return s.Value
		}
	}
	return "unknown"
}
