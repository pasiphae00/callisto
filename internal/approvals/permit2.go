package approvals

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// permit2Address is the canonical Uniswap Permit2 contract, deployed at the same
// address on every chain.
var permit2Address = common.HexToAddress("0x000000000022D473030F116dDEE9F6B43aC78BA3")

// maxUint160 is Permit2's "infinite allowance" sentinel (type(uint160).max); a
// balance at that value is never decremented, i.e. effectively unlimited.
var maxUint160 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 160), big.NewInt(1))

// Permit2 AllowanceTransfer surface: allowance() to read live state, lockdown()
// to revoke (set an allowance to zero).
var permit2ABI = mustABI(`[
  {"name":"allowance","type":"function","stateMutability":"view",
   "inputs":[{"name":"user","type":"address"},{"name":"token","type":"address"},{"name":"spender","type":"address"}],
   "outputs":[{"name":"amount","type":"uint160"},{"name":"expiration","type":"uint48"},{"name":"nonce","type":"uint48"}]},
  {"name":"lockdown","type":"function","stateMutability":"nonpayable",
   "inputs":[{"name":"approvals","type":"tuple[]","components":[{"name":"token","type":"address"},{"name":"spender","type":"address"}]}],
   "outputs":[]}
]`)

// Permit2 emits Approval and Permit when an allowance is granted; both index
// owner/token/spender (topics 1/2/3).
var (
	permit2ApprovalSig = crypto.Keccak256Hash([]byte("Approval(address,address,address,uint160,uint48)"))
	permit2PermitSig   = crypto.Keccak256Hash([]byte("Permit(address,address,address,uint160,uint48,uint48)"))
)

// tokenSpenderPair mirrors Permit2's IAllowanceTransfer.TokenSpenderPair for
// packing lockdown(). Field order/names match the ABI tuple.
type tokenSpenderPair struct {
	Token   common.Address
	Spender common.Address
}

// scanPermit2 finds live Permit2 inner allowances: Approval/Permit logs from the
// Permit2 contract by owner give the (token, spender) pairs; the live allowance()
// (non-zero and not expired) decides which are still outstanding.
func (s *Scanner) scanPermit2(ctx context.Context, owner common.Address, from, head uint64, onBlock func(uint64)) ([]Approval, error) {
	ownerTopic := common.BytesToHash(owner.Bytes())
	topics := [][]common.Hash{{permit2ApprovalSig, permit2PermitSig}, {ownerTopic}}
	logs, err := s.getLogs(ctx, []common.Address{permit2Address}, topics, from, head, onBlock)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	var out []Approval
	seen := map[string]bool{}
	for _, lg := range logs {
		if len(lg.Topics) < 4 {
			continue
		}
		token := common.BytesToAddress(lg.Topics[2].Bytes())
		spender := common.BytesToAddress(lg.Topics[3].Bytes())
		key := token.Hex() + spender.Hex()
		if seen[key] {
			continue
		}
		seen[key] = true

		amount, expiration, err := s.permit2Allowance(ctx, owner, token, spender)
		if err != nil || amount == nil || amount.Sign() == 0 {
			continue
		}
		if expiration != 0 && expiration <= now {
			continue // lapsed allowance; nothing to revoke
		}
		out = append(out, s.build(ctx, LayerPermit2, token, spender, amount, maxUint160, expiration))
	}
	return out, nil
}

// permit2Allowance reads Permit2.allowance(owner, token, spender) → (amount,
// expiration). nonce is ignored.
func (s *Scanner) permit2Allowance(ctx context.Context, owner, token, spender common.Address) (*big.Int, int64, error) {
	out, err := s.call(ctx, permit2Address, permit2ABI, "allowance", owner, token, spender)
	if err != nil {
		return nil, 0, err
	}
	var res struct {
		Amount     *big.Int
		Expiration *big.Int
		Nonce      *big.Int
	}
	if err := permit2ABI.UnpackIntoInterface(&res, "allowance", out); err != nil {
		return nil, 0, err
	}
	var exp int64
	if res.Expiration != nil {
		exp = res.Expiration.Int64()
	}
	return res.Amount, exp, nil
}

// permit2LockdownCalldata builds lockdown([(token, spender)]), which zeroes the
// allowance for that pair.
func permit2LockdownCalldata(token, spender common.Address) ([]byte, error) {
	return permit2ABI.Pack("lockdown", []tokenSpenderPair{{Token: token, Spender: spender}})
}
