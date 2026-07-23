// Command callisto is the entrypoint for the Callisto transaction system: a
// locally-run GUI for preparing, signing, and broadcasting Ethereum transactions.
//
// It loads persisted settings and the local database, wires the UI, and runs the
// Fyne event loop on the main goroutine.
package main

import (
	"log"

	"github.com/pasiphae00/callisto/internal/buildsecrets"
	"github.com/pasiphae00/callisto/internal/config"
	"github.com/pasiphae00/callisto/internal/rpc"
	"github.com/pasiphae00/callisto/internal/store"
	"github.com/pasiphae00/callisto/internal/ui"
)

func main() {
	// Resolve build-embedded RPC bearer tokens (e.g. the Ganymede default) at dial
	// time. Kept here (the composition root) so the rpc package stays secret-free.
	rpc.ResolveAuthToken = buildsecrets.Token

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("callisto: load config: %v", err)
	}

	st, err := store.Open()
	if err != nil {
		log.Fatalf("callisto: open database: %v", err)
	}
	defer func() { _ = st.Close() }()

	ui.New(cfg, st).Run()
}
