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

### default rpc
- option to select an rpc as default when adding, if checked, auto-connect on start

### font
- lets use `BerkelyMono` for the font for addresses and numerical values (e.g. token amounts) in the GUI
- please organize the font's i uploaded to the main directory to where they should be

### decimal trimming
- lets show 5 decimals of token amounts, no need for anything beyond that

### hide zero (and near) token amounts
- on the asset page, lets hide tokens that have zero or dust balance
- dust can be configurable maybe, but lets use a sensible default (say `0.00005`, even for BTC thats only $3)

### indicator next to active wallet
- currently gray, maybe should be green

## medium

## major

### researching multi-step transactions

the original design document specifies usage of the existing defisaver v3 contracts for multi-step transactions. 

please research any alternatives. this was specified because i've used them extensively and they work well. 

if there are alternatives to consider, we should when we get there.
