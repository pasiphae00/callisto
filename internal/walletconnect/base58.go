package walletconnect

import "math/big"

// base58btc is the Bitcoin/base58btc alphabet used by multibase "z" (did:key).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Encode encodes bytes with the base58btc alphabet, preserving leading-zero
// bytes as leading '1's (standard base58 behavior).
func base58Encode(input []byte) string {
	// Count leading zero bytes → leading '1's.
	zeros := 0
	for zeros < len(input) && input[zeros] == 0 {
		zeros++
	}

	num := new(big.Int).SetBytes(input)
	radix := big.NewInt(58)
	mod := new(big.Int)

	// Encode the big-endian integer into base58 (reversed), then flip.
	var out []byte
	for num.Sign() > 0 {
		num.DivMod(num, radix, mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		out = append(out, base58Alphabet[0])
	}
	// Reverse.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
