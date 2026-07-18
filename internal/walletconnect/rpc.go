package walletconnect

import (
	"encoding/json"
	"math/rand"
	"time"
)

// JSON-RPC 2.0 message types shared by the relay transport and the Sign engine.
// The relay carries these directly (irn_*), and the inner (decrypted) WalletConnect
// protocol messages (wc_*) use the same shapes.

type rpcRequest struct {
	ID      uint64          `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	ID      uint64          `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// newRequest builds a JSON-RPC request with a fresh id and marshaled params.
func newRequest(method string, params interface{}) (rpcRequest, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return rpcRequest{}, err
	}
	return rpcRequest{ID: newPayloadID(), JSONRPC: "2.0", Method: method, Params: raw}, nil
}

// newPayloadID mirrors WalletConnect's payloadId(): Date.now()*1000 + rand(0..999),
// which stays a monotonic-ish integer safely below 2^53 (JS number-safe).
func newPayloadID() uint64 {
	return uint64(time.Now().UnixMilli())*1000 + uint64(rand.Intn(1000))
}
