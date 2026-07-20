# Advanced transaction preparation — design & scope

Callisto's take on DESIGN.md's "Complex transaction preparation" phase: let a Safe reach
common ecosystem actions (wrap ETH, stake with Lido, wrap/unwrap wstETH, request/claim a
Lido withdrawal) as reviewed, signable **proposals**, built from a curated in-code
registry.

## Scope decision (2026-07)

**This is a Safe-only feature.** EOAs interact with the ecosystem by linking the active
account to a dApp over **WalletConnect** and using the dApp's native flows — that's
strictly better than re-implementing each protocol's UX in Callisto. A Safe *can't* drive
a synchronous dApp handshake (see `docs/safe-walletconnect-research.md`: a dApp expects a
tx hash back from `eth_sendTransaction`, but a Safe tx is propose → collect M signatures →
execute, which doesn't exist yet at request time — n-of-M is impossible synchronously).
So curated on-Safe preparation is how a Safe reaches the same actions.

Consequences:
- The standalone "Prepare" pane and the **optional Claude/AI resolver were removed.** With
  the feature scoped to a small, discoverable curated set on a Safe, a natural-language
  front end wasn't earning its complexity. The registry (`internal/actions`) stays; it's
  reachable from the Safe pane's **Build** sub-tab. If NL intent ever returns, it plugs in
  above the registry without changing the trust model.

## Safety principle (unchanged)

Only pre-vetted contracts/functions are ever executable. Each action is a **curated Go
builder** — per-chain address, ABI, deterministic `encode()`, and a human-readable
`describe()` — so the review shows the decoded contract/function/params, never raw
calldata, and the user gives final confirmation. Adding a protocol = adding a
code-reviewed registry entry.

## Architecture (shipped)

```
Safe pane → Build tab
   │  pick a curated action + fill params
   ▼
[action registry]  internal/actions — per-chain address, ABI, encode(), describe(),
   │                and a declared approval requirement (token, spender, amount)
   ▼
[approval check]  read live allowance from the Safe; batch any needed approve()s
   │               atomically with the action via MultiSendCallOnly (one proposal)
   ▼
[review]  decoded action (+ batched approvals) + Safe nonce + safeTxHash
   ▼
[Safe proposal]  collect owner signatures, execute — existing Safe pipeline
```

Actions today (mainnet): `weth.wrap`/`unwrap`, `lido.deposit`, `wsteth.wrap`/`unwrap`,
`lido.withdraw` (request), `lido.claim`. Each declaring its approval need lets the
pipeline batch `approve` + action into one atomic MultiSend, so owners never approve
separately.

## Next

- **More ecosystem actions** (Uniswap V3 trade, Aave v3 supply/borrow/repay/withdraw) as
  curated registry entries — same Build→proposal path.
- **Simulation** (its own phase): `eth_call` at the pending block for revert/return
  decoding, plus `debug_traceCall` prestate diff (the Ganymede archive supports it) for a
  before/after balance view — surfaced behind a **Simulate** button before signing. Falls
  back to explicit balance `eth_call`s where tracing is unavailable.
- **Multi-step (Safe-only)** — see the DeFiSaver note below; a later, separate effort.

## The DeFiSaver / multi-step problem

DESIGN.md mandates the DeFiSaver SDK + RecipeExecutor for multi-step transactions. Two
hard facts:

1. **The SDK is JavaScript/TypeScript** (~77% TS). Callisto is Go with a minimal
   dependency set and no JS runtime — using the SDK directly is out; we'd re-implement the
   recipe ABI-encoding in Go (action structs + `executeRecipe`), which is real,
   security-sensitive work. Sources to mine for P-multi:
   - Contracts: <https://github.com/defisaver/defisaver-v3-contracts>
   - SDK encoding (`DEV.md` / `ACTIONS.md`): <https://github.com/defisaver/defisaver-sdk>
2. **Recipes need parameter piping** — feeding one action's *output* into the next ("deposit
   the **full amount** received") via subData placeholders. Plain batching can't express it.

Options, increasing effort: **A. Safe MultiSend** (atomic, fixed amounts, no piping — what
we already use for approve+action); **B. hand-encode DeFiSaver recipes in Go** (full
piping, honors the mandate, intricate + must be verified against the JS SDK's output);
**C. alternatives** (Enso, Furucombo — similarly JS-first, no clear Go win).

**Recommendation:** MultiSend (A) covers fixed sequences now; evaluate B for true piping
later, and record a documented DESIGN.md deviation if hand-encoding proves the better fit
than the mandated SDK (a MUST worth re-examining given the Go/JS mismatch).

## Approvals (done)

An action *declares* its approval requirement (token, spender, amount). The pipeline reads
the Safe's live allowance and, if short, bundles the `approve` with the action atomically
in one MultiSend proposal — so nothing is approved as a separate step. (The EOA path —
EIP-7702 atomic batching from a plain EOA, live since Pectra — is moot now that EOAs use
WalletConnect; see `docs/account-abstraction-research.md`.)
