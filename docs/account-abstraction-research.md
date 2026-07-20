# Account abstraction & new wallet types — research (future version)

Research only — no commitment yet. Question: should Callisto support smart-account /
account-abstraction wallet types, and what changed with recent Ethereum upgrades?

## Landscape (as of 2026)

Two complementary standards, both now mainstream:

- **ERC-4337 — smart contract accounts.** A wallet *is* a contract, operated via
  UserOperations through bundlers, with paymasters (gas sponsorship, pay gas in any
  token), session keys, and custom validation. Bundler/paymaster infra is production
  across major L2s; tens of millions of smart accounts deployed. A distinct account type
  (its own address, no private key in the classic sense — validation is contract logic).

- **EIP-7702 — "smart EOAs" (Pectra, activated 2025-05-07).** A new type-`0x04`
  transaction lets an existing **EOA** delegate its execution to a smart-contract
  implementation *while keeping its address and key*. Unlocks the 4337 feature set
  (**batched transactions**, gas sponsorship, session keys, passkey auth) for the ~all
  existing EOAs, no migration. Adopted by MetaMask, Rabby, Trust; steady growth in
  type-`0x04` usage. Complements 4337 rather than replacing it.

## Why it matters for Callisto

- **Batching solves the approval-UX problem** (see `advanced-transactions.md`): EIP-7702
  lets a plain EOA do `approve` + action (or any multi-step) atomically in one tx —
  today only Safes can batch (MultiSend). This is the single biggest reason to look at
  7702.
- **Gas sponsorship / pay-gas-in-token** could smooth onboarding and L2 use.
- **New wallet types** users increasingly hold: 4337 smart accounts, 7702-delegated
  EOAs, passkey/WebAuthn accounts.

## Open questions to resolve before committing

1. **7702 for our EOAs (highest value):** construct type-`0x04` txs (authorization list
   signed by the EOA), pick/ship a **trusted, audited batcher/delegate contract**
   (whose code the EOA delegates to — a security-critical choice), and confirm **signer
   support** — do our Ledger/Trezor drivers (the LGPL usbwallet fork) sign 7702
   authorizations / type-`0x04` txs? Firmware support varies and must be verified per
   device. Hot wallets can sign it directly.
2. **4337 as a wallet type:** import/operate an existing smart account? That means a
   UserOperation path (bundler RPC, paymaster, entryPoint) — a sizable new subsystem and
   dependency surface, weighed against the minimal-deps principle.
3. **Security posture:** delegating an EOA to contract code (7702) changes its trust
   model; the delegate contract is now part of the account's security. Callisto's
   emphasis on key-material correctness must extend to "what code can act as this
   account." Default-off, explicit, well-reviewed delegate only.
4. **Scope & sequencing:** likely order — (a) 7702 *batching* to power the advanced-tx
   approval UX for EOAs (narrow, high value), then (b) broader smart-account support if
   warranted. Keep it its own version after the advanced-tx work lands.

## Sources
- EIP-7702 / Pectra overview: <https://www.alchemy.com/blog/eip-7702-ethereum-pectra-hardfork>
- AA in 2026 (4337 + 7702): <https://blog.thirdweb.com/account-abstraction-in-2026-how-eip-7702-and-erc-4337-are-transforming-ethereum-wallets-for-developers/>
- Ledger on Pectra: <https://www.ledger.com/academy/topics/crypto/what-is-the-ethereum-pectra-upgrade>
