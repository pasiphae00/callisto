package hot

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/pasiphae00/callisto/internal/signer"
)

func TestSignPersonalMessageRecovers(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()

	msg := []byte("Sign in to Example dApp")
	sig, err := w.SignPersonalMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("SignPersonalMessage: %v", err)
	}
	if len(sig) != 65 || (sig[64] != 27 && sig[64] != 28) {
		t.Fatalf("bad signature: len=%d v=%d", len(sig), sig[64])
	}
	recoverEq(t, accounts.TextHash(msg), sig, w)
}

func TestSignTypedDataRecovers(t *testing.T) {
	w, err := Open(junkMnemonic, "", DefaultPath(0))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Lock()

	// A minimal but valid EIP-712 document (the classic Mail example, trimmed).
	typedData := []byte(`{
	  "types":{
	    "EIP712Domain":[
	      {"name":"name","type":"string"},
	      {"name":"version","type":"string"},
	      {"name":"chainId","type":"uint256"},
	      {"name":"verifyingContract","type":"address"}
	    ],
	    "Person":[{"name":"name","type":"string"},{"name":"wallet","type":"address"}],
	    "Mail":[{"name":"from","type":"Person"},{"name":"to","type":"Person"},{"name":"contents","type":"string"}]
	  },
	  "primaryType":"Mail",
	  "domain":{"name":"Ether Mail","version":"1","chainId":1,"verifyingContract":"0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC"},
	  "message":{
	    "from":{"name":"Cow","wallet":"0xCD2a3d9F938E13CD947Ec05AbC7FE734Df8DD826"},
	    "to":{"name":"Bob","wallet":"0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB"},
	    "contents":"Hello, Bob!"
	  }
	}`)

	sig, err := w.SignTypedData(context.Background(), typedData)
	if err != nil {
		t.Fatalf("SignTypedData: %v", err)
	}
	if len(sig) != 65 || (sig[64] != 27 && sig[64] != 28) {
		t.Fatalf("bad signature: len=%d v=%d", len(sig), sig[64])
	}
	_, _, digest, err := signer.TypedDataHashes(typedData)
	if err != nil {
		t.Fatal(err)
	}
	recoverEq(t, digest, sig, w)
}

// recoverEq asserts a v27/28 signature over digest recovers to the wallet address.
func recoverEq(t *testing.T, digest, sig []byte, w *Wallet) {
	t.Helper()
	rec := make([]byte, 65)
	copy(rec, sig)
	rec[64] -= 27
	pub, err := crypto.SigToPub(digest, rec)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if got := crypto.PubkeyToAddress(*pub); got != w.Address() {
		t.Errorf("recovered %s, want %s", got.Hex(), w.Address().Hex())
	}
}
