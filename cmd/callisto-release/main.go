// Command callisto-release is a maintainer-only tool for the release pipeline: it
// generates the ed25519 signing keypair and produces the detached signature over a
// release's SHA256SUMS. It is not shipped in the app bundle — it's invoked by the
// Makefile (`make gen-release-key`, `make sign`).
//
// The private key (a 32-byte ed25519 seed, hex-encoded) is written to a path
// outside the repo and MUST be kept offline. The public key (hex-encoded, 32
// bytes) is written to internal/updater/release_pubkey.ed25519, which is committed
// and embedded into the binary so the in-app updater can verify downloads.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"codeberg.org/pasiphae/callisto/internal/buildsecrets"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "genkey":
		err = genkey(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "obf-token":
		err = obfToken(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "callisto-release:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  callisto-release genkey --out <private-key-path> --pub <public-key-path>
  callisto-release sign   --key <private-key-path> --in <file> --out <sig-path>
  callisto-release verify --pub <public-key-path>  --in <file> --sig <sig-path>
  callisto-release obf-token   (reads GANYMEDE_RPC_TOKEN from the env, prints the obfuscated form)`)
	os.Exit(2)
}

// obfToken prints the build-time obfuscated form of $GANYMEDE_RPC_TOKEN, for the
// Makefile to inject via ldflags. Empty env → empty output (dev/no-token build).
func obfToken([]string) error {
	fmt.Print(buildsecrets.Obfuscate(os.Getenv("GANYMEDE_RPC_TOKEN")))
	return nil
}

// flags is a tiny --key value parser (avoids the flag package's per-subcommand
// ceremony for three fixed shapes).
func flags(args []string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		m[strings.TrimPrefix(args[i], "--")] = args[i+1]
	}
	return m
}

func genkey(args []string) error {
	f := flags(args)
	out, pub := f["out"], f["pub"]
	if out == "" || pub == "" {
		return fmt.Errorf("genkey needs --out and --pub")
	}
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// Persist the private key as its 32-byte seed (hex). 0600, and refuse to
	// clobber an existing key so a keypair is never silently overwritten.
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("refusing to overwrite existing key %s", out)
	}
	seed := privKey.Seed()
	if err := os.WriteFile(out, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pub, []byte(hex.EncodeToString(pubKey)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote private key -> %s (0600, keep offline)\n", out)
	fmt.Printf("wrote public key  -> %s (commit this)\n", pub)
	return nil
}

func sign(args []string) error {
	f := flags(args)
	keyPath, in, out := f["key"], f["in"], f["out"]
	if keyPath == "" || in == "" || out == "" {
		return fmt.Errorf("sign needs --key, --in and --out")
	}
	priv, err := loadPrivate(keyPath)
	if err != nil {
		return err
	}
	msg, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, msg)
	return os.WriteFile(out, []byte(hex.EncodeToString(sig)+"\n"), 0o644)
}

func verify(args []string) error {
	f := flags(args)
	pubPath, in, sigPath := f["pub"], f["in"], f["sig"]
	if pubPath == "" || in == "" || sigPath == "" {
		return fmt.Errorf("verify needs --pub, --in and --sig")
	}
	pub, err := loadHex(pubPath, ed25519.PublicKeySize)
	if err != nil {
		return err
	}
	msg, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	sig, err := loadHex(sigPath, ed25519.SignatureSize)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
		return fmt.Errorf("signature does NOT verify")
	}
	fmt.Println("signature OK")
	return nil
}

func loadPrivate(path string) (ed25519.PrivateKey, error) {
	seed, err := loadHex(path, ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func loadHex(path string, wantLen int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s: not valid hex: %w", path, err)
	}
	if len(b) != wantLen {
		return nil, fmt.Errorf("%s: expected %d bytes, got %d", path, wantLen, len(b))
	}
	return b, nil
}
