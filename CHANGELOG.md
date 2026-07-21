# Changelog

All notable changes to Callisto are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
During pre-1.0 development, minor versions (`v0.x.0`) may introduce breaking
changes; `v1.0.0` marks the first stable, documented release.

## [Unreleased]

### Added
- **Show QR code** for a wallet's receive address — a button in the Wallets detail bar
  pops a scannable QR of the selected wallet's address (plus the address and a Copy
  button). Uses `rsc.io/qr` (pure-Go, no transitive dependencies).

### Fixed
- **WalletConnect sessions survive the relay's load-balancing disconnects.** The relay
  routinely closes idle sockets (close code `4010`); Callisto now **auto-reconnects**
  (re-dialing with backoff and re-subscribing to active topics) instead of dropping the
  session, and shows a brief "reconnecting…" status.
- **WalletConnect transaction dialog now updates to "included."** After a dApp
  transaction is broadcast, the result dialog advances from "submitted" to the block
  and status in place, matching the Send flow (previously it stayed on "submitted").

## [0.12.2] - 2026-07-21

Small UI fixes, and macOS builds are now **Apple-notarized** — they open with no
Gatekeeper prompt (the right-click → Open / `xattr` step is no longer needed).

### Changed
- **Copy a Safe's address** from the Safe → Overview tab (a Copy button next to the
  address, matching the wallet detail view).
- The Safe "Label owner" dialog now shows the owner address in the **monospace** font
  (moved out of the dialog title, which can't be monospaced).

### Fixed
- The **import keystore file** and **import private key** dialogs are now tall enough to
  show all fields — the passphrase-strength line is no longer clipped at the bottom.

## [0.12.1] - 2026-07-20

Security-hardening release from a pre-beta internal review
([docs/security-review-2026-07.md](docs/security-review-2026-07.md)). The cryptographic
core (keystore, HD derivation, signed updates) was reviewed and found sound; these are
the fixes from that pass. **Callisto has not had a formal third-party audit** — treat it
as pre-audit software (test networks / small amounts).

### Security
- **Untrusted display strings are sanitized** (`internal/textsafe`). On-chain token
  names/symbols, ENS names, and dApp/proposal-supplied text are stripped of Unicode bidi
  overrides, zero-width/format characters, and control characters before display — so a
  scam token can't masquerade as a real one (e.g. a Cyrillic "USDC" or a right-to-left
  override) and can't forge extra rows with embedded newlines.
- **WalletConnect requests decode dangerous calls.** The review now decodes token
  `approve`/`increaseAllowance`/`permit`/`setApprovalForAll`/`transfer`/`transferFrom`
  into plain language and flags **UNLIMITED** approvals with a red warning, instead of
  showing opaque calldata.
- **Patched a vulnerable dependency.** `golang.org/x/image` bumped to v0.43.0, clearing 4
  known image-decoder CVEs; `govulncheck` is now part of the release checklist.
- **Private-key clipboard auto-clears.** Copying a revealed private key now clears the
  clipboard after 45 seconds (if unchanged).
- **Signed-update download is bounded** (500 MB cap) and the **local database is `0600`**.

## [0.12.0] - 2026-07-20

### Added
- **Switch chain — one-click networks with bundled default RPCs.** Settings now has a
  **Switch chain…** picker covering Ethereum plus **Base, Arbitrum One, Optimism,
  Polygon, zkSync Era, and BNB Smart Chain**, each shipping a default public HTTPS RPC
  (PublicNode; zkSync via its official endpoint); Ethereum keeps its archive node +
  Flashbots fallback. Public L2 endpoints are rate-limited — bring your own under
  Manage endpoints for heavy use. Picking a network
  connects to its RPC and remembers it across restarts. You can still change any chain's
  RPC or add your own under **Manage endpoints** ("Custom endpoint…" in the picker).
- **Safe "Build" sub-tab — curated ecosystem actions as proposals.** Prepare common
  actions (wrap/unwrap WETH, stake with Lido, wrap/unwrap wstETH, request/claim a Lido
  withdrawal) for a Safe: pick an action, fill in the amounts, and Callisto builds and
  decodes the call for you to review, then creates a Safe proposal to sign and execute.
  Any ERC-20 approval an action needs is **batched atomically** into the same proposal
  (via MultiSend), so nothing is approved as a separate step.

### Changed
- **Far fewer RPC calls — works within public-endpoint rate limits.** Balances now load
  in a single **Multicall3** `aggregate3` call (native + every token at once) instead of
  one call per token, and token metadata is batched the same way on first load. Only the
  **currently-visible pane** refreshes on new blocks, throttled to at most once every 30s
  (navigating to a pane still refreshes immediately). Together these cut idle RPC traffic
  by roughly an order of magnitude, so public L2 endpoints stop rate-limiting normal use.
- **Advanced transaction preparation is now Safe-only.** EOAs are best served by linking
  the active account to a dApp over WalletConnect and using the dApp's native flows, so
  the standalone **Prepare** pane and the optional Claude/AI integration were removed;
  curated preparation now lives in the Safe tab, where it's genuinely needed (a Safe
  can't drive a synchronous dApp session).

### Fixed
- **Connection failover no longer switches chains.** If an L2 RPC dropped mid-session,
  Callisto used to silently fail over to the Ethereum-mainnet Flashbots endpoint; it now
  only does so when you were on Ethereum, and otherwise just reports the RPC is
  unreachable so you can reconnect or switch chain.

## [0.11.1] - 2026-07-20

### Fixed
- **macOS arm64 downloads no longer show "damaged."** Release `.app` bundles are now
  **ad-hoc code-signed** during packaging. Apple silicon requires native arm64 binaries
  to carry a valid signature, so an unsigned, quarantined (downloaded) v0.11.0 arm64
  build was rejected as "damaged and can't be opened" — the amd64 build slipped through
  under Rosetta. The normal first-launch step still applies: right-click → **Open**, or
  `xattr -dr com.apple.quarantine /Applications/Callisto.app`. (Developer-ID signing +
  notarization, which removes the prompt entirely, is on the roadmap.)
- **Switching wallets reloads balances immediately** instead of lagging until the next
  block; the Assets/Send panes clear and show "Loading…" on an account change rather
  than briefly showing the previous wallet's numbers.

### Changed
- Tidied the default RPC endpoint labels/URLs.

## [0.11.0] - 2026-07-20

Safe / multisig deep dive.

### Added
- **Safe workspace, reorganized** into `Overview | Proposals | Assets` sub-tabs.
  Proposals is second and named for the primary action, with a live count on the tab
  label (e.g. `Proposals (2)`) so it's obvious where to sign/execute.
- **Safe balances via auto-discovery.** The Assets sub-tab reuses the EOA
  automatic-balance engine keyed on the Safe address — discovering held tokens from
  transfer history, refreshing each block, with per-Safe spam-hiding and stable
  ordering. The transfer picker draws from the same set, so a Safe's full holdings are
  offered (not just ETH/USDC). The balances list is now a reusable component shared by
  the EOA Assets tab and the Safe Assets sub-tab.
- **Proposal Activity view.** Proposals are split into **Active** (collecting/ready,
  by nonce) and **History** (executed/rejected/failed), with status-colored dots, a
  same-nonce **conflict flag**, and a richer review dialog (created time, signatures by
  owner label, an explorer link for the execution tx, and the failure reason).
- **Distributed signing.** Owners on different machines can collaborate without a Safe
  transaction service: **Export** a proposal (copy-paste text or a JSON file), a
  co-owner **Imports** it, reviews, and signs, then exports a signature envelope back.
  On import Callisto recomputes the `safeTxHash` from the transaction fields and
  verifies every signature recovers to a current owner — the envelope's own
  hash/signer fields are never trusted (`internal/safe/envelope.go`).
- **WalletConnect-for-Safe feasibility** documented in
  `docs/safe-walletconnect-research.md` (research; implementation deferred).

### Fixed
- **Inclusion tracking no longer stalls on a transient receipt-query error.**
  `WaitForReceipt` bailed on the first non-`NotFound` error from
  `eth_getTransactionReceipt` (which archive/proxied endpoints can return for a pending
  hash), wrongly reporting "could not confirm inclusion" for a transaction that lands
  fine. It now polls through transient errors until the receipt appears or the context
  expires. Affects both Send and Safe execution.
- **Safe execution now reflects inclusion.** It marks its history record
  included/failed with the block and time, and pops a result dialog with the outcome
  and an explorer link (matching the Send flow) — previously the record stayed
  "submitted" and the static "Execution submitted" dialog never updated.

### Changed
- The Send broadcast confirmation matches the Safe execution dialog (full hash in mono
  + a "View on explorer" button).

## [0.10.1] - 2026-07-19

### Fixed
- **Trezor no longer asks for the PIN twice.** Connecting a Trezor cleared the device
  session on every open, which also drops the cached PIN — forcing a second PIN entry
  before the passphrase step. The session is now cleared only when the device already
  holds a cached passphrase (the stale-passphrase case that clear was meant to guard),
  so a fresh connect keeps the PIN you just entered and goes straight to passphrase.
- **Safe details no longer stick on "cached" when connected.** The Safe pane read
  on-chain owners/threshold/nonce at selection time — before the asynchronous
  auto-connect finished — and never retried, leaving "Showing cached Safe details" up
  even while the RPC was connected. It now loads live info as soon as the connection is
  up (guarded to run once per selection, not every block).

### Changed
- The "Hardware wallet added" and "Safe imported" dialogs render the address in the
  monospace font.
- The Safe transfer asset picker hides zero/dust-balance tokens and lists assets in a
  stable order, matching the Assets and Send panes.

## [0.10.0] - 2026-07-19

### Added
- **Hot-wallet key management.** A substantial hardening of how hot-wallet keys are
  handled, all built on the seed-only model (the recovery phrase is never persisted):
  - **Change passphrase** — re-encrypt a wallet's keystore under a new passphrase in
    place (scrypt + AES-GCM re-key), with a strength hint at import and change.
  - **Reveal private key** — view the selected account's private key behind a
    passphrase re-prompt and a prominent danger warning (reveal exposes a key, not
    the mnemonic, since the phrase is never stored).
  - **Export encrypted backup** — save the wallet's encrypted keystore JSON to a
    chosen path via a native macOS save panel.
  - **Derive more accounts** — add further BIP-44 accounts from an existing seed
    without re-importing the phrase.
  - **Import** — a raw private key, a MetaMask/geth V3 keystore JSON (decrypted with
    its own password, re-sealed under a Callisto passphrase), or a **watch-only**
    address (viewable, not signable — signing paths are guarded).
  - **Auto-lock** — the unlocked signer is wiped after an idle period and on
    wake-from-sleep; configurable (Never / 5 / 15 / 30 / 60 min, plus lock-on-sleep)
    under **Settings › Security**. Tuned to be gentle rather than aggressive.
  - **Touch ID / Keychain unlock (macOS)** — enroll a wallet to unlock with Touch ID;
    the scrypt-derived key is held in the Secure-Enclave-backed Keychain, gated by
    user presence, and the passphrase always remains a fallback. Hidden until the app
    is Developer-ID-signed (an unsigned build can't create a biometric Keychain item).
  - Color-coded **danger/caution warnings** on every flow that exposes key material.
- **Automatic balances.** Callisto detects the tokens a wallet holds on its own by
  scanning `Transfer(→ wallet)` logs — the full history on connect, then an
  incremental scan from a persisted watermark on every new block, so a token received
  live appears within a block. No curated list or Refresh button needed; 4-topic
  ERC-721 transfers are filtered out so NFTs don't pollute the fungible list. The
  discovered token set is persisted (SQLite) and hydrated on launch, so there is no
  full re-scan each start.
- **Hide spam tokens.** Select a token and **Hide (spam)** to keep it out of the
  balance list and the Send picker; a **Hidden…** manager lists what you've hidden
  (with best-effort symbols) and unhides it. The decision persists, and hidden tokens
  are not balance-fetched each block — which matters for spam-heavy wallets.
- **Transaction details dialog.** Selecting a History row now opens a full-detail
  popup: wallet, type, the parsed summary, network, status, block, the timeline
  (prepared / submitted / mined), **live gas info** (gas used, effective price, and
  fee, read from the receipt), any error, and the block-explorer link.

### Changed
- **No more manual balance refresh.** The "Refresh" / "Refresh assets" buttons are
  gone; balances update automatically each block.
- Assets display in a stable order (native first, then by symbol) instead of
  reshuffling between reloads, and the columns (ticker / balance / name) align in the
  mono font. The Send asset picker hides zero-balance tokens.
- The selected asset row is preserved across per-block refreshes (tracked by token
  identity, not row index), hiding a token removes it instantly (optimistic update)
  without waiting for a network reload, and steady-state refreshes no longer flicker
  the status line.

### Security
- Hot-wallet key material is decrypted into memory only while unlocked and actively
  wiped on lock, auto-lock, disconnect, and exit. The recovery phrase is never
  persisted; backup and reveal expose an encrypted keystore or a private key, never
  the mnemonic. Every key-exposing action is passphrase-gated and carries an explicit
  warning.

## [0.9.1] - 2026-07-19

### Added
- **Default archive RPC with automatic failover.** Callisto now ships with the
  maintainer's archive node (`wss://ganymede.pasiphae.io`, bearer-authenticated) as
  the auto-connecting default, so approval scans have full history and live
  subscriptions work out of the box — with **Flashbots Protect (fast)** kept as a
  pre-populated secondary. If the primary can't be reached (or drops mid-session),
  Callisto **fails over to Flashbots** automatically. RPC endpoints can now carry
  bearer auth (WSS + HTTPS).

### Security
- The default endpoint's bearer token is compiled into release builds from a
  gitignored env file and obfuscated (kept out of a `strings` dump). Note this is a
  **shared, effectively public access key** — a token in a distributed open-source
  binary can be extracted; it is rate-limited/rotated server-side, not treated as a
  secret. The config file never stores the token (only a reference name).

## [0.9.0] - 2026-07-19

### Added
- **Approvals pane.** Discover and revoke the active wallet's outstanding token
  approvals — both direct ERC-20 allowances and Uniswap **Permit2** inner
  allowances — showing the token, the spender (named where known, e.g. Uniswap
  Universal Router, CoW Protocol, Permit2), and whether the allowance is UNLIMITED
  or a specific amount (with Permit2 expiry). **Revoke** builds a reviewed, signed,
  inclusion-tracked transaction (`approve(spender, 0)` or Permit2 `lockdown`),
  logs it to History, and drops the row once confirmed. Discovery scans `Approval`
  logs on the active RPC, bounded below by the wallet's first tx (a `NonceAt` binary
  search) so it never scans from genesis — it needs an **archive** endpoint for full
  history (a pruned node only keeps recent logs), and surfaces a clear message when
  the RPC can't serve `eth_getLogs`. Scans honor a node's per-query block-range cap
  automatically, and the progress bar shows an ETA.
  - **Incremental re-scans:** results and a per-wallet scan watermark are persisted,
    so a later scan only covers new blocks (and re-checks known allowances to catch
    externally-revoked/spent ones) — seconds instead of minutes.
  - **Live detection (opt-in):** an "Auto-detect new approvals" toggle subscribes to
    Approval events over a WSS endpoint and updates the list in real time.

## [0.8.1] - 2026-07-19

### Added
- Release pipeline now builds **both macOS architectures** — `darwin-arm64`
  (Apple silicon) and `darwin-amd64` (Intel) — from one Mac (`make release`), plus
  `make package-mac-intel` / `package-mac-arm` targets. `docs/RELEASING.md` gains a
  full release checklist, a build-target matrix, and a release-message template.

### Fixed
- **WalletConnect transactions now advance in History from "submitted" to
  "included."** The dApp-send path tracked broadcast but never watched for the
  receipt, so those rows were stuck at "submitted"; they now follow the same
  inclusion tracking (block, status, timestamp) as the Send flow.

## [0.8.0] - 2026-07-19

### Added
- **Native app packaging pipeline.** A `Makefile` builds a double-clickable
  `Callisto.app` (macOS) and a Linux `.tar.gz` via `fyne package`, with
  reproducible `make release` producing checksummed, signed artifacts for a
  Codeberg release. See `docs/RELEASING.md`.
- **In-app updates.** Settings → **Check for updates** pulls new releases from the
  Codeberg API, shows the changelog, and (on request) downloads, verifies, installs,
  and relaunches. Updates are authenticated with an embedded **ed25519 maintainer
  key** (`SHA256SUMS` signature + per-artifact SHA-256) and refused if verification
  fails — your wallets, RPC config, and history are preserved across updates.
- Wallets: a **Rename** button changes a wallet's label (address, derivation
  path, and keystore are untouched).

## [0.7.1] - 2026-07-18

### Added
- **Trezor typed-data (EIP-712) signing now works without device experimental
  features.** Replaced the experimental `EthereumSignTypedHash` message (which
  newer firmware rejects unless experimental features are enabled) with the full
  native streaming flow (`EthereumSignTypedData` → struct/value request/ack →
  `EthereumTypedDataSignature`), so WalletConnect `eth_signTypedData_v4` and
  Safe-owner typed-data signatures work on a stock Trezor. **Verified live** with a
  CoW Swap order on a Trezor Safe 5.

### Changed
- Every pane's content now has a small left/edge margin instead of sitting flush
  against the navigation divider.
- The action button beneath each pane's introductory text now lines up with that
  text instead of sitting slightly to its left.

## [0.7.0] - 2026-07-18

### Added
- Settings: **double-click an RPC endpoint** to edit its label and URL, with
  Set Default and a red Remove shortcut in the dialog.

### Changed
- The left navigation is wider with left-aligned labels.
- Refreshing balances on the Assets or Send pane now refreshes the other too — no
  need to press Refresh on both.
- **Trezor no longer needs Trezor Suite or Trezor Bridge running.** The USB
  backend was migrated from HID (hidapi) to `github.com/karalabe/usb` (bundled
  libusb + hidapi, no system libraries — still a single self-contained binary), so
  Callisto now talks to the Trezor Safe directly over raw libusb (its WebUSB
  interface, which hidapi could not claim). Just plug in the device and sign.
  **Verified live** on a Trezor Safe 5 with Trezor Suite closed. Trezor Bridge is
  kept only as an automatic fallback, so existing setups aren't regressed.
  - Ledger's driver was forked in alongside Trezor (so Callisto no longer imports
    upstream go-ethereum's usbwallet, which is what let us drop the `ethereum/hid`
    dependency), and gained personal-message signing (`personal_sign` / Safe
    owner `eth_sign`), which upstream never implemented.
  - `go run ./cmd/hwscan` now lists both raw (libusb) and HID interfaces.

## [0.6.2] - 2026-07-18

### Added
- On exit, Callisto now cleanly disconnects every active WalletConnect session
  (`wc_sessionDelete`) before closing the relay, so connected dApps see a proper
  disconnect instead of a dropped connection. Bounded so it never hangs shutdown.

## [0.6.1] - 2026-07-18

### Changed
- Transaction hashes in the WalletConnect and Send result dialogs are now
  clickable monospace links that open the block explorer.
- The hardware-wallet **Passphrase** field appears only for Trezor (it's a
  Trezor-only hidden-wallet feature) — hidden when adding a Ledger, and skipped
  when unlocking one.
- The bottom status bar now reads `● RPC: <label> | Active wallet: <label>` as a
  single baseline-aligned monospace line, with the "RPC:"/"Active wallet:" labels
  in a smaller, muted font and a green/amber/gray connection dot.
- Settings RPC list shows each endpoint's URL in monospace, plus a new
  **Set Default** button to change the auto-connect endpoint.
- The pre-sign **Review transaction** screen reverse-resolves the To/From
  addresses to their primary ENS name, shown beneath the address.

## [0.6.0] - 2026-07-18

### Added
- **WalletConnect** — connect Callisto to web dApps as a wallet. A new
  WalletConnect tab: paste the `wc:` link from a dApp (Uniswap, CoW Swap, …),
  approve a session that exposes your active wallet, then review and sign the
  dApp's requests here. Supports `eth_sendTransaction` (built, gas-estimated,
  broadcast, and tracked through the normal pipeline), `eth_signTransaction`,
  `personal_sign`, and `eth_signTypedData_v4`. Sessions are listed and can be
  disconnected. Works out of the box (an embedded WalletConnect project id,
  overridable via `CALLISTO_WC_PROJECT_ID`).
  - The WalletConnect v2 Sign protocol (relay transport, X25519/HKDF +
    ChaCha20-Poly1305 envelopes, Ed25519 relay auth, and the pairing/session
    state machine) is implemented from scratch in `internal/walletconnect` —
    there is no Go SDK — using only the standard library, already-vendored
    `x/crypto`, and the existing `gorilla/websocket` dependency (no new deps).
  - **Verified live on mainnet:** a Uniswap swap and a CoW Swap order signed and
    submitted through Callisto.
  - Support: **hot wallets and Ledger** are fully supported. **Trezor** does
    transactions and `personal_sign`; typed-data (`eth_signTypedData_v4`) needs
    the device's experimental features enabled for now (native support comes with
    a planned Trezor overhaul). Sessions are not yet persisted across restarts.
- Double-click a wallet in the Wallets list to make it the active wallet.
- Message and typed-data signing (`signer.PersonalSigner` / `TypedDataSigner`) on
  hot and hardware wallets, backing the WalletConnect signature requests.

### Changed
- Wallets pane help text updated to reflect encrypted-keystore storage (the seed
  is encrypted at rest, not "nothing written to disk").

## [0.5.0] - 2026-07-18

### Added
- **Encrypted hot-wallet keystores.** A recovery phrase is now a one-time import:
  the BIP-39 seed is sealed with scrypt (memory-hard) + AES-256-GCM under a
  passphrase you choose and written to a `0600` keystore file
  (`<config>/keystores/<id>.json`). Every subsequent unlock needs only that
  passphrase — the phrase is no longer re-entered. Wrong passphrase / tampering is
  rejected cleanly (authenticated encryption). No new dependency.
- **Account selection at import.** Import shows a derived index→address list; pick
  one or several accounts to add (each becomes a wallet, all sharing one encrypted
  keystore) instead of guessing a derivation index.
- Deleting an encrypted hot wallet securely wipes its keystore file once no
  remaining account references it (best-effort overwrite + remove).

### Changed
- Hot-wallet unlock is now passphrase-based. Wallets imported before this release
  (no keystore) still unlock by re-entering the recovery phrase.
- Security model: seeds are now persisted, but only as encrypted keystores —
  updated in README/DESIGN/CLAUDE. The recovery phrase remains the authoritative
  backup.

## [0.4.0] - 2026-07-18

### Added
- **Gnosis Safe multisig support.** A dedicated **Safe** tab: import an existing
  Safe by address (owners, threshold, nonce, and version are read on-chain),
  label owners locally, and see the Safe's ETH/ERC-20 balances.
  - *Propose* an ETH or ERC-20 transfer from the Safe, or an owner/threshold
    administrative change — add, remove, or replace an owner, or change the
    threshold — each built as a Safe transaction whose canonical `safeTxHash`
    comes from the Safe contract's own `getTransactionHash` (cross-checked against
    a local EIP-712 computation).
  - *Collect signatures* locally by switching owners: unlock an owner in the
    Wallets tab, click **Sign**, and repeat until the threshold is met. Hot
    wallets sign the hash directly; **Ledger and Trezor** owners sign via the
    device's personal-message (eth_sign) route. No external Safe service is used —
    proposals and signatures are stored locally.
  - *Execute* once the threshold is met: the collected signatures are packed and
    `execTransaction` is broadcast as a normal EIP-1559 transaction from the
    executing owner, then tracked to inclusion and recorded in history.
  - *Reject* a proposal by creating a same-nonce rejection that, once executed,
    consumes the nonce and cancels the original.
- Trezor personal-message signing (`EthereumSignMessage`) in the usbwallet fork,
  enabling Trezor owners to sign Safe transactions.
- **Default RPC out of the box.** On first launch Callisto now ships with a
  Flashbots Protect Ethereum Mainnet endpoint, configured and auto-connecting, so
  it works immediately. It can be replaced, have auto-connect disabled, or be
  removed in favor of your own node at any time. (This supersedes the previous
  no-default-RPC behavior — a deliberate product decision; see `DESIGN.md`.)
- Licensed under GPL-3.0-or-later (`LICENSE`). The forked
  `internal/signer/hardware/usbwallet` files remain attributed to go-ethereum
  under LGPL-3.0-or-later, which permits relicensing under GPL-3.0.

## [0.3.2] - 2026-07-18

### Fixed
- Signing an ETH transfer with a Trezor failed with "transaction type not
  supported" — after the device had already signed it. Callisto builds EIP-1559
  (dynamic-fee) transactions, but go-ethereum's vendored Trezor protobuf only
  has the legacy `EthereumSignTx` message; the driver signed the tx as legacy
  on-device, then couldn't apply that signature back to the dynamic-fee tx
  (`EIP155Signer` rejects non-legacy types). Trezor is now signed **natively as
  EIP-1559**: the local fork sends the `EthereumSignTxEIP1559` message (message
  type 452, hand-encoded since the vendored package has no Go type for it, with
  field encoding verified by tests) and applies the resulting signature with the
  dynamic-fee signer — matching how Frame and Trezor Suite sign. Genuine legacy
  transactions still use the legacy path. **Verified live**: a real ETH transfer
  signed on a Trezor Safe 5 broadcast and confirmed successfully.

## [0.3.1] - 2026-07-18

### Fixed
- macOS menu bar showed two app menus ("callisto" and a separate "Callisto"):
  Fyne's macOS driver only splices a custom menu item into the native app menu
  when its label is exactly "About" (it then replaces the OS-provided item's
  action) — any other label, including "About Callisto", creates a second menu
  instead. The About item is now labeled "About" so it lands under the single
  native app menu.

## [0.3.0] - 2026-07-18

Hardware wallets now genuinely work: Trezor Safe-family devices went from
completely undetectable to fully functional (detection, signing, hidden
wallets, on-device passphrase entry) across both the standalone Trezor
Bridge and the newer bridge embedded in recent Trezor Suite builds — found
and fixed through extensive live-hardware testing this round, not just
code review. Also adds the About dialog, app icon, and full/copyable
wallet addresses.

### Fixed
- Trezor Safe-family devices (confirmed: Safe 5) were never detected, even
  connected and unlocked — fixed and verified live end-to-end (address
  derivation, multi-account listing, standard + hidden wallets, on-device
  passphrase entry). Two compounding causes:
  - The device's USB descriptor (interface 1, usage page 0xf1d0) doesn't
    satisfy go-ethereum's hardcoded `usbwallet` matcher (interface 0 or usage
    page 0xffff) — a known, still-open upstream issue
    ([go-ethereum#31841](https://github.com/ethereum/go-ethereum/issues/31841)).
  - Even once matched, this device's real wallet-protocol endpoint isn't
    reachable through the OS's HID API at all on this platform (writes
    succeed, reads never return data).
  Fixed with a local, LGPL-attributed fork of three go-ethereum files
  (`internal/signer/hardware/usbwallet`): Trezor now matches on vendor+product
  ID alone (Ledger is unaffected, still uses upstream directly), and — since
  direct USB is a dead end for this device regardless of matching — a Trezor
  Bridge (trezord) HTTP transport was added as the primary path, with port
  discovery (trezord's port isn't fixed) and self-healing session handling.
  Both the raw-USB and Bridge transports now have a bounded (60s) read
  timeout; previously a non-responding device hung `Open()` forever with no
  way to cancel.
- Trezor hidden wallets (passphrase-protected) now work correctly: the
  passphrase is submitted at the right point in the handshake regardless of
  whether it's empty (standard wallet) or not, and on-device passphrase entry
  (`PassphraseRequest.OnDevice`, a Trezor security feature that keeps the
  passphrase off the host entirely) is detected and respected rather than
  silently overridden with a host-supplied value.
- Newer Trezor Suite builds embed `@trezor/transport` instead of classic
  `trezord-go`, which speaks an undocumented `/call` wire format: JSON request
  and response bodies (vs. bare hex), with the legacy USB-HID report framing
  (`0x3f` + `0x23 0x23` magic + type + length) still embedded inside the hex
  data field. `BridgeClient` now detects which format a given bridge wants and
  frames each correctly, remembering the result per connection. Verified live
  against both the standalone Trezor Bridge (classic format) and this newer
  embedded variant (wrapped format).
- Fixed a Bridge session leak: a device connection that failed partway through
  `Open()` (e.g. a timed-out exchange) left its acquired Bridge session
  un-released, which could confuse a subsequent connection attempt. Failed
  opens now clean up via `Close()`.

### Added
- **About dialog** (Callisto menu → About Callisto): transparent Callisto logo,
  Berkeley Mono throughout, version and short commit (`internal/buildinfo`,
  commit read automatically from Go's embedded VCS metadata), tagline, link,
  copyright, and a "trust but verify" disclaimer. Background reads the theme's
  overlay color (`ColorNameOverlayBackground`, what dialogs actually render
  on — confirmed against a color-picker measurement, RGB 24,29,37) instead of a
  hardcoded value, so it matches the dialog's own chrome exactly.
- **App/window icon**: the Callisto logo is embedded and set as the dock/taskbar
  and window icon.
- **Full wallet address + copy**: the Wallets pane shows the selected wallet's
  full checksummed address in a dedicated, copyable field (button or text
  selection) below the list; the list rows themselves stay shortened.
- `cmd/hwscan`: diagnostic tool listing every raw USB HID device the OS
  reports, independent of whether Callisto recognizes it as a wallet — for
  debugging hardware-wallet detection issues.
- Passphrase field on the "Add hardware wallet" and hardware-unlock dialogs
  (Trezor only) for hidden-wallet support.
- Default RPC endpoint with startup auto-connect (opt-in checkbox when adding an
  endpoint; the default is exclusive and marked in the list).
- Berkeley Mono is embedded and applied to addresses, hashes, and numeric amounts
  (list rows, the pre-sign review, broadcast/inclusion dialog values, and
  resolved-address status) via a custom Fyne theme; a user font can override it
  via `CALLISTO_FONT_DIR`.

### Changed
- Token amounts display with at most 5 fractional digits (truncated, never
  rounded up); exact values are still used for signing.
- The Assets page hides non-native tokens below a dust threshold (0.00005) and
  notes how many were hidden; the native asset is always shown, and the Send
  picker still lists everything.
- The active wallet and connection status are marked with a genuinely colored
  `●` glyph (green/amber/gray) rather than an emoji, which doesn't reliably
  match theme/font for a color signal; the 🔒/🔓 lock-state icons are kept as-is
  (they convey a category, not a color, so emoji reads fine there).

## [0.2.0] - 2026-07-18

Completes the v1 basic-transaction feature set: end-to-end ETH/ERC-20 sends
(build → gas → review → sign → broadcast → track → history) and hardware-wallet
signing, plus a standard README. Safe multisig and the Claude-assisted
complex-transaction pipeline remain future work.

### Added
- Hardware wallet signing (`internal/signer/hardware`): Ledger and Trezor via
  go-ethereum's `accounts/usbwallet`, behind the common `Signer` interface — keys
  never leave the device. Add and unlock hardware wallets from the Wallets pane
  (unlock reconnects the device and verifies it reproduces the stored address).
  GridPlus Lattice is stubbed (`ErrLatticeUnsupported`) pending a Go SDK. Device
  flows require physical hardware; the device-independent logic is unit-tested.
- Gas estimation + pre-sign review: EIP-1559 fee estimation (`internal/tx`
  `EstimateFees`/`Prepare`) — estimated gas with headroom, node-suggested tip,
  and a `2*baseFee + tip` max fee — assembled into a dynamic-fee transaction. The
  Send flow now shows a full review (decoded transfer, nonce, per-gas fees, and
  max total fee) before signing. Verified against live Sepolia fee data.
- Sign / broadcast / inclusion tracking: the review's "Sign & send" signs with the
  unlocked wallet (only when it matches the sender), broadcasts, surfaces the hash
  with an explorer link, then tracks inclusion (status/block/timestamp) in the
  background.
- `internal/history` + History pane: transactions are recorded through their
  lifecycle (prepared → submitted → included/failed) in the SQLite store and
  listed with status and an explorer link.
- `internal/tx`: chain/gas-agnostic transaction-build core. `BuildNativeSend`
  and `BuildERC20Send` produce a `Send` (recipient/asset/amount + the concrete
  to/value/calldata); ERC-20 calldata encoding is verified byte-for-byte.
  `NativeSendAll` computes the max native amount after reserving the fee.
- Send pane: pick an asset, enter an ENS-or-address recipient and amount (with
  Max), and prepare a transfer. Validates against balance and shows a summary.
  Gas estimation, review, and signing follow in later phases.

## [0.1.0] - 2026-07-18

First tagged milestone: a runnable Fyne desktop app that connects to a
user-configured Ethereum node, manages hot wallets (seed-derived, in-memory
keys), and shows live balances — the foundation for the v1 transaction flows.

### Added
- Color-coded connection status dot (green connected / amber selected-but-offline
  / gray none) and locked/unlocked wallet state in the status bar.
- `internal/assets`: account asset detection and display. Native currency first,
  then curated + user-added ERC-20 tokens; metadata (name/symbol/decimals) read
  on-chain with a bytes32 fallback for legacy tokens; per-token failures are
  skipped rather than failing the whole load. Human⇄base-unit conversion is done
  with big.Int arithmetic (no floats) and rigorously tested; verified against
  real mainnet contracts (USDC, vitalik.eth balances) via integration tests.
- Assets pane: shows the active wallet's balances on the active connection
  (works whether or not the wallet is unlocked), reloads on each new block and on
  demand, and supports adding tokens by contract address (persisted per chain).
- `internal/signer`: common `Signer` interface (`Address`/`SignTx`/`Kind`) that
  all wallet types implement, plus `Lockable` for wiping in-memory key material.
- `internal/signer/hot`: in-memory seed-derived signer. BIP-32/BIP-44 HD
  derivation implemented in-house on decred secp256k1 (no btcutil), verified
  against canonical vectors (Hardhat `test…junk`, `abandon…about`). The BIP-39
  seed is held only while unlocked (to switch derived accounts) and, with the
  selected private key, is zeroed on `Lock`; nothing secret is persisted.
  Account switching, multi-account derivation, and signature round-trip covered
  by tests (green under `-race`).
- Wallets pane + signer-session management on the app: add / unlock / lock /
  remove wallets; unlocking re-derives from a freshly entered phrase and only
  installs the signer if it reproduces the stored address; the session is wiped
  on lock, disconnect, or exit. Mnemonic entry is cleared after use.
- `internal/address`: EIP-55 address validation (rejects bad-checksum mixed-case
  input) and canonical checksummed / truncated display formatting.
- `internal/ens`: ENS forward (name→address) and reverse (address→name)
  resolution implemented directly on the mockable `rpc.Client` (no third-party
  ENS dependency); reverse records are forward-verified before being trusted.
  EIP-137 namehash covered by known-answer vectors; verified end-to-end against
  mainnet (`vitalik.eth`) via integration tests.
- ENS-aware address entry widget: accepts a hex address or ENS name, validates /
  resolves off the UI thread with debouncing, and shows inline colored status.
- RPC connection layer: `rpc.Client` interface (satisfied by go-ethereum's
  ethclient) for mockable domain logic; `Dial` with chain-ID verification; and a
  thread-safe connection `Manager` with a head-watching goroutine (WebSocket
  subscription, HTTP polling fallback) that fans new blocks out to listeners.
- Settings pane: add / select / connect / remove RPC endpoints, persisted; live
  connection state reflected in the status bar. Verified end-to-end against a
  public Sepolia node (build-tagged integration tests, run with
  `go test -tags integration ./internal/rpc/`).
- Project foundation: Go module (`codeberg.org/pasiphae/callisto`), package
  skeleton, and Fyne GUI shell with a tabbed layout (Wallets, Assets, Send,
  History, Settings) and a status bar.
- `internal/chain`: static per-network metadata (native asset + block explorer),
  chain-aware with a graceful fallback for unknown chains.
- `internal/rpc`: persisted RPC endpoint descriptor with scheme validation
  (http(s)/ws(s)); no default endpoint, per design.
- `internal/wallet`: persisted, secret-free wallet descriptor (address, signer
  kind, derivation path).
- `internal/config`: atomic, 0600 JSON settings document under the OS config dir.
- `internal/store`: pure-Go SQLite (modernc) store with migrations for
  transaction history, a contract address book, and a 4-byte selector table.
- Release/versioning workflow docs (`docs/RELEASING.md`) and this changelog.

### Notes
- HD derivation is implemented directly on `decred/dcrd/dcrec/secp256k1`
  (already vendored by go-ethereum) rather than pulling in `btcutil`, which drags
  a personal-fork transitive dependency into a signing wallet.

[Unreleased]: https://codeberg.org/pasiphae/callisto/compare/v0.7.0...HEAD
[0.7.0]: https://codeberg.org/pasiphae/callisto/compare/v0.6.2...v0.7.0
[0.6.2]: https://codeberg.org/pasiphae/callisto/compare/v0.6.1...v0.6.2
[0.6.1]: https://codeberg.org/pasiphae/callisto/compare/v0.6.0...v0.6.1
[0.6.0]: https://codeberg.org/pasiphae/callisto/compare/v0.5.0...v0.6.0
[0.5.0]: https://codeberg.org/pasiphae/callisto/compare/v0.4.0...v0.5.0
[0.4.0]: https://codeberg.org/pasiphae/callisto/compare/v0.3.2...v0.4.0
[0.3.2]: https://codeberg.org/pasiphae/callisto/compare/v0.3.1...v0.3.2
[0.3.1]: https://codeberg.org/pasiphae/callisto/compare/v0.3.0...v0.3.1
[0.3.0]: https://codeberg.org/pasiphae/callisto/compare/v0.2.0...v0.3.0
[0.2.0]: https://codeberg.org/pasiphae/callisto/compare/v0.1.0...v0.2.0
[0.1.0]: https://codeberg.org/pasiphae/callisto/releases/tag/v0.1.0
