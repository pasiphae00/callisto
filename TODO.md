# To-do items for `callisto`

## bugs

- trezor not connected. when plugged in and unlocked, callisto seems not to detect it
  - ✅ root cause confirmed and fixed. Added `go run ./cmd/hwscan` (lists every raw
    USB HID device the OS sees, regardless of whether Callisto recognizes it) —
    running it with the Trezor Safe 5 connected showed:
    `VID=0x1209 PID=0x53c1 iface=1 usagePage=0xf1d0 "Trezor Company" "Trezor Safe 5"`.
    go-ethereum's `usbwallet` matcher requires vendor+product ID **and**
    (usagePage==0xffff **or** interface==0) — this device's interface(1)/usagePage
    (0xf1d0) satisfy neither, so it was silently filtered out even though the OS
    exposed it fine. This is a known, still-open upstream bug:
    [go-ethereum#31841](https://github.com/ethereum/go-ethereum/issues/31841); the
    proposed fix ([PR #32752](https://github.com/ethereum/go-ethereum/pull/32752))
    is a draft blocked on an unrelated, unmerged dependency swap, so no upstream
    release fixes this yet.
  - Fix: forked the three files this touches (`hub.go`, `wallet.go`, `trezor.go`,
    LGPL-3.0, license/attribution preserved) into
    `internal/signer/hardware/usbwallet/`, patched so Trezor matches on vendor+
    product ID alone (Trezor's PID is already unambiguous, unlike Ledger's, which
    packs model+interface bits and genuinely needs the interface check — so Ledger
    keeps using upstream unmodified). Full rationale is in that package's doc
    comment. Drop this fork once upstream ships a real fix.
  - ⏳ **Not yet re-verified end-to-end**: the device dropped off the USB
    enumeration entirely between two scans in the same session (confirmed
    unplugged, not a bug). Please reconnect + unlock and retest — Wallets →
    "Add hardware…" → Trezor.

## minor
- ~~clarify: when connected to an RPC, there's a little gray dot. is that supposed to be green?~~
  - Resolved: the dot is now color-coded by connection state:
    - **green ●** — connected to a live endpoint
    - **amber ●** — an endpoint is selected but not currently connected
    - **gray ●** — no endpoint configured/selected
  - (Previously it was a theme-default gray `●`/`○` regardless of state — the fix
    gives it real color. The wallet label also now shows locked/unlocked.)

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

### trezor wallet types
- trezor is kind of weird. there's a "passphrase wallet" type, where you enter a passphrase after unlocking the device with a pin
- i believe each passphrase lets you derive different wallet
- so its not always just "unlock -> wallet derivation index"
- its "unlock with pin (on device) -> enter passphrase (on computer) -> THEN select derivation index"
- research this, then lets implement it

## major

### researching multi-step transactions

the original design document specifies usage of the existing defisaver v3 contracts for multi-step transactions. 

please research any alternatives. this was specified because i've used them extensively and they work well. 

if there are alternatives to consider, we should when we get there.
