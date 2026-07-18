# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Source of truth

Read `DESIGN.md` (full product/feature spec, RFC-2119 MUST/SHOULD) and `PRINCIPLES.md` (development principles) before making design decisions — they define what to build and the constraints. `README.md` is the user-facing overview (features, quick start, security model, architecture table) and `CHANGELOG.md` tracks releases.

**Implemented (v0.1.0 → in progress):** RPC config + live connection, EIP-55 + ENS, hot-wallet (seed) and hardware (Ledger/Trezor) signers behind a common `signer.Signer` interface, ETH/ERC-20 balances, the full basic-send flow — build → gas estimate → pre-sign review → sign → broadcast → inclusion tracking → history — and **Gnosis Safe multisig** (`internal/safe` + the Safe UI tab): import by address, propose transfers and owner/threshold admin actions, local signature collection by switching owners (hot signs the hash directly, Ledger/Trezor via eth_sign — see `signer.SafeHashSigner`), execute, and same-nonce rejection. **Deferred (designed for, not built):** the Claude-assisted complex/multi-step transaction pipeline (DeFiSaver SDK), transaction simulation, and GridPlus Lattice (stubbed — no Go SDK). Keep these slot-in-able without core rewrites.

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

## Dependencies (deliberately minimal — see PRINCIPLES.md)

`fyne.io/fyne/v2` (GUI), `github.com/ethereum/go-ethereum` (RPC/ABI/crypto/EIP-55, and `accounts/usbwallet` for Ledger+Trezor), `modernc.org/sqlite` (pure-Go, no CGo — history + address book), `github.com/tyler-smith/go-bip39` (mnemonic↔seed). **Do not add `btcutil`/`hdkeychain`**: it drags a personal-fork transitive dep (`kcalvinalvin/anet`) into a signing wallet. Implement BIP32/BIP44 derivation directly on `github.com/decred/dcrd/dcrec/secp256k1/v4`, which go-ethereum already vendors (zero new dependency).

## Workflow & releases

Hosted on **Codeberg** (Forgejo — the GitHub `gh` CLI does not apply). Work on `feat/…` / `fix/…` branches off `main`; never commit to `main` directly. Semantic versioning with `v`-prefixed tags (`v0.x.y` pre-release, `v1.0.0` = first stable). Update `CHANGELOG.md` (Keep a Changelog) for every user-facing change. Full process in `docs/RELEASING.md`.

## What Callisto is

A locally-run, Go-based GUI application for preparing, signing, and broadcasting Ethereum transactions — for EOAs, Safe multisig accounts, and (in the future) other account types. Priorities per `PRINCIPLES.md`, in order: **functionality > security > performance**, with particular emphasis on correctness around key material handling.

## Architecture implied by the design

The design in `DESIGN.md` implies several major subsystems that should stay cleanly separated (this is called out explicitly as a requirement — new wallet types, chains, and transaction-prep methods must be addable without major rewrites):

- **Signer abstraction** — a common interface spanning hot wallets (seed phrase, derived accounts, in-memory key material that must be wiped on disconnect/close), and hardware wallets (Trezor, Ledger, Grid Lattice). Safe multisig is layered on top of this, not a signer type itself — a Safe transaction is proposed, signed one-or-more times by individual EOA/hardware signers switching in and out, then executed once the threshold is met.
- **RPC/connection layer** — supports multiple user-configured JSONRPC backends (both `wss://` and `https://`); persisted across launches; must support live chain monitoring (subscriptions) as well as general JSONRPC calls. Ships a default Flashbots Protect mainnet endpoint (seeded on first run, auto-connecting) that users can replace or remove — this supersedes the original no-default-RPC rule.
- **Asset detection/population** — detects ETH and ERC20 balances for the connected account, parses token metadata (name/symbol/decimals), and formats amounts converting between human units and base units using each asset's decimals. Native asset must adapt per `chain_id` (not hardcoded to ETH/mainnet).
- **ENS resolution** — used bidirectionally: reverse-resolve any displayed address to a name, and forward-resolve name input with clear valid/invalid UI states. This needs to hook into every place an address is displayed or entered, so it's a cross-cutting concern rather than a single feature.
- **Basic transaction preparation** — a consistent "send" flow (to/amount/send-all) for ETH and ERC20 transfers, independent of the complex pipeline below.
- **Complex transaction preparation (Claude-driven, secondary phase)** — natural-language transaction requests (e.g. "deposit 10 ether to AAVE v3") resolved into concrete calldata. Requires a persistent, growing "address book" of on-chain contracts with their functions/parameters so the system can explain what it's about to do in human-readable terms. Multi-step complex transactions (Safe-only) must be built via the DeFiSaver SDK/contracts (recipe creator) — see links in `DESIGN.md` — rather than hand-rolled. **This is explicitly scoped as a required but secondary phase, built after basic transaction support is working.**
- **Transaction review/signing/broadcast pipeline** — a pre-signing human-readable review step (decoded contract/function/params, not raw calldata) is required before every signature, for both simple and multi-step transactions. Broadcast must track submission acceptance/errors, then listen for inclusion and surface block/status/timestamp once mined.
- **Transaction history** — a local persisted record of prepared transactions (type, prep instructions, timestamps for each pipeline stage, hash, execution status, explorer link).

## Working from DESIGN.md

`DESIGN.md` uses RFC-2119-style MUST/SHOULD language to distinguish hard requirements from soft preferences — preserve that distinction when implementing or when proposing deviations. Notably: hot wallet key material must be actively cleared on disconnect/close, and the DeFiSaver SDK is mandated (not optional) for multi-step Safe transactions. (One deliberate deviation is recorded in DESIGN.md itself: Callisto now ships a default Flashbots Protect RPC, superseding the original no-default-RPC MUST — user-approved.)
