# `callisto` transaction system

_A lightweight, flexible, and locally-run ethereum transaction preparation and signing utility._

## Design

Callisto MUST be a Go-based GUI transaction preparation system that enables the preparation, signing, and broadcast of ethereum transactions for EOAs, Safe multisignature accounts, and other types of accounts (in the future).

The Callisto GUI must prioritize simple, legible, and functional design, with helpful formatting, layout, and color selection where sensible and helpful for making the system easy to use, and data quick to interpret.

### Requirements

#### Wallet configuration and selection

Callisto MUST be designed to support multiple wallets and enable the easy selection of each. 

Callisto SHOULD have a mechanism to both "add" (or "connect") and "remove" (or "delete") wallets, and maintain a persistent list of connected wallets for easy selection. A wallet SHOULD be able to be selected even if the required signer is not `connected` or `unlocked` so that other features may still be used. 

#### RPC configuration and selection

Callisto MUST support the easy configuration, selection, and memory of different ethereum RPC backends for convenience and redudancy. For example, a user MUST be able to configure and add several different RPCs that they may select under different circumstances (e.g. if one is down, they can select their backup). 

Callisto ships with a default RPC — a Flashbots Protect Ethereum Mainnet endpoint — configured and auto-connecting on first launch, so it works out of the box. Users MUST be able to replace it, disable its auto-connect, or add their own endpoints at any time; the default is a convenience, not a lock-in. (This supersedes the original requirement that Callisto have no default RPC — a deliberate product decision to give users a working kit immediately while preserving full user control.)

Callisto MUST save the RPC configuration so that it does not have to be repeated at each lauch.

Callisto MUST support both a `wss://` and `https://` connection to an external ethereum node over JSONRPC so that the blockchain may be monitored live, and the full JSONRPC feature set is accessible where needed.

#### Automatic asset population

Callisto MUST do basic population and parsing of the assets held by a connected account, and display/update them 

This includes:
- detecting ether and ERC20 tokens
- parsing ERC20 token names, symbols, and decimals
- populating a logo where possible for ERC20 tokens
- correctly dislpaying amounts in human-format based on `decimals`
- ensuring ether, as the native asset, is shown first in the list
- when `chain_id` is not `1` (ethereum mainnet), adapting to the correct native asset for that chain

Callisto MUST be architected in such a way that future token types may be supported in the future as an additional feature (but not requried for an initial implementation).

#### ENS support
Callisto MUST support name resolution with the Ethereum Name System. Anywhere an address is shown or surfaced, Callisto should check and see if there is a reverse record for that address so that a readable ENS may be shown in its place. If an ENS domain is found, the underlying full address should still be visible through an "on-hover" type action.

Where a user is prompted to _enter_ an address, ENS should also be supported. Callisto should clearly show if the ENS name entry resolved correctly, and to what address it resolved to. If the name is invalid or does not resolve, Callisto must clearly show that error state and handle it gracefully (e.g. the input box transfering from <say> light gray to light red).

#### Address validation and display
Callisto must do standard ethereum address checksum validation and display them correctly with the standard mixed case format.

#### Basic transaction preparation

Simple sends of ether and erc20 tokens should have a dedicated and consistent UX model. For example, clicking `send` on the list of assets for both ether and ERC20s initiates the "basic send" UX flow where `to` and `amount` (including a "send all" functionality) are supported for simple asset transfers. 

Callisto MUST ensure that `amount` entires are correctly handled when they are entered in human format, and then converted to base unit format correctly for the actual transaction preparation, based on the assets decimal value (e.g. some tokens use 18 decimals, others use 6, or 8 etc.)

#### Complex transaction preparation

Callisto should support "complex" transactions of many types through an optional Claude-based transaction preparation pipline with multiple steps of validation and review. 

For example, a user should be able to enter into a dedicated transaction preparation box queries of the following sort (and others)

```
deposit 10 ether to AAVE v3

borrow 2000 USDC from AAVE v3

stake 15.75 ether with lido

revoke all approvals for WETH tokens on my account

wrap 22 ether

unwrap 15 ether
```

Some types of complex transactions may initiate several types of external API calls (to the ethereum blockchain or even the broader internet) to support Claude's preparation of a proposed transaction data. 

If the connected/selected account is a Gnosis Safe, Callisto should support complex multi-step transaction preparation. 

```
wrap 10 eth, deposit the full amount to aave v3, borrow 1000 USDC, buy the maximum amount of cbETH, and deposit the resulting full amount to aave v3

withdraw 25 weth from aave v3, sell it to USDC, and use the USDC to repay the maximum possible amonut of outstanding USDC debt, then transfer any leftover USDC to the wallet
```

Claude MUST do it's best to figure out what contracts are necessary to fulfil the transaction request, and keep a persistent "address book" of on-chain contracts, their functions, parameters, and functionality so that a library is constructed over time. It SHOULD use this data to be able to surface to the user in a human-readable format:

1. what contract is being used, what is it called
2. what functions are being called
3. what values are being passsed to the function, etc.

Claude SHOULD carefully validate if the transaction request type is possible to craft using available tools, and then display all the necessary steps after a transaction is crafted, OR gracefully indicate that it could not prepare the transaction. 

For complex multi-step transactions, Callisto MUST use the battle-tested DeFiSaver SDK and contracts (e.g. their "recipie creator") to craft the multi-step transaction.

View that documentation here:
- DeFiSaver contracts: https://github.com/defisaver/defisaver-v3-contracts
- DeFiSaver SDK: https://github.com/defisaver/defisaver-sdk


**THIS FEATURE SHOULD BE IMPLEMENTED AFTER BASIC TRANSACTION SUPPORT, AS A REQUIRED BUT SECONDARY IMPLEMENTATION STEP.**

#### Account types

Callisto MUST support BOTH externally-owned accounts (EOAs) as simple single-wallets, and multisignature wallets (e.g. Safe accounts) where transaction signing and execution is a multi-step process.

- EOAs
  - single signature required for transaction execution

- Safe/multi-signature wallets
  - one or more account (`1` to `M`) owners that must sign individually
  - optional additional role of proposer (without signing ability)
  - specified threshold (e.g. `n` of `M` signatures required for execution)
  - multi-step transaction pipline:
    - preparation and proposal
    - initial signing
    - subsequent signatures
    - transaction execution (once threshold is met)
  - rejection process
    - transaction proposal
    - no signatures provided, or partial threshold met,
    - OR full threshold met but no execution
    - ability for owners to initiate rejection

#### Signer support

Callisto MUST initially support the following signer/wallet types through a common interface enabling future expansion of signer support.

- hot wallets
  - pass in 12-word seed phrase and select a derived account
  - ability to select one of several derived acounts
  - ability to switch between derived accounts on one wallet
  - automatic discard of phrase and private key on close
  - support "disconnect" featuer to manually clear sensitive memory
  - maximum protection of key material while "unlocked

- hardware wallets
  - support for trezor
  - support for ledger
  - support for grid lattice

Additionally, through a separate pane/window, Callisto MUST offer *full* support for Safe multisig operations (including the on-chain addition and removal of owners, and the client-side labeling of owners).

#### Gas management
Callisto MUST intelligently estimat the gas parameters needed for timely execution of the prepared transaction, without over-paying. An external API or calls to the ethereum JSONRPC may be used to esimate gas parameters needed (base fee and tip).

#### Pre-signing transaction review

After a transaction is prepared but before it is signed, Callisto MUST show an overview of the transaction data, and if multi-step, an overview of each step. 

- basic values:
  - `value` of ether in transaction (in human format)
  - `fee` in ether (total maximum fee)
  - `from` address
  - `to` address
  - others?
- transaction summary
  - if applicable, what contract is being called?
    - what is the contract (check directory)
  - what function/functions is/are being called?
  - what parameters are being used?

To the greatest extend possible, contract names, function selectors, and parameters should be parsed and displayed in a human-readable and easy to digest manner in the GUI to enable the human signer to fully review the transaction before it is signed and submitted.

#### Transaction signing

Callisto MUST support signing of the transactions prepared by the system. Signing method MUST be based on the selected and connected wallet/signer. 

For EOAs (hot and hardware) signing is a relatively simple process of ensuring the correct account/derivation path is selected, and using the necessary interfaces for the selected wallet type (e.g. a common interface will allow hot wallets, trezor accounts, ledger accounts, etc. to sign easily).

For Safe multi-signature wallets, Callisto MUST manage the transaction proposal and signature collection process until the threshold is met. For this purpose, it should also support the simple switching of connected signers so that all signers can easily provide their signature. When `signatures_so_far < threshold`, the action prompt will be `sign`, when `signatures_so_far >= threshold`, the action prompts become `sign` and `sign and execute` (which should be self-explanitory).

#### Transaction broadcast

Callisto MUST support the broadcast of a signed transaction payload to the ethereum network for execution and settlement. 

Callisto MUST be able to interpret the response from a node/node RPC it submits transactions to to confirm if a transaction was submitted successfuly, or if there was an error. It MUST surface the transaction hash to the user with a clear indication of:

1. did the node/JSONRPC accept the transaction submission?
1. was there an error?
1. what is the transaction hash?
1. an external web link (e.g. etherscan) to track the transaction

If a signed transaction payload is accepted by the receiving node, Callisto MUST correctly track it's progress and "listen" for inclusion of the transaction in a block. 

Upon inclusion, Callisto MUST automatically and rapidly indicate:

1. that it was included,
1. the execution status (`success` or `failed`),
1. the block it was included in, and
1. the timestamp of the block it was included in

#### Transaction records

Callisto SHOULD maintain a local database of transactions it helped prepare and collect signature(s) for, and provide a separate GUI pane (`history`, for example) for a user to look back at transaction history with rich data (e.g. at minimum: transaction type, preparation instructions, times of prep, sign, and submission, hash, execution status, and a link to view it on a block explorer).

### Expanded features

Callisto SHOULD be designed and implemented in such a manner that additional features (such as new wallet types, new transaction preparation methods, new chain support, and more) are able to be implemented without major re-writes of core components. 

In essense, the DESIGN PHILOSOPHY should include an eye for future extensibility, and easy maintnence while not sacrificing functionality, performance, or security.

During implementation, these features do not need to be implemented initially, but the primary feature set and implementation MUST be done in such a way that feature inclusion is "minimally invasive."

#### Transaction simulation

In the future, Callisto SHOULD support transaction simulation against a blockchain snapshot so that a user may simulate the execution of a prepared transaction and view the state changes it affects. For example, it SHOULD showing the relevant `before` and `after` states in a human-readible manner of the simulated transaction so that a user can confirm the transaction was correctly prepared, and will result in the intended effects.
