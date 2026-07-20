# To-do items for `callisto`

## v0.11 — Safe / multisig deep dive (next major)

User has ideas to scope here; captured items so far:

- **Safe balances use auto-discovery.** The Safe transfer picker currently loads
  only curated + user tokens (`safe_pane.go` → `Load(safeAddr, TokensForChain(...))`),
  so a Safe that holds many tokens shows just ETH/USDC. Wire the EOA automatic-balance
  machinery (`assets.DiscoverTokens` + `tokenDiscovery`, already account-keyed) onto the
  Safe address: discover held tokens, persist per Safe, and decide UX — likely a proper
  Safe **Assets view** (not just the transfer dropdown), with the same hide-spam / sort /
  dust-hiding behavior. Consider whether the Safe gets its own hidden-set and refresh
  cadence.

## v0.10 — hot-wallet key management (next major, user-approved 2026-07-19)

All four areas approved. Key material is the #1 correctness/security concern
(PRINCIPLES), so plan carefully and keep the passphrase-encrypted keystore the
portable source of truth throughout. Suggested build order (pure-Go first, the
platform/CGo Keychain last):

1. **Passphrase lifecycle** — `keystore.Rekey(old,new)` (decrypt seed → re-encrypt
   under a new passphrase → atomic replace); "Change passphrase" UI; strength hint
   at import; clarify shared-keystore-passphrase semantics (accounts from one import
   share a keystore today).
2. **Backup / reveal / import** — reveal recovery phrase behind an auth gate (+ big
   warning, decrypt→show→wipe); export an encrypted keystore backup; derive more
   accounts from an existing seed; import geth/MetaMask JSON keystore, raw private
   key, and watch-only addresses.
3. **Auto-lock & re-auth** — a session/lock manager: inactivity timer (reset on
   activity), lock-on-sleep, configurable timeout; optional "confirm each signature"
   requiring re-auth (passphrase or Touch ID) before `SignTx`. Wires into the signer
   session / `App.clearSigner`.
4. **Keychain + Touch ID unlock** (headline; macOS-first, CGo) — a `SecretStore`
   interface in `internal/keystore` with a macOS backend (Security framework;
   Touch ID via LocalAuthentication/LAContext) that holds the keystore's *wrapping
   key* (never the seed); passphrase file stays the fallback. Enroll/remove UI;
   graceful degrade off macOS. Supersedes the "OS keychain-backed keystore" item
   below. Needs live-device verification. Then wire Touch ID into #3's re-auth.

## bugs

- ~~trezor eth transfer: "Sign: transaction type not supported"~~
  - ✅ **Fixed with native EIP-1559 signing — verified live** (a real ETH
    transfer signed on a Trezor Safe 5 broadcast and confirmed). Callisto builds
    EIP-1559 dynamic-fee txs, but go-ethereum's
    vendored Trezor protobuf only has the legacy `EthereumSignTx` message: the
    driver signed the tx as legacy on-device, then couldn't apply that signature
    back to the dynamic-fee tx (`EIP155Signer` rejects non-legacy types →
    `ErrTxTypeNotSupported`), exactly after the on-device confirmation.
  - Fix (`internal/signer/hardware/usbwallet/trezor.go`): `SignTx` routes
    dynamic-fee txs to a new `trezorSignEIP1559`, which sends the
    `EthereumSignTxEIP1559` message (wire type **452**, from trezor-firmware's
    `messages.proto`). Since the vendored trezor package has no Go type for it,
    the message is hand-encoded with `protowire` (`encodeEthereumSignTxEIP1559`;
    field numbers/types from `messages-ethereum.proto`, verified field-by-field
    in `trezor_eip1559_test.go`), and the returned signature (y-parity v
    normalized to 0/1) is applied with the dynamic-fee signer. This matches how
    Frame and Trezor Suite sign — full 1559, no legacy downgrade. Genuine legacy
    txs still use the original path. Released in v0.3.2.

## researched / planned

### Trezor overhaul: drop Suite/Bridge (direct libusb, like Frame) + native streaming typed-data
- **Priority (user, 2026-07-18):** the Trezor integration is too clunky. Frame
  works with a Trezor with no Trezor Suite/Bridge running; Callisto shouldn't
  need it either. Two parts:
  1. **Drop the Bridge dependency** — direct libusb transport. ✅ **DONE in v0.7.0,
     verified live** (Trezor Safe 5, ETH send signed with Trezor Suite closed):
     migrated the USB backend from `ethereum/hid` to `github.com/karalabe/usb`
     (bundled libusb+hidapi, self-contained), forked the Ledger driver in so
     upstream go-ethereum usbwallet is no longer imported, Trezor Safe uses raw
     libusb (its WebUSB interface, `Hub.raw`), Bridge kept as an auto-fallback.
     `hwscan` shows `[raw] VID=0x1209 PID=0x53c1 iface=0`.
  2. **Native streaming EIP-712** — ✅ **DONE in v0.7.1, verified live** (CoW Swap
     order signed on a Trezor Safe 5). Replaced the experimental
     `EthereumSignTypedHash` (470) — which newer firmware rejects as "Unexpected
     message" unless experimental features are on — with the full streaming flow
     (`EthereumSignTypedData` 464 → StructRequest/StructAck 465/466 →
     ValueRequest/ValueAck 467/468 → EthereumTypedDataSignature 469, all wire
     types hand-encoded via protowire, plus a typed-data JSON → type/value model in
     `trezor_typeddata.go`). WalletConnect `eth_signTypedData_v4` and Safe-owner
     typed-data now work on a stock Trezor.
- Frame (github.com/floating/frame, `main/signers/trezor`) talks to Trezor with
  **no** Trezor Suite/Bridge running. Confirmed from its source: it uses
  `@trezor/connect` configured with `transports: ['NodeUsbTransport']` — i.e.
  **direct USB via libusb** (the `usb` npm package), not HID and not the local
  HTTP bridge.
- This is *the* reason our own direct-USB attempt failed and we fell back to the
  bridge: go-ethereum's `usbwallet` uses **HID** (hidapi), and the Trezor Safe's
  wallet-protocol USB interface is a vendor/WebUSB interface that **hidapi cannot
  claim** — but **libusb can**. (Same reason go-ethereum's own blocked fix,
  PR #32752, is switching from `karalabe/hid` to `karalabe/usb`.)
- Plan: add a **libusb-based transport** in Go (e.g. `github.com/google/gousb`
  or `karalabe/usb`) that claims the Trezor vendor interface directly and speaks
  the wire protocol over bulk transfers — reusing the existing `trezorTransport`
  abstraction and protobuf logic; only the transport differs. This would make
  the bridge optional (nice UX: no background app) and likely resolve the
  repeated-reconnect instability seen with the bridge.
- Cost: a new native/CGo dependency (libusb) + USB endpoint discovery + bulk
  transfer framing + per-OS testing. Meaningful effort; the bridge path works
  today, so this is an enhancement, not a blocker.
  - ✅ **Resolved and verified live** against a real Trezor Safe 5 (multiple
    successful runs: standard-wallet address derivation, 3-account listing,
    passphrase/hidden-wallet flow, on-device passphrase entry).
  - **Root cause 1 — detection.** `go run ./cmd/hwscan` (new diagnostic tool;
    lists every raw USB HID device the OS sees, regardless of whether Callisto
    recognizes it) showed the device as `VID=0x1209 PID=0x53c1 iface=1
    usagePage=0xf1d0 "Trezor Company" "Trezor Safe 5"`. go-ethereum's
    `usbwallet` matcher requires vendor+product ID **and** (usagePage==0xffff
    **or** interface==0) — this device satisfies neither, so it was silently
    filtered out even though the OS exposed it fine. Known, still-open upstream
    bug: [go-ethereum#31841](https://github.com/ethereum/go-ethereum/issues/31841)
    (the proposed fix, [PR #32752](https://github.com/ethereum/go-ethereum/pull/32752),
    is a draft blocked on an unrelated, unmerged dependency swap).
  - **Root cause 2 — even when matched, direct USB doesn't work.** For this
    device, the OS's HID layer can enumerate *something* at that VID/PID, but
    writes to it are accepted while reads never return data — direct USB
    access simply cannot reach the device's real wallet-protocol endpoint on
    this platform. Confirmed by timing out at exactly the (new) 60s read-timeout
    bound rather than hanging forever.
  - **Fix — three parts**, all in a local fork of three go-ethereum files
    (`hub.go`, `wallet.go`, `trezor.go`; LGPL-3.0, license/attribution
    preserved) at `internal/signer/hardware/usbwallet/` (see that package's doc
    comments for full rationale):
    1. Trezor's hub matcher now matches on vendor+product ID alone (safe for
       Trezor specifically — unlike Ledger's PID, which encodes model+interface
       bits and genuinely needs the interface check; Ledger is untouched and
       still uses upstream go-ethereum directly).
    2. Added a **Trezor Bridge (trezord) HTTP transport** as the primary path
       for Trezor (tried before direct USB, which is kept as a fallback for
       older devices) — Bridge already solves USB access correctly on every
       OS, so this sidesteps the direct-USB dead end entirely rather than
       chasing it further. Includes port discovery (trezord's own port isn't
       fixed — observed shifting from 21325 to 21328 across a single Suite
       relaunch on this machine), and self-healing session acquisition
       (recovers from a stale "wrong previous session" without requiring the
       user to replug).
    3. Added bounded read timeouts on **both** transports (60s) — previously a
       non-responding device hung `Open()` forever with the UI stuck on
       "Connecting…" and no way to cancel.
  - **Passphrase / hidden wallets** — folded into this fix once Bridge
    communication was proven live (see "trezor wallet types" below, now
    resolved): standard wallet (empty passphrase) and host-supplied hidden-wallet
    passphrases both verified to derive distinct, correct addresses; **on-device
    passphrase entry** (`PassphraseRequest.OnDevice`) is also detected and
    respected — when the device's own "enter passphrase on Trezor" security
    setting is active, Callisto no longer sends a possibly-ignored host string;
    it waits for the user to type on the device screen instead.
  - **Newer Trezor Suite bridge variant (v3.2.1+) — also resolved.** The
    "known gap" noted earlier turned out to be findable: newer Suite builds
    embed `@trezor/transport` (TypeScript), not classic `trezord-go`, and its
    `/call` wire format is undocumented in the classic Bridge API reference.
    Found by reading trezor-suite's own source
    (`packages/transport/src/utils/bridgeProtocolMessage.ts`): both request
    and response bodies are JSON (`{"protocol":"v1","data":"<hex>"}`), **and**
    the hex `data` field itself still carries the legacy USB-HID report
    framing (`0x3f` report-ID byte + `0x23 0x23` magic + type + length) rather
    than the simpler 6-byte header classic `trezord-go` uses — confirmed by
    decoding a live device response and finding exactly that byte layout.
    `BridgeClient.Call` now detects which of the two formats a given bridge
    wants (tries the newer wrapped format first, falls back to legacy,
    remembers the result per client) and frames each correctly. Verified live:
    a clean `Open()` succeeded against this device/bridge combination with the
    corrected framing.
  - ⚠️ **Caveat, not fully closed out:** rapid repeated connect/disconnect
    cycles against the same device in a short window (as happened during
    testing — 15+ attempts in quick succession) produced inconsistent
    behavior on later attempts (varying errors), likely bridge/device-side
    session state churn rather than a Callisto bug — a fix for leaking a
    Bridge session on a failed `Open()` (now released via `Close()`) was found
    and applied along the way, but full stability under back-to-back
    reconnects wasn't independently reverified afterward. Normal usage
    (connect once per session, not rapid test cycling) is expected to be
    fine; flag if repeated connect/disconnect in real use shows the same
    instability.

### OS keychain-backed keystore (defense in depth)
- **What:** back the encrypted hot-wallet keystore (`internal/keystore`, today a
  scrypt+AES-256-GCM file guarded by a user passphrase) with the operating
  system's secret store, so the seed's encryption secret is held by the OS and
  unlock can ride the platform's own auth (e.g. Touch ID) instead of only a typed
  passphrase.
- **Design constraints:**
  - The passphrase-encrypted **file stays the source of truth and portable
    fallback**. The keychain is an *optional* second factor/convenience, not a
    replacement — a user must always be able to unlock with the passphrase alone
    (e.g. after moving the config to another machine, or if the keychain entry is
    lost). Never make the OS store the *only* copy of anything unrecoverable.
  - Store the *keystore encryption key* (or a wrapping key) in the OS store, not
    the raw seed, so the on-disk format and its scrypt guard are unchanged and the
    keychain is a clean add-on layer behind a small `SecretStore` interface.
  - Per-platform backends, degrading gracefully to "passphrase only" when absent:
    **macOS** Keychain (Security framework; Touch ID via LAContext — needs CGo or
    a small Objective-C shim), **Linux** Secret Service / libsecret (D-Bus),
    **Windows** DPAPI / Credential Manager.
  - **Dependency discipline (PRINCIPLES):** prefer a tiny self-contained shim over
    a heavy cross-platform keyring dep; if one is used, vet its transitive tree the
    same way we rejected btcutil. Evaluate `github.com/zalando/go-keyring` (no-CGo
    on Linux/Windows, shells out to `security` on macOS — but that path can't gate
    on Touch ID) vs. a small direct Security-framework binding for the macOS
    biometric story.
  - Wipe-on-remove must also clear the OS keychain entry (extend
    `maybeWipeKeystore`). Enrolling/removing keychain backing needs UI in Settings
    or the wallet's context menu.
- **Status:** designed-for, not built. Supersedes the brief roadmap note under the
  v0.5.0 keystore item below. Primary user is on macOS, so lead with the macOS
  Keychain + Touch ID path.

### GridPlus Lattice1 signer
- **What:** add the GridPlus Lattice1 as a hardware `signer.Signer` alongside
  Ledger/Trezor (currently stubbed — see DESIGN.md "support for grid lattice" and
  the README roadmap). Lets a Lattice sign EOA sends, Safe-owner hashes, and dApp
  (WalletConnect) requests, same as the other hardware signers.
- **Why it's non-trivial:** **no Go SDK exists.** The reference is the JS
  `gridplus-sdk` (`@gridplus/gridplus-sdk`). Unlike Ledger/Trezor (local USB), the
  Lattice is reached over an **end-to-end-encrypted channel relayed through
  GridPlus's routing service** (the device is addressed by its `deviceId`): a
  one-time **pairing** (ECDH → shared secret, user enters a pairing code shown on
  the Lattice screen) establishes a persistent secure channel, then signing
  requests (`sign` with an EVM/generic-signing payload) are encrypted to the
  device and the user approves on-device. This is the same "implement the protocol
  ourselves from the reference SDK, no new heavy dep" pattern we used for
  WalletConnect and the Trezor wire messages.
- **Design constraints:**
  - New `internal/signer/hardware/lattice` (or similar) implementing
    `signer.Signer` + the optional capabilities it can serve (`SafeHashSigner`,
    `PersonalSigner`, `TypedDataSigner`) so it slots into every existing flow with
    no core rewrite — mirror how Ledger/Trezor register.
  - Reuse existing crypto primitives (secp256k1/ECDH, AES) already vendored via
    go-ethereum; **no new signing-critical dependency** (same bar as HD
    derivation). Persist the pairing (device id + channel keys) in config,
    encrypted, so re-pairing isn't needed every launch.
  - Port from the `gridplus-sdk` protocol (pairing handshake, request/response
    envelope framing, the EVM signing request encoding), verified against a real
    Lattice1 — a live device is required, like all prior hardware work.
- **Status:** designed-for, stubbed, not built. Out of scope until a Lattice1 is on
  hand to verify against.

## minor

### show full wallet address in wallets pane — ✅ done
- ~~lets show the full wallet address in the wallets selection pane... copyable~~
  - Wallets pane now has a detail bar below the list: selecting a wallet shows
    its full, checksummed address in a mono, selectable field, plus an explicit
    Copy button (writes to the system clipboard). The field is read-only in
    effect (edits revert) but stays fully interactive so text-selection copy
    works too. The list row itself still shows the short address, as intended.

### populate "about" dialogue — ✅ done
- ~~lets fill in the callisto -> about dialogue... black background, gray text
  in berkely mono... callisto png (w/ transparency)... trust but verify~~
  - Callisto menu → "About Callisto": transparent NASA Callisto logo, Berkeley
    Mono throughout, "Callisto <version>" / "commit <short>" (commit read
    automatically from Go's embedded VCS info, no manual step), tagline, link,
    ©2026, and the italic disclaimer at the bottom. Background reads the live
    theme color rather than a hardcoded black, so it matches the dialog's own
    chrome/border seamlessly instead of showing a two-tone box (caught via a
    screenshot review — first pass used flat black, which visibly mismatched).
  - `internal/buildinfo`: `Version` (bumped by hand at release) + `ShortCommit()`
    (from `runtime/debug.ReadBuildInfo()`'s VCS stamp — automatic, no ldflags).

### app logo — ✅ done
- ~~the app logo for the dock should be `images/CALLISTO - LOGO.png`~~
  - Embedded and set via both `fyne.App.SetIcon` and `Window.SetIcon` — covers
    dock/taskbar and window icon across platforms.

### "green dot emoji" indicator next to active wallet — ✅ done
- ~~the green circle emoji looks a bit out of place... personally not a fan of emoji's in UIs~~
  - Wallets list now uses a genuinely colored `●` glyph (canvas.Text, same
    green/gray pattern as the connection status dot) instead of the 🟢 emoji —
    that was the actual complaint. The 🔒/🔓 lock icons were kept (explicitly
    requested back after the first pass over-corrected and dropped those too).

### out of the box default rpc — ✅ done (v0.4.0)
- ~~lets actually ship callisto with a default mainnet endpoint, `https://rpc.flashbots.net/fast?originId=callisto-system`, labeled `Ethereum Mainnet (via flashbots protect)`, so users have a working kit out of the box but can still replace it~~
  - Seeded on genuine first run only (no config file), selected and auto-connecting;
    removing all endpoints later is respected (not re-seeded). Implemented in
    `config.defaultConfig` / `Load`. Supersedes the original no-default-RPC MUST in
    DESIGN.md (updated there, in CLAUDE.md, and the README security model).

### default rpc — ✅ done
- ~~option to select an rpc as default when adding, if checked, auto-connect on start~~
  - Add-endpoint dialog now has an "Auto-connect on startup (default endpoint)"
    checkbox; the default is exclusive (marked ⭐ in the list) and auto-connects
    when the app launches.

### font — ✅ done
- ~~lets use `BerkeleyMono` for the font for addresses and numerical values~~
  - Berkeley Mono is now embedded (go:embed, under the project's indie license)
    and applied via a custom Fyne theme to monospace-tagged display text:
    Assets/Wallets/History rows, the pre-sign review values, and resolved-address
    status. A user override is supported via `CALLISTO_FONT_DIR`.
  - Fonts organized into `internal/ui/fonts/` (Regular + Bold embedded; the
    oblique/light variants are kept there for future use).

### decimal trimming — ✅ done
- ~~lets show 5 decimals of token amounts, no need for anything beyond that~~
  - Display truncates to 5 decimals (Assets list + Send picker); exact values are
    still used for signing.

### hide zero (and near) token amounts — ✅ done
- ~~on the asset page, lets hide tokens that have zero or dust balance~~
  - Assets page hides non-native tokens below 0.00005 (default), showing a
    "N dust hidden" note. Native asset is always shown. (Send lists everything.)

### indicator next to active wallet — ✅ done
- ~~currently gray, maybe should be green~~ → active wallet now marked with 🟢.

## medium

### improve balances system — ✅ shipped v0.10.0 (automatic balances)
- ~~during testing, callisto did not auto-populate balances of new tokens (purchased several random coins)~~
- ~~detect transfer logs for the active wallet (live and historical) and show all non-0 non-dust balances~~ → `assets.DiscoverTokens` scans `Transfer(→account)` logs (full history on connect, then incremental from a watermark on every new head = live), feeding the token set into `Service.Load`.
- ~~detect transfer logs, then call `name()`/`symbol()`/`decimals()` to parse+populate~~ → discovered contracts go through the existing metadata/balance load (which already drops non-ERC-20s and dust); 4-topic ERC-721 Transfers are filtered out so NFTs don't pollute the list.
- ~~no more "refresh tokens"/"refresh balances" clicks~~ → both Refresh buttons removed; balances auto-refresh per block and a metadata-caching `assetService` avoids re-fetching immutable metadata each block.
- ~~persist the discovered token set so a full Transfer scan isn't re-run every launch~~ → `discovered_tokens` + `token_scan` tables (store migrations 8/9), `tokenCache` in `internal/ui`; the set is hydrated on launch and re-discovery only scans blocks since the persisted watermark.

### create "approvals" pane — ✅ shipped v0.9.0, enhanced v0.9.1, verified live
- ~~see/scroll all outstanding ERC-20 approvals for the selected wallet (however
  created); show token, spender (by name — cowswap/uniswap/…), and unlimited vs a
  specific amount; a Revoke button; revoked entries disappear once confirmed on
  chain (revoke tx logged in history)~~
  - **`internal/approvals`** scans `Approval` logs on the active RPC, bounded below
    by the wallet's first tx (a `NonceAt` binary search — no genesis scan), then
    reads live `allowance()` to keep only outstanding ones. **Full Permit2 coverage**
    (user chose it): the Permit2 contract's own `Approval`/`Permit` logs + `allowance`
    give the inner per-dApp grants (with expiry). Spender names via a bundled
    per-chain `knownSpenders` map (`labels.go`). Unlimited = ≥ 2^255 (ERC-20) /
    MaxUint160 (Permit2). Revoke = `approve(spender,0)` or Permit2 `lockdown`.
  - **UI** `internal/ui/approvals_pane.go` (nav "Approvals"): Scan → list rows
    (token → spender, UNLIMITED/amount, Permit2 badge+expiry) with a red Revoke that
    runs the review→sign→broadcast→track pipeline, logs to History (Kind `revoke`),
    and drops the row on confirmation. `rpc.Client` gained `FilterLogs`.
  - **v0.9.1 enhancements:** persisted approvals + per-(chain,owner) scan watermark
    (`internal/approvals/cache.go`, store migrations 6/7) → **incremental re-scans**
    (`Scanner.Refresh` scans only new blocks + re-reads live allowances); **live WSS
    watch** (`Scanner.Watch` + `rpc.Client.SubscribeFilterLogs`, opt-in
    `config.AutoDetectApprovals` checkbox); **progress-bar ETA**; instant cached
    display on tab open. Adaptive `getLogs` window honors a node's block-range cap.
  - **Discovery needs an ARCHIVE RPC** for full history — a pruned node (incl. erigon
    `--prune.mode=full`) only keeps recent logs, so old approvals are invisible.
    Verified live against the Ganymede archive node (full history + Permit2 found).
    The v0.9.1 default endpoint is that archive node, so it works out of the box.
  - **⭐ NEXT follow-up — "Full re-scan" button:** a scan on a limited/pruned RPC
    advances the watermark to head with an empty result; a later scan then runs
    incrementally and never re-reads old history (a real trap — hit during v0.9.1
    testing, worked around by deleting the `approval_scan` row). Add a button that
    clears the watermark + cache and forces a full scan. Small; slot into v0.9.2.
  - **Other follow-ups:** NFT `setApprovalForAll` (ERC-721/1155); an optional
    external-API / node-proxy discovery accelerator behind a Settings toggle;
    allowance *editing* (only full revoke today).

### created "packaged" pipeline — ✅ shipped in v0.8.0
- ~~makefile to deliver a native, logo-bearing, clickable app for macOS + Linux;
  each tagged version downloadable; updating preserves wallet/rpc/history; an
  "update app" button in settings~~
  - **Packaging:** `Makefile` (+ `FyneApp.toml`) builds `Callisto.app` (macOS) and a
    Linux `.tar.gz` via `fyne package` (CLI installed locally to `./bin`, no new
    module dep). `make release` = package + `SHA256SUMS` + ed25519 signature, ready
    to upload to a Codeberg release. Version is single-sourced from
    `internal/buildinfo`. See `docs/RELEASING.md`.
  - **In-app updates (`internal/updater`):** Settings → Check for updates hits the
    Forgejo releases API, compares via `x/mod/semver`, shows the changelog, then
    downloads → **verifies (embedded ed25519 maintainer key over SHA256SUMS +
    per-artifact SHA-256)** → swaps the bundle in place → relaunches. Refuses any
    unverified/tampered artifact. User data lives in the OS config dir (outside the
    bundle), so updates preserve it automatically. Signing key via
    `make gen-release-key` / `cmd/callisto-release`.
  - **Deferred follow-ups:** Apple Developer-ID signing + notarization (drops the
    one-time right-click→Open); Windows packaging; Linux AppImage/`.deb`; a `.dmg`
    cosmetic installer; delta updates. Full auto-update verified headlessly (unit
    tests) + local packaging; the live download→install→relaunch is verified on the
    user's machine once the first signed release is published.

### CI/CD: automate releases on tag push (user, 2026-07-19)
- **Goal:** pushing a `vX.Y.Z` tag triggers CI to build, package, checksum, **sign**,
  and publish the Codeberg release automatically — no manual `make release` +
  web-UI upload. Builds on the v0.8.0 pipeline (`make release` already does
  everything; CI just needs to run it per-OS and upload).
- **Runner:** Codeberg supports **Forgejo Actions** (GitHub-Actions-compatible
  workflows in `.forgejo/workflows/`). A tag-triggered job matrix over macOS +
  Linux runners runs `make package-<os>`, then a job collects artifacts, runs
  `make checksums` + `make sign`, and creates the release via the Forgejo API
  (`POST /repos/pasiphae/callisto/releases` + asset uploads). Fyne is CGo, so each
  OS needs a native runner (or fyne-cross for Linux); confirm Codeberg's hosted
  runner availability vs. self-hosting one.
- **⭐ Signing on the CD server — the crux (security):** moving the ed25519 release
  key off the maintainer's offline machine into CI weakens the trust root (the
  updater installs anything signed by it). Options, roughly increasing safety:
  (a) CI secret holding the private key — simplest, but the key is exposed to the
  runner/anyone who can edit workflows; (b) sign in a hardened/isolated job with
  restricted secret scope; (c) a hosted KMS/HSM signer (sign via API, key never
  leaves the HSM); (d) keep signing **offline/manual** and let CI only build +
  draft the release, maintainer signs + publishes. Given this signs a **wallet's
  auto-update trust root**, lean toward (c) or (d); (a) is a real supply-chain risk.
  Decide this explicitly before wiring it up.
- **Also:** cache Go build + the local `./bin/fyne`; gate release jobs on tests
  passing; keep `internal/buildinfo.Version` as the version source (or derive from
  the tag and assert they match).

### enable passphrase unlock of hot-wallets — ✅ done (v0.5.0)
- ~~the recovery phrase should only be needed on the first import; enforce a passphrase to encrypt the keystore; unlock re-enters only the passphrase; one-time import, not a frequent phrase re-entry~~
  - New `internal/keystore` (scrypt N=2^18 + AES-256-GCM, authenticated) encrypts the
    BIP-39 seed under a user passphrase; `hot.NewKeystore`/`OpenFromKeystore`. Import is
    now one-time with an **account-selection list** (index→address, multi-select); each
    selected account is a descriptor sharing one keystore (`Descriptor.KeystoreID`).
    Unlock is passphrase-only (legacy phrase-unlock kept as a fallback for pre-0.5
    wallets). Delete wipes the keystore once the last referencing account is removed.
    Security-model docs (README/DESIGN/CLAUDE) updated.
  - **roadmap follow-up:** back the keystore with the OS keychain for defense in
    depth — see the dedicated **"OS keychain-backed keystore"** section under
    researched / planned above.

### support wallet connect — ✅ shipped in v0.6.0 (initial integration)
- ~~paste a walletconnect link and sign transactions with the configured wallet on arbitrary web3 dApps; a separate pane linking whichever wallet is selected~~
  - Hand-implemented WalletConnect v2 Sign (wallet role) in `internal/walletconnect`
    — no Go SDK exists — using only stdlib + already-vendored x/crypto + the
    existing gorilla/websocket dep. Relay client (Ed25519 JWT + embedded Reown
    projectId, env-overridable), X25519/HKDF + ChaCha20-Poly1305 envelopes, the
    full pair→propose→approve→settle→request session engine, and a WalletConnect
    pane: paste URI, approve a session exposing the active wallet, review + sign
    requests. Handles `eth_sendTransaction` (through the tx pipeline),
    `eth_signTransaction`, `personal_sign`, and `eth_signTypedData_v4`.
  - **Verified live on mainnet:** a real Uniswap swap (eth_sendTransaction) and a
    CoW Swap order (eth_signTypedData_v4) signed with a hot wallet.
  - Caveats for the initial release: **hot + Ledger** are fully supported;
    **Trezor** does sends + personal_sign, but typed-data needs the device's
    experimental features enabled (fixed properly by the Trezor overhaul above).
    Sessions are in-memory (not persisted across restarts).

### trezor wallet types — ✅ done (see "bugs" above for the full story)
- ~~trezor is kind of weird... "unlock with pin (on device) -> enter passphrase
  (on computer) -> THEN select derivation index"~~
  - Confirmed and implemented, folded into the Trezor detection fix above since
    it surfaced during that live testing. Add-hardware and unlock dialogs now
    have a Passphrase field (Trezor only); empty = standard wallet, non-empty =
    a distinct hidden wallet — both verified live to derive different, correct
    addresses. On-device passphrase entry (a separate device security setting)
    is also handled correctly: when active, the host-supplied field is ignored
    and Callisto waits for entry on the device's own screen instead.

## major

### gnosis Safe multisig — ✅ shipped in v0.4.0
- Dedicated Safe tab: import by address (owners/threshold/nonce/version read
  on-chain), client-side owner labels, Safe balances.
- Propose ETH/ERC-20 transfers and owner/threshold admin actions (add / remove /
  replace owner, change threshold); canonical `safeTxHash` from the contract's
  `getTransactionHash`, cross-checked against a local EIP-712 computation.
- Local signature collection by switching unlocked owners (no Safe service): hot
  wallets sign the hash directly (v 27/28); Ledger + Trezor sign via eth_sign
  (v 31/32) — Trezor `EthereumSignMessage` wired into the usbwallet fork. New
  `signer.SafeHashSigner` optional capability.
- Execute once threshold met (packs sigs → `execTransaction` as a normal EIP-1559
  tx from the executing owner → tracked to inclusion → history). Same-nonce
  rejection cancels a proposal.
- ✅ **Verified live on mainnet** — a real propose → sign (one **hot** owner +
  one **Trezor** owner, eth_sign path) → execute cycle on a live Safe (v1.4.1,
  2-of-4) broadcast and confirmed. Ledger's eth_sign path uses upstream go-ethereum
  `SignText` unchanged; not separately device-tested but shares the same code path.

### transaction simulation
- lets plan and figure out the best way to surface to the user the option to "simulate" a transaction against a blockchain snapshot
- we can implement it ourself, we could also use tenderly
  - i lean towards keeping everything within callisto instead of relying on a external api
  - we should have it be a button next to sign and send for both multisig and eoa accounts
  - if a user presses simulate, they should ulatimately see a dialogue box (or maybe be directed to a separate pane) that shows the relevant before and after state of the account (and ether balance before and after) to confirm the transaction does what they expect
  - if a simulation is run, the user should be prompted after to continue to an actual sign and submission, or a reject path if something is wrong
  - if we implement this well, we should advertise it in the documentation as an imporant safety feature

### claude-assisted advanced transaction preparation
- can be used for both EOA and Safe wallets
- if a Safe wallet is connected, "multi-step" transactions are also enabled
- the user should have an option in settings to _fully_ disable all AI features, and they should be off by default
- in settings, there should be a place to enter a claude API key, and a toggle switch to enable/disable the AI features
- this is a security and performance measure, so if the toggle is off the backend should truly put all AI features in a "cold path" (untouched until it's flipped on)
- the entry of the claude api key should be in settings, formatted well and the key should be saved and persist across restarts
- there should be an option to delete the key and a separate toggle to fully disable the AI feature

### researching multi-step transactions

the original design document specifies usage of the existing defisaver v3 contracts for multi-step transactions. 

please research any alternatives. this was specified because i've used them extensively and they work well. 

if there are alternatives to consider, we should when we get there.
