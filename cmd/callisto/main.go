// Command callisto is the entrypoint for the Callisto transaction system: a
// locally-run GUI for preparing, signing, and broadcasting Ethereum transactions.
//
// It loads persisted settings and the local database, wires the UI, and runs the
// Fyne event loop on the main goroutine.
package main

import (
	"log"

	"codeberg.org/pasiphae/callisto/internal/config"
	"codeberg.org/pasiphae/callisto/internal/store"
	"codeberg.org/pasiphae/callisto/internal/ui"
)

func main() {
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
