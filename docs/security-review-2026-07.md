# Callisto security review — 2026-07 (pre-beta self-audit)

**Scope:** a self-conducted review of the Callisto codebase (v0.12.0, ~21k LOC Go)
ahead of inviting beta users. Not a substitute for a professional third-party audit —
it is an internal pass to catch obvious problems, with an emphasis (per PRINCIPLES.md)
on **key-material handling, signing correctness, and untrusted input**.

**Method:** manual review of the security-critical paths (keystore, HD derivation, hot
signer, updater, WalletConnect request handling, SQL, config/secrets), plus
`govulncheck`, `go test -race` on the crypto/key packages, and a sweep for secret
logging, file permissions, and unsanitized untrusted input.

**Headline result:** no critical or high-severity key-exposure or signing-bypass issues
were found. The cryptographic core (keystore, BIP-32/44 derivation, ed25519 update
verification) is implemented carefully and correctly. Findings are hardening items and
one dependency bump. Nothing here should block a **careful** beta (small-value / testnet
first), but the Medium items are worth fixing before it.

---

## What's done well (verified)

- **Keystore** (`internal/keystore`): scrypt N=2¹⁸ + AES-256-GCM, fresh random salt and
  nonce per seal, authenticated encryption (wrong passphrase and tampering both fail as
  an indistinguishable auth-tag error — no plaintext/padding oracle), atomic 0600 writes,
  key zeroed after use.
- **HD derivation** (`internal/signer/hot/derive.go`): BIP-32/44 implemented directly on
  the vendored secp256k1; scalar validity checked at master and each child, correct fixed
  32-byte serialization (`ModNScalar.Bytes()`), parent key wiped as the path descends.
- **Hot signer** (`internal/signer/hot/hot.go`): seed + private key held only while
  unlocked, zeroed on `Lock` and on account switch; no raw-digest signing is reachable
  from external input (personal/typed-data hash their inputs; Safe signs a
  Callisto-computed `safeTxHash`).
- **Updater** (`internal/updater`): downloads are **verified before install** — ed25519
  signature over `SHA256SUMS` (embedded maintainer key, all-zero placeholder rejected),
  then the artifact's SHA-256 is checked against `SHA256SUMS`, and only then is it
  extracted/installed. Zip-slip guarded (`safeJoin`), checksum/sig reads bounded.
- **Input at the boundaries:** addresses are EIP-55 checksum-validated on entry
  (`internal/address`), send amounts are rejected unless `Sign() > 0` at the tx-build
  layer (`internal/tx/build.go`), calldata/quantities decode via `hexutil` with errors.
- **SQL** (`internal/store`, history, approvals, token cache): every query is
  parameterized — no string-built SQL, no injection surface.
- **Secrets hygiene:** no seed/key/passphrase is ever logged or printed; the config file
  holds no key material (only addresses, keystore IDs, endpoint names); keystore and
  config live in a 0700 directory with 0600 files.
- **WalletConnect dispatch:** every request is shown for review before signing; the
  unlocked wallet must match the session account (`dispatch`, confused-deputy guard);
  contract-creation (`to: null`) is rejected rather than nil-dereferenced; the request's
  chain is checked against the connected chain; `eth_sign` is handled as a *prefixed*
  personal-sign, which prevents raw-hash blind-signing.
- **Concurrency:** `go test -race` on `keystore`, `signer/hot`, `walletconnect`, and
  `safe` is clean — no data races on key material in the covered paths.

---

## Findings

| # | Severity | Area | Finding |
|---|----------|------|---------|
| 1 | Medium | Dependencies | `golang.org/x/image@v0.24.0` has 4 known CVEs (govulncheck-reachable); fixed ≥ v0.43.0 |
| 2 | Medium | Untrusted input / display | On-chain token name/symbol are rendered without sanitization (spoofing via homoglyph / bidi / control chars) |
| 3 | Medium | WalletConnect | The tx-request review shows raw calldata, not decoded — users can't spot a malicious `approve` |
| 4 | Low | Secrets | "Reveal private key" copies the key to the system clipboard with no auto-clear |
| 5 | Low | Untrusted input | ENS names are `ToLower/TrimSpace` only — not UTS-46/ENSIP-15 normalized; displayed reverse names unsanitized |
| 6 | Low | Auth | Keystore passphrase soft-minimum is 8 chars, no strength feedback |
| 7 | Low | Updater | No explicit anti-downgrade: a signed *older* release replayed as "latest" would install (needs API MITM/compromise) |
| 8 | Low | Storage | `callisto.db` isn't explicitly `0600` — it relies on the 0700 parent dir |
| 9 | Low | Updater | Update artifact download is unbounded (disk-fill DoS; hash-checked so not code-exec) |
| 10 | Info | Language | Go `string` passphrases can't be zeroed; `zero()` could in theory be optimized away |
| 11 | Info | Crypto | The from-scratch WalletConnect v2 crypto/session engine hasn't had a focused review |
| 12 | Info | Platform | macOS Keychain shim uses the deprecated `kSecUseOperationPrompt` |

### 1 — `golang.org/x/image` known CVEs *(Medium)*

`govulncheck` reports 4 reachable vulnerabilities in `golang.org/x/image@v0.24.0`
(GO-2026-4815/5032/5062/5066), all image-decoder issues, fixed in v0.38–0.43.
**Practical reachability is low** — Callisto only decodes its own embedded PNG/SVG assets
(the about-dialog logo, theme icons); token "logos" come from a *curated* in-code list
and aren't rendered from untrusted URLs. But it's a trivial, high-value fix.
**Fix:** `go get golang.org/x/image@v0.43.0 && go mod tidy` (transitive via Fyne; verify
it still builds), and add `govulncheck` to the release checklist.

### 2 — Unsanitized on-chain strings (display spoofing) *(Medium)*

Token `Name`/`Symbol` come from attacker-controlled ERC-20 metadata and are rendered
directly (`assets_view.go` asset rows, the Send/Safe asset pickers). A malicious token
can use Unicode **homoglyphs** ("USDC" in Cyrillic), **bidi overrides** (U+202E),
**zero-width** joiners, or embedded newlines to impersonate a legitimate asset or break
row layout. This is the classic malicious-token-metadata vector the user flagged.
**Fix:** a small `sanitizeDisplay(s)` helper — strip control/bidi/zero-width runes, coerce
to valid UTF-8, collapse whitespace, cap length — applied wherever an untrusted string
(token name/symbol, ENS name, dApp/peer name, proposal description) is shown. This also
covers finding #5.

### 3 — WalletConnect review shows raw calldata *(Medium)*

`describeRequest` renders `eth_sendTransaction` data as shortened hex, not a decoded
function/arguments view. A malicious dApp can request `approve(attacker, uint256_max)` or
`transfer(attacker, …)` and the user sees only `To: <token>` + opaque hex. Callisto
already has approval/method-decoding infrastructure (`internal/approvals`,
`internal/actions`).
**Fix:** decode at least the common dangerous methods (`approve`, `increaseAllowance`,
`transfer`, `transferFrom`, `setApprovalForAll`, Permit2) in the WC review, and highlight
unlimited approvals — mirroring what the Approvals pane already knows.

### 4 — Private-key reveal via clipboard *(Low)*

`revealPrivateKeySelected` → Copy writes the raw private key to the OS clipboard. It's
gated behind a passphrase re-prompt and a prominent danger warning (good), but the
clipboard is readable by any local process and is often persisted by clipboard managers.
**Fix:** auto-clear the clipboard a short time after copy (e.g. 30–60 s, only if unchanged),
note that it was copied, and consider making the on-screen reveal the primary path.

### 5 — ENS normalization is minimal *(Low)*

`normalize()` is `strings.ToLower(TrimSpace(...))`, not ENSIP-15/UTS-46. Reverse-resolved
names are *ownership*-verified (forward re-resolution) — good — but the displayed string
isn't confusable-checked or sanitized, so a registered homoglyph/bidi name can spoof.
**Fix:** apply the #2 sanitizer to displayed names at minimum; longer-term, adopt an
ENSIP-15 normalization pass for input.

### 6 — Weak passphrase floor *(Low)*

`minKeystorePassphraseLen = 8`, no strength feedback. scrypt N=2¹⁸ makes offline brute
force expensive, but 8 characters is a low floor for the secret guarding a seed.
**Fix:** raise to ~10–12 and/or add a lightweight strength hint at import (a zxcvbn-style
estimate, no new heavy dep required).

### 7 — No explicit anti-downgrade in the updater *(Low)*

`Check` trusts the releases API for the "latest" version/tag; the artifact is signed, but
an attacker able to tamper with the API response (TLS MITM, or a Codeberg compromise)
could **replay a genuinely-signed older release** with a higher tag and downgrade a user
to known-vulnerable code. TLS + the signature make this hard, but it's a gap.
**Fix:** refuse to "update" to a version ≤ current, and/or bind the version into the
signed material and verify the installed binary's `buildinfo.Version` matches.

### 8 — Database file permissions *(Low)*

`callisto.db` (+ WAL/SHM) inherits umask perms (typically 0644). It holds tx history and
the address book — privacy-sensitive, not key material — and sits inside the 0700 config
dir, so it's protected in practice. Defense-in-depth: `chmod 0600` on create so a stray
dir-perm change or a copied-out file doesn't expose it.

### 9 — Unbounded update download *(Low)*

The artifact is streamed with no size cap (checksum/sig reads *are* bounded). A
compromised host could serve a huge file to fill the disk; it can't achieve code-exec
(the hash won't match). **Fix:** wrap the artifact copy in a `LimitReader` with a sane
ceiling.

### 10–12 — Informational

- **Passphrase zeroing (10):** Fyne's `Entry` yields a Go `string`, which is immutable and
  can't be wiped; it lingers until GC. `zero()` on byte secrets is also, per Go, not
  guaranteed against compiler elision. Both are standard Go-wallet limitations — note them,
  don't over-promise "wiped." A custom secure entry is the only full fix (out of scope).
- **From-scratch WC crypto (11):** the WalletConnect v2 relay/envelope/session crypto is
  bespoke (no Go SDK). It only ever transports requests the user must still approve, so the
  blast radius is bounded, but the handshake/key-derivation deserves a focused pass — a good
  target for the eventual professional audit.
- **Deprecated Keychain API (12):** `secretstore_darwin.go` uses `kSecUseOperationPrompt`
  (deprecated since macOS 11). Functional, not a vulnerability; migrate to
  `LAContext.localizedReason` when convenient.

---

## Not covered (flag for the professional audit)

- The from-scratch WalletConnect v2 cryptography (#11) end-to-end.
- The `usbwallet` hardware-wallet fork and device transcript handling (needs hardware).
- The macOS Keychain / Touch ID CGo shim beyond a read-through.
- Fyne itself and the rest of the transitive dependency surface.
- Side-channels (timing/memory) — out of scope for a source review.

## Recommended before beta

1. Bump `golang.org/x/image` (#1) and add `govulncheck` to the release checklist. *(quick)*
2. Add the untrusted-string sanitizer and apply it to token name/symbol, ENS names, and
   dApp/peer/proposal strings (#2, #5). *(small, high-value)*
3. Decode common dangerous methods in the WalletConnect review (#3). *(medium)*
4. Clipboard auto-clear after private-key reveal (#4). *(quick)*
5. The Low items (#6–9) as time permits.

Start beta on **testnets / small values**, and make clear to testers that this is
pre-audit software.
