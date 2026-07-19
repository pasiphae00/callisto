package assets

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestDiscoverTokens verifies the Go-side filtering of Transfer logs: 3-topic
// (ERC-20) logs contribute their contract, 4-topic (ERC-721) logs are skipped, and
// the same token seen twice is returned once (preserving first-seen order).
func TestDiscoverTokens(t *testing.T) {
	account := common.HexToAddress("0x1111111111111111111111111111111111111111")
	acctTopic := common.BytesToHash(account.Bytes())
	fromTopic := common.BytesToHash(common.HexToAddress("0x2222222222222222222222222222222222222222").Bytes())

	usdc := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	dai := common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")
	nft := common.HexToAddress("0xBC4CA0EdA7647A8aB7C2061c2E118A18a936f13D")

	erc20 := func(token common.Address, block uint64) types.Log {
		return types.Log{
			Address:     token,
			Topics:      []common.Hash{transferTopic, fromTopic, acctTopic}, // 3 topics
			BlockNumber: block,
		}
	}
	erc721 := func(token common.Address, block uint64) types.Log {
		return types.Log{
			Address: token,
			// 4 topics: sig, from, to, tokenId (indexed) — must be skipped.
			Topics:      []common.Hash{transferTopic, fromTopic, acctTopic, common.HexToHash("0x01")},
			BlockNumber: block,
		}
	}

	client := &mockClient{
		head: 500,
		logs: []types.Log{
			erc20(usdc, 100),
			erc721(nft, 150), // NFT received — should not appear
			erc20(dai, 200),
			erc20(usdc, 300), // duplicate token — dedup
		},
	}

	tokens, head, err := DiscoverTokens(context.Background(), client, account, 0)
	if err != nil {
		t.Fatalf("DiscoverTokens: %v", err)
	}
	if head != 500 {
		t.Errorf("head = %d, want 500", head)
	}
	want := []common.Address{usdc, dai}
	if len(tokens) != len(want) {
		t.Fatalf("got %d tokens %v, want %d %v", len(tokens), tokens, len(want), want)
	}
	for i, w := range want {
		if tokens[i] != w {
			t.Errorf("tokens[%d] = %s, want %s", i, tokens[i], w)
		}
	}
}

// TestDiscoverTokensNoNewBlocks ensures an incremental scan past the head is a
// cheap no-op that still reports the current head for the next watermark.
func TestDiscoverTokensNoNewBlocks(t *testing.T) {
	client := &mockClient{head: 100}
	tokens, head, err := DiscoverTokens(context.Background(), client, common.Address{}, 200)
	if err != nil {
		t.Fatalf("DiscoverTokens: %v", err)
	}
	if head != 100 {
		t.Errorf("head = %d, want 100", head)
	}
	if len(tokens) != 0 {
		t.Errorf("got %d tokens, want 0", len(tokens))
	}
}
