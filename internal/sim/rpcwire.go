package sim

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// zeroAddress is the all-zero address, used as a harmless `to` for the
// debug_traceCall capability probe.
var zeroAddress common.Address

// nativeSentinel is the pseudo-token address eth_simulateV1 attributes native
// ETH movements to. With traceTransfers enabled the node reports value transfers
// as ERC-20-shaped Transfer logs from this address, which is the ERC-7528
// "native asset" placeholder (go-ethereum internal/ethapi/logtracer.go).
//
// Beware go-ethereum's own doc comment directly above that constant, which still
// claims the address is 0x0 — it describes an earlier implementation and is
// simply wrong about the current one. Trust the constant, not the comment.
var nativeSentinel = common.HexToAddress("0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE")

// isNativeLogAddress reports whether a Transfer log from this address represents
// native currency rather than an ERC-20.
//
// Both sentinels are accepted: the ERC-7528 address current geth uses, and the
// zero address, which geth's stale comment documents and other clients may still
// emit. Accepting both is safe because neither address holds code on any chain
// Callisto connects to, and only executing code can emit a log — so no genuine
// token transfer can ever be misread as native.
func isNativeLogAddress(a common.Address) bool {
	return a == nativeSentinel || a == zeroAddress
}

// ---------------------------------------------------------------------------
// eth_simulateV1
// ---------------------------------------------------------------------------

// simCallPayload is one call inside a simulated block. Fee fields are pinned to
// zero by the callers so a gas-poor account still simulates its transaction's
// *effect* (docs/transaction-simulation.md, "Gas for asset diffs"); the real
// fee estimate stays with internal/tx.
type simCallPayload struct {
	From                 *common.Address `json:"from,omitempty"`
	To                   *common.Address `json:"to,omitempty"`
	Gas                  *hexutil.Uint64 `json:"gas,omitempty"`
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas,omitempty"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas,omitempty"`
	Value                *hexutil.Big    `json:"value,omitempty"`
	Input                *hexutil.Bytes  `json:"input,omitempty"`
}

// stateOverride is an account's overridden state for a simulation. Only the
// fields Callisto uses are modelled (balance today; storage slots arrive with
// the P3b Safe signature bypass).
type stateOverride struct {
	Balance   *hexutil.Big                `json:"balance,omitempty"`
	Code      *hexutil.Bytes              `json:"code,omitempty"`
	StateDiff map[common.Hash]common.Hash `json:"stateDiff,omitempty"`
}

type simBlockStateCall struct {
	Calls          []simCallPayload                 `json:"calls"`
	StateOverrides map[common.Address]stateOverride `json:"stateOverrides,omitempty"`
}

type simPayload struct {
	BlockStateCalls []simBlockStateCall `json:"blockStateCalls"`
	TraceTransfers  bool                `json:"traceTransfers"`
	Validation      bool                `json:"validation"`
}

type simBlockResult struct {
	Calls []simCallResult `json:"calls"`
}

// simCallResult is one call's outcome inside a simulated block.
//
// Logs deliberately uses the minimal callLog rather than types.Log: geth's
// Log.UnmarshalJSON rejects any object missing transactionHash/blockHash, which
// are meaningless for a transaction that was never mined and which not every
// implementation bothers to synthesize. Only address/topics/data carry meaning
// here anyway.
type simCallResult struct {
	ReturnData hexutil.Bytes  `json:"returnData"`
	Logs       []callLog      `json:"logs"`
	GasUsed    hexutil.Uint64 `json:"gasUsed"`
	Status     hexutil.Uint64 `json:"status"`
	Error      *simCallError  `json:"error"`
}

// simCallError is the per-call failure geth reports inside a simulate result.
// Data is a plain string rather than hexutil.Bytes: it is the ABI-encoded
// revert payload on a revert, but nodes have been seen to put a human message
// there instead, and a strict hex decode would fail the whole unmarshal.
type simCallError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

// ---------------------------------------------------------------------------
// debug_traceCall (callTracer)
// ---------------------------------------------------------------------------

type traceConfig struct {
	Tracer       string            `json:"tracer"`
	TracerConfig *callTracerConfig `json:"tracerConfig,omitempty"`
}

type callTracerConfig struct {
	WithLog bool `json:"withLog"`
}

// callFrame is one node of callTracer's call tree. Logs is populated only when
// the tracer runs with withLog.
type callFrame struct {
	Type         string          `json:"type"`
	From         common.Address  `json:"from"`
	To           *common.Address `json:"to"`
	Value        *hexutil.Big    `json:"value"`
	GasUsed      hexutil.Uint64  `json:"gasUsed"`
	Output       hexutil.Bytes   `json:"output"`
	Error        string          `json:"error"`
	RevertReason string          `json:"revertReason"`
	Calls        []callFrame     `json:"calls"`
	Logs         []callLog       `json:"logs"`
}

type callLog struct {
	Address common.Address `json:"address"`
	Topics  []common.Hash  `json:"topics"`
	Data    hexutil.Bytes  `json:"data"`
}

// toLogs adapts wire logs to the types.Log the decoders in decode.go work on.
func toLogs(in []callLog) []*types.Log {
	out := make([]*types.Log, 0, len(in))
	for _, lg := range in {
		out = append(out, &types.Log{Address: lg.Address, Topics: lg.Topics, Data: lg.Data})
	}
	return out
}

// value returns the frame's ETH value, never nil.
func (f callFrame) value() *big.Int {
	if f.Value == nil {
		return new(big.Int)
	}
	return (*big.Int)(f.Value)
}

// flatten walks the call tree depth-first, collecting the logs of every frame
// that actually took effect and the ETH moved by each such frame.
//
// A frame with Error set reverted, which undoes it *and its whole subtree* --
// state, logs, transfers all roll back -- so those branches are skipped
// entirely. (The root frame is handled by the caller: if the root errored the
// transaction reverted and there are no effects at all.)
func (f callFrame) flatten(logs *[]*types.Log, transfers *[]ethMove) {
	*logs = append(*logs, toLogs(f.Logs)...)
	// DELEGATECALL/STATICCALL/CALLCODE carry a `value` field for display but
	// move no ether -- only CALL and CREATE* actually transfer it.
	if f.To != nil && movesValue(f.Type) {
		if v := f.value(); v.Sign() > 0 {
			*transfers = append(*transfers, ethMove{From: f.From, To: *f.To, Value: v})
		}
	}
	for _, child := range f.Calls {
		if child.Error != "" {
			continue
		}
		child.flatten(logs, transfers)
	}
}

func movesValue(frameType string) bool {
	switch frameType {
	case "CALL", "CREATE", "CREATE2", "SELFDESTRUCT":
		return true
	default:
		return false
	}
}

// ethMove is a single native-ETH transfer recovered from a trace.
type ethMove struct {
	From  common.Address
	To    common.Address
	Value *big.Int
}
