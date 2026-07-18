# To-do items for `callisto`

## bugs

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

### font — ⏳ pending licensing decision
- lets use `BerkeleyMono` for the font for addresses and numerical values (e.g. token amounts) in the GUI
- please organize the font's i uploaded to the main directory to where they should be
  - NOTE: BerkeleyMono is a commercial, non-redistributable font. The mechanism
    (custom Fyne theme applying it to monospace-tagged text) is ready to wire up,
    but committing the .otf files to a public repo may violate the license —
    awaiting a decision on how to handle the font files (embed+commit vs
    gitignore+build-tag vs runtime path).

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
