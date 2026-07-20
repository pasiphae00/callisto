# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Source of truth

Read `DESIGN.md` (full product/feature spec, RFC-2119 MUST/SHOULD) and `PRINCIPLES.md` (development principles) before making design decisions — they define what to build and the constraints. `README.md` is the user-facing overview (features, quick start, security model, architecture table) and `CHANGELOG.md` tracks releases.

**Implemented (through v0.11.0):** RPC config + live connection with **bearer-auth endpoints** and a default **Ganymede archive** node (`wss://`) that auto-fails-over to Flashbots (`internal/rpc`, token in `internal/buildsecrets`); EIP-55 + ENS; **encrypted hot-wallet keystore** (one-time seed import → scrypt+AES-GCM in `internal/keystore`, passphrase unlock) and hardware (Ledger/Trezor) signers behind a common `signer.Signer`, with **Trezor over direct libusb — no Trezor Suite/Bridge — incl. native streaming EIP-712** (the overhaul is done); **full hot-wallet key management** (v0.10 — change passphrase/`Rekey`, reveal private key, export encrypted backup, derive more accounts, import raw key / MetaMask-V3 JSON / watch-only, idle+sleep **auto-lock** in `internal/ui/autolock.go` + Settings › Security, and **Touch ID / Keychain unlock** on macOS via `internal/keystore/secretstore_darwin.go` — hidden until Developer-ID-signed); **automatic balances** (v0.10 — `assets.DiscoverTokens` scans `Transfer(→wallet)` logs, persisted+incremental via `discovered_tokens`/`token_scan` tables and `internal/ui/tokenCache`; per-wallet **spam hiding** via `hidden_tokens`); the full basic-send flow (build → gas → pre-sign review → sign → broadcast → inclusion → history, with a per-tx detail dialog); **Gnosis Safe multisig** (`internal/safe` + Safe tab — v0.11 deep dive: `Overview | Proposals | Assets` sub-tabs, auto-discovered Safe balances via the shared `assetsView`, an Active/History Proposals view with same-nonce conflict flags, and **distributed signing** — export/import proposals + signatures across machines with `safeTxHash` recomputed and every signature owner-verified on import, `internal/safe/envelope.go`; import, propose transfers + owner/threshold admin, local signature collection by switching owners, execute, same-nonce reject; `signer.SafeHashSigner`; plus a **Build** sub-tab — curated ecosystem actions (`internal/actions`: WETH wrap/unwrap, Lido deposit/wrap/unwrap/withdraw/claim) prepared as Safe proposals, with any required ERC-20 `approve` batched atomically into the same MultiSend, `internal/safe/multisend.go`); **WalletConnect v2** (`internal/walletconnect` — from-scratch wallet Sign, no Go SDK/deps; hot + Ledger + Trezor incl. typed-data); the **Approvals pane** (`internal/approvals` — discover/revoke ERC-20 + Permit2 allowances via `getLogs`, incremental re-scans + persisted watermark, live WSS watch, ETA; needs an archive endpoint for full history); and the **release pipeline** — native `fyne` packaging (`Makefile`) + **in-app self-updater** (`internal/updater`, ed25519-signed Codeberg releases). **Advanced-tx scope (2026-07):** advanced preparation is **Safe-only** (the Build tab above) — EOAs use WalletConnect → native dApps, so the standalone Prepare pane and the optional Claude/AI resolver were **removed** (the registry stays, reachable from the Safe Build tab; NL intent can re-plug above it later). See `docs/advanced-transactions.md`. **Deferred (designed for, not built):** transaction simulation (`eth_call` + `debug_traceCall` prestate diff), multi-step Safe recipes (MultiSend for fixed sequences; DeFiSaver hand-encoding for parameter piping — JS SDK has no Go path), and GridPlus Lattice (stubbed — no Go SDK). Keep these slot-in-able without core rewrites.

## Commands

```sh
go build ./...            # build all packages (cmd/callisto is the binary)
go test ./...             # run all tests
go test ./internal/chain  # test a single package
go test -run TestLookupUnknownFallback ./internal/chain   # run a single test
go vet ./...              # static checks
go run ./cmd/callisto     # launch the GUI (needs a display; won't run headless)

# Integration tests hit live public nodes and are behind a build tag:
go test -tags integration ./...   # override endpoints via CALLISTO_TEST_RPC / CALLISTO_TEST_MAINNET_RPC
```

Integration tests (`//go:build integration`) verify real-network behavior (RPC dial, ENS against mainnet, ERC-20 decode, gas/fee inputs on Sepolia) and are excluded from the default `go test ./...`. Hardware-wallet device flows require physical hardware and are not covered by automated tests.

On macOS the linker prints a benign `ignoring duplicate libraries: '-lobjc'` warning from Fyne's CGo driver — it is not an error. The GUI cannot be launched in a headless environment; UI construction is verified instead by the Fyne test-driver smoke test in `internal/ui/ui_test.go`.

**UI glyph gotcha:** the default Fyne theme font does **not** contain many non-ASCII glyphs (notably the arrow `→`), which render as `�` in a plain `widget.NewLabel`/`canvas.Text`. Only text set in the bundled **Berkeley Mono** (i.e. `monoLabel(...)` or `TextStyle{Monospace:true}`) renders them. So in default-font labels use ASCII (`->`, `>`) — reserve `→`/similar for mono text. Known-safe in the default font: `●` (dots), `✓`, `⚠`, `⭐`.

## Dependencies (deliberately minimal — see PRINCIPLES.md)

`fyne.io/fyne/v2` (GUI), `github.com/ethereum/go-ethereum` (RPC/ABI/crypto/EIP-55; Ledger+Trezor drivers are a local LGPL fork under `internal/signer/hardware/usbwallet`), `github.com/karalabe/usb` (bundled libusb+hidapi — the USB backend for hardware wallets; **replaced `github.com/ethereum/hid`** in v0.7.0 so Trezor works over raw libusb with no Trezor Suite/Bridge), `modernc.org/sqlite` (pure-Go, no CGo — history + address book), `github.com/tyler-smith/go-bip39` (mnemonic↔seed). **Do not add `btcutil`/`hdkeychain`**: it drags a personal-fork transitive dep (`kcalvinalvin/anet`) into a signing wallet. Implement BIP32/BIP44 derivation directly on `github.com/decred/dcrd/dcrec/secp256k1/v4`, which go-ethereum already vendors (zero new dependency).

## Workflow & releases

Hosted on **Codeberg** (Forgejo — the GitHub `gh` CLI does not apply). Work on `feat/…` / `fix/…` branches off `main`; never commit to `main` directly. Semantic versioning with `v`-prefixed tags (`v0.x.y` pre-release, `v1.0.0` = first stable). Update `CHANGELOG.md` (Keep a Changelog) for every user-facing change. Full process in `docs/RELEASING.md`.

**Documentation style:** keep prose concise — favor short, information-dense lines over long explanations; the `CHANGELOG.md` is the single detailed record. **Release notes stay brief:** a one/two-line summary + install + checksum steps, and a link to the release's `CHANGELOG.md` section rather than pasting the full changes (template in `docs/RELEASING.md`).

## What Callisto is

A locally-run, Go-based GUI application for preparing, signing, and broadcasting Ethereum transactions — for EOAs, Safe multisig accounts, and (in the future) other account types. Priorities per `PRINCIPLES.md`, in order: **functionality > security > performance**, with particular emphasis on correctness around key material handling.

## Architecture implied by the design

The design in `DESIGN.md` implies several major subsystems that should stay cleanly separated (this is called out explicitly as a requirement — new wallet types, chains, and transaction-prep methods must be addable without major rewrites):

- **Signer abstraction** — a common interface spanning hot wallets (one-time seed import → the seed is persisted only as a scrypt+AES-GCM-encrypted keystore in `internal/keystore`, decrypted into memory only while unlocked and wiped on disconnect/close; unlock is by passphrase, not by re-entering the phrase), and hardware wallets (Trezor, Ledger, Grid Lattice). Safe multisig is layered on top of this, not a signer type itself — a Safe transaction is proposed, signed one-or-more times by individual EOA/hardware signers switching in and out, then executed once the threshold is met.
- **RPC/connection layer** — supports multiple user-configured JSONRPC backends (both `wss://` and `https://`, with optional bearer auth via `Endpoint.AuthRef` → build-embedded token in `internal/buildsecrets`); persisted across launches; live chain monitoring (subscriptions) + general JSONRPC. Seeds two defaults on first run: the maintainer's **Ganymede archive** node (`wss://ganymede.pasiphae.io`, bearer auth, auto-connecting when a token is embedded) and **Flashbots Protect** (fallback). On primary connect-failure or mid-session loss it **fails over to Flashbots** (`Manager.SetOnConnectionLost` → `App.failoverToFallback`). This supersedes the original no-default-RPC rule. The Ganymede token is injected obfuscated at build time from a gitignored `GANYMEDE_RPC_TOKEN.env` — a shared/public key, not a secret.
- **Asset detection/population** — detects ETH and ERC20 balances for the connected account, parses token metadata (name/symbol/decimals), and formats amounts converting between human units and base units using each asset's decimals. Native asset must adapt per `chain_id` (not hardcoded to ETH/mainnet).
- **ENS resolution** — used bidirectionally: reverse-resolve any displayed address to a name, and forward-resolve name input with clear valid/invalid UI states. This needs to hook into every place an address is displayed or entered, so it's a cross-cutting concern rather than a single feature.
- **Basic transaction preparation** — a consistent "send" flow (to/amount/send-all) for ETH and ERC20 transfers, independent of the complex pipeline below.
- **Complex transaction preparation (Claude-driven, secondary phase)** — natural-language transaction requests (e.g. "deposit 10 ether to AAVE v3") resolved into concrete calldata. Requires a persistent, growing "address book" of on-chain contracts with their functions/parameters so the system can explain what it's about to do in human-readable terms. Multi-step complex transactions (Safe-only) must be built via the DeFiSaver SDK/contracts (recipe creator) — see links in `DESIGN.md` — rather than hand-rolled. **This is explicitly scoped as a required but secondary phase, built after basic transaction support is working.**
- **Transaction review/signing/broadcast pipeline** — a pre-signing human-readable review step (decoded contract/function/params, not raw calldata) is required before every signature, for both simple and multi-step transactions. Broadcast must track submission acceptance/errors, then listen for inclusion and surface block/status/timestamp once mined.
- **Transaction history** — a local persisted record of prepared transactions (type, prep instructions, timestamps for each pipeline stage, hash, execution status, explorer link).

## Working from DESIGN.md

`DESIGN.md` uses RFC-2119-style MUST/SHOULD language to distinguish hard requirements from soft preferences — preserve that distinction when implementing or when proposing deviations. Notably: hot wallet key material must be actively cleared on disconnect/close, and the DeFiSaver SDK is mandated (not optional) for multi-step Safe transactions. (One deliberate deviation is recorded in DESIGN.md itself: Callisto now ships a default Flashbots Protect RPC, superseding the original no-default-RPC MUST — user-approved.)
