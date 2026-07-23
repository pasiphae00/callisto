// keystore-recover is a standalone, local-only diagnostic tool for troubleshooting
// a Callisto keystore file that won't unlock. It never sends anything over the
// network. Run it directly in your own terminal so the passphrase never passes
// through any other tool or transcript:
//
//	go run ./cmd/keystore-recover [-expect 0xAddress] [-repair] /path/to/keystore.json
//
// Background: a keystore's "secret" field labels what the encrypted bytes are --
// "private-key" for a single raw-key import, or absent/"seed" for an HD wallet
// seed. keystore.Rekey (used by "Change passphrase") used to silently drop this
// label, so a raw-private-key wallet that ever had its passphrase changed gets
// mis-derived as an HD seed thereafter, deriving the WRONG address even with the
// correct passphrase -- decryption succeeds, only the derived address is wrong.
// This tool decrypts with the passphrase you type (plus a few deterministic
// variants of it -- whitespace/newline trimmed, Unicode NFC/NFD normalized, curly
// vs straight quotes), then shows the address BOTH ways so you can see which one
// is actually right, regardless of what the file's "secret" label currently says.
//
// -expect compares against a known-correct address and reports match/no-match.
// -repair, only once a match is found, rewrites the file's "secret" field to
// match (after writing a timestamped .bak alongside it first). It never touches
// ciphertext/salt/nonce -- only ever adds/corrects the "secret" label.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
	"golang.org/x/text/unicode/norm"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/pasiphae00/callisto/internal/keystore"
	"github.com/pasiphae00/callisto/internal/signer/hot"
)

func main() {
	expect := flag.String("expect", "", "expected 0x address, to check which interpretation is correct")
	repair := flag.Bool("repair", false, "if a matching interpretation is found, fix the file's \"secret\" label (backs up the original first)")
	reveal := flag.Bool("reveal", false, "on success, also print the account private key (sensitive)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: keystore-recover [-expect 0xAddr] [-repair] [-reveal] /path/to/keystore.json")
		os.Exit(2)
	}
	path := flag.Arg(0)

	ks, err := keystore.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load %s: %v\n", path, err)
		os.Exit(1)
	}
	label := ks.Secret
	if label == "" {
		label = "(empty -> treated as \"seed\")"
	}
	fmt.Printf("keystore: version=%d kdf=%s cipher=%s secret=%s created_at=%d\n",
		ks.Version, ks.KDF, ks.Cipher, label, ks.CreatedAt)

	fmt.Print("passphrase: ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read passphrase: %v\n", err)
		os.Exit(1)
	}
	pw := string(pwBytes)

	candidates := buildCandidates(pw)
	var (
		secret   []byte
		which    string
		usedCand string
	)
	for desc, cand := range candidates {
		if s, derr := ks.Decrypt(cand); derr == nil {
			secret = s
			which = desc
			usedCand = cand
			break
		}
	}
	if secret == nil {
		fmt.Println("\nno candidate passphrase decrypted this file. Tried:")
		for desc := range candidates {
			fmt.Println(" -", desc)
		}
		os.Exit(1)
	}
	defer zero(secret)
	fmt.Printf("\ndecryption SUCCEEDED via variant: %s\n", which)
	fmt.Printf("secret is %d bytes\n\n", len(secret))

	// Show both interpretations regardless of what the file's label says.
	var pkAddr, seedAddr string
	if priv, perr := crypto.ToECDSA(secret); perr == nil {
		pkAddr = crypto.PubkeyToAddress(priv.PublicKey).Hex()
		fmt.Printf("as a raw PRIVATE KEY  -> address %s\n", pkAddr)
	} else {
		fmt.Printf("as a raw PRIVATE KEY  -> not valid (%v)\n", perr)
	}
	if w, werr := hot.OpenFromKeystore(mustLabelSeed(ks), usedCand, hot.DefaultPath(0)); werr == nil {
		seedAddr = w.Address().Hex()
		w.Lock()
		fmt.Printf("as an HD SEED (path %s) -> address %s\n", hot.DefaultPath(0), seedAddr)
	} else {
		fmt.Printf("as an HD SEED -> error (%v)\n", werr)
	}

	if *expect != "" {
		want := common.HexToAddress(*expect)
		fmt.Println()
		switch {
		case pkAddr != "" && common.HexToAddress(pkAddr) == want:
			fmt.Println("MATCH: this is a raw-private-key keystore mislabeled as a seed.")
			if *repair {
				repairSecret(path, "private-key")
			} else {
				fmt.Println("Re-run with -repair to fix the file's \"secret\" label.")
			}
		case seedAddr != "" && common.HexToAddress(seedAddr) == want:
			fmt.Println("MATCH: this is correctly an HD seed keystore; the address matches as-is.")
		default:
			fmt.Println("NEITHER interpretation matches -expect. Something else is going on -- stop here and don't repair blindly.")
		}
	}

	if *reveal {
		fmt.Printf("\nprivate key: 0x%x\n", secret)
	}
}

// mustLabelSeed returns a shallow copy of ks with Secret forced to "" so
// OpenFromKeystore always takes the HD-seed path, regardless of the file's
// current (possibly wrong) label -- used only to show the alternate
// interpretation, never to persist anything.
func mustLabelSeed(ks *keystore.Keystore) *keystore.Keystore {
	cp := *ks
	cp.Secret = ""
	return &cp
}

// repairSecret patches only the "secret" field of the keystore JSON at path,
// leaving every other field untouched. It writes path+".bak-<unixtime>" first.
func repairSecret(path, secretLabel string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repair: read: %v\n", err)
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "repair: parse: %v\n", err)
		return
	}
	backup := fmt.Sprintf("%s.bak-%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "repair: write backup: %v\n", err)
		return
	}
	raw["secret"] = secretLabel
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "repair: encode: %v\n", err)
		return
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "repair: write: %v\n", err)
		return
	}
	fmt.Printf("repaired: %s (backup at %s)\n", path, backup)
}

// buildCandidates returns the raw passphrase plus deterministic, order-stable
// variants of it, keyed by a human description (no passphrase content in the key).
func buildCandidates(pw string) map[string]string {
	out := map[string]string{"as typed": pw}
	add := func(desc, v string) {
		if v != pw {
			if _, exists := out[desc]; !exists {
				out[desc] = v
			}
		}
	}
	add("trimmed whitespace", strings.TrimSpace(pw))
	add("NFC-normalized", norm.NFC.String(pw))
	add("NFD-normalized", norm.NFD.String(pw))
	add("trimmed + NFC", norm.NFC.String(strings.TrimSpace(pw)))
	add("smart quotes -> straight", straightenQuotes(pw))
	add("straight quotes -> smart", curlifyQuotes(pw))
	add("trimmed trailing newline only", strings.TrimRight(pw, "\r\n"))
	return out
}

var quoteReplacer = strings.NewReplacer(
	"‘", "'", "’", "'", // single smart quotes
	"“", "\"", "”", "\"", // double smart quotes
	"–", "-", "—", "-", // en/em dash -> hyphen
)

func straightenQuotes(s string) string { return quoteReplacer.Replace(s) }

func curlifyQuotes(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteRune('’')
		case '"':
			b.WriteRune('”')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
