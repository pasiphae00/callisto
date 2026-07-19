// Package updater implements Callisto's in-app self-update: it checks the Codeberg
// (Forgejo) releases API for a newer tagged version and, on request, downloads,
// cryptographically verifies, and installs it, then relaunches.
//
// Security: Callisto is a signing wallet, so an update is only ever applied after
// its SHA256SUMS is verified against an embedded ed25519 maintainer public key and
// the downloaded artifact's SHA-256 matches its entry. An unverifiable or tampered
// artifact is refused and the running app is left untouched. See verify.go.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	// defaultAPIBase is Codeberg's Forgejo API root. Overridable in tests.
	defaultAPIBase = "https://codeberg.org/api/v1"
	repoOwner      = "pasiphae"
	repoName       = "callisto"
)

// Updater checks for and applies Callisto updates.
type Updater struct {
	current string       // running version, e.g. "0.7.1" or "v0.7.1"
	base    string       // API base URL
	client  *http.Client // HTTP client (injectable for tests)
}

// New returns an Updater for the given running version (typically
// buildinfo.Version).
func New(currentVersion string) *Updater {
	return &Updater{
		current: currentVersion,
		base:    defaultAPIBase,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Release describes the latest published release and whether it is newer than the
// running version.
type Release struct {
	Version string  // normalized tag, e.g. "v0.8.0"
	Notes   string  // release body (changelog excerpt)
	Assets  []Asset // attached files
	Newer   bool    // true if Version > the running version
}

// forgejoRelease is the subset of the Forgejo releases API response we use.
type forgejoRelease struct {
	TagName string  `json:"tag_name"`
	Body    string  `json:"body"`
	Assets  []Asset `json:"assets"`
}

// Check queries the releases API for the latest release and reports whether it is
// newer than the running version.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", u.base, repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact release server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release server returned %s", resp.Status)
	}
	var fr forgejoRelease
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return releaseFrom(fr, u.current)
}

// releaseFrom builds a Release from the API payload and the running version.
func releaseFrom(fr forgejoRelease, current string) (*Release, error) {
	v := ensureV(fr.TagName)
	if !semver.IsValid(v) {
		return nil, fmt.Errorf("release has non-semver tag %q", fr.TagName)
	}
	return &Release{
		Version: v,
		Notes:   fr.Body,
		Assets:  fr.Assets,
		Newer:   semver.Compare(v, ensureV(current)) > 0,
	}, nil
}

// ensureV normalizes a version to a leading-"v" semver string ("0.8.0" → "v0.8.0").
func ensureV(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if !strings.HasPrefix(s, "v") {
		return "v" + s
	}
	return s
}

// platformSuffix is the artifact-name fragment identifying the running platform,
// e.g. "darwin-arm64" or "linux-amd64". Artifacts are named
// Callisto-v<ver>-<suffix>.<ext> (see the Makefile).
func platformSuffix() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// platformAsset returns the release artifact for the running platform.
func (r *Release) platformAsset() (Asset, error) {
	suffix := platformSuffix()
	for _, a := range r.Assets {
		if strings.Contains(a.Name, suffix) && (strings.HasSuffix(a.Name, ".zip") || strings.HasSuffix(a.Name, ".tar.gz")) {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("no download for this platform (%s) in release %s", suffix, r.Version)
}

// assetByName returns the named asset (SHA256SUMS, SHA256SUMS.sig).
func (r *Release) assetByName(name string) (Asset, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s is missing %s", r.Version, name)
}
