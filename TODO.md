# To-do items for `callisto`

## bugs

- ~~trezor not connected. when plugged in and unlocked, callisto seems not to detect it~~
  - ✅ **Resolved and verified live** against a real Trezor Safe 5 (multiple
    successful runs: standard-wallet address derivation, 3-account listing,
    passphrase/hidden-wallet flow, on-device passphrase entry).
  - **Root cause 1 — detection.** `go run ./cmd/hwscan` (new diagnostic tool;
    lists every raw USB HID device the OS sees, regardless of whether Callisto
    recognizes it) showed the device as `VID=0x1209 PID=0x53c1 iface=1
    usagePage=0xf1d0 "Trezor Company" "Trezor Safe 5"`. go-ethereum's
    `usbwallet` matcher requires vendor+product ID **and** (usagePage==0xffff
    **or** interface==0) — this device satisfies neither, so it was silently
    filtered out even though the OS exposed it fine. Known, still-open upstream
    bug: [go-ethereum#31841](https://github.com/ethereum/go-ethereum/issues/31841)
    (the proposed fix, [PR #32752](https://github.com/ethereum/go-ethereum/pull/32752),
    is a draft blocked on an unrelated, unmerged dependency swap).
  - **Root cause 2 — even when matched, direct USB doesn't work.** For this
    device, the OS's HID layer can enumerate *something* at that VID/PID, but
    writes to it are accepted while reads never return data — direct USB
    access simply cannot reach the device's real wallet-protocol endpoint on
    this platform. Confirmed by timing out at exactly the (new) 60s read-timeout
    bound rather than hanging forever.
  - **Fix — three parts**, all in a local fork of three go-ethereum files
    (`hub.go`, `wallet.go`, `trezor.go`; LGPL-3.0, license/attribution
    preserved) at `internal/signer/hardware/usbwallet/` (see that package's doc
    comments for full rationale):
    1. Trezor's hub matcher now matches on vendor+product ID alone (safe for
       Trezor specifically — unlike Ledger's PID, which encodes model+interface
       bits and genuinely needs the interface check; Ledger is untouched and
       still uses upstream go-ethereum directly).
    2. Added a **Trezor Bridge (trezord) HTTP transport** as the primary path
       for Trezor (tried before direct USB, which is kept as a fallback for
       older devices) — Bridge already solves USB access correctly on every
       OS, so this sidesteps the direct-USB dead end entirely rather than
       chasing it further. Includes port discovery (trezord's own port isn't
       fixed — observed shifting from 21325 to 21328 across a single Suite
       relaunch on this machine), and self-healing session acquisition
       (recovers from a stale "wrong previous session" without requiring the
       user to replug).
    3. Added bounded read timeouts on **both** transports (60s) — previously a
       non-responding device hung `Open()` forever with the UI stuck on
       "Connecting…" and no way to cancel.
  - **Passphrase / hidden wallets** — folded into this fix once Bridge
    communication was proven live (see "trezor wallet types" below, now
    resolved): standard wallet (empty passphrase) and host-supplied hidden-wallet
    passphrases both verified to derive distinct, correct addresses; **on-device
    passphrase entry** (`PassphraseRequest.OnDevice`) is also detected and
    respected — when the device's own "enter passphrase on Trezor" security
    setting is active, Callisto no longer sends a possibly-ignored host string;
    it waits for the user to type on the device screen instead.
  - ⚠️ **Known gap:** a newer Trezor Suite build (embedded bridge v3.2.1+,
    observed after a Suite relaunch, on a non-default port) speaks an
    undocumented protocol variant beyond the classic trezord-go API followed
    here (rejects our `/call` body with an internal, non-public error). Not
    pursued further — no public documentation to work from, and the standard
    protocol (v3.1.0, the standalone Trezor Bridge, and older/typical Suite
    installs) is fully verified working. Revisit if this becomes common.

## minor
- ~~clarify: when connected to an RPC, there's a little gray dot. is that supposed to be green?~~
  - Resolved: the dot is now color-coded by connection state:
    - **green ●** — connected to a live endpoint
    - **amber ●** — an endpoint is selected but not currently connected
    - **gray ●** — no endpoint configured/selected
  - (Previously it was a theme-default gray `●`/`○` regardless of state — the fix
    gives it real color. The wallet label also now shows locked/unlocked.)

### "green dot emoji" indicator next to active wallet — ✅ done
- ~~the green circle emoji looks a bit out of place... personally not a fan of emoji's in UIs~~
  - Wallets list now uses a genuinely colored `●` glyph (canvas.Text, same
    green/gray pattern as the connection status dot) instead of the 🟢 emoji.
    Also dropped the 🔒/🔓 lock emojis in the same row for plain `[locked]` /
    `[unlocked]` text, consistent with the "not really an emoji person" note.

### out of the box default rpc
- lets actually ship callisto with a default mainnet endpoint 
- lets use: `https://rpc.flashbots.net/fast?originId=callisto-system`, labeled `Ethereum Mainnet (via flashbots protect)`
- this way, users have a working kit out of the box, but can still replace it with a different rpc if they like

### default rpc — ✅ done
- ~~option to select an rpc as default when adding, if checked, auto-connect on start~~
  - Add-endpoint dialog now has an "Auto-connect on startup (default endpoint)"
    checkbox; the default is exclusive (marked ⭐ in the list) and auto-connects
    when the app launches.

### font — ✅ done
- ~~lets use `BerkeleyMono` for the font for addresses and numerical values~~
  - Berkeley Mono is now embedded (go:embed, under the project's indie license)
    and applied via a custom Fyne theme to monospace-tagged display text:
    Assets/Wallets/History rows, the pre-sign review values, and resolved-address
    status. A user override is supported via `CALLISTO_FONT_DIR`.
  - Fonts organized into `internal/ui/fonts/` (Regular + Bold embedded; the
    oblique/light variants are kept there for future use).

### decimal trimming — ✅ done
- ~~lets show 5 decimals of token amounts, no need for anything beyond that~~
  - Display truncates to 5 decimals (Assets list + Send picker); exact values are
    still used for signing.

### hide zero (and near) token amounts — ✅ done
- ~~on the asset page, lets hide tokens that have zero or dust balance~~
  - Assets page hides non-native tokens below 0.00005 (default), showing a
    "N dust hidden" note. Native asset is always shown. (Send lists everything.)

### indicator next to active wallet — ✅ done
- ~~currently gray, maybe should be green~~ → active wallet now marked with 🟢.

## medium

### trezor wallet types — ✅ done (see "bugs" above for the full story)
- ~~trezor is kind of weird... "unlock with pin (on device) -> enter passphrase
  (on computer) -> THEN select derivation index"~~
  - Confirmed and implemented, folded into the Trezor detection fix above since
    it surfaced during that live testing. Add-hardware and unlock dialogs now
    have a Passphrase field (Trezor only); empty = standard wallet, non-empty =
    a distinct hidden wallet — both verified live to derive different, correct
    addresses. On-device passphrase entry (a separate device security setting)
    is also handled correctly: when active, the host-supplied field is ignored
    and Callisto waits for entry on the device's own screen instead.

## major

### researching multi-step transactions

the original design document specifies usage of the existing defisaver v3 contracts for multi-step transactions. 

please research any alternatives. this was specified because i've used them extensively and they work well. 

if there are alternatives to consider, we should when we get there.
