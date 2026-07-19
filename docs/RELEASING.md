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

1. Ensure `main` is green: `go build ./... && go vet ./... && go test ./...`.
2. Move `CHANGELOG.md`'s `[Unreleased]` section to the new version + date.
3. Commit the changelog on `main`.
4. Create an annotated tag and push it:
   ```sh
   git tag -a v0.1.0 -m "Callisto v0.1.0 — <one-line summary>"
   git push origin main --follow-tags
   ```
5. Publish the release on Codeberg:
   - **Web UI:** repo → Releases → New release → pick the tag → paste the
     changelog section as the release notes → attach any built binaries.
   - **API (Forgejo):** `POST /api/v1/repos/pasiphae/callisto/releases` with a
     token, `tag_name`, `name`, and `body`. See the Forgejo API docs.

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

Fyne uses CGo, so each OS builds **natively** (no plain cross-compile). On the
release machine for that OS:

```sh
make release        # package for the current OS + checksums + sign
```

`make release` runs, for the current OS:
- `package-mac` → `Callisto.app` + `Callisto-v<ver>-darwin-<arch>.zip`, **or**
  `package-linux` → `Callisto-v<ver>-linux-<arch>.tar.gz`;
- `checksums` → `dist/SHA256SUMS`;
- `sign` → `dist/SHA256SUMS.sig` (ed25519, from `CALLISTO_RELEASE_KEY`).

The version comes from `internal/buildinfo` (single source of truth). For a Linux
build from macOS, `make package-linux-cross` uses `fyne-cross` (Docker). Run
`make release` once per target OS and collect the artifacts into one `dist/`.

### Publishing on Codeberg

Create the release for the tag and upload **all** of: each platform archive
(`*.zip` / `*.tar.gz`), `SHA256SUMS`, and `SHA256SUMS.sig`. The updater matches its
platform archive by the `-<goos>-<goarch>` fragment in the filename, so keep the
names produced by the Makefile.

- **Web UI:** repo → Releases → New release → pick the tag → paste the changelog
  section as the notes → attach the artifacts.
- **API (Forgejo):** `POST /api/v1/repos/pasiphae/callisto/releases`, then
  `POST …/releases/{id}/assets` per file. See the Forgejo API docs.

### First-launch note for users (unsigned build)

Callisto is not yet Apple-notarized, so the **first** launch of a browser-downloaded
build is gated by Gatekeeper. Document for users: right-click the app → **Open**
(once), or `xattr -dr com.apple.quarantine /Applications/Callisto.app`. In-app
updates afterward are downloaded by Callisto itself (not a browser), so macOS does
not quarantine them and they relaunch without a prompt.
