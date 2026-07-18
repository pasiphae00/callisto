# Changelog

All notable changes to Callisto are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
During pre-1.0 development, minor versions (`v0.x.0`) may introduce breaking
changes; `v1.0.0` marks the first stable, documented release.

## [Unreleased]

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

[Unreleased]: https://codeberg.org/pasiphae/callisto/compare/v0.3.1...HEAD
[0.3.1]: https://codeberg.org/pasiphae/callisto/compare/v0.3.0...v0.3.1
[0.3.0]: https://codeberg.org/pasiphae/callisto/compare/v0.2.0...v0.3.0
[0.2.0]: https://codeberg.org/pasiphae/callisto/compare/v0.1.0...v0.2.0
[0.1.0]: https://codeberg.org/pasiphae/callisto/releases/tag/v0.1.0
