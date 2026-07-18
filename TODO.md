# To-do items for `callisto`

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

### drop the Trezor Suite/Bridge dependency (use direct libusb, like Frame)
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

### enable passphrase unlock of hot-wallets
- the recovery phrase should only be needed on the first import of a hot wallet
- we should enforce that the user provides a passphrase at input to encrypt the keystore
- this way, on each "unlock" the user only needs to re-enter the passphrase they set that encrypts the keystore, rather than the whole recovery phrase again
- this is a better pattern; the recovery phrase should not be treated like a password
- it should be a one-time import flow, not the frequent lock/unlock flow

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

### claude-assisted advanced transaction preparation
- can be used for both EOA and Safe wallets
- if a Safe wallet is connected, "multi-step" transactions are also enabled
- the user should have an option in settings to _fully_ disable all AI features, and they should be off by default
- in settings, there should be a place to enter a claude API key, and a toggle switch to enable/disable the AI features
- this is a security and performance measure, so if the toggle is off the backend should truly put all AI features in a "cold path" (untouched until it's flipped on)

### researching multi-step transactions

the original design document specifies usage of the existing defisaver v3 contracts for multi-step transactions. 

please research any alternatives. this was specified because i've used them extensively and they work well. 

if there are alternatives to consider, we should when we get there.
