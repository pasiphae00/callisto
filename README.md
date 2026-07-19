<p align="left">
  <img src="images/CALLISTO-NASA-GALILEO-transparent-small.PNG" alt="Callisto" width="160">
</p>

<h1 align="left">Callisto</h1>

<p align="left">
  <em>A lightweight, flexible, desktop Ethereum wallet management and signing utility.</em>
</p>

<p align="left">
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/CHANGELOG.md">Changelog</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/DESIGN.md">Design</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/PRINCIPLES.md">Principles</a> ·
  <a href="https://codeberg.org/pasiphae/callisto/src/branch/main/docs/RELEASING.md">Releasing</a>
</p>

---

Callisto is a native desktop application for preparing, signing, and broadcasting Ethereum transactions. It is implemented 100% in Go.

Callisto runs entirely on your machine, talks to an Ethereum node you choose, and keeps signing keys under your control — hot-wallet key material lives in memory only while unlocked and is wiped when locked or the application is closed. Hardware-wallet keys never leave the signing device. It features full support for managing and using Safe multi-signature wallets.

Callisto can act as wallet middleware for any web3 application that supports [WalletConnect](https://walletconnect.network/), enabling you to easily switch between several different wallets and wallet types from one simple interface for use as a self-custody backend in the web3 ecosystem.

Currently, in addition to Safe contract wallets, it supports Trezor and Ledger hardware wallets, with richer support for additional signer types on the roadmap.

See some screenshots of it in action [here](./EXAMPLES.md).

> **Status: pre-1.0 (`v0.6.0`).** The foundation, basic transaction flows, Gnosis Safe multisig (including owner/threshold administration), encrypted hot-wallet keystores, and WalletConnect (sign for web dApps) are in place and usable. The Claude-assisted complex-transaction pipeline is planned — see [Roadmap](#roadmap).

## Features

- **Bring your own node.**
  - Configure multiple Ethereum RPC endpoints (`https://` or `wss://`). WebSocket endpoints get live block updates; HTTP endpoints are polled.
  - If you do not specify a node, [Flashbots Protect](https://protectrpc.flashbots.net/about) (fast) is used by default.
- **Multiple wallets, multiple signers.**
  - *Hot wallets* — import a BIP-39 seed phrase **once**, pick the account(s) to add from a derived index→address list, and set an encryption passphrase. The seed is stored only as a scrypt+AES-GCM-encrypted keystore; afterwards you unlock with just the passphrase (no re-entering the phrase). Keys are held in memory only while unlocked and wiped on lock.
  - *Hardware wallets* — Ledger and Trezor over direct USB, via a common signing interface; keys never leave the device. **No Trezor Suite or Bridge required** — Callisto talks to the Trezor Safe directly over libusb (Trezor Bridge is kept only as an automatic fallback). Trezor hidden wallets (passphrase-protected, including on-device passphrase entry) are supported.
- **Chain-aware.**
  - The native asset and block explorer adapt to the connected chain (Ethereum, Sepolia, Holesky, OP, Base, Arbitrum, Polygon, Gnosis, etc.) with a safe fallback for unknown chains.
- **WalletConnect.**
  - Paste a WalletConnect (WC) URI from any web3 application that supports WC into Callisto and use it to review, sign, and broadcast transactions from applications across the web3/L2 ecosystem. 
- **Balances.** 
  - Ether and ERC-20 tokens auto-populate with on-chain metadata (name/symbol/decimals, including legacy `bytes32` tokens), and add-your-own tokens by address.
- **ENS everywhere.** 
  - Addresses are shown as their primary ENS name where one is set (forward-verified), and recipient fields accept ENS names or addresses with live, verified resolution. All addresses are EIP-55 checksum-validated on entry.
- **Basic transfers.**
  - Send ETH or ERC-20 tokens with a consistent flow and a detailed pre-signature summary — review decoded calldata, nonce, EIP-1559 fees, and maximum total fee — before signing.
- **Broadcast & track.** 
  - Transaction monitoring post-broadcast to pre-configured chain and node.
  - Live monitoring for block inclusion and execution status.
- **Safe multisig.** 
  - Import an existing [Safe](https://safe.global) by address and work with it from a dedicated tab: propose ETH/ERC-20 transfers or owner and threshold changes, collect owner signatures locally by switching unlocked wallets (hot, Ledger, or Trezor) until the threshold is met, then execute — or reject a proposal with a same-nonce cancellation.
  - No external Safe service; everything is pre-configured locally until on-chain broadcast.
  - Pattern is primarily designed for personal Safe setups. Org support a roadmap item.
- **WalletConnect.**
  - Connect Callisto to web dApps (Uniswap, CoW Swap, …) as a wallet: paste the WalletConnect link, approve a session exposing your active wallet, then review and sign the dApp's transaction and signature requests (`eth_sendTransaction`, `personal_sign`, `eth_signTypedData_v4`) here. Works out of the box with no signup.
  - The WalletConnect v2 protocol is implemented from scratch (no Go SDK) with no new dependencies. Hot wallets and Ledger are fully supported; Trezor does transactions and message signing today (native typed-data lands with a planned Trezor overhaul).
- **History.**
  - A local record of every transaction Callisto prepared, with status and explorer links, kept in an embedded SQLite database.

## Install & run

Callisto builds from source. You need **Go 1.24+** and a C toolchain (Fyne uses CGo for its desktop driver — see Fyne's [getting-started prerequisites](https://docs.fyne.io/started/) for your OS; on macOS the Xcode command-line tools suffice).

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

1. **Connect.** Callisto auto-connects to its default mainnet endpoint on first launch (the status dot turns green). In **Settings** you can replace it, disable auto-connect, or add your own `https://…` / `wss://…` endpoints (e.g. a testnet).
2. **Wallets** → **Add hot wallet…** (use a *throwaway/test* seed for experimentation): enter the phrase once, pick the account(s), and set an encryption passphrase — you'll unlock with just that passphrase afterwards. Or **Add hardware…** for a Ledger/Trezor — both work over direct USB with no extra software (no Trezor Suite/Bridge needed); just plug in, unlock the device, and confirm on-device.
3. **Assets** → view balances for the selected wallet; they refresh on each block.
4. **Send** → choose an asset, enter a recipient (address or ENS) and amount, **Prepare transfer**, review, then **Sign & send**.
5. **Safe** (optional) → **Import Safe…** by address, propose a transfer or an owner/threshold change, collect owner signatures (unlock each owner in **Wallets** and click **Sign**), then **Execute** once the threshold is met.
6. **History** → track what you've sent; select a row to open it on a block explorer.

## Security model

- **Seeds are stored only as encrypted keystores — never in the clear.** A hot wallet's BIP-39 seed is sealed with **scrypt (memory-hard KDF) + AES-256-GCM** under a passphrase you choose at import, and written to a `0600` file. The config itself holds only inert *descriptors* (label, address, derivation path) and never key material. Deleting a wallet securely wipes its keystore once no account references it.
- **Hot wallets** decrypt the seed into memory only while unlocked (so you can switch derived accounts); the seed and the selected private key are zeroed on lock, disconnect, or exit. HD derivation (BIP-32/44) is implemented in-house on the secp256k1 primitives go-ethereum already vendors, deliberately avoiding extra dependencies in the signing path.
- **Your recovery phrase is your backup.** The passphrase protects the on-disk keystore; it is not recoverable and does not replace your seed phrase. Best-effort file wiping is not a guaranteed secure-erase on modern SSDs — keep your phrase safe offline.
- **Hardware wallets** keep keys on the device; Callisto only requests signatures you confirm on the device.
- Callisto makes **no outbound connections except to the RPC endpoint and services you use.** It ships with a default Flashbots Protect mainnet endpoint for convenience (auto-connecting on first launch); you can replace it, disable auto-connect, or point Callisto at your own node at any time in Settings.

Treat this as pre-1.0 software: review transactions on-device, and prefer test networks and throwaway keys while the project matures.

## Configuration & data

Stored under your OS config directory (e.g. `~/Library/Application Support/callisto/` on macOS):

- `config.json` — RPC endpoints, wallet descriptors, imported Safes (address + cached owners/threshold + local owner labels), and added tokens (no secrets; written atomically, `0600`).
- `keystores/<id>.json` — per-hot-wallet encrypted seed keystores (scrypt + AES-256-GCM), `0600` in a `0700` directory. This is the only place seed material is stored, and only ever encrypted.
- `callisto.db` — SQLite database for transaction history, Safe proposals and collected signatures, and the contract address book.

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

On macOS the linker prints a benign `ignoring duplicate libraries: '-lobjc'` warning from Fyne's CGo driver — it is not an error.

### Architecture

The GUI (`internal/ui`) is a thin layer over independent domain packages; the domain never depends on the UI, and all key material is isolated in the signer and keystore packages. New signer types, chains, and asset kinds are designed to slot in without touching transaction preparation, review, or broadcast.

| Package | Responsibility |
|---|---|
| `internal/chain` | Per-chain metadata (native asset, explorer) |
| `internal/rpc` | Endpoint config + connection manager (`Client` interface, head watcher) |
| `internal/address` | EIP-55 validation & display |
| `internal/ens` | ENS forward/reverse resolution (forward-verified) |
| `internal/signer`, `.../hot`, `.../hardware` | Signing interface; hot (seed) and Ledger/Trezor signers |
| `internal/keystore` | scrypt + AES-256-GCM encryption of hot-wallet seeds at rest |
| `internal/assets` | ETH + ERC-20 detection, metadata, unit conversion |
| `internal/tx` | Build, gas estimation, assembly, broadcast, inclusion |
| `internal/safe` | Safe multisig: reads, safeTxHash, exec/admin encoding, proposals |
| `internal/walletconnect` | WalletConnect v2 Sign (relay, envelope crypto, session engine) |
| `internal/history` | Transaction lifecycle records |
| `internal/config`, `internal/store` | JSON settings; SQLite store |

See [`DESIGN.md`](DESIGN.md) for the full specification and [`PRINCIPLES.md`](PRINCIPLES.md) for development principles. Contributions follow the branch/version/release workflow in [`docs/RELEASING.md`](docs/RELEASING.md).

## Roadmap

Implemented above; still to come (designed for, not yet built):

- **Claude-assisted complex transactions**: natural-language requests ("deposit 10 ETH to Aave v3") resolved to reviewed calldata, with a growing on-chain contract address book, and multi-step flows via the DeFiSaver SDK.
- **Transaction simulation** against a fork before signing.
- **OS-native keystore storage**: back the encrypted keystore with the OS keychain where available (macOS Keychain, and the Secret Service / DPAPI equivalents) for defense in depth beyond the passphrase-encrypted file.
- More signer types (incl. GridPlus Lattice, pending a Go SDK) and chains.

## Credits

- Callisto imagery: NASA/JPL's [Galileo](https://solarsystem.nasa.gov/) mosaic of Jupiter's moon Callisto (public domain).
- Addresses and numeric values are set in [Berkeley Mono](https://usgraphics.com/products/berkeley-mono) by U.S. Graphics Company, bundled and embedded under the project's font license.
- `internal/signer/hardware/usbwallet` is a local, patched fork of three files from [go-ethereum](https://github.com/ethereum/go-ethereum)'s `accounts/usbwallet` (LGPL-3.0-or-later; license and attribution preserved in-file) — see that package's doc comment for why.

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
