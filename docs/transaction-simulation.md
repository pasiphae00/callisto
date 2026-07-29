# Transaction simulation — design & plan

Pre-sign simulation for the review step (`DESIGN.md`'s "simulate before signing"), for
**both EOAs and Safe multisig**. Two related features, driven by **one in-house
simulation** of the prepared transaction against **current chain state** (no third-party
service; privacy per `PRINCIPLES.md`):

1. **Asset-change preview — the safety feature (the point).** Before signing, show the
   user the actual **balance/state changes** the transaction would produce right now —
   "you send 10 ETH, you receive 9.998 stETH; you grant an **UNLIMITED** USDC approval" —
   so they can confirm it does what they expect. A self-custody guardrail against scams,
   buggy dApps, and fat-fingers.
2. **Automatic revert warning — the guardrail.** If the transaction **would revert**,
   surface a prominent automatic warning (with the reason) so the user doesn't sign a
   doomed tx and waste gas.

They come from the same simulation call — a successful sim yields the asset changes, a
failing one yields the revert — but differ in **surface and availability**: the revert
warning works on **any** RPC (`eth_call`), while the asset-change preview needs a capable
endpoint (`eth_simulateV1` or archive `debug`) because it requires the execution
**logs/state-diff**, not just pass/fail. So where we can't render a rich preview we can
still auto-warn on reverts.

Where it plugs in: the pre-sign review dialogs — basic **Send**, **WalletConnect**
`eth_sendTransaction`, and **Safe** proposals (both the Proposals review and the **Build**
tab). WalletConnect is the highest-value target (arbitrary dApp calldata); Safe is the
most technically involved.

---

## The RPC landscape (what we can call)

Three tiers, best-to-worst fidelity, with graceful degradation — probe the connected RPC
once and use the best available:

| Method | Gives us | Availability |
| --- | --- | --- |
| **`eth_call`** (+ state overrides) | revert / return data. No diff. | Universal |
| **`eth_simulateV1`** (`traceTransfers:true`, returns per-call **logs**) | success, gas, **logs** (→ token/NFT/approval diffs), ETH transfers, multi-tx, state overrides | geth ≥1.14.9, Nethermind ≥1.28, Base/OP/Gnosis; growing but **not** on every public RPC |
| **`debug_traceCall`** (`callTracer` + `prestateTracer{diffMode:true}`) | call tree + **full state diff** (native balances, storage) + logs | needs the `debug` namespace — the **Ganymede archive has it**; most public L2 RPCs don't |

**Rejected:** external simulation APIs (Tenderly, Alchemy `simulateExecution`, Blocknative).
They're excellent but send the transaction to a third party — that breaks "no outbound
except the RPC you choose." Keep simulation in-house; the tiers above cover it.

**Recommended primary:** `eth_simulateV1` where present (standardized, returns logs so we
get token diffs without the `debug` namespace, supports overrides and batching), falling
back to `debug_traceCall` (rich, on the archive node) and finally `eth_call` (revert-check
only, universal). Ganymede gives us the full picture; public L2s at least give revert
checks.

---

## Safe simulation (the hard half)

A Safe owner signs a `safeTxHash`; execution happens later via `execTransaction(...)`.
We must simulate the **effect of execution**, and usually **without a full signature set**.

### Revert/success — universal, any RPC — `SimulateTxAccessor`

Safe ships a helper, **`SimulateTxAccessor`** (canonical 1.3.0 deployment
`0x59AD6735bCd8152B84860Cb256dD9e96b85F69Da`; **resolve per Safe version/chain** via
`safe-deployments` — 1.4.1 differs), with `simulate(to, value, data, operation) →
(estimate, success, returnData)`. Invoke it through the Safe itself:

```
eth_call({
  to:   <safe>,
  data: Safe.simulateAndRevert(
          <SimulateTxAccessor>,
          SimulateTxAccessor.simulate(to, value, data, operation))
})
```

`simulateAndRevert` **delegatecalls** the accessor, so the inner tx runs **in the Safe's
context** (correct `msg.sender`, correct `operation` — handles both `Call` **and**
`DelegateCall`/MultiSend), then reverts with the ABI-encoded `(success, gas, returnData)`.
`eth_call` returns that revert payload → we decode success/gas/reason. **No signatures, no
threshold, works on any RPC.** This is the Safe revert-check baseline.

⚠️ Because it *reverts by design*, a state-diff tracer sees no net change — so this path
gives success/revert only, **not** asset diffs.

### Asset diffs for Safe

- **`operation == Call`:** simulate the inner call directly as the Safe —
  `eth_simulateV1({from: <safe>, to, value, data, traceTransfers:true})` (or `from=safe`
  trace). Faithful for plain calls, no signatures, returns logs → diffs.
- **`operation == DelegateCall` (MultiSend batches from Build), or full fidelity:**
  simulate the real `execTransaction` with a **state override** to bypass the signature
  check — override the Safe's `threshold` storage slot to `1` and pass one
  pre-approved-hash signature (`{r: owner, s: 0, v: 1}` for an owner we override as having
  approved), or override owner/threshold storage so `checkSignatures` passes. Then
  `eth_simulateV1`/`debug_traceCall` runs `execTransaction` to completion → real logs +
  state diff. Most complete; needs overrides + sim/debug support.
- **Build tab shortcut:** we *built* the action, so we already know its intended effect
  (e.g. "wrap 10 ETH → 10 WETH") — show that decoded intent alongside a `simulateAndRevert`
  revert-check even on a bare RPC, and layer the real diff on when the RPC supports it.

---

## Computing human-readable changes

From simulation **logs** (via `eth_simulateV1` or `callTracer`), decode and net-out for the
account of interest (the EOA, or the Safe):

- ERC-20 `Transfer(from,to,value)` → ± token balance; `Approval(owner,spender,value)` /
  Permit2 → approval granted (flag **UNLIMITED**, reuse the WalletConnect decoder from
  `internal/ui/wc_decode.go`).
- ERC-721 `Transfer(from,to,tokenId)`, ERC-1155 `TransferSingle/Batch` → NFT in/out.
- Native ETH: from `eth_simulateV1` **`traceTransfers`** (ETH moves surface as logs) or the
  `prestateTracer` diff (authoritative balance delta).

Resolve token metadata with `assets.Metadata` (already Multicall3-batched) and sanitize
symbols/names through `internal/textsafe` before display. Present as signed rows:
`−10 ETH`, `+9.998 stETH`, `Approve UNLIMITED USDC → 0x…`.

---

## Architecture (`internal/sim`)

A new, RPC-only package (no UI deps), mirroring how `internal/tx` and `internal/assets`
are structured:

- `Capability(ctx, client) Tier` — probe `eth_simulateV1` / `debug_traceCall` once per
  connection, cache it (like the asset-service cache).
- `Request` — the tx(s) to simulate + account-of-interest + block (pending). A Safe
  variant carries `{safe, to, value, data, operation, owner?}`.
- `Result` — `{Status: OK|Revert|Unavailable, RevertReason, GasUsed, ETHDelta,
  Tokens []TokenDelta, NFTs []NFTDelta, Approvals []ApprovalChange, Tier, Note}`.
- `SimulateEOA` / `SimulateSafe` pick the best strategy for the tier and decode logs.
- Log decoders (Transfer/Approval/1155) shared with the approvals/wc-decode code.

**UI:** each review dialog gains a **Simulation** section that **auto-runs async on open**
(spinner → result; Sign stays enabled but a **REVERT** result shows a red warning to heed),
plus a manual **Re-simulate**. On a Tier-0 RPC it shows "revert check only — connect an
`eth_simulateV1`/archive endpoint for full asset changes." A Settings toggle can disable
auto-simulate.

---

## Phasing

The asset-change preview is the deliverable; the revert warning falls out of the same
engine and degrades to bare RPCs.

**Status: P3a is built** (`internal/sim`, `internal/ui/sim_section.go`), wired into all
four review surfaces. P3b and P3c remain open.

- **P3a — The engine + both surfaces (EOA + Safe-`Call`).** ✅ **Done.** Simulate the prepared tx and
  render the **asset-change preview** on capable RPCs (`eth_simulateV1` `traceTransfers` +
  logs → decoded ETH/token/approval deltas; `debug_traceCall` fallback on Ganymede), **and**
  the **automatic revert warning** universally (`eth_call` for EOA, `simulateAndRevert` +
  `SimulateTxAccessor` for Safe). Sensible build order *within* P3a: (1) the sim call +
  revert warning (quick, universal), (2) log→asset-diff decoding + the preview UI (the
  meat), (3) wire into the Send / WalletConnect / Safe review dialogs. On a bare RPC the
  preview shows "asset preview needs an `eth_simulateV1`/archive endpoint — revert check
  only."
- **P3b — Safe `DelegateCall`/MultiSend diffs.** `execTransaction` with a signature-bypass
  state override so Build-tab batches get a real asset preview (P3a already covers their
  revert-check via `simulateAndRevert`, and plain-`Call` Safe txs get full previews).
- **P3c — Polish.** ERC-721/1155 in/out, storage-diff niceties for un-logged effects,
  capability-probe caching.

---

## Caveats (surface honestly to users)

- **A snapshot, not a guarantee.** Simulated at latest/pending; real inclusion can differ
  if state changes first (front-running, other txs). Label it "simulation," not "result."
- **`prestateTracer` diff quirk:** on reth ≥2.0, `diffMode` with `gasPrice > 0` and explicit
  gas can report phantom balance credits — simulate with `gasPrice: 0` (or prefer
  `eth_simulateV1`) to avoid it.
- **Safe not-yet-final state:** simulating at the current nonce assumes no other same-nonce
  tx executes first; note when a competing same-nonce proposal exists (we already flag
  those).
- **Gas for asset diffs:** run sims with generous/`0` gas and no balance validation so a
  gas-poor account still simulates the *effect*; keep a separate real gas estimate for the
  fee display.

## Decisions (all resolved)

1. **Trigger** — the **revert check** runs automatically on review-open; the
   **asset-change preview** is on demand, behind a **Simulate…** button. Auto-running the
   rich call on every review would spend a rate-limited endpoint's budget on work the user
   didn't ask for, and the cheap check already covers the "don't sign a doomed tx" case
   universally. No Settings toggle was needed as a result.
2. **Primary method** — `eth_simulateV1` first, `debug_traceCall` (`callTracer`,
   `withLog`) second, `eth_call` last. `eth_simulateV1` is standardized, needs no `debug`
   namespace, and its `traceTransfers` reports native ETH moves as pseudo-logs from the
   zero address, so one call yields the whole picture. Capability is probed once per
   connection (`sim.Prober`) and cached; `sim.Caps` records both bits, since an archive
   node serves both and the UI needs to know whether a rich preview is possible at all.
3. **Surfaces** — all four at once (Send, WalletConnect, Safe Build, Safe Proposals),
   through one shared `simSection` widget. Four bespoke copies of the hedging wording
   would have drifted, and the wording is itself part of the safety feature.
4. **Decoding scope** — ERC-20 + native + approvals (incl. Permit2); ERC-721/1155 in P3c.

## Implementation notes worth remembering

- **`SimulateTxAccessor` addresses** are pinned from safe-deployments' raw
  `simulate_tx_accessor.json`, not a paraphrase: 1.3.0 canonical
  `0x59AD6735bCd8152B84860Cb256dD9e96b85F69Da`, 1.4.1 canonical
  `0x3d4BA2E0884aa488718476ca2FB8Efc291A46199`, plus zkSync-Era-specific deployments for
  both. Canonical is deployed on every chain in `config.ChainCatalog`. Safes older than
  1.3.0 have no accessor (nor `simulateAndRevert`) and report simulation as unavailable.
- **A delegatecall into a codeless address succeeds and returns nothing**, so "the
  accessor isn't deployed here" and "the transaction is fine" look identical unless the
  accessor's return-data length is checked explicitly. `parseSimulateAndRevert` does.
- **Safe `Call` on a capable endpoint skips the accessor** and simulates the inner call
  with `from: <safe>` — `msg.sender` is then exactly what `execTransaction` produces, and
  one round trip gives both the revert check and the deltas. The accessor path is for bare
  RPCs and for `DelegateCall`, where it is the only faithful option.
- **`eth_simulateV1` attributes native transfers to the ERC-7528 placeholder**
  `0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE`, not the zero address — and go-ethereum's
  own doc comment in `internal/ethapi/logtracer.go` says `0x0`, describing an earlier
  implementation. Trust the constant, not the comment. Both are accepted as native
  markers, which is safe because neither address holds code and only executing code can
  emit a log.
- **`types.Log` cannot be used for simulated logs**: its `UnmarshalJSON` rejects anything
  missing `transactionHash`/`blockHash`, which are meaningless for a transaction that was
  never mined. The wire types use a minimal log struct and convert.
- **Reverted sub-calls must be pruned from a `callTracer` tree** before their logs are
  counted — a caught `try/catch` failure emits logs that never took effect.
