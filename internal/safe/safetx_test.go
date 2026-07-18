package safe

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// referenceHash computes the safeTxHash via go-ethereum's independent EIP-712
// implementation, so LocalHash is validated against a completely different code
// path rather than a hand-copied magic constant.
func referenceHash(t *testing.T, chainID *big.Int, safe common.Address, tx SafeTx) common.Hash {
	t.Helper()
	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"SafeTx": {
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "data", Type: "bytes"},
				{Name: "operation", Type: "uint8"},
				{Name: "safeTxGas", Type: "uint256"},
				{Name: "baseGas", Type: "uint256"},
				{Name: "gasPrice", Type: "uint256"},
				{Name: "gasToken", Type: "address"},
				{Name: "refundReceiver", Type: "address"},
				{Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "SafeTx",
		Domain: apitypes.TypedDataDomain{
			ChainId:           (*math.HexOrDecimal256)(chainID),
			VerifyingContract: safe.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"to":             tx.To.Hex(),
			"value":          (*math.HexOrDecimal256)(tx.Value),
			"data":           hexutil.Encode(tx.Data),
			"operation":      (*math.HexOrDecimal256)(big.NewInt(int64(tx.Operation))),
			"safeTxGas":      (*math.HexOrDecimal256)(tx.SafeTxGas),
			"baseGas":        (*math.HexOrDecimal256)(tx.BaseGas),
			"gasPrice":       (*math.HexOrDecimal256)(tx.GasPrice),
			"gasToken":       tx.GasToken.Hex(),
			"refundReceiver": tx.RefundReceiver.Hex(),
			"nonce":          (*math.HexOrDecimal256)(tx.Nonce),
		},
	}
	digest, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		t.Fatalf("reference TypedDataAndHash: %v", err)
	}
	return common.BytesToHash(digest)
}

func TestLocalHashMatchesEIP712Reference(t *testing.T) {
	chainID := big.NewInt(1)
	safe := common.HexToAddress("0x1c511D88ba898b4D9cd9113D13B9c360a02Fcea1")
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	tx := NewSafeTx(to, big.NewInt(1_000_000_000_000_000_000), nil, 5)
	got := tx.LocalHash(chainID, safe)
	want := referenceHash(t, chainID, safe, tx)
	if got != want {
		t.Errorf("empty-data safeTxHash\n got %s\nwant %s", got.Hex(), want.Hex())
	}
}

func TestLocalHashWithDataAndDifferentChain(t *testing.T) {
	chainID := big.NewInt(11155111) // sepolia
	safe := common.HexToAddress("0xA063Cb7CFd8E57c30c788A0572CBbf2129ae56B6")
	to := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	// ERC-20 transfer-like calldata.
	data := common.FromHex("a9059cbb0000000000000000000000001111111111111111111111111111111111111111000000000000000000000000000000000000000000000000000000000000000a")

	tx := NewSafeTx(to, big.NewInt(0), data, 42)
	got := tx.LocalHash(chainID, safe)
	want := referenceHash(t, chainID, safe, tx)
	if got != want {
		t.Errorf("with-data safeTxHash\n got %s\nwant %s", got.Hex(), want.Hex())
	}
}

func TestNewSafeTxDefaultsZeroed(t *testing.T) {
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	tx := NewSafeTx(to, nil, nil, 0)
	if tx.Operation != Call {
		t.Errorf("operation = %d, want Call", tx.Operation)
	}
	for name, v := range map[string]*big.Int{
		"value": tx.Value, "safeTxGas": tx.SafeTxGas, "baseGas": tx.BaseGas, "gasPrice": tx.GasPrice,
	} {
		if v == nil || v.Sign() != 0 {
			t.Errorf("%s = %v, want 0", name, v)
		}
	}
	if tx.GasToken != (common.Address{}) || tx.RefundReceiver != (common.Address{}) {
		t.Error("gasToken/refundReceiver must be zero address")
	}
}
