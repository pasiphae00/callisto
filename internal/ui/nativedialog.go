package ui

import (
	"os/exec"
	"runtime"
	"strings"
)

// nativeDialogsAvailable reports whether the OS-native file panels can be used
// (macOS only, via osascript). Elsewhere callers fall back to Fyne's in-app dialog.
func nativeDialogsAvailable() bool { return runtime.GOOS == "darwin" }

// nativeSavePath shows the native macOS "save as" panel and returns the chosen
// POSIX path. A non-nil error means the panel was cancelled or is unavailable —
// callers treat that as "do nothing". Must be called off the UI goroutine (it
// blocks until the user dismisses the panel).
func nativeSavePath(prompt, defaultName string) (string, error) {
	script := `POSIX path of (choose file name with prompt "` + escapeAppleScript(prompt) +
		`" default name "` + escapeAppleScript(defaultName) + `")`
	return runOsascript(script)
}

// nativeOpenPath shows the native macOS "open" panel and returns the chosen POSIX
// path (same cancellation semantics as nativeSavePath).
func nativeOpenPath(prompt string) (string, error) {
	script := `POSIX path of (choose file with prompt "` + escapeAppleScript(prompt) + `")`
	return runOsascript(script)
}

func runOsascript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil { // includes "User canceled" (-128)
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// escapeAppleScript escapes a Go string for embedding in an AppleScript double-quoted
// literal.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
