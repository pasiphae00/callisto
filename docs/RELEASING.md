# Releasing Callisto

Callisto is hosted on **Codeberg** (a Forgejo instance). This document defines
the branch, versioning, and release workflow. It is intentionally lightweight but
consistent, so releases are reproducible and well documented.

## Branching model

- `main` is always buildable and releasable. Do not commit directly to it.
- Work happens on short-lived branches, named by intent:
  - `feat/<slug>` — new functionality
  - `fix/<slug>` — bug fixes
  - `docs/<slug>`, `chore/<slug>`, `refactor/<slug>` — everything else
- Open a pull request into `main`. Squash or merge, then delete the branch.

## Versioning

We follow [Semantic Versioning](https://semver.org):

- `MAJOR.MINOR.PATCH`, tags prefixed with `v` (e.g. `v0.3.1`).
- **Pre-1.0 (`v0.x.y`):** the API/UX may change between minor versions. Use
  `v0.x.0` for milestones and `v0.x.y` for fixes.
- **`v1.0.0`** marks the first stable release: the full v1 feature set (hot- and
  hardware-wallet EOA flows) complete, documented, and verified.
- After 1.0, breaking changes bump MAJOR, features bump MINOR, fixes bump PATCH.

## Changelog

Every user-facing change updates `CHANGELOG.md` under `## [Unreleased]`, grouped
as Added / Changed / Fixed / Removed / Security. On release, rename the
`[Unreleased]` heading to the version and date and start a fresh `[Unreleased]`.

## Cutting a release

Because Codeberg runs Forgejo, the GitHub `gh` CLI does **not** apply here.
Releases are created from an annotated tag, then published via the Codeberg web
UI or the Forgejo API.

### Release checklist

Do these in order on `main` (replace `X.Y.Z` throughout). Steps 1–5 are one commit.

**Prepare (code + docs):**
- [ ] 1. **`internal/buildinfo/buildinfo.go`** — set `const Version = "X.Y.Z"`
      (the single source of truth; the Makefile and the in-app updater read it).
- [ ] 2. **`FyneApp.toml`** — set `Version = "X.Y.Z"` to match (fallback for a bare
      `fyne package`; the Makefile overrides it from buildinfo anyway).
- [ ] 3. **`CHANGELOG.md`** — rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`
      and add a fresh empty `## [Unreleased]` above it.
- [ ] 4. **`README.md`** — update the status line (`Status: pre-1.0 (vX.Y.Z)`) if the
      milestone/feature summary changed.
- [ ] 5. **`docs/RELEASING.md` / `TODO.md`** — update if the process or roadmap
      changed. Commit steps 1–5: `git commit -m "release: vX.Y.Z"`.

**Verify:**
- [ ] 6. `go build ./... && go vet ./... && go test ./...` all green. (Test
      `TestEmbeddedReleaseKeyConfigured` guards that a real signing key is embedded.)

**Tag & push:**
- [ ] 7. `git tag -a vX.Y.Z -m "Callisto vX.Y.Z — <one-line summary>"`
- [ ] 8. `git push origin main --follow-tags`

**Build, sign, publish artifacts** (see below): run `make release` on each target
OS, collect `dist/`, then create the Codeberg release and upload every artifact.

**Post-release:**
- [ ] 9. Install the published build and confirm **Settings → Check for updates**
      reports up-to-date; on the *next* release, confirm it updates from the prior one.

### Publishing on Codeberg

- **Web UI:** repo → Releases → **New release** → pick the `vX.Y.Z` tag → title
  `Callisto vX.Y.Z` → paste the release message (template below) → attach **all**
  artifacts (each `*.zip`/`*.tar.gz`, `SHA256SUMS`, `SHA256SUMS.sig`) → leave
  "pre-release" unchecked → Publish.
- **API (Forgejo):** `POST /api/v1/repos/pasiphae/callisto/releases` (token,
  `tag_name`, `name`, `body`), then `POST …/releases/{id}/assets` per file.

Keep the Makefile's artifact filenames — the in-app updater matches its platform
build by the `-<goos>-<goarch>` fragment and fetches `SHA256SUMS`/`.sig` by name.

## Release artifacts & the packaging pipeline

Callisto ships as a native, double-clickable app, built by the `Makefile`. The
in-app updater (Settings → **Check for updates**) pulls new releases from the
Codeberg releases API and verifies them against a maintainer signing key before
installing, so **every release must carry a signed `SHA256SUMS`**.

### One-time setup: the release signing key

The updater embeds the maintainer's ed25519 **public** key
(`internal/updater/release_pubkey.ed25519`) and refuses to install any update whose
`SHA256SUMS` is not signed by the matching private key. Generate the keypair once:

```sh
make gen-release-key   # writes ~/.callisto/release_ed25519.key (private, 0600)
                       # and internal/updater/release_pubkey.ed25519 (public)
```

Keep the **private** key offline and backed up — losing it means shipping a new
public key in a build users already have (breaking auto-update until they manually
reinstall). Commit the regenerated **public** key. Until this is run, the repo
carries an all-zero placeholder and the updater reports "updates are not
configured" rather than installing anything unverifiable.

### Building & signing artifacts

```sh
make release        # build every artifact for the current OS + checksums + sign
```

`make release` produces, into `dist/`:
- **On macOS — both Mac architectures:** `Callisto-v<ver>-darwin-arm64.zip`
  (Apple silicon) *and* `Callisto-v<ver>-darwin-amd64.zip` (Intel). The Xcode
  toolchain cross-builds x86_64 from an arm64 host (and vice-versa), so one Mac
  produces both. Each user's in-app updater matches **its own** arch by filename, so
  a release must ship both.
- **On Linux:** `Callisto-v<ver>-linux-<arch>.tar.gz`.
- `SHA256SUMS` (over the archives) and `SHA256SUMS.sig` (ed25519, from
  `CALLISTO_RELEASE_KEY`).

The version comes from `internal/buildinfo` (single source of truth). Fyne is CGo,
so each **OS** must build natively — run `make release` on a Mac and on a Linux box
and merge the two `dist/` sets, or use `make package-linux-cross` (fyne-cross /
Docker) for the Linux build from macOS.

### Build-target matrix

| Target | Filename | How | Status |
| --- | --- | --- | --- |
| macOS Apple silicon | `…-darwin-arm64.zip` | `make package-mac-arm` (or `release` on a Mac) | shipped |
| macOS Intel | `…-darwin-amd64.zip` | `make package-mac-intel` (or `release` on a Mac) | shipped |
| Linux amd64 | `…-linux-amd64.tar.gz` | `make package-linux` on Linux, or `make package-linux-cross` | shipped |
| Linux arm64 | `…-linux-arm64.tar.gz` | native on an arm64 Linux box | not built yet |
| Windows | `…-windows-amd64.zip` | `fyne package` on Windows | not built yet |

Adding a target is just another artifact whose filename carries the
`-<goos>-<goarch>` fragment the updater matches on — no updater changes needed.
(A single universal-mac artifact was considered but rejected: `darwin-universal`
wouldn't match the arch-specific lookup already shipped in released builds.)

### First-launch note for users (unsigned build)

Callisto is not yet Apple-notarized, so the **first** launch of a browser-downloaded
build is gated by Gatekeeper. Document for users: right-click the app → **Open**
(once), or `xattr -dr com.apple.quarantine /Applications/Callisto.app`. In-app
updates afterward are downloaded by Callisto itself (not a browser), so macOS does
not quarantine them and they relaunch without a prompt.

## Release message template

Paste this as the Codeberg release body, replacing `X.Y.Z` and the highlights, and
appending the `CHANGELOG.md` section for this version.

```markdown
## Callisto vX.Y.Z

<one- or two-line summary of the headline change>

### Install

**macOS** — download `Callisto-vX.Y.Z-darwin-arm64.zip` (Apple silicon) or
`-darwin-amd64.zip` (Intel), unzip, and move **Callisto.app** to /Applications.
First launch: right-click → **Open** (once), or
`xattr -dr com.apple.quarantine /Applications/Callisto.app`.

**Linux** — download `Callisto-vX.Y.Z-linux-amd64.tar.gz` and extract.

Already running Callisto? Just use **Settings → Check for updates** — it verifies
and installs this release for you.

### Verify (optional)

```
shasum -a 256 -c SHA256SUMS
```
`SHA256SUMS` is ed25519-signed as `SHA256SUMS.sig` with the maintainer key (the
same key the in-app updater checks).

### Changes

<paste the CHANGELOG.md section for vX.Y.Z here>
```
