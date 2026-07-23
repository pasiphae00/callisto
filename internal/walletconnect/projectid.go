package walletconnect

import "os"

// defaultProjectID is Callisto's own Reown/WalletConnect Cloud project id. It is
// embedded so users get a zero-config "paste a URI and it works" experience — a
// projectId is a per-application rate-limit/attribution key, not a secret (every
// dApp ships its own in client JS). Override it with CALLISTO_WC_PROJECT_ID (e.g.
// to run your own project) without rebuilding.
const defaultProjectID = "96a0c0043da6e6d15905a658c6540c51"

// ProjectID returns the WalletConnect project id to use: the CALLISTO_WC_PROJECT_ID
// environment override if set, otherwise the embedded default.
func ProjectID() string {
	if v := os.Getenv("CALLISTO_WC_PROJECT_ID"); v != "" {
		return v
	}
	return defaultProjectID
}

// WalletMetadata is the wallet identity shown to dApps in their connection UI.
var WalletMetadata = Metadata{
	Name:        "Callisto",
	Description: "A locally-run Ethereum wallet and signing utility.",
	URL:         "https://github.com/pasiphae00/callisto",
	Icons:       []string{},
}
