<p align="left">
  <img src="images/CALLISTO-NASA-GALILEO-transparent-small.PNG" alt="Callisto" width="180">
</p>

<h1 align="left">Callisto</h1>

<p align="left">
  <em>A lightweight, flexible, locally-run Ethereum transaction preparation and signing utility.</em>
</p>

<p align="left">
  <a href="./CHANGELOG.md">Changelog</a> ·
  <a href="./DESIGN.md">Design</a> ·
  <a href="./PRINCIPLES.md">Principles</a> ·
  <a href="docs/RELEASING.md">Releasing</a>
</p>

---

Callisto is a Go + [Fyne](https://fyne.io) native desktop application for preparing,
signing, and broadcasting Ethereum transactions. It runs entirely on your
machine, talks to an Ethereum node you choose, and keeps signing keys under your
control — hot-wallet key material lives in memory only while unlocked and is
wiped on lock, and hardware-wallet keys never leave the device.

> **Status: pre-1.0 (`v0.4.0`).** The foundation, basic transaction flows, and
> Gnosis Safe multisig (including owner/threshold administration) are in place and
> usable. The Claude-assisted complex-transaction pipeline is planned — see
> [Roadmap](#roadmap).

## Features

- **Bring your own node.** Configure multiple Ethereum RPC endpoints
  (`https://` or `wss://`), and your selection is
  remembered. WebSocket endpoints get live block updates; HTTP endpoints are
  polled. If you do not specify a node, (Flashbots Protect)[https://protectrpc.flashbots.net/about] (fast) is used by default.
- **Multiple wallets, multiple signers.**
  - *Hot wallets* — import a BIP-39 seed phrase, switch between derived accounts,
    with keys held in memory only while unlocked.
  - *Hardware wallets* — Ledger (direct USB) and Trezor (Trezor Bridge, with a
    direct-USB fallback for older devices) via a common signing interface; keys
    never leave the device. Trezor hidden wallets (passphrase-protected,
    including on-device passphrase entry) are supported.
- **Chain-aware.** The native asset and block explorer adapt to the connected
  chain (Ethereum, Sepolia, Holesky, OP, Base, Arbitrum, Polygon, Gnosis, …),
  with a safe fallback for unknown chains.
- **Balances.** ETH (shown first) plus ERC-20 tokens, with on-chain metadata
  (name/symbol/decimals, including legacy `bytes32` tokens), correct
  human/base-unit formatting, and add-your-own tokens by address.
- **ENS everywhere.** Addresses are shown as their primary ENS name where one is
  set (forward-verified), and recipient fields accept ENS names or addresses with
  live, color-coded resolution. Addresses are EIP-55 checksum-validated on entry.
- **Basic transfers.** Send ETH or ERC-20 tokens with a consistent flow: pick an
  asset, enter a recipient and amount (with **Max**), then review a full pre-sign
  summary — decoded call, nonce, EIP-1559 fees, and maximum total fee — before
  signing.
- **Broadcast & track.** Submit the signed transaction, get the hash and an
  explorer link, and watch for inclusion (status, block, timestamp).
- **Safe multisig.** Import an existing [Safe](https://safe.global) by address and
  work with it from a dedicated tab: propose ETH/ERC-20 transfers or owner and
  threshold changes, collect owner signatures locally by switching unlocked
  wallets (hot, Ledger, or Trezor) until the threshold is met, then execute —
  or reject a proposal with a same-nonce cancellation. No external Safe service;
  everything is on-chain plus a local record.
- **History.** A local record of every transaction Callisto prepared, with status
  and explorer links, kept in an embedded SQLite database.

## Install & run

Callisto builds from source. You need **Go 1.24+** and a C toolchain (Fyne uses
CGo for its desktop driver — see Fyne's
[getting-started prerequisites](https://docs.fyne.io/started/) for your OS; on
macOS the Xcode command-line tools suffice).

```sh
git clone https://codeberg.org/pasiphae/callisto.git
cd callisto
go run ./cmd/callisto
```

Or build a binary:

```sh
go build -o callisto ./cmd/callisto
./callisto
```

## Quick start

1. **Settings** → add an RPC endpoint (e.g. a Sepolia `https://…` or `wss://…`
   URL) and click **Connect**. The status dot turns green.
2. **Wallets** → **Add hot wallet…** (use a *throwaway/test* seed for
   experimentation) or **Add hardware…** for a Ledger/Trezor. Trezor requires
   [Trezor Bridge](https://trezor.io/learn/a/what-is-trezor-bridge) or Trezor
   Suite running in the background (Callisto talks to it over its local API,
   which correctly handles USB access across platforms/devices); Ledger works
   over direct USB with no extra software.
3. **Assets** → view balances for the selected wallet; they refresh on each block.
4. **Send** → choose an asset, enter a recipient (address or ENS) and amount,
   **Prepare transfer**, review, then **Sign & send**.
5. **History** → track what you've sent; select a row to open it on a block
   explorer.

## Security model

- **Keys are never persisted.** The on-disk config stores only inert wallet
  *descriptors* (label, address, derivation path) — never seeds or private keys.
- **Hot wallets** hold the BIP-39 seed in memory only while unlocked (so you can
  switch derived accounts); the seed and the selected private key are zeroed on
  lock, disconnect, or exit. HD derivation (BIP-32/44) is implemented in-house on
  the secp256k1 primitives go-ethereum already vendors, deliberately avoiding
  extra dependencies in the signing path.
- **Hardware wallets** keep keys on the device; Callisto only requests signatures
  you confirm on the device.
- Callisto ships **no default RPC** and makes no outbound connections except to
  the node and services you configure.

Treat this as pre-1.0 software: review transactions on-device, and prefer test
networks and throwaway keys while the project matures.

## Configuration & data

Stored under your OS config directory (e.g.
`~/Library/Application Support/callisto/` on macOS):

- `config.json` — RPC endpoints, wallet descriptors, and added tokens (no secrets;
  written atomically, `0600`).
- `callisto.db` — SQLite database for transaction history and the contract
  address book.

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

On macOS the linker prints a benign `ignoring duplicate libraries: '-lobjc'`
warning from Fyne's CGo driver — it is not an error.

### Architecture

The GUI (`internal/ui`) is a thin layer over independent domain packages; the
domain never depends on the UI, and all key material is isolated in the signer
packages. New signer types, chains, and asset kinds are designed to slot in
without touching transaction preparation, review, or broadcast.

| Package | Responsibility |
|---|---|
| `internal/chain` | Per-chain metadata (native asset, explorer) |
| `internal/rpc` | Endpoint config + connection manager (`Client` interface, head watcher) |
| `internal/address` | EIP-55 validation & display |
| `internal/ens` | ENS forward/reverse resolution (forward-verified) |
| `internal/signer`, `.../hot`, `.../hardware` | Signing interface; hot (seed) and Ledger/Trezor signers |
| `internal/assets` | ETH + ERC-20 detection, metadata, unit conversion |
| `internal/tx` | Build, gas estimation, assembly, broadcast, inclusion |
| `internal/safe` | Safe multisig: reads, safeTxHash, exec/admin encoding, proposals |
| `internal/history` | Transaction lifecycle records |
| `internal/config`, `internal/store` | JSON settings; SQLite store |

See [`DESIGN.md`](DESIGN.md) for the full specification and
[`PRINCIPLES.md`](PRINCIPLES.md) for development principles. Contributions follow
the branch/version/release workflow in [`docs/RELEASING.md`](docs/RELEASING.md).

## Roadmap

Implemented above; still to come (designed for, not yet built):

- **Claude-assisted complex transactions**: natural-language requests
  ("deposit 10 ETH to Aave v3") resolved to reviewed calldata, with a growing
  on-chain contract address book, and multi-step flows via the DeFiSaver SDK.
- **Transaction simulation** against a fork before signing.
- More signer types (incl. GridPlus Lattice, pending a Go SDK) and chains.

## Credits

- Callisto imagery: NASA/JPL's [Galileo](https://solarsystem.nasa.gov/) mosaic of
  Jupiter's moon Callisto (public domain).
- Addresses and numeric values are set in
  [Berkeley Mono](https://usgraphics.com/products/berkeley-mono) by U.S. Graphics
  Company, bundled and embedded under the project's font license.
- `internal/signer/hardware/usbwallet` is a local, patched fork of three files
  from [go-ethereum](https://github.com/ethereum/go-ethereum)'s
  `accounts/usbwallet` (LGPL-3.0-or-later; license and attribution preserved
  in-file) — see that package's doc comment for why.

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
