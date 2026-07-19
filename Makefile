# Callisto build & release pipeline.
#
# Common targets:
#   make build            - build a plain dev binary into ./dist
#   make package-mac      - build Callisto.app (run on macOS) + a .zip artifact
#   make package-linux    - build a Linux .tar.gz artifact (run on Linux)
#   make checksums        - write dist/SHA256SUMS over the artifacts
#   make sign             - ed25519-sign SHA256SUMS (needs CALLISTO_RELEASE_KEY)
#   make release          - package + checksums + sign for the current OS
#   make gen-release-key  - one-time: generate the maintainer signing keypair
#   make clean            - remove ./dist
#
# Notes:
#   - Fyne uses CGo, so packaging must run natively on each target OS (no plain
#     cross-compile). package-linux-cross uses fyne-cross (Docker) if you need a
#     Linux build from macOS.
#   - The fyne packaging CLI is installed locally to ./bin (pinned to the library
#     version) by the `tools` target; it is not committed.

APP        := Callisto
APP_ID     := io.pasiphae.callisto
# Absolute so `fyne package --sourceDir ./cmd/callisto` resolves it (fyne treats a
# relative --icon as relative to the source dir, not the repo root).
ICON       := $(CURDIR)/images/CALLISTO-LOGO.png
MAIN       := ./cmd/callisto
DIST       := dist
BIN        := bin

# Single source of truth for the version: internal/buildinfo.
VERSION    := $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' internal/buildinfo/buildinfo.go)
GOOS       := $(shell go env GOOS)
GOARCH     := $(shell go env GOARCH)
FYNE       := $(BIN)/fyne
FYNE_VER   := v2.8.0

# Release signing key (ed25519 seed, hex). Keep the private key offline.
CALLISTO_RELEASE_KEY ?= $(HOME)/.callisto/release_ed25519.key
PUBKEY     := internal/updater/release_pubkey.ed25519

.PHONY: all build test vet package-mac package-linux package-linux-cross \
        checksums sign release gen-release-key tools clean

all: build

## --- development -----------------------------------------------------------

build:
	@mkdir -p $(DIST)
	go build -o $(DIST)/callisto $(MAIN)

test:
	go test ./...

vet:
	go vet ./...

## --- packaging -------------------------------------------------------------

# Install the fyne packaging CLI locally, pinned to the library version. Kept out
# of the module (its packaging-only deps aren't in our go.sum) by installing a
# standalone binary into ./bin.
tools: $(FYNE)
$(FYNE):
	GOBIN="$(CURDIR)/$(BIN)" go install fyne.io/fyne/v2/cmd/fyne@$(FYNE_VER)

# macOS: produce Callisto.app and zip it. Run on macOS.
package-mac: tools
	@mkdir -p $(DIST)
	rm -rf $(DIST)/$(APP).app
	$(FYNE) package --sourceDir $(MAIN) --icon $(ICON) --name $(APP) \
		--appID $(APP_ID) --appVersion $(VERSION) --appBuild 1 --release
	@# fyne writes $(APP).app next to the source; move it into dist/.
	mv $(MAIN)/$(APP).app $(DIST)/$(APP).app 2>/dev/null || mv $(APP).app $(DIST)/$(APP).app
	@# strip any quarantine xattr so a locally built app opens cleanly.
	-xattr -cr $(DIST)/$(APP).app
	cd $(DIST) && rm -f $(APP)-v$(VERSION)-darwin-$(GOARCH).zip && \
		ditto -c -k --sequesterRsrc --keepParent $(APP).app $(APP)-v$(VERSION)-darwin-$(GOARCH).zip
	@echo "built $(DIST)/$(APP)-v$(VERSION)-darwin-$(GOARCH).zip"

# Linux: produce a tar.gz of the packaged app dir. Run on Linux.
package-linux: tools
	@mkdir -p $(DIST)
	$(FYNE) package --sourceDir $(MAIN) --icon $(ICON) --name $(APP) \
		--appID $(APP_ID) --appVersion $(VERSION) --appBuild 1 --release
	mv $(MAIN)/$(APP).tar.xz $(DIST)/ 2>/dev/null || mv $(APP).tar.xz $(DIST)/ 2>/dev/null || true
	@# Re-emit as tar.gz (stdlib-friendly for the in-app updater): unpack the
	@# fyne tar.xz and repack, or build the tree directly if already extracted.
	cd $(DIST) && tar -xf $(APP).tar.xz && rm -f $(APP).tar.xz && \
		tar -czf $(APP)-v$(VERSION)-linux-$(GOARCH).tar.gz usr && rm -rf usr
	@echo "built $(DIST)/$(APP)-v$(VERSION)-linux-$(GOARCH).tar.gz"

# Linux build from macOS via Docker. Requires: go install github.com/fyne-io/fyne-cross@latest
package-linux-cross:
	fyne-cross linux -arch=amd64 -app-id $(APP_ID) -icon $(ICON) -name $(APP) $(MAIN)
	@echo "see fyne-cross/dist/ for the Linux artifact"

## --- release integrity -----------------------------------------------------

# SHA-256 over every distributable artifact (zip/tar.gz) in dist/.
checksums:
	cd $(DIST) && shasum -a 256 *.zip *.tar.gz 2>/dev/null > SHA256SUMS || true
	@cat $(DIST)/SHA256SUMS 2>/dev/null || echo "no artifacts to checksum"

# One-time: generate the maintainer signing keypair. Writes the private seed to
# $(CALLISTO_RELEASE_KEY) (keep it offline) and the public key to $(PUBKEY)
# (committed and embedded into the binary).
gen-release-key:
	@mkdir -p $(dir $(CALLISTO_RELEASE_KEY))
	go run ./cmd/callisto-release genkey --out "$(CALLISTO_RELEASE_KEY)" --pub "$(PUBKEY)"

# Detached ed25519 signature over SHA256SUMS.
sign:
	go run ./cmd/callisto-release sign --key "$(CALLISTO_RELEASE_KEY)" \
		--in $(DIST)/SHA256SUMS --out $(DIST)/SHA256SUMS.sig
	@echo "wrote $(DIST)/SHA256SUMS.sig"

# Full local release for the current OS: package, checksum, sign.
release:
	@if [ "$(GOOS)" = "darwin" ]; then $(MAKE) package-mac; else $(MAKE) package-linux; fi
	@$(MAKE) checksums
	@$(MAKE) sign
	@echo
	@echo "Release artifacts in $(DIST)/ (v$(VERSION)):"
	@ls -1 $(DIST) | sed 's/^/  /'
	@echo
	@echo "Next: create the v$(VERSION) release on Codeberg and upload the .zip/.tar.gz,"
	@echo "SHA256SUMS, and SHA256SUMS.sig. See docs/RELEASING.md."

clean:
	rm -rf $(DIST)
