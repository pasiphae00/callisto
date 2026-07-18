# Changelog

All notable changes to Callisto are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
During pre-1.0 development, minor versions (`v0.x.0`) may introduce breaking
changes; `v1.0.0` marks the first stable, documented release.

## [Unreleased]

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

[Unreleased]: https://codeberg.org/pasiphae/callisto/compare/v0.2.0...HEAD
[0.2.0]: https://codeberg.org/pasiphae/callisto/compare/v0.1.0...v0.2.0
[0.1.0]: https://codeberg.org/pasiphae/callisto/releases/tag/v0.1.0
