package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrRelaunching is returned by Apply when the update was installed successfully
// and a relaunch of the new version has been scheduled. The caller should treat it
// as success and quit the app cleanly (so the scheduled relaunch takes over).
var ErrRelaunching = errors.New("update installed; relaunching")

// ManualInstallError is returned when the update was downloaded and verified but
// could not be installed in place (e.g. the app lives in a location this user
// can't write to). The verified artifact is left at Path for the user to install
// manually.
type ManualInstallError struct {
	Path string
}

func (e *ManualInstallError) Error() string {
	return "could not replace the app automatically; the verified update was saved to " + e.Path
}

// Apply downloads the platform artifact for rel, verifies it against the embedded
// maintainer key, installs it over the running app, and schedules a relaunch.
// progress (may be nil) receives human-readable status lines.
//
// On success it returns ErrRelaunching (the caller then quits so the new version
// starts). On a verification failure it returns a descriptive error and leaves the
// running app untouched.
func (u *Updater) Apply(ctx context.Context, rel *Release, progress func(string)) error {
	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}

	pub, err := releasePubkey()
	if err != nil {
		return err
	}
	art, err := rel.platformAsset()
	if err != nil {
		return err
	}
	sumsAsset, err := rel.assetByName("SHA256SUMS")
	if err != nil {
		return err
	}
	sigAsset, err := rel.assetByName("SHA256SUMS.sig")
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "callisto-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	report("Downloading " + art.Name + "…")
	artPath := filepath.Join(tmp, art.Name)
	if err := u.downloadFile(ctx, art.URL, artPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	report("Verifying signature…")
	sums, err := u.downloadBytes(ctx, sumsAsset.URL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	sig, err := u.downloadBytes(ctx, sigAsset.URL)
	if err != nil {
		return fmt.Errorf("download signature: %w", err)
	}
	if err := verifySums(sums, sig, pub); err != nil {
		return err // untrusted; do not install
	}
	if err := verifyFileSum(artPath, art.Name, sums); err != nil {
		return err // corrupt/tampered; do not install
	}

	report("Installing…")
	extractDir := filepath.Join(tmp, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if err := extract(artPath, extractDir); err != nil {
		return fmt.Errorf("extract update: %w", err)
	}

	return u.install(extractDir, artPath, report)
}

// install swaps the running app for the freshly extracted one and schedules a
// relaunch, returning ErrRelaunching on success. If it cannot write in place it
// returns a *ManualInstallError pointing at the verified artifact.
func (u *Updater) install(extractDir, artifactPath string, report func(string)) error {
	if runtime.GOOS == "darwin" {
		return installMac(extractDir, artifactPath, report)
	}
	return installUnix(extractDir, artifactPath, report)
}

// --- downloading -----------------------------------------------------------

func (u *Updater) downloadFile(ctx context.Context, url, dest string) error {
	body, err := u.get(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, body)
	return err
}

func (u *Updater) downloadBytes(ctx context.Context, url string) ([]byte, error) {
	body, err := u.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, 1<<20)) // SHA256SUMS/.sig are tiny
}

func (u *Updater) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// --- extraction ------------------------------------------------------------

func extract(archivePath, destDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".tar.gz"):
		return extractTarGz(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive type: %s", filepath.Base(archivePath))
	}
}

// safeJoin joins destDir and name, refusing paths that escape destDir (zip-slip).
func safeJoin(destDir, name string) (string, error) {
	p := filepath.Join(destDir, name)
	if p != destDir && !strings.HasPrefix(p, destDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return p, nil
}

func extractZip(src, destDir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		info := f.FileInfo()
		switch {
		case info.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case info.Mode()&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
		default:
			if err := writeFileFrom(f, target, info.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFileFrom(f *zip.File, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func extractTarGz(src, destDir string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // size bounded by our own artifact
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

// --- install (per-OS) ------------------------------------------------------

// findDir returns the first entry under root whose base name matches want (e.g.
// "Callisto.app"), searching a couple of levels deep.
func findByName(root, want string) (string, bool) {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if found == "" && d.Name() == want {
			found = path
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return found, found != ""
}

// currentAppBundle returns the running app's .app bundle path (macOS), walking up
// from the executable.
func currentAppBundle() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	for dir := exe; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if strings.HasSuffix(dir, ".app") {
			return dir, true
		}
	}
	return "", false
}

func installMac(extractDir, artifactPath string, report func(string)) error {
	newApp, ok := findByName(extractDir, "Callisto.app")
	if !ok {
		return errors.New("update archive did not contain Callisto.app")
	}
	curApp, ok := currentAppBundle()
	if !ok {
		// Running as a bare binary (e.g. `go run`) — nothing to swap in place.
		return saveForManualInstall(artifactPath)
	}

	old := curApp + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(curApp, old); err != nil {
		return saveForManualInstall(artifactPath) // typically a permissions issue
	}
	// ditto copies the bundle across filesystems preserving symlinks/modes.
	if out, err := exec.Command("ditto", newApp, curApp).CombinedOutput(); err != nil {
		_ = os.Rename(old, curApp) // roll back
		return fmt.Errorf("install new app: %v: %s", err, strings.TrimSpace(string(out)))
	}
	_ = os.RemoveAll(old)
	_ = exec.Command("xattr", "-cr", curApp).Run() // strip any quarantine

	report("Restarting…")
	// Sleep briefly so the current process quits first, then open a fresh instance
	// (-n) — avoids two Callisto instances sharing the config/db at once.
	relaunchDetached("/bin/sh", "-c", fmt.Sprintf("sleep 2; open -n %q", curApp))
	return ErrRelaunching
}

func installUnix(extractDir, artifactPath string, report func(string)) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	newBin, ok := findByName(extractDir, filepath.Base(exe))
	if !ok {
		if newBin, ok = findByName(extractDir, "Callisto"); !ok {
			return errors.New("update archive did not contain the Callisto executable")
		}
	}

	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil { // move the running binary aside
		return saveForManualInstall(artifactPath)
	}
	if err := copyFile(newBin, exe, 0o755); err != nil {
		_ = os.Rename(old, exe) // roll back
		return saveForManualInstall(artifactPath)
	}
	_ = os.Remove(old)

	report("Restarting…")
	relaunchDetached("/bin/sh", "-c", fmt.Sprintf("sleep 1; exec %q", exe))
	return ErrRelaunching
}

// saveForManualInstall copies the verified artifact to the user's Downloads (or
// home) directory and returns a *ManualInstallError pointing at it.
func saveForManualInstall(artifactPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return &ManualInstallError{Path: artifactPath}
	}
	dest := filepath.Join(home, "Downloads", filepath.Base(artifactPath))
	if _, err := os.Stat(filepath.Dir(dest)); err != nil {
		dest = filepath.Join(home, filepath.Base(artifactPath))
	}
	if err := copyFile(artifactPath, dest, 0o644); err != nil {
		return &ManualInstallError{Path: artifactPath}
	}
	return &ManualInstallError{Path: dest}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// relaunchDetached starts a command that outlives this process, so it can relaunch
// the app once we exit. The brief sleep in the invocations above lets the current
// process quit first.
func relaunchDetached(name string, args ...string) {
	cmd := exec.Command(name, args...)
	// On macOS `open -n` returns immediately; on unix we sleep-then-exec in a shell.
	// Either way we don't wait.
	_ = cmd.Start()
	_ = cmd.Process.Release()
}
