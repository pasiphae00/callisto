# Changelog

All notable changes to Callisto are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
During pre-1.0 development, minor versions (`v0.x.0`) may introduce breaking
changes; `v1.0.0` marks the first stable, documented release.

## [Unreleased]

### Added
- Project foundation: Go module (`github.com/pasiphae/callisto`), package
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
- HD derivation will be implemented directly on `decred/dcrd/dcrec/secp256k1`
  (already vendored by go-ethereum) rather than pulling in `btcutil`, which drags
  a personal-fork transitive dependency into a signing wallet.
