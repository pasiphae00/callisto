# Advanced (Claude-assisted) transaction preparation — research & plan

Design work for the DESIGN.md "Complex transaction preparation" phase: let a user type
an intent ("deposit 10 ether to Aave v3", "stake 15.75 ether with Lido") and have
Callisto prepare a reviewed, signable transaction — with an **optional, default-off**
Claude integration doing the natural-language interpretation. Nothing is built yet.

## Non-negotiable safety principle

**Claude proposes; Callisto verifies and builds.** Claude never emits raw calldata that
gets signed. Its job is to map an intent onto a **curated, in-code action** and extract
parameters. Callisto owns the deterministic calldata builder for each supported action,
decodes the result back to a human-readable review, optionally simulates it, and the
**user gives final confirmation**. This bounds the blast radius: only pre-vetted
contracts/functions are ever executable, and a wrong Claude answer produces a *wrong
review the user can reject*, never a silent malicious call. It also satisfies DESIGN.md's
"validate if the request is possible with available tools, or gracefully say it can't."

The alternative — letting Claude emit arbitrary `to`/`data` — is rejected: it makes an
LLM a signing authority over a wallet, which is unacceptable for this project's threat
model.

## Architecture

```
intent text
   │
   ▼
[intent resolver]  ── optional Claude (tool-use) OR a manual action/param form
   │  picks: action id + params (from the curated registry only)
   ▼
[action registry]  ── curated Go builders: per-chain address, ABI, encode(), describe()
   │  produces: tx.Send (to, value, calldata) + a human-readable decode
   ▼
[review]  ── decoded contract/function/params (never raw calldata)
   │
   ▼
[simulate]  (optional) ── eth_call + debug_traceCall state diff → before/after
   │
   ▼
[sign & broadcast]  ── existing pipeline (EOA send, or a Safe proposal)
```

### 1. Action registry (the heart of it)
A curated set of **supported actions** in Go, each with:
- a stable `id` (e.g. `weth.wrap`, `lido.stake`, `aavev3.supply`, `erc20.approve`),
- per-chain contract address(es) (verified, bundled — not fetched blind),
- the function ABI + a deterministic `encode(params) → calldata`,
- a `describe(params) → human-readable` for the review,
- a param schema (types, which are amounts/addresses/token symbols) for validation.

This *is* the "growing address book" DESIGN.md calls for, backed by the existing
`contracts` / `selectors` store tables. Claude (or a form) can only select from this set;
adding a protocol = adding a vetted registry entry (code-reviewed), not trusting an LLM.

Phase-1 candidate actions (all single-step, direct calls): `weth.wrap`/`unwrap`,
`lido.stake`, `aavev3.supply`/`borrow`/`repay`/`withdraw`, `erc20.approve`/revoke (reuse
the Approvals pane), plain transfers (reuse Send). These cover most DESIGN.md examples.

### 2. Claude integration (optional, default-OFF)
- **Settings:** an "AI features" section — a master toggle (off by default), an API-key
  field (persisted, with a delete button), and clear copy that enabling it sends the
  intent text (and resolved on-chain context) to Anthropic.
- **Cold path:** when the toggle is off, no Claude client is constructed and no key is
  read — the code path is inert (a `internal/ai` package that's only wired up when
  enabled). Matches the WalletConnect/relay "lazy client" pattern.
- **No SDK:** implement the Anthropic **Messages API** directly over HTTPS (a small
  `internal/ai` package), same ethos as the from-scratch WalletConnect client — no new
  heavy dependency, full control over what's sent.
- **Tool-use loop:** Claude is given tools that mirror the registry — `list_actions`,
  `resolve_token`, `get_balance`, `propose_action(id, params)` — and Callisto executes
  them. The final `propose_action` is validated against the registry, built, and shown.
  Claude reasons over on-chain facts Callisto supplies; it doesn't invent addresses.
- **Privacy/cost:** the intent text and minimal context leave the machine only when
  enabled; make that explicit. Bring-your-own-key, so cost is the user's.

### 3. Simulation (in-house, no external service)
- **Revert + outcome check:** `eth_call` the prepared tx from the account at the pending
  block — catches reverts and decodes return values before signing.
- **Before/after state:** `debug_traceCall` with the prestate tracer (erigon/the default
  Ganymede archive supports it) to diff touched balances/storage, surfaced as
  human-readable "ETH: 10 → 0, stETH: 0 → 9.998" style rows. Falls back to explicit
  balance `eth_call`s (native + relevant ERC-20s) when tracing is unavailable.
- A **Simulate** button beside Sign (for both EOA and Safe), then a continue/reject
  prompt — as the TODO's simulation section specifies. Usable independently of Claude.

### 4. Single-step vs multi-step
- **Phase 1 — single-step**, EOA **and** Safe. One action → one `tx.Send` → EOA send or a
  Safe proposal. No DeFiSaver needed; covers the bulk of the examples.
- **Phase 2 — multi-step (Safe-only)** is where DESIGN.md mandates DeFiSaver. Key finding
  below; likely a later, separate effort.

## The DeFiSaver problem (multi-step)

DESIGN.md mandates the DeFiSaver SDK + RecipeExecutor for multi-step transactions. Two
hard facts from the research:

1. **The DeFiSaver SDK is JavaScript/TypeScript.** Callisto is Go with a deliberately
   minimal dependency set and no JS runtime. Using the SDK directly is out; we'd have to
   **re-implement the recipe ABI-encoding in Go** (action structs + the RecipeExecutor
   `executeRecipe` call), which is real, security-sensitive work.
2. **Recipes need parameter piping.** DeFiSaver's value is feeding one action's *output*
   into the next ("deposit the **full amount** received", "buy the **maximum**") via its
   subData/placeholder mechanism. Plain batching can't express that.

Options for multi-step, roughly increasing effort:
- **A. Safe MultiSend (no piping).** Batch several fixed-amount actions atomically in one
  Safe tx using the MultiSend contract. Simple, no new deps, reuses our Safe pipeline —
  but can't do "the full amount" piping. Covers fixed-sequence recipes only.
- **B. Hand-encode DeFiSaver recipes in Go.** Honor the DESIGN mandate: encode the
  RecipeExecutor call + action structs ourselves, executed via the Safe. Full piping,
  battle-tested contracts — but the encoding is intricate and must be exhaustively tested
  and verified against the JS SDK's output.
- **C. Alternatives** (Enso, Furucombo, etc.) — similar JS-first tooling; no clear Go win.

**Recommendation:** ship Phase 1 (single-step) first — it's most of the value and has no
DeFiSaver dependency. Treat multi-step as its own project, starting with **A (MultiSend
batching)** for fixed sequences and evaluating **B** for true piping later. Revisit the
DESIGN.md "MUST use DeFiSaver" clause as a documented deviation if MultiSend/hand-encoding
proves the better fit (it's a MUST worth re-examining given the Go/JS mismatch).

## Phasing
- **P1 — Foundation + single-step, AI-off:** the Prepare pane, the action registry with a
  handful of vetted actions, a **manual** action/param path (no Claude), review + sign
  (EOA + Safe). Proves the builder/review/registry with zero AI surface.
- **P2 — Claude intent resolver:** the `internal/ai` Messages-API client, settings
  (toggle/key/cold-path), the tool-use loop mapping NL → a registry action. Default off.
- **P3 — Simulation:** in-house eth_call + trace state-diff, Simulate button, before/after
  review. Independent of Claude.
- **P4 — Multi-step (Safe-only):** MultiSend batching first; DeFiSaver piping evaluated
  as a follow-on.

## Open decisions (for review)
1. **Action model:** curated registry (recommended) vs open Claude calldata (rejected on
   safety) — confirm the curated approach.
2. **AI-off value:** should P1 ship a manual action/param form (feature works with AI
   off), or is the Prepare pane AI-only (empty until a key is set)?
3. **Phase-1 protocol set:** confirm the starting actions (WETH wrap/unwrap, Lido stake,
   Aave v3 supply/borrow/repay/withdraw, approve/revoke, transfer).
4. **Multi-step path:** accept "MultiSend first, DeFiSaver later" and record the DESIGN.md
   deviation, or commit to hand-encoding DeFiSaver recipes up front.
5. **Simulation timing:** in P1 alongside single-step, or its own phase after the AI
   resolver.
```
