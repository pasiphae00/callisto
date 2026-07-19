package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseFromNewer(t *testing.T) {
	fr := forgejoRelease{TagName: "v0.8.0", Body: "notes"}
	rel, err := releaseFrom(fr, "0.7.1")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v0.8.0" || !rel.Newer {
		t.Errorf("v0.8.0 vs 0.7.1: got version=%s newer=%v", rel.Version, rel.Newer)
	}

	// Same version → not newer. Tolerates the running version lacking the "v".
	same, _ := releaseFrom(forgejoRelease{TagName: "v0.7.1"}, "0.7.1")
	if same.Newer {
		t.Errorf("v0.7.1 vs 0.7.1 should not be newer")
	}
	// Older tag than running → not newer.
	older, _ := releaseFrom(forgejoRelease{TagName: "v0.7.0"}, "v0.7.1")
	if older.Newer {
		t.Errorf("v0.7.0 vs v0.7.1 should not be newer")
	}
}

func TestReleaseFromBadTag(t *testing.T) {
	if _, err := releaseFrom(forgejoRelease{TagName: "not-a-version"}, "0.1.0"); err == nil {
		t.Error("expected error for non-semver tag")
	}
}

func TestPlatformAsset(t *testing.T) {
	suffix := platformSuffix()
	ext := ".tar.gz"
	if runtime.GOOS == "darwin" {
		ext = ".zip"
	}
	rel := &Release{Assets: []Asset{
		{Name: "SHA256SUMS"},
		{Name: "Callisto-v0.8.0-someotheros-arch" + ext, URL: "u1"},
		{Name: "Callisto-v0.8.0-" + suffix + ext, URL: "match"},
	}}
	a, err := rel.platformAsset()
	if err != nil || a.URL != "match" {
		t.Fatalf("platformAsset = %+v, err %v", a, err)
	}

	empty := &Release{Assets: []Asset{{Name: "SHA256SUMS"}}}
	if _, err := empty.platformAsset(); err == nil {
		t.Error("expected error when no platform asset present")
	}
}

func TestExpectedSumAndVerifyFile(t *testing.T) {
	dir := t.TempDir()
	artPath := filepath.Join(dir, "art.zip")
	data := []byte("hello callisto")
	if err := os.WriteFile(artPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	sums := []byte(hex.EncodeToString(sum[:]) + "  art.zip\n" +
		"deadbeef  other.zip\n")

	if err := verifyFileSum(artPath, "art.zip", sums); err != nil {
		t.Errorf("verifyFileSum valid: %v", err)
	}
	// Tampered file → mismatch.
	if err := os.WriteFile(artPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSum(artPath, "art.zip", sums); err == nil {
		t.Error("verifyFileSum should fail on a tampered file")
	}
	// Missing entry.
	if _, err := expectedSum(sums, "absent.zip"); err == nil {
		t.Error("expectedSum should fail for a filename not listed")
	}
}

func TestVerifySums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte("abc123  Callisto-v0.8.0-darwin-arm64.zip\n")
	sig := ed25519.Sign(priv, sums)
	sigHex := []byte(hex.EncodeToString(sig))

	if err := verifySums(sums, sigHex, pub); err != nil {
		t.Errorf("verifySums valid: %v", err)
	}
	// Tampered SHA256SUMS → reject.
	if err := verifySums([]byte("evil  x.zip\n"), sigHex, pub); err == nil {
		t.Error("verifySums should reject a tampered body")
	}
	// Wrong key → reject.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := verifySums(sums, sigHex, otherPub); err == nil {
		t.Error("verifySums should reject a signature from another key")
	}
}

func TestReleasePubkeyPlaceholder(t *testing.T) {
	// The committed key is the all-zero placeholder until `make gen-release-key`.
	if _, err := releasePubkey(); !errors.Is(err, ErrUpdatesNotConfigured) {
		t.Errorf("placeholder key should yield ErrUpdatesNotConfigured, got %v", err)
	}
}

func TestCheckAgainstFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A list, deliberately out of order, with an older release and a draft, to
		// confirm Check picks the highest-semver non-draft.
		w.Write([]byte(`[
			{"tag_name":"v0.8.0","body":"old","draft":false,"assets":[]},
			{"tag_name":"v1.0.0","body":"draft","draft":true,"assets":[]},
			{"tag_name":"v0.9.0","body":"## Added\n- stuff","draft":false,
			 "assets":[{"name":"Callisto-v0.9.0-` + platformSuffix() + `.zip","browser_download_url":"http://x/a"}]}
		]`))
	}))
	defer srv.Close()

	u := New("0.7.1")
	u.base = srv.URL
	u.client = srv.Client()
	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v0.9.0" || !rel.Newer || rel.Notes == "" {
		t.Errorf("unexpected release: %+v", rel)
	}
}

func TestExtractZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A nested executable file to confirm mode + subdirs survive.
	fw, _ := zw.CreateHeader(&zip.FileHeader{Name: "Callisto.app/Contents/MacOS/callisto", Method: zip.Deflate})
	fw.Write([]byte("#!/bin/sh\n"))
	zw.Close()
	os.WriteFile(zipPath, buf.Bytes(), 0o644)

	dest := filepath.Join(dir, "out")
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "Callisto.app/Contents/MacOS/callisto"))
	if err != nil || string(got) != "#!/bin/sh\n" {
		t.Errorf("extracted file = %q, err %v", got, err)
	}
	if app, ok := findByName(dest, "Callisto.app"); !ok || filepath.Base(app) != "Callisto.app" {
		t.Errorf("findByName Callisto.app = %q, %v", app, ok)
	}
}

func TestExtractTarGzRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tgz := filepath.Join(dir, "a.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("binary")
	tw.WriteHeader(&tar.Header{Name: "usr/local/bin/Callisto", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	os.WriteFile(tgz, buf.Bytes(), 0o644)

	dest := filepath.Join(dir, "out")
	if err := extractTarGz(tgz, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "usr/local/bin/Callisto"))
	if err != nil || string(got) != "binary" {
		t.Errorf("extracted = %q, err %v", got, err)
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	if _, err := safeJoin("/tmp/dest", "../../etc/passwd"); err == nil {
		t.Error("safeJoin should reject a path escaping the destination")
	}
	if _, err := safeJoin("/tmp/dest", "ok/file"); err != nil {
		t.Errorf("safeJoin rejected a valid path: %v", err)
	}
}
