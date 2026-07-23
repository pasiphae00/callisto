// Package updater implements Callisto's in-app self-update: it checks the GitHub
// releases API for a newer tagged version and, on request, downloads,
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
	// defaultAPIBase is GitHub's REST API root. Overridable in tests.
	defaultAPIBase = "https://api.github.com"
	repoOwner      = "pasiphae00"
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

// apiRelease is the subset of GitHub's releases API response we use. The shape
// (tag_name/body/draft/assets[].name/assets[].browser_download_url) happens to be
// identical to Forgejo's, which this package originally targeted.
type apiRelease struct {
	TagName string  `json:"tag_name"`
	Body    string  `json:"body"`
	Draft   bool    `json:"draft"`
	Assets  []Asset `json:"assets"`
}

// Check queries the releases API and reports the highest-semver published release
// and whether it is newer than the running version. We pick by semver (not the
// API's "latest", which excludes anything flagged pre-release) so it works for a
// 0.x project regardless of how a release is flagged; drafts are excluded (GitHub
// only returns drafts to authenticated requests with push access anyway, but the
// client-side filter below is a harmless belt-and-suspenders).
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=20", u.base, repoOwner, repoName)
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
	var list []apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	best, ok := highestSemver(list)
	if !ok {
		return nil, fmt.Errorf("no published release with a semver tag found")
	}
	return releaseFrom(best, u.current)
}

// highestSemver returns the release with the greatest valid semver tag.
func highestSemver(list []apiRelease) (apiRelease, bool) {
	var best apiRelease
	found := false
	for _, r := range list {
		if r.Draft {
			continue
		}
		v := ensureV(r.TagName)
		if !semver.IsValid(v) {
			continue
		}
		if !found || semver.Compare(v, ensureV(best.TagName)) > 0 {
			best, found = r, true
		}
	}
	return best, found
}

// releaseFrom builds a Release from the API payload and the running version.
func releaseFrom(fr apiRelease, current string) (*Release, error) {
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
