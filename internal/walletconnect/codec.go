package walletconnect

import "encoding/json"

// WalletConnect Sign relay tags and TTLs (seconds), from the v2 spec rpc-methods.
const (
	tagSessionProposeReq    = 1100
	tagSessionProposeRes    = 1101
	tagSessionProposeReject = 1120
	tagSessionSettleReq     = 1102
	tagSessionSettleRes     = 1103
	tagSessionRequestReq    = 1108
	tagSessionRequestRes    = 1109
	tagSessionDeleteReq     = 1112
	tagSessionDeleteRes     = 1113
	tagSessionPingReq       = 1114
	tagSessionPingRes       = 1115

	ttlProposal = 300
	ttlSettle   = 300
	ttlRequest  = 300
	ttlResponse = 300
	ttlDelete   = 86400
	ttlPing     = 30
)

// WalletConnect Sign method names (the inner, encrypted JSON-RPC).
const (
	wcSessionPropose = "wc_sessionPropose"
	wcSessionSettle  = "wc_sessionSettle"
	wcSessionRequest = "wc_sessionRequest"
	wcSessionDelete  = "wc_sessionDelete"
	wcSessionPing    = "wc_sessionPing"
)

// Metadata describes a peer (dApp or wallet) shown to the user.
type Metadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Icons       []string `json:"icons"`
}

// relayInfo is the "relay" field carried in proposals/settlements.
type relayInfo struct {
	Protocol string `json:"protocol"`
	Data     string `json:"data,omitempty"`
}

// Namespace is a CAIP-2 namespace entry. Proposals list chains/methods/events;
// settlements additionally list the concrete accounts (CAIP-10).
type Namespace struct {
	Chains   []string `json:"chains,omitempty"`
	Methods  []string `json:"methods"`
	Events   []string `json:"events"`
	Accounts []string `json:"accounts,omitempty"`
}

// proposer is the dApp side of a session proposal.
type proposer struct {
	PublicKey string   `json:"publicKey"`
	Metadata  Metadata `json:"metadata"`
}

// sessionProposeParams is wc_sessionPropose params (from the dApp).
type sessionProposeParams struct {
	Relays             []relayInfo          `json:"relays"`
	Proposer           proposer             `json:"proposer"`
	RequiredNamespaces map[string]Namespace `json:"requiredNamespaces"`
	OptionalNamespaces map[string]Namespace `json:"optionalNamespaces,omitempty"`
	Pairing            json.RawMessage      `json:"pairingTopic,omitempty"`
	ExpiryTimestamp    int64                `json:"expiryTimestamp,omitempty"`
}

// sessionProposeResult is our approval response: our public key for the ECDH.
type sessionProposeResult struct {
	Relay              relayInfo `json:"relay"`
	ResponderPublicKey string    `json:"responderPublicKey"`
}

// controller is the wallet side of a settled session.
type controller struct {
	PublicKey string   `json:"publicKey"`
	Metadata  Metadata `json:"metadata"`
}

// sessionSettleParams is wc_sessionSettle params (wallet → dApp).
type sessionSettleParams struct {
	Relay      relayInfo            `json:"relay"`
	Controller controller           `json:"controller"`
	Namespaces map[string]Namespace `json:"namespaces"`
	Expiry     int64                `json:"expiry"`
}

// requestPayload is the inner request in wc_sessionRequest.
type requestPayload struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Expiry int64           `json:"expiry,omitempty"`
}

// sessionRequestParams is wc_sessionRequest params (dApp → wallet).
type sessionRequestParams struct {
	Request requestPayload `json:"request"`
	ChainID string         `json:"chainId"`
}

// sessionDeleteParams is wc_sessionDelete params.
type sessionDeleteParams struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
