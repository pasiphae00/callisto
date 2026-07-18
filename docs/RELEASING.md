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

## Release artifacts (from v1.0.0)

Build reproducible binaries per platform before publishing, e.g.:

```sh
GOOS=darwin  GOARCH=arm64 go build -o dist/callisto-darwin-arm64  ./cmd/callisto
GOOS=darwin  GOARCH=amd64 go build -o dist/callisto-darwin-amd64  ./cmd/callisto
GOOS=linux   GOARCH=amd64 go build -o dist/callisto-linux-amd64   ./cmd/callisto
```

Attach the binaries (and their SHA-256 checksums) to the Codeberg release.

> Note: Fyne uses CGo for the desktop driver, so cross-compilation needs the
> target platform's toolchain (or build natively on each OS / in CI).
