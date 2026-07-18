package tx

import (
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestBuildNativeSend(t *testing.T) {
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	amount := big.NewInt(1_000_000_000_000_000_000) // 1 ETH

	s, err := BuildNativeSend(from, to, amount, "ETH", 18)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsNative || s.Call.To != to || s.Call.Value.Cmp(amount) != 0 || len(s.Call.Data) != 0 {
		t.Errorf("native send = %+v", s)
	}
	// The build must copy the amount, not alias it.
	amount.SetInt64(0)
	if s.Amount.Sign() == 0 {
		t.Error("BuildNativeSend must not alias the caller's amount")
	}
}

func TestBuildERC20Send(t *testing.T) {
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	amount := big.NewInt(1_500_000) // 1.5 USDC

	s, err := BuildERC20Send(from, token, to, amount, "USDC", 6)
	if err != nil {
		t.Fatal(err)
	}
	if s.IsNative || s.Call.To != token || s.Call.Value.Sign() != 0 {
		t.Errorf("erc20 send targeting wrong fields: %+v", s)
	}
	// Calldata must be transfer(to, amount): selector a9059cbb + 32-byte to + 32-byte amount.
	got := hex.EncodeToString(s.Call.Data)
	wantPrefix := "a9059cbb" +
		"0000000000000000000000002222222222222222222222222222222222222222" +
		"000000000000000000000000000000000000000000000000000000000016e360" // 1_500_000
	if got != wantPrefix {
		t.Errorf("calldata = %s\nwant       %s", got, wantPrefix)
	}
}

func TestBuildRejectsNonPositive(t *testing.T) {
	from := common.HexToAddress("0x1")
	to := common.HexToAddress("0x2")
	for _, amt := range []*big.Int{nil, big.NewInt(0), big.NewInt(-5)} {
		if _, err := BuildNativeSend(from, to, amt, "ETH", 18); !errors.Is(err, ErrNonPositiveAmount) {
			t.Errorf("native amount %v should be rejected", amt)
		}
		if _, err := BuildERC20Send(from, to, to, amt, "X", 18); !errors.Is(err, ErrNonPositiveAmount) {
			t.Errorf("erc20 amount %v should be rejected", amt)
		}
	}
}

func TestNativeSendAll(t *testing.T) {
	balance := big.NewInt(1_000_000_000_000_000_000) // 1 ETH
	gasLimit := uint64(21000)
	maxFee := big.NewInt(30_000_000_000) // 30 gwei
	// fee = 21000 * 30e9 = 6.3e14 wei
	got, err := NativeSendAll(balance, gasLimit, maxFee)
	if err != nil {
		t.Fatal(err)
	}
	wantFee := new(big.Int).Mul(big.NewInt(21000), maxFee)
	want := new(big.Int).Sub(balance, wantFee)
	if got.Cmp(want) != 0 {
		t.Errorf("send-all = %s, want %s", got, want)
	}
}

func TestNativeSendAllInsufficient(t *testing.T) {
	// Balance below the fee -> error, not a negative amount.
	if _, err := NativeSendAll(big.NewInt(100), 21000, big.NewInt(30_000_000_000)); !errors.Is(err, ErrNonPositiveAmount) {
		t.Errorf("insufficient balance err = %v, want ErrNonPositiveAmount", err)
	}
}
