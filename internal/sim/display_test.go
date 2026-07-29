package sim

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ---------------------------------------------------------------------------
// aggregation
// ---------------------------------------------------------------------------

func TestAggregateNetsRepeatedTransfers(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	pool := common.HexToAddress("0x2222222222222222222222222222222222222b")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")

	// A swap that routes through the wallet twice: 1000 out, 400 back.
	logs := []*types.Log{
		{Address: token, Topics: []common.Hash{transferSig, topicOf(wallet), topicOf(pool)}, Data: word(big.NewInt(1000))},
		{Address: token, Topics: []common.Hash{transferSig, topicOf(pool), topicOf(wallet)}, Data: word(big.NewInt(400))},
	}

	got := aggregate(logs, wallet)
	if len(got.Tokens) != 1 {
		t.Fatalf("Tokens = %+v; want one netted entry", got.Tokens)
	}
	if got.Tokens[0].Delta.Cmp(big.NewInt(-600)) != 0 {
		t.Errorf("Delta = %v; want -600 (1000 out, 400 back)", got.Tokens[0].Delta)
	}
}

func TestAggregateDropsTokensThatNetToZero(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	pool := common.HexToAddress("0x2222222222222222222222222222222222222b")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")

	// A flash-loan shape: borrowed and repaid within the same transaction.
	logs := []*types.Log{
		{Address: token, Topics: []common.Hash{transferSig, topicOf(pool), topicOf(wallet)}, Data: word(big.NewInt(5000))},
		{Address: token, Topics: []common.Hash{transferSig, topicOf(wallet), topicOf(pool)}, Data: word(big.NewInt(5000))},
	}

	if got := aggregate(logs, wallet); len(got.Tokens) != 0 {
		t.Fatalf("Tokens = %+v; a token that nets to zero should not be shown", got.Tokens)
	}
}

func TestAggregateIgnoresUnrelatedAccounts(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	a := common.HexToAddress("0x2222222222222222222222222222222222222b")
	b := common.HexToAddress("0x3333333333333333333333333333333333333c")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")

	logs := []*types.Log{
		{Address: token, Topics: []common.Hash{transferSig, topicOf(a), topicOf(b)}, Data: word(big.NewInt(1))},
	}
	got := aggregate(logs, wallet)
	if len(got.Tokens) != 0 || got.ETH.Sign() != 0 {
		t.Fatalf("= %+v; internal hops between other accounts must not appear", got)
	}
}

func TestAggregateLastApprovalWinsPerSpender(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	spender := common.HexToAddress("0x2222222222222222222222222222222222222b")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

	// approve() sets rather than adds: a router that raises then drops the
	// allowance leaves only the final value in force.
	logs := []*types.Log{
		{Address: token, Topics: []common.Hash{approvalSig, topicOf(wallet), topicOf(spender)}, Data: word(maxUint256)},
		{Address: token, Topics: []common.Hash{approvalSig, topicOf(wallet), topicOf(spender)}, Data: word(big.NewInt(0))},
	}

	got := aggregate(logs, wallet)
	if len(got.Approvals) != 1 {
		t.Fatalf("Approvals = %+v; want one entry per (token, spender)", got.Approvals)
	}
	if got.Approvals[0].Unlimited || got.Approvals[0].Amount.Sign() != 0 {
		t.Errorf("Approvals[0] = %+v; want the final zero allowance, not the intermediate unlimited one", got.Approvals[0])
	}
}

func TestAggregateSkipsAnonymousAndMalformedLogs(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")

	logs := []*types.Log{
		nil,
		{Address: token}, // anonymous event: no topics at all
		{Address: token, Topics: []common.Hash{transferSig}},                                                    // truncated
		{Address: token, Topics: []common.Hash{transferSig, topicOf(wallet), topicOf(wallet), topicOf(wallet)}}, // ERC-721 shape
	}

	got := aggregate(logs, wallet) // must not panic
	if len(got.Tokens) != 0 || len(got.Approvals) != 0 {
		t.Fatalf("= %+v; malformed logs must be ignored", got)
	}
}

func TestApplyETHMovesNetsSelfPayment(t *testing.T) {
	account := common.HexToAddress("0x1111111111111111111111111111111111111a")
	c := changes{ETH: new(big.Int)}
	applyETHMoves(&c, []ethMove{{From: account, To: account, Value: big.NewInt(500)}}, account)
	if c.ETH.Sign() != 0 {
		t.Fatalf("ETH = %v; an account paying itself nets to zero", c.ETH)
	}
}

func TestAggregateRecognizesBothNativeSentinels(t *testing.T) {
	wallet := common.HexToAddress("0x1111111111111111111111111111111111111a")
	to := common.HexToAddress("0x2222222222222222222222222222222222222b")
	amount := big.NewInt(10_000_000_000_000) // 0.00001 ETH

	// geth emits native transfers from the ERC-7528 placeholder; its own stale
	// doc comment claims the zero address. Both must read as native, or a plain
	// ETH send renders as an unknown token with raw base units and no symbol.
	for _, sentinel := range []common.Address{nativeSentinel, zeroAddress} {
		logs := []*types.Log{{
			Address: sentinel,
			Topics:  []common.Hash{transferSig, topicOf(wallet), topicOf(to)},
			Data:    word(amount),
		}}

		got := aggregate(logs, wallet)
		if got.ETH.Cmp(new(big.Int).Neg(amount)) != 0 {
			t.Errorf("%s: ETH = %v; want -%v", sentinel.Hex(), got.ETH, amount)
		}
		if len(got.Tokens) != 0 {
			t.Errorf("%s: native transfer leaked into Tokens as %+v", sentinel.Hex(), got.Tokens)
		}
	}
}

func TestRowsRenderASmallNativeSendReadably(t *testing.T) {
	// Regression: a 0.00001 ETH send rendered as "-10000000000000 0xEeee...EEeE"
	// because the sentinel wasn't recognized, so the amount fell through to the
	// token path with no decimals and no symbol.
	res := Result{Status: StatusOK, ETHDelta: big.NewInt(-10_000_000_000_000)}

	rows := res.Rows(1)
	if len(rows) != 1 {
		t.Fatalf("Rows = %+v; want one native row", rows)
	}
	if rows[0].Text != "-0.00001 ETH" {
		t.Errorf("row = %q; want \"-0.00001 ETH\"", rows[0].Text)
	}
}

// ---------------------------------------------------------------------------
// revert decoding
// ---------------------------------------------------------------------------

func TestDecodeRevert(t *testing.T) {
	errPayload := func(msg string) []byte {
		packed, err := revertStringArgs.Pack(msg)
		if err != nil {
			t.Fatal(err)
		}
		return append(append([]byte{}, errorStringSelector...), packed...)
	}
	panicPayload := func(code int64) []byte {
		packed, err := revertPanicArgs.Pack(big.NewInt(code))
		if err != nil {
			t.Fatal(err)
		}
		return append(append([]byte{}, panicSelector...), packed...)
	}

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"no payload", nil, ""},
		{"require message", errPayload("ERC20: insufficient allowance"), "ERC20: insufficient allowance"},
		{"panic overflow", panicPayload(0x11), "panic: arithmetic overflow or underflow"},
		{"panic unknown code", panicPayload(0x99), "panic: code 0x99"},
		{"custom error", []byte{0xde, 0xad, 0xbe, 0xef}, "custom error deadbeef"},
	}
	for _, tc := range tests {
		if got := decodeRevert(tc.data); got != tc.want {
			t.Errorf("%s: decodeRevert = %q; want %q", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// display rows
// ---------------------------------------------------------------------------

func TestRowsRenderSignedAmounts(t *testing.T) {
	oneETH := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	res := Result{
		Status:   StatusOK,
		ETHDelta: new(big.Int).Neg(oneETH),
		Tokens: []TokenDelta{
			{Token: common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"),
				Symbol: "stETH", Decimals: 18, Delta: big.NewInt(998_000_000_000_000_000)},
		},
	}

	rows := res.Rows(1)
	if len(rows) != 2 {
		t.Fatalf("Rows = %+v; want 2", rows)
	}
	if rows[0].Text != "-1 ETH" || rows[0].Incoming {
		t.Errorf("row 0 = %+v; want an outgoing \"-1 ETH\"", rows[0])
	}
	if rows[1].Text != "+0.998 stETH" || !rows[1].Incoming {
		t.Errorf("row 1 = %+v; want an incoming \"+0.998 stETH\"", rows[1])
	}
}

func TestRowsUseTheChainsNativeSymbol(t *testing.T) {
	res := Result{Status: StatusOK, ETHDelta: big.NewInt(-1_000_000_000_000_000_000)}
	// Polygon's native asset is POL, not ETH.
	if got := res.Rows(137)[0].Text; strings.HasSuffix(got, " ETH") {
		t.Errorf("row = %q; want Polygon's native symbol, not ETH", got)
	}
}

func TestRowsFlagUnlimitedApproval(t *testing.T) {
	res := Result{
		Status: StatusOK,
		Approvals: []ApprovalChange{{
			Token:     common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
			Symbol:    "USDC",
			Spender:   common.HexToAddress("0x2222222222222222222222222222222222222b"),
			Unlimited: true,
		}},
	}

	rows := res.Rows(1)
	if len(rows) != 1 || !rows[0].Warn {
		t.Fatalf("Rows = %+v; an unlimited allowance must be flagged", rows)
	}
	if !strings.Contains(rows[0].Text, "UNLIMITED") || !strings.Contains(rows[0].Text, "USDC") {
		t.Errorf("row = %q; want it to name UNLIMITED and the token", rows[0].Text)
	}
}

func TestRowsNameAZeroApprovalARevocation(t *testing.T) {
	res := Result{Status: StatusOK, Approvals: []ApprovalChange{{
		Symbol: "USDC", Amount: big.NewInt(0),
		Spender: common.HexToAddress("0x2222222222222222222222222222222222222b"),
	}}}
	if got := res.Rows(1)[0].Text; !strings.HasPrefix(got, "Revoke ") {
		t.Errorf("row = %q; want a zero allowance described as a revocation", got)
	}
}

func TestRowsFallBackToAnAddressWhenSymbolIsUnknown(t *testing.T) {
	token := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	res := Result{Status: StatusOK, Tokens: []TokenDelta{{Token: token, Delta: big.NewInt(5)}}}

	got := res.Rows(1)[0].Text
	if !strings.Contains(got, "0xA0b8") {
		t.Errorf("row = %q; an unreadable symbol should fall back to the token address", got)
	}
}

func TestHasChangesIsFalseForAStateOnlyCall(t *testing.T) {
	res := Result{Status: StatusOK, ETHDelta: new(big.Int)}
	if res.HasChanges() {
		t.Fatal("a transaction that moves nothing must not report changes")
	}
}
