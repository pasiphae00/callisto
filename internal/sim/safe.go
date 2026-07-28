package sim

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Safe operation values, mirroring safe.Operation (Enum.Operation on-chain).
// Redeclared rather than imported so this package stays free of a dependency on
// internal/safe -- the values are part of the Safe contract ABI and cannot drift.
const (
	OperationCall         uint8 = 0
	OperationDelegateCall uint8 = 1
)

// Notes explaining what a Safe simulation could and could not determine.
const (
	noteSafeDelegateCall = "Revert check only — asset preview for a DelegateCall/MultiSend batch needs signature-bypass simulation, which is not implemented yet."
	noteSafeNoAccessor   = "Simulation unavailable — this Safe's version has no SimulateTxAccessor deployment on this chain."
	noteSafeNoRevertData = "Simulation unavailable — this endpoint did not return revert data, which the Safe simulation needs."
)

// SafeRequest describes a Safe transaction to simulate. It is the SafeTx fields
// that affect execution, plus the context needed to locate the right helper
// contract.
type SafeRequest struct {
	Safe    common.Address
	Version string // the Safe's VERSION(); selects the accessor deployment
	ChainID uint64

	To        common.Address
	Value     *big.Int
	Data      []byte
	Operation uint8 // OperationCall or OperationDelegateCall
}

// SimulateTxAccessor deployments, taken from the safe-global/safe-deployments
// raw asset JSON (src/assets/<version>/simulate_tx_accessor.json), not from any
// secondary source. The canonical (CREATE2) address is deployed on every chain
// in config.ChainCatalog for both versions; zkSync Era needs its own because
// its CREATE2 derivation differs, and safe-deployments lists that deployment
// first for chain 324.
var (
	accessor130       = common.HexToAddress("0x59AD6735bCd8152B84860Cb256dD9e96b85F69Da")
	accessor130ZkSync = common.HexToAddress("0x4191E2e12E8BC5002424CE0c51f9947b02675a44")
	accessor141       = common.HexToAddress("0x3d4BA2E0884aa488718476ca2FB8Efc291A46199")
	accessor141ZkSync = common.HexToAddress("0xdd35026932273768A3e31F4efF7313B5B7A7199d")
	zkSyncEraChainID  = uint64(324)
)

// accessorFor resolves the SimulateTxAccessor for a Safe version and chain.
//
// The accessor (and the simulateAndRevert entry point it is invoked through)
// arrived with Safe 1.3.0, so older Safes have no simulation path at all.
// Version strings carry suffixes on the L2 singletons ("1.3.0+L2"), hence
// prefix matching rather than equality.
func accessorFor(version string, chainID uint64) (common.Address, bool) {
	v := strings.TrimSpace(version)
	switch {
	case strings.HasPrefix(v, "1.4"):
		if chainID == zkSyncEraChainID {
			return accessor141ZkSync, true
		}
		return accessor141, true
	case strings.HasPrefix(v, "1.3"):
		if chainID == zkSyncEraChainID {
			return accessor130ZkSync, true
		}
		return accessor130, true
	default:
		return common.Address{}, false
	}
}

var safeSimABI = mustSimABI(`[
  {"name":"simulateAndRevert","type":"function","stateMutability":"nonpayable","inputs":[
    {"name":"targetContract","type":"address"},{"name":"calldataPayload","type":"bytes"}],"outputs":[]},
  {"name":"simulate","type":"function","stateMutability":"nonpayable","inputs":[
    {"name":"to","type":"address"},{"name":"value","type":"uint256"},
    {"name":"data","type":"bytes"},{"name":"operation","type":"uint8"}],
   "outputs":[{"name":"estimate","type":"uint256"},{"name":"success","type":"bool"},
    {"name":"returnData","type":"bytes"}]}
]`)

func mustSimABI(s string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("sim: bad built-in ABI: " + err.Error())
	}
	return a
}

// SimulateSafe simulates the *effect of executing* a Safe transaction, without
// signatures and regardless of whether the threshold has been met.
//
// Two paths, because no single one covers both goals:
//
//   - Operation == Call on a capable endpoint: run the inner call directly as
//     the Safe (from: safe). msg.sender is then exactly what execTransaction
//     would produce, and one round trip yields both the revert check and the
//     asset deltas.
//   - Otherwise: Safe's own simulateAndRevert + SimulateTxAccessor, which
//     delegatecalls into the Safe's context so both Call and DelegateCall run
//     faithfully, then reverts with the encoded outcome. Works on any RPC, but
//     because it reverts by design the state changes are rolled back, so it can
//     only report success/revert -- never asset deltas.
func (s *Simulator) SimulateSafe(ctx context.Context, req SafeRequest) (Result, error) {
	if s.client == nil {
		return Result{Status: StatusUnavailable}, errors.New("sim: no RPC connection")
	}

	if req.Operation == OperationCall && s.prober.Caps(ctx).Rich() {
		res, err := s.SimulateEOA(ctx, Request{
			From:  req.Safe,
			To:    req.To,
			Value: req.Value,
			Data:  req.Data,
		})
		if err == nil && res.Status != StatusUnavailable {
			return res, nil
		}
		// Fall through: the accessor still gives a revert check.
	}

	return s.viaAccessor(ctx, req)
}

// viaAccessor runs the universal Safe revert check.
func (s *Simulator) viaAccessor(ctx context.Context, req SafeRequest) (Result, error) {
	accessor, ok := accessorFor(req.Version, req.ChainID)
	if !ok {
		return Result{Status: StatusUnavailable, Tier: TierCallOnly, Note: noteSafeNoAccessor}, nil
	}

	inner, err := safeSimABI.Pack("simulate", req.To, orZero(req.Value), req.Data, req.Operation)
	if err != nil {
		return Result{Status: StatusUnavailable}, fmt.Errorf("pack simulate: %w", err)
	}
	outer, err := safeSimABI.Pack("simulateAndRevert", accessor, inner)
	if err != nil {
		return Result{Status: StatusUnavailable}, fmt.Errorf("pack simulateAndRevert: %w", err)
	}

	// simulateAndRevert always reverts; the outcome is the revert payload, so a
	// nil error here means the Safe did not behave as expected.
	_, callErr := s.client.CallContract(ctx, ethereum.CallMsg{
		To:       &req.Safe,
		Gas:      simulateGasCap,
		GasPrice: new(big.Int),
		Data:     outer,
	}, nil)
	if callErr == nil {
		return Result{Status: StatusUnavailable, Tier: TierCallOnly,
			Note: "Simulation unavailable — simulateAndRevert returned instead of reverting; this may not be a Safe 1.3.0+."}, nil
	}

	payload := revertDataFrom(callErr)
	if len(payload) == 0 {
		return Result{Status: StatusUnavailable, Tier: TierCallOnly, Note: noteSafeNoRevertData}, nil
	}

	gasUsed, innerOK, returnData, perr := parseSimulateAndRevert(payload)
	if perr != nil {
		return Result{Status: StatusUnavailable, Tier: TierCallOnly, Note: noteSafeNoAccessor}, nil
	}

	res := Result{Tier: TierCallOnly, GasUsed: gasUsed}
	if !innerOK {
		res.Status = StatusRevert
		res.RevertReason = decodeRevert(returnData)
		return res, nil
	}

	res.Status = StatusOK
	if req.Operation == OperationDelegateCall {
		res.Note = noteSafeDelegateCall
	} else {
		res.Note = noteCallOnly
	}
	return res, nil
}

var accessorResultArgs = abi.Arguments{
	{Type: mustType("uint256")}, // estimate
	{Type: mustType("bool")},    // success
	{Type: mustType("bytes")},   // returnData
}

// parseSimulateAndRevert decodes the payload simulateAndRevert reverts with.
//
// The Safe's assembly writes three things back to back: the boolean result of
// the delegatecall into the accessor, the length of what the accessor returned,
// and then that return data itself:
//
//	[0x00..0x20)  outer delegatecall success
//	[0x20..0x40)  returndatasize
//	[0x40..)      the accessor's ABI-encoded (estimate, success, returnData)
//
// A delegatecall to an address holding no code *succeeds* with zero return
// data, so an empty tail is exactly how "the accessor isn't deployed here"
// presents -- hence the explicit length check rather than trusting the flag.
func parseSimulateAndRevert(payload []byte) (gasUsed uint64, success bool, returnData []byte, err error) {
	if len(payload) < 64 {
		return 0, false, nil, fmt.Errorf("simulateAndRevert: payload too short (%d bytes)", len(payload))
	}
	if new(big.Int).SetBytes(payload[:32]).Sign() == 0 {
		return 0, false, nil, errors.New("simulateAndRevert: delegatecall into the accessor failed")
	}

	size := new(big.Int).SetBytes(payload[32:64])
	if !size.IsUint64() || size.Uint64() == 0 {
		return 0, false, nil, errors.New("simulateAndRevert: accessor returned no data")
	}
	end := 64 + size.Uint64()
	if uint64(len(payload)) < end {
		return 0, false, nil, errors.New("simulateAndRevert: truncated accessor return data")
	}

	vals, uerr := accessorResultArgs.Unpack(payload[64:end])
	if uerr != nil || len(vals) < 3 {
		return 0, false, nil, fmt.Errorf("simulateAndRevert: decode accessor result: %w", uerr)
	}
	estimate, _ := vals[0].(*big.Int)
	success, _ = vals[1].(bool)
	returnData, _ = vals[2].([]byte)

	if estimate != nil && estimate.IsUint64() {
		gasUsed = estimate.Uint64()
	}
	return gasUsed, success, returnData, nil
}
