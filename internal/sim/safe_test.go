package sim

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestAccessorForResolvesVersionAndChain(t *testing.T) {
	// Addresses come from safe-deployments' raw simulate_tx_accessor.json; a
	// wrong one here would silently delegatecall into a codeless address, which
	// *succeeds* and returns nothing -- so pin them.
	tests := []struct {
		version string
		chainID uint64
		want    common.Address
		ok      bool
	}{
		{"1.3.0", 1, accessor130, true},
		{"1.3.0+L2", 8453, accessor130, true},
		{"1.4.1", 1, accessor141, true},
		{"1.4.1+L2", 42161, accessor141, true},
		{"1.3.0", 324, accessor130ZkSync, true},
		{"1.4.1", 324, accessor141ZkSync, true},
		// simulateAndRevert and the accessor both arrived in 1.3.0.
		{"1.1.1", 1, common.Address{}, false},
		{"", 1, common.Address{}, false},
	}

	for _, tc := range tests {
		got, ok := accessorFor(tc.version, tc.chainID)
		if ok != tc.ok || got != tc.want {
			t.Errorf("accessorFor(%q, %d) = %s, %v; want %s, %v",
				tc.version, tc.chainID, got.Hex(), ok, tc.want.Hex(), tc.ok)
		}
	}
}

func TestAccessorAddressesMatchSafeDeployments(t *testing.T) {
	// Guards against a transcription slip in the constants above.
	want := map[string]string{
		"1.3.0 canonical": "0x59AD6735bCd8152B84860Cb256dD9e96b85F69Da",
		"1.3.0 zksync":    "0x4191E2e12E8BC5002424CE0c51f9947b02675a44",
		"1.4.1 canonical": "0x3d4BA2E0884aa488718476ca2FB8Efc291A46199",
		"1.4.1 zksync":    "0xdd35026932273768A3e31F4efF7313B5B7A7199d",
	}
	got := map[string]string{
		"1.3.0 canonical": accessor130.Hex(),
		"1.3.0 zksync":    accessor130ZkSync.Hex(),
		"1.4.1 canonical": accessor141.Hex(),
		"1.4.1 zksync":    accessor141ZkSync.Hex(),
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s accessor = %s; want %s", k, got[k], w)
		}
	}
}

// encodeSimulateAndRevertPayload builds the revert payload the Safe's
// simulateAndRevert produces: the outer delegatecall's success word, the length
// of the accessor's return data, then that data.
func encodeSimulateAndRevertPayload(t *testing.T, estimate uint64, innerOK bool, returnData []byte) []byte {
	t.Helper()
	inner, err := accessorResultArgs.Pack(new(big.Int).SetUint64(estimate), innerOK, returnData)
	if err != nil {
		t.Fatalf("pack accessor result: %v", err)
	}
	out := make([]byte, 64, 64+len(inner))
	out[31] = 1 // outer delegatecall succeeded
	new(big.Int).SetUint64(uint64(len(inner))).FillBytes(out[32:64])
	return append(out, inner...)
}

func TestParseSimulateAndRevertSuccess(t *testing.T) {
	payload := encodeSimulateAndRevertPayload(t, 84_000, true, nil)

	gas, ok, ret, err := parseSimulateAndRevert(payload)
	if err != nil {
		t.Fatalf("parseSimulateAndRevert: %v", err)
	}
	if !ok || gas != 84_000 || len(ret) != 0 {
		t.Fatalf("= gas %d, ok %v, ret %x; want 84000, true, empty", gas, ok, ret)
	}
}

func TestParseSimulateAndRevertRejectsMissingAccessor(t *testing.T) {
	// A delegatecall to an address with no code succeeds and returns nothing --
	// which must not be mistaken for "the transaction is fine".
	payload := make([]byte, 64)
	payload[31] = 1 // outer success, zero-length return data

	if _, _, _, err := parseSimulateAndRevert(payload); err == nil {
		t.Fatal("an empty accessor return must be an error, not a successful simulation")
	}
}

func TestParseSimulateAndRevertRejectsTruncated(t *testing.T) {
	payload := make([]byte, 64)
	payload[31] = 1
	payload[63] = 200 // claims 200 bytes of return data that aren't there

	if _, _, _, err := parseSimulateAndRevert(payload); err == nil {
		t.Fatal("a truncated payload must be rejected")
	}
}

func TestSimulateSafeRevertCheckOnBareRPC(t *testing.T) {
	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	reason := errorStringPayload(t, "GS013")
	payload := encodeSimulateAndRevertPayload(t, 50_000, false, reason)

	// No eth_simulateV1, no debug: the accessor path is all that's available.
	node := newFakeNode(t).on("eth_call", func([]json.RawMessage) (interface{}, error) {
		return nil, &rpcError{code: 3, msg: "execution reverted", data: "0x" + common.Bytes2Hex(payload)}
	})

	res, err := NewSimulator(node.client(t)).SimulateSafe(context.Background(), SafeRequest{
		Safe: safeAddr, Version: "1.4.1", ChainID: 1,
		To: common.HexToAddress("0x2222222222222222222222222222222222222b"), Operation: OperationCall,
	})
	if err != nil {
		t.Fatalf("SimulateSafe: %v", err)
	}
	if res.Status != StatusRevert {
		t.Fatalf("Status = %v; want StatusRevert", res.Status)
	}
	if res.RevertReason != "GS013" {
		t.Errorf("RevertReason = %q; want the Safe's GS013", res.RevertReason)
	}
}

func TestSimulateSafeDelegateCallNotesMissingAssetPreview(t *testing.T) {
	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	payload := encodeSimulateAndRevertPayload(t, 210_000, true, nil)

	// A capable endpoint -- but a MultiSend batch is a DelegateCall, so the
	// accessor path is still the only faithful one until P3b lands.
	node := newFakeNode(t).
		ok("eth_simulateV1", []interface{}{}).
		on("eth_call", func([]json.RawMessage) (interface{}, error) {
			return nil, &rpcError{code: 3, msg: "execution reverted", data: "0x" + common.Bytes2Hex(payload)}
		})

	res, err := NewSimulator(node.client(t)).SimulateSafe(context.Background(), SafeRequest{
		Safe: safeAddr, Version: "1.4.1", ChainID: 1, Operation: OperationDelegateCall,
	})
	if err != nil {
		t.Fatalf("SimulateSafe: %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("Status = %v; want StatusOK", res.Status)
	}
	if res.Note != noteSafeDelegateCall {
		t.Errorf("Note = %q; want the DelegateCall limitation note", res.Note)
	}
	if len(res.Tokens) != 0 || res.ETHDelta != nil {
		t.Error("the accessor path must not claim asset changes it cannot observe")
	}
}

func TestSimulateSafeCallUsesRichPathAsTheSafe(t *testing.T) {
	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	recipient := common.HexToAddress("0x2222222222222222222222222222222222222b")
	amount := big.NewInt(1_000_000)

	var sawFrom string
	node := newFakeNode(t).on("eth_simulateV1", func(params []json.RawMessage) (interface{}, error) {
		// Capture `from` on the real (non-probe) call to prove the inner call
		// runs as the Safe -- msg.sender is what execTransaction would give it.
		var p struct {
			BlockStateCalls []struct {
				Calls []struct {
					From string `json:"from"`
				} `json:"calls"`
			} `json:"blockStateCalls"`
		}
		if err := json.Unmarshal(params[0], &p); err == nil &&
			len(p.BlockStateCalls) > 0 && len(p.BlockStateCalls[0].Calls) > 0 {
			sawFrom = p.BlockStateCalls[0].Calls[0].From
		}
		return simOK(60_000,
			logJSON(token, []common.Hash{transferSig, topicOf(safeAddr), topicOf(recipient)}, word(amount)),
		), nil
	})

	res, err := NewSimulator(node.client(t)).SimulateSafe(context.Background(), SafeRequest{
		Safe: safeAddr, Version: "1.4.1", ChainID: 1, To: token, Operation: OperationCall,
	})
	if err != nil {
		t.Fatalf("SimulateSafe: %v", err)
	}
	if res.Status != StatusOK || res.Tier != TierSimulate {
		t.Fatalf("Status/Tier = %v/%v; want StatusOK/TierSimulate", res.Status, res.Tier)
	}
	if common.HexToAddress(sawFrom) != safeAddr {
		t.Errorf("simulated from %q; want the Safe %s", sawFrom, safeAddr.Hex())
	}
	if len(res.Tokens) != 1 || res.Tokens[0].Delta.Cmp(new(big.Int).Neg(amount)) != 0 {
		t.Fatalf("Tokens = %+v; want the Safe debited %v", res.Tokens, amount)
	}
}

func TestSimulateSafeUnknownVersionIsUnavailable(t *testing.T) {
	res, err := NewSimulator(newFakeNode(t).client(t)).SimulateSafe(context.Background(), SafeRequest{
		Version: "1.1.1", ChainID: 1,
	})
	if err != nil {
		t.Fatalf("SimulateSafe: %v", err)
	}
	if res.Status != StatusUnavailable || res.Note != noteSafeNoAccessor {
		t.Fatalf("= %v %q; want StatusUnavailable with the no-accessor note", res.Status, res.Note)
	}
}

// The accessor's return tuple must stay in lockstep with the on-chain
// signature; a mismatch would decode garbage rather than fail loudly.
func TestAccessorResultArgsMatchSimulateSignature(t *testing.T) {
	method, ok := safeSimABI.Methods["simulate"]
	if !ok {
		t.Fatal("safeSimABI has no simulate method")
	}
	if len(method.Outputs) != len(accessorResultArgs) {
		t.Fatalf("simulate returns %d values; accessorResultArgs has %d", len(method.Outputs), len(accessorResultArgs))
	}
	for i, out := range method.Outputs {
		if out.Type.String() != accessorResultArgs[i].Type.String() {
			t.Errorf("output %d = %s; accessorResultArgs has %s", i, out.Type, accessorResultArgs[i].Type)
		}
	}
	var _ abi.Arguments = accessorResultArgs
}
