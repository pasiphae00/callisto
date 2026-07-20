<p align="left">
  <img src="images/CALLISTO-NASA-GALILEO-transparent-small.PNG" alt="Callisto" width="160">
</p>

<h1 align="left">Callisto</h1>

<p align="left">
  <em>A lightweight, powerful, desktop Ethereum wallet management system.</em>
</p>

<p align="left">
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/FEATURES.md">Features</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/CHANGELOG.md">Changelog</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/DESIGN.md">Design</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/PRINCIPLES.md">Principles</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/docs/RELEASING.md">Releasing</a>
</p>

· · ·

<p align="left">
  <a href="https://codeberg.org/pasiphae/callisto/releases"><strong>Download here</strong></a> 
</p>

---

Callisto is a multi-wallet management system for the Ethereum ecosystem, implemented 100% in Go. It runs entirely on your machine, talks to an Ethereum node you choose, and keeps signing keys under your control — no telemetry, no accounts, no outbound connections except the RPC and services you use.

It manages hot wallets, Trezor and Ledger hardware wallets, and Safe multi-signature wallets from one interface, and can act as a [WalletConnect](https://walletconnect.network/) backend for any web3 app — letting you switch between wallet types as a self-custody signer across the ecosystem.

_Screenshots [here](./FEATURES.md)._

> **Status: pre-1.0 (`v0.11.1`).** Distributed as a native, self-updating desktop app (see [Download](https://codeberg.org/pasiphae/callisto/releases)). The features below are in place and usable; the Claude-assisted complex-transaction pipeline is still planned — see [Roadmap](#roadmap).

## Features

- **Bring your own node.** 
  - Configure multiple RPC endpoints (`https://` or `wss://`, optional bearer auth); WebSocket gets live block updates, HTTP is polled. Out of the box Callisto connects to a maintainer-run **archive** node (so approval history and live subscriptions work immediately) and **falls over to [Flashbots Protect](https://protectrpc.flashbots.net/about)** if it's unreachable. Replace either, or use your own node, in Settings.
- **Multiple wallets, multiple signers.**
  - *Hot wallets* — import a BIP-39 seed **once**, pick the account(s) to add, and set an encryption passphrase; the seed is stored only as a scrypt+AES-GCM keystore and unlocked thereafter with just the passphrase. Keys live in memory only while unlocked and are wiped on lock. Full key management: change passphrase, reveal a private key, export an encrypted backup, derive more accounts, import a raw key / MetaMask JSON / watch-only address, idle auto-lock, and Touch ID unlock on macOS.
  - *Hardware wallets* — Ledger and Trezor over direct USB via a common signing interface; keys never leave the device. **No Trezor Suite or Bridge required** — Callisto talks to the Trezor directly over libusb (Bridge is kept only as a fallback). Trezor hidden wallets (passphrase-protected, incl. on-device entry) are supported.
- **Chain-aware.** 
  - Native asset and block explorer adapt to the connected chain (Ethereum, Sepolia, Holesky, OP, Base, Arbitrum, Polygon, Gnosis, …) with a safe fallback for unknown chains.
- **Balances.** 
  - Held ETH and ERC-20 tokens are **discovered automatically** from on-chain transfer history (with name/symbol/decimals, incl. legacy `bytes32` tokens) and refresh each block — no manual refresh. Hide spam tokens (persisted), or add a token by address.
- **ENS everywhere.** 
  - Addresses display as their primary ENS name where set (forward-verified); recipient fields accept names or addresses with live resolution. All addresses are EIP-55 checksum-validated on entry.
- **Transfers, broadcast & track.** 
  - Send ETH or ERC-20 with a detailed pre-signature summary (decoded calldata, nonce, EIP-1559 fees, max total fee). After broadcast, Callisto tracks block inclusion and execution status live.
- **Approvals management.** 
  - See every outstanding token approval for the active wallet — direct ERC-20 *and* Uniswap Permit2 allowances — with spenders named where known and unlimited allowances flagged, and **revoke** any with a reviewed, tracked transaction. Discovery scans on-chain logs (needs an archive RPC for full history), bounded to the wallet's first tx; re-scans are incremental and update live over WSS.
- **Safe multisig.** 
  - Import an existing [Safe](https://safe.global) by address and work with it from a dedicated tab (`Overview | Proposals | Assets`): propose ETH/ERC-20 transfers or owner/threshold changes, collect owner signatures locally by switching unlocked wallets (hot, Ledger, or Trezor) until the threshold is met, then execute — or reject with a same-nonce cancellation. No external Safe service; everything is local until on-chain broadcast. (Primarily designed for personal Safes; org support is on the roadmap.)
  - **Distributed signing** — owners on different machines can collaborate without a Safe transaction service: **Export** a proposal (copy-paste text or a file), a co-owner **Imports** it, reviews, and signs, then sends a signature envelope back. On import Callisto recomputes the `safeTxHash` from the transaction fields and verifies every signature recovers to a current owner — it never trusts the envelope's contents.
- **WalletConnect.** 
  - Connect Callisto to web dApps (Uniswap, CoW Swap, …) as a wallet: paste the WC link, approve a session exposing your active wallet, then review and sign the dApp's `eth_sendTransaction` / `personal_sign` / `eth_signTypedData_v4` requests here. The WC v2 protocol is implemented from scratch (no Go SDK, no new dependencies); hot, Ledger, and Trezor (incl. native typed-data) are all supported.
- **History.** 
  - A local record of every transaction Callisto prepared — status, gas, explorer links — in an embedded SQLite database. Select a row for full details.

## Install & run

### Download a release (recommended)

Get the latest build from the **[releases page](https://codeberg.org/pasiphae/callisto/releases)**:

1. Download the archive for your platform — `Callisto-v<version>-darwin-arm64.zip` / `-darwin-amd64.zip` (macOS) or `-linux-amd64.tar.gz` (Linux).
2. **(Recommended) verify it:** `shasum -a 256 -c SHA256SUMS`. `SHA256SUMS` is itself ed25519-signed (`SHA256SUMS.sig`) with the maintainer key — the same key Callisto's in-app updater checks.
3. Unzip and, on macOS, move **`Callisto.app`** to **/Applications**.

macOS builds aren't yet Apple-notarized, so the **first** launch needs one step: right-click the app → **Open** (once), or `xattr -dr com.apple.quarantine /Applications/Callisto.app`.

**Linux** runs with near-complete feature parity — the same wallets, Safe, WalletConnect, and self-updater. Two differences: hardware wallets need the usual `udev` rules for non-root device access, and the Touch-ID/Keychain unlock is macOS-only (Linux uses the passphrase, which always works). Native OS keychain backends for Linux/Windows are on the roadmap.

**Updating:** Settings → **Check for updates** — Callisto pulls the newest release, verifies it against the maintainer key, installs it, and restarts. Your wallets, RPC config, and history are preserved (they live in your OS config directory, outside the app bundle).

### Build from source

Needs **Go 1.24+** and a C toolchain (Fyne uses CGo — see Fyne's [prerequisites](https://docs.fyne.io/started/); on macOS the Xcode command-line tools suffice).

```sh
git clone https://codeberg.org/pasiphae/callisto.git
cd callisto
go run ./cmd/callisto
```

Or build a binary (`go build -o callisto ./cmd/callisto`) or a native app bundle (`make package-mac` / `make package-linux`) — see [docs/RELEASING.md](docs/RELEASING.md).

## Quick start

1. **Connect.** Callisto auto-connects to its default mainnet endpoint on first launch (status dot turns green). Replace it, disable auto-connect, or add your own endpoints in **Settings**.
2. **Add a wallet.** **Wallets → Add hot wallet…** (use a *throwaway/test* seed to experiment): enter the phrase once, pick account(s), set a passphrase. Or **Add hardware…** for a Ledger/Trezor — plug in, unlock the device, confirm on-device (no extra software).
3. **Assets** → view balances (refresh each block; spam hidden).
4. **Approvals** → manage ERC20 token approvals (one-click revoke).
4. **Send** → pick an asset, enter recipient (address or ENS) and amount, **Prepare transfer**, review, **Sign & send**.
5. **Safe** (optional) → **Import Safe…** by address, propose a transfer or owner/threshold change, collect signatures (unlock each owner in **Wallets** → **Sign**), then **Execute** at threshold.
6. **WalletConnect** → start and manage WalletConnect sessions with Callisto-managed wallets.
6. **History** → track what you've sent; select a row for details and an explorer link.

## Security model

- **Seeds are stored only as encrypted keystores — never in the clear.** A hot wallet's BIP-39 seed is sealed with **scrypt + AES-256-GCM** under your chosen passphrase in a `0600` file. The config holds only inert *descriptors* (label, address, derivation path), never key material. Deleting a wallet securely wipes its keystore once unreferenced.
- **Keys are held in memory only while unlocked** and zeroed on lock, idle auto-lock, disconnect, or exit. HD derivation (BIP-32/44) is implemented in-house on the secp256k1 primitives go-ethereum already vendors — no extra dependency in the signing path. On macOS you can optionally cache the unlock key in the Secure-Enclave-backed Keychain for Touch ID unlock; the passphrase always remains a fallback.
- **Your recovery phrase is your backup.** The passphrase protects the on-disk keystore; it is never persisted and does not replace your seed. Best-effort file wiping isn't a guaranteed secure-erase on modern SSDs — keep your phrase safe offline.
- **Hardware wallets** keep keys on the device; Callisto only requests signatures that you confirm there.
- **No outbound connections except the RPC endpoint and services you use.** The default is a maintainer-run archive endpoint (with a Flashbots Protect RPC fallback), replaceable anytime. Its bearer token is a **shared access key** baked into release builds (rate-limited server-side), not a per-user secret. 
- **No telemetry, user tracking, or metrics collection.** for maximum privacy, use your own node as the RPC, or Flashbots Protect (optional by default).

_Treat Callisto as pre-1.0 software: review transactions on-device, and prefer test networks and throwaway keys while the project matures._

## Configuration & data

Stored under your OS config directory (e.g. `~/Library/Application Support/callisto/` on macOS), managed in-app:

- `config.json` — RPC endpoints, wallet descriptors, imported Safes (address + cached owners/threshold + local labels), added tokens (no secrets; atomic, `0600`).
- `keystores/<id>.json` — per-hot-wallet encrypted seed keystores (scrypt + AES-256-GCM), `0600` in a `0700` directory. The only place seed material is stored, and only ever in cipher form.
- `callisto.db` — SQLite: transaction history, Safe proposals + signatures, discovered/hidden tokens, and the contract address book.

## Development

```sh
go build ./...                                   # build everything
go test ./...                                    # unit tests
go test ./internal/tx                            # a single package
go test -run TestEstimateFees ./internal/tx      # a single test
go vet ./...                                      # static checks

# Integration tests hit live public nodes and are excluded by default:
go test -tags integration ./...                  # (override endpoints via CALLISTO_TEST_RPC / CALLISTO_TEST_MAINNET_RPC)
```

On macOS the linker prints a benign `ignoring duplicate libraries: '-lobjc'` warning from Fyne's CGo driver — not an error.

### Architecture

The GUI (`internal/ui`) is a thin layer over independent domain packages; the domain never depends on the UI, and all key material is isolated in the signer and keystore packages. New signer types, chains, and asset kinds slot in without touching transaction preparation, review, or broadcast.

| Package | Responsibility |
|---|---|
| `internal/chain` | Per-chain metadata (native asset, explorer) |
| `internal/rpc` | Endpoint config + connection manager (`Client` interface, head watcher) |
| `internal/address` | EIP-55 validation & display |
| `internal/ens` | ENS forward/reverse resolution (forward-verified) |
| `internal/signer`, `.../hot`, `.../hardware` | Signing interface; hot (seed) and Ledger/Trezor signers |
| `internal/keystore` | scrypt + AES-256-GCM encryption of hot-wallet seeds at rest |
| `internal/assets` | ETH + ERC-20 detection, discovery, metadata, unit conversion |
| `internal/tx` | Build, gas estimation, assembly, broadcast, inclusion |
| `internal/safe` | Safe multisig: reads, safeTxHash, exec/admin encoding, proposals |
| `internal/walletconnect` | WalletConnect v2 Sign (relay, envelope crypto, session engine) |
| `internal/history` | Transaction lifecycle records |
| `internal/config`, `internal/store` | JSON settings; SQLite store |

See [`DESIGN.md`](DESIGN.md) for the original full specification and [`PRINCIPLES.md`](PRINCIPLES.md) for development principles.

Contributions follow the workflow in [`docs/RELEASING.md`](docs/RELEASING.md).

## Roadmap

Still to come (designed, pending implementation):

- **Claude-assisted complex transactions**: natural-language requests ("deposit 10 ETH to Aave v3") resolved to reviewed calldata, with a growing on-chain contract address book and multi-step flows via the DeFiSaver SDK.
- **Transaction simulation** against a fork before signing.
- **OS keychain on more platforms**: Touch ID / macOS Keychain ships today; Linux Secret Service and Windows DPAPI backends to follow.
- **More signer types**: support for more hardware signers (incl. GridPlus Lattice, pending a Go SDK) and richer multi-chain support.

## Credits

- Callisto imagery: NASA/JPL's [Galileo](https://solarsystem.nasa.gov/) mosaic of Jupiter's moon Callisto (public domain).
- Addresses and numeric values are set in [Berkeley Mono](https://usgraphics.com/products/berkeley-mono) by U.S. Graphics Company, bundled and embedded under the project's font license.
- `internal/signer/hardware/usbwallet` is a local, patched fork of three files from [go-ethereum](https://github.com/ethereum/go-ethereum)'s `accounts/usbwallet` (LGPL-3.0-or-later; license and attribution preserved in-file).

## License
```
Callisto
Copyright (©)2026 pasiphae

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
```
