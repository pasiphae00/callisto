package sim

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/pasiphae00/callisto/internal/rpc"
)

// noteCallOnly is shown when the endpoint can only answer "would this revert?".
const noteCallOnly = "Revert check only — connect an eth_simulateV1 or archive (debug) endpoint to preview asset changes."

// noteEndpointCannotSimulate is shown when even eth_call is refused, so the UI
// says why there is no verdict instead of implying the transaction is fine.
const noteEndpointCannotSimulate = "This endpoint does not serve eth_call, so Callisto cannot check this transaction. Switch to a general-purpose RPC to simulate it."

// simulateGasCap is the gas budget handed to a simulation. Simulations run
// with zero fees and a generous limit so that an account which cannot afford
// the real transaction still gets to see what the transaction would *do*; the
// user-facing fee estimate is produced separately by internal/tx.
//
// Deliberately *not* sent to eth_simulateV1: that method sums the gas of every
// call in the block and rejects the request outright (-38015) when the total
// exceeds the chain's block gas limit, which on Optimism (40M) is below this
// cap. Omitting the field lets each node apply its own block limit, which is
// both chain-correct and always at least as generous as a number we'd pick.
const simulateGasCap = 50_000_000

// Simulator runs pre-sign simulations against one RPC connection, reusing a
// cached capability probe across calls. Build a new one whenever the connection
// is replaced; it is safe for concurrent use.
type Simulator struct {
	client rpc.Client
	prober *Prober
	metaCache
}

// NewSimulator returns a Simulator bound to client.
func NewSimulator(client rpc.Client) *Simulator {
	return &Simulator{client: client, prober: NewProber(client)}
}

// Caps reports what the connected endpoint can do, probing on first use. The UI
// calls this to decide whether to offer an asset-change preview at all.
func (s *Simulator) Caps(ctx context.Context) Caps { return s.prober.Caps(ctx) }

// SimulateEOA simulates req as sent from req.From against current chain state
// and reports the resulting asset changes, or the revert that would occur.
//
// Strategy, best first, falling through when a method doesn't work out:
// eth_simulateV1 (standardized, returns logs *and* native transfers), then
// debug_traceCall with callTracer (archive nodes), then a bare eth_call, which
// can only distinguish success from revert.
//
// Fall-through is unconditional: *any* failure of a richer method drops to the
// next one, not just an outright "method not found". An endpoint can serve a
// method and still reject our particular request -- zkSync Era's callTracer
// refuses a tracerConfig geth accepts, and eth_simulateV1 rejects a gas budget
// above the chain's block limit. Losing the universal revert check because a
// richer method misbehaved is the wrong trade: a downgraded preview is a
// nuisance, an unanswered "would this revert?" is a hazard. The reason the
// richer method failed is kept and surfaced on the downgraded result, so a
// broken endpoint is visible rather than silently degrading forever.
//
// An error is returned only when no strategy could be run at all; a transaction
// that *would revert* is a successful simulation reporting StatusRevert.
func (s *Simulator) SimulateEOA(ctx context.Context, req Request) (Result, error) {
	if s.client == nil {
		return Result{Status: StatusUnavailable}, errors.New("sim: no RPC connection")
	}

	caps := s.prober.Caps(ctx)
	raw := s.client.RawClient()
	var downgrade error

	if caps.SimulateV1 && raw != nil {
		res, err := s.viaSimulateV1(ctx, raw, req, nil)
		if err == nil {
			return res, nil
		}
		downgrade = fmt.Errorf("eth_simulateV1: %w", err)
	}

	if caps.DebugTrace && raw != nil {
		res, err := s.viaDebugTrace(ctx, raw, req)
		if err == nil {
			return res, nil
		}
		downgrade = fmt.Errorf("debug_traceCall: %w", err)
	}

	res, err := s.viaCall(ctx, req)
	if err == nil && downgrade != nil {
		res.Note = noteDowngraded(downgrade)
	}
	return res, err
}

// noteDowngraded explains a preview that fell back to the revert check because a
// richer method failed, naming the reason so the endpoint's problem is visible.
func noteDowngraded(err error) string {
	return "Revert check only — this endpoint's asset preview failed (" + err.Error() + ")."
}

// RevertCheckEOA answers only "would this revert, and why?", using the
// universal eth_call path regardless of what the endpoint is capable of.
//
// This is the guardrail every review dialog runs automatically on open: one
// cheap call that works on any RPC. The richer SimulateEOA stays behind an
// explicit action, so opening a review never silently spends a capable
// endpoint's budget on work the user didn't ask for.
func (s *Simulator) RevertCheckEOA(ctx context.Context, req Request) (Result, error) {
	if s.client == nil {
		return Result{Status: StatusUnavailable}, errors.New("sim: no RPC connection")
	}
	return s.viaCall(ctx, req)
}

// RevertCheckSafe is RevertCheckEOA's Safe counterpart: simulateAndRevert
// through the SimulateTxAccessor, which needs no signatures and works on any
// RPC for both Call and DelegateCall.
func (s *Simulator) RevertCheckSafe(ctx context.Context, req SafeRequest) (Result, error) {
	if s.client == nil {
		return Result{Status: StatusUnavailable}, errors.New("sim: no RPC connection")
	}
	return s.viaAccessor(ctx, req)
}

// ---------------------------------------------------------------------------
// Tier 2: eth_simulateV1
// ---------------------------------------------------------------------------

// viaSimulateV1 simulates one call with traceTransfers, which reports native
// ETH movements as pseudo-logs alongside the real event logs, so a single round
// trip yields the complete asset picture. overrides may be nil.
func (s *Simulator) viaSimulateV1(ctx context.Context, raw *gethrpc.Client, req Request, overrides map[common.Address]stateOverride) (Result, error) {
	// Gas is left unset on purpose -- see simulateGasCap's doc comment.
	call := simCallPayload{
		From:                 addrPtr(req.From),
		To:                   addrPtr(req.To),
		MaxFeePerGas:         zeroFee(),
		MaxPriorityFeePerGas: zeroFee(),
		Value:                bigPtr(req.Value),
	}
	if len(req.Data) > 0 {
		input := hexutil.Bytes(req.Data)
		call.Input = &input
	}

	payload := simPayload{
		BlockStateCalls: []simBlockStateCall{{
			Calls:          []simCallPayload{call},
			StateOverrides: overrides,
		}},
		TraceTransfers: true,
		// validation:false skips the block-level sender checks (nonce,
		// affordability) that would reject a simulation we deliberately run
		// with zero fees.
		Validation: false,
	}

	var blocks []simBlockResult
	if err := raw.CallContext(ctx, &blocks, "eth_simulateV1", payload, "latest"); err != nil {
		return Result{}, err
	}
	if len(blocks) == 0 || len(blocks[0].Calls) == 0 {
		return Result{}, errors.New("eth_simulateV1: empty result")
	}

	call0 := blocks[0].Calls[0]
	res := Result{Tier: TierSimulate, GasUsed: uint64(call0.GasUsed)}

	if call0.Status == 0 {
		res.Status = StatusRevert
		res.RevertReason = revertReasonOf(call0)
		return res, nil
	}

	res.Status = StatusOK
	ch := aggregate(toLogs(call0.Logs), req.From)
	res.ETHDelta, res.Tokens, res.Approvals = ch.ETH, ch.Tokens, ch.Approvals
	return res, nil
}

// revertReasonOf prefers the structured error payload geth attaches to a failed
// simulated call, falling back to its returnData (some nodes populate one and
// not the other) and finally to the plain error message.
func revertReasonOf(c simCallResult) string {
	if c.Error != nil {
		if reason := decodeRevert(hexBytes(c.Error.Data)); reason != "" {
			return reason
		}
	}
	if reason := decodeRevert(c.ReturnData); reason != "" {
		return reason
	}
	if c.Error != nil {
		return c.Error.Message
	}
	return ""
}

// ---------------------------------------------------------------------------
// Tier 3: debug_traceCall
// ---------------------------------------------------------------------------

// viaDebugTrace simulates via callTracer with logs enabled. Unlike
// eth_simulateV1 the tracer has no traceTransfers mode, so native ETH movement
// is recovered by walking the call tree's value fields (see callFrame.flatten).
func (s *Simulator) viaDebugTrace(ctx context.Context, raw *gethrpc.Client, req Request) (Result, error) {
	args := map[string]interface{}{
		"from": req.From,
		"to":   req.To,
		"gas":  hexutil.Uint64(simulateGasCap),
		// Zero gas price for the same reason as eth_simulateV1's
		// validation:false -- and it sidesteps the reth prestate quirk noted in
		// docs/transaction-simulation.md.
		"gasPrice": (*hexutil.Big)(new(big.Int)),
		"value":    (*hexutil.Big)(orZero(req.Value)),
	}
	if len(req.Data) > 0 {
		args["input"] = hexutil.Bytes(req.Data)
	}

	var frame callFrame
	err := raw.CallContext(ctx, &frame, "debug_traceCall", args, "latest", traceConfig{
		Tracer:       "callTracer",
		TracerConfig: &callTracerConfig{WithLog: true},
	})
	if err != nil {
		return Result{}, err
	}

	res := Result{Tier: TierDebug, GasUsed: uint64(frame.GasUsed)}
	if frame.Error != "" {
		res.Status = StatusRevert
		res.RevertReason = traceRevertReason(frame)
		return res, nil
	}

	var logs []*types.Log
	var moves []ethMove
	frame.flatten(&logs, &moves)

	res.Status = StatusOK
	ch := aggregate(logs, req.From)
	applyETHMoves(&ch, moves, req.From)
	res.ETHDelta, res.Tokens, res.Approvals = ch.ETH, ch.Tokens, ch.Approvals
	return res, nil
}

// traceRevertReason reads the reason off a failed root frame. callTracer
// already decodes Error(string) into revertReason on recent geth; where it
// doesn't, the raw payload is in output.
func traceRevertReason(frame callFrame) string {
	if frame.RevertReason != "" {
		return frame.RevertReason
	}
	if reason := decodeRevert(frame.Output); reason != "" {
		return reason
	}
	return frame.Error
}

// ---------------------------------------------------------------------------
// Tier 1: eth_call
// ---------------------------------------------------------------------------

// viaCall is the universal fallback: any endpoint can answer whether a call
// reverts, but not what it would change. Its Result carries noteCallOnly so the
// UI can explain the missing preview rather than implying nothing happens.
func (s *Simulator) viaCall(ctx context.Context, req Request) (Result, error) {
	msg := ethereum.CallMsg{
		From:     req.From,
		To:       &req.To,
		Gas:      simulateGasCap,
		GasPrice: new(big.Int),
		Value:    orZero(req.Value),
		Data:     req.Data,
	}

	_, err := s.client.CallContract(ctx, msg, nil)
	if err == nil {
		return Result{Status: StatusOK, Tier: TierCallOnly, Note: noteCallOnly}, nil
	}

	if reason, reverted := revertOf(err); reverted {
		return Result{
			Status:       StatusRevert,
			RevertReason: reason,
			Tier:         TierCallOnly,
			Note:         noteCallOnly,
		}, nil
	}
	// Some endpoints don't serve eth_call at all -- Flashbots Protect, the
	// mainnet failover target, whitelists only the methods needed to submit a
	// transaction and answers -32601 "rpc method is not whitelisted". That is a
	// property of the endpoint, not a fault to report as an error: say plainly
	// that this RPC can't answer, so the UI shows the reason rather than an
	// error dialog on every review.
	if methodUnavailable(err) {
		return Result{
			Status: StatusUnavailable,
			Tier:   TierCallOnly,
			Note:   noteEndpointCannotSimulate,
		}, nil
	}
	return Result{Status: StatusUnavailable, Tier: TierCallOnly}, fmt.Errorf("eth_call: %w", err)
}

// revertOf decides whether an eth_call error means "the transaction reverts"
// (a simulation result to show the user) as opposed to "the node could not
// answer" (an error to report as such). A decodable revert payload settles it;
// otherwise fall back to the node's own wording, since a bare `revert()`
// carries no payload at all.
func revertOf(err error) (reason string, reverted bool) {
	if data := revertDataFrom(err); len(data) > 0 {
		if decoded := decodeRevert(data); decoded != "" {
			return decoded, true
		}
		return "", true
	}
	if containsAny(err.Error(), "execution reverted", "execution aborted", "invalid opcode", "out of gas") {
		return "", true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func addrPtr(a common.Address) *common.Address { return &a }

func gasPtr(v uint64) *hexutil.Uint64 { h := hexutil.Uint64(v); return &h }

func zeroFee() *hexutil.Big { return (*hexutil.Big)(new(big.Int)) }

func bigPtr(v *big.Int) *hexutil.Big { return (*hexutil.Big)(orZero(v)) }

func orZero(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return v
}

func containsAny(s string, needles ...string) bool {
	s = strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
