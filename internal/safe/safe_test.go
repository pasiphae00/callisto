package safe

import (
	"context"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/rpc"
)

// mockClient implements just enough of rpc.Client for ReadInfo: it dispatches
// eth_call by the 4-byte selector to a canned, ABI-encoded response. Embedding the
// interface makes any unexpected method call panic, which is what we want in a
// focused test.
type mockClient struct {
	rpc.Client
	responses map[string][]byte // selector hex -> return data
}

func (m *mockClient) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	sel := common.Bytes2Hex(msg.Data[:4])
	if out, ok := m.responses[sel]; ok {
		return out, nil
	}
	return nil, ethereum.NotFound
}

func packOutput(t *testing.T, method string, vals ...interface{}) []byte {
	t.Helper()
	out, err := safeABI.Methods[method].Outputs.Pack(vals...)
	if err != nil {
		t.Fatalf("pack %s output: %v", method, err)
	}
	return out
}

func selector(t *testing.T, method string) string {
	t.Helper()
	return common.Bytes2Hex(safeABI.Methods[method].ID)
}

func TestReadInfo(t *testing.T) {
	owners := []common.Address{
		common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8"),
		common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"),
	}
	m := &mockClient{responses: map[string][]byte{
		selector(t, "getOwners"):    packOutput(t, "getOwners", owners),
		selector(t, "getThreshold"): packOutput(t, "getThreshold", big.NewInt(2)),
		selector(t, "nonce"):        packOutput(t, "nonce", big.NewInt(9)),
		selector(t, "VERSION"):      packOutput(t, "VERSION", "1.3.0"),
	}}

	safeAddr := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	info, err := ReadInfo(context.Background(), m, safeAddr)
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.Threshold != 2 || info.Nonce != 9 || info.Version != "1.3.0" {
		t.Errorf("info = %+v", info)
	}
	if len(info.Owners) != 2 || info.Owners[0] != owners[0] || info.Owners[1] != owners[1] {
		t.Errorf("owners = %v", info.Owners)
	}
}

func TestReadInfoRejectsNonSafe(t *testing.T) {
	// getOwners returns an empty array -> not a Safe.
	m := &mockClient{responses: map[string][]byte{
		selector(t, "getOwners"): packOutput(t, "getOwners", []common.Address{}),
	}}
	if _, err := ReadInfo(context.Background(), m, common.HexToAddress("0xdead")); err == nil {
		t.Error("ReadInfo should reject an address with no owners")
	}
}
