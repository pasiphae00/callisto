# WalletConnect for Safe — feasibility (v0.11 P4 research)

Can a Callisto-managed **Safe** connect to dApps over WalletConnect the way an EOA
does today? Short answer: **partially, and worth doing for the common case** — but a
Safe is fundamentally different from an EOA, so it needs a purpose-built request
handler, not a reuse of the EOA path. This documents the constraints and a
recommended, phased implementation. Nothing here is built yet.

## The core problem

A Safe is a **contract**, not a keypair. Two consequences drive everything:

1. **It can't produce an ECDSA signature.** A dApp `personal_sign` /
   `eth_signTypedData_v4` can't be answered with a normal signature; it needs an
   **EIP-1271** contract signature that the Safe's `isValidSignature` will validate.
2. **Its transactions are asynchronous.** An EOA `eth_sendTransaction` is signed and
   broadcast in one step, returning a tx hash. A Safe transaction is *proposed*,
   *signed by ≥ threshold owners*, then *executed* — which may span time, owners, and
   (with Callisto's export/import) machines. A WalletConnect request is essentially
   synchronous: the dApp waits for a single response (a tx hash, or an error).

So the EOA assumptions (one key, immediate hash) don't hold. WalletConnect *transport*
is reusable (we already implement WC v2 Sign from scratch); the *request semantics*
are what change.

## The two request types

### `eth_sendTransaction`
The request carries `{to, value, data}` — the **inner** call the Safe should make.
Callisto must turn it into a SafeTx (next Safe nonce, operation Call), collect
signatures, and execute. The gap: the dApp expects a tx hash **now**. Options:

- **A. Execute-now when the threshold can be met.** If the Safe is 1-of-1, or enough
  owners are unlocked on this machine to reach the threshold immediately, build → sign
  (locally, switching owners) → execute, and return the **real execution tx hash**.
  This is a correct, synchronous answer and covers the primary target: personal /
  low-threshold Safes. **Recommended primary path.**
- **B. Create-and-defer for higher thresholds.** When the threshold can't be met now,
  create the proposal locally (it appears in the Proposals tab) and **reject the WC
  request** with a clear message ("Proposal created in Callisto — collect signatures
  and execute there"). Honest, but the dApp sees a failed request; behavior varies by
  dApp. **Recommended fallback.**
- **C. Return the `safeTxHash`.** Rejected: most dApps treat the returned value as an
  L1 tx hash and poll `eth_getTransactionReceipt`, which never resolves for a
  safeTxHash. Misleading.

### `personal_sign` / `eth_signTypedData_v4`
Answer with an **EIP-1271** signature: hash the message as a Safe message (EIP-712
`SafeMessage(bytes message)` bound to the Safe's domain), collect owner signature(s)
over that hash (the same `SafeHashSigner` path we already have), pack them, and return.
The dApp validates by calling `isValidSignature` on the Safe. **Constraint:** the dApp
must support EIP-1271 — many do (login/SIWE, orders), but some assume an EOA sig and
will reject a contract signature. Feasible for the threshold-now case (A); deferrable
otherwise.

## What Safe{Wallet} does (for reference)
The official flow leans on infrastructure Callisto deliberately avoids: the **Safe
Transaction Service** (a hosted queue) and a relayer. Its WalletConnect integration
generally queues the transaction and returns the `safeTxHash`, with the dApp often
using the Safe Apps SDK rather than raw WC. Callisto has **no** Safe service, so it
can't queue-and-return-hash the same way — which is exactly why the **execute-now**
path (A) is the tractable, self-custody-friendly option.

## Additional constraints
- **Chain match:** the WC session's chainId must equal the Safe's chain.
- **Nonce coordination:** a WC-initiated SafeTx takes the next Safe nonce and can
  collide with pending proposals — the same-nonce conflict flag we built in P2 applies.
- **Session identity:** the session must expose the **Safe address** as the account,
  not an owner EOA; the existing WC engine assumes the active wallet's address.
- **Review before signing** still applies: decode the inner call and show it, exactly
  as the pre-sign review does elsewhere.

## Recommendation
Ship this research in v0.11; implement in a later phase, scoped to the tractable slice:

1. **Phase A (high value, tractable):** support a Safe as a WC account for
   **`eth_sendTransaction` when the threshold can be met on this machine** (build →
   local multi-owner sign → execute → return the execution hash) and **EIP-1271
   message signing** for the same case. This covers personal and low-threshold Safes —
   the primary audience — end to end.
2. **Phase B:** create-and-defer for higher-threshold Safes (proposal appears in the
   Proposals tab; WC request returns a clear "continue in Callisto" result), tying into
   the P3 distributed-signing flow.
3. **Explicitly document limitations:** dApps that require EOA signatures (no EIP-1271)
   won't work for signing; high-threshold Safes can't answer a WC transaction
   synchronously.

Effort is moderate: a Safe-aware WC request handler (transaction → SafeTx + execute;
message → EIP-1271), session wiring to advertise the Safe address, and UI review reuse.
No new dependencies. Deferred until after the current Safe deep-dive lands.
