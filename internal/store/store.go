// Package store is Callisto's local persistence for structured, queryable data:
// the transaction history and the contract "address book" that grows over time.
//
// It uses modernc.org/sqlite — a pure-Go SQLite (no CGo) — so Callisto stays a
// single static binary. Inert settings (RPC endpoints, wallet descriptors) live
// in the JSON config instead; this store is for records that benefit from SQL
// queries. No key material is ever written here.
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"codeberg.org/pasiphae/callisto/internal/config"
)

// DBFile is the database filename within the Callisto config directory.
const DBFile = "callisto.db"

// Store wraps the SQLite handle and provides typed accessors (added per phase).
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the Callisto database at the default path
// under the config directory and applies migrations.
func Open() (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return OpenAt(filepath.Join(dir, DBFile))
}

// OpenAt opens a database at an explicit path (used by tests). Pass ":memory:"
// for an ephemeral in-memory database.
func OpenAt(path string) (*Store, error) {
	// _pragma options enable foreign keys and a sane busy timeout.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc's driver is not safe for unbounded concurrent writers; a single
	// connection keeps write ordering simple for a desktop app.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying handle for packages that own their own queries.
func (s *Store) DB() *sql.DB {
	return s.db
}

// migrations are applied in order; the schema_version table records progress so
// re-running Open is idempotent. Each entry is a self-contained DDL step.
var migrations = []string{
	// 1: transaction history — one row per transaction Callisto helped prepare.
	`CREATE TABLE IF NOT EXISTS tx_history (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		chain_id       INTEGER NOT NULL,
		wallet_address TEXT    NOT NULL,
		kind           TEXT    NOT NULL,            -- e.g. "send-eth", "send-erc20"
		instructions   TEXT,                        -- human/NL description if any
		to_address     TEXT,
		value_wei      TEXT,                         -- decimal string (may exceed int64)
		tx_hash        TEXT,
		status         TEXT    NOT NULL DEFAULT 'prepared', -- prepared|signed|submitted|included|failed
		block_number   INTEGER,
		block_time     INTEGER,                      -- unix seconds
		prepared_at    INTEGER NOT NULL,             -- unix seconds
		signed_at      INTEGER,
		submitted_at   INTEGER,
		included_at    INTEGER,
		error          TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_tx_history_wallet ON tx_history(wallet_address, chain_id)`,
	`CREATE INDEX IF NOT EXISTS idx_tx_history_hash ON tx_history(tx_hash)`,

	// 2: contract address book — decoded contract metadata accreted over time so
	// pre-sign review can name contracts/functions instead of showing raw calldata.
	`CREATE TABLE IF NOT EXISTS contracts (
		chain_id   INTEGER NOT NULL,
		address    TEXT    NOT NULL,               -- lowercased hex
		name       TEXT,
		abi_json   TEXT,                            -- optional cached ABI
		source     TEXT,                            -- where the metadata came from
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (chain_id, address)
	)`,

	// 3: known 4-byte function selectors -> human signature, for calldata decode.
	`CREATE TABLE IF NOT EXISTS selectors (
		selector   TEXT PRIMARY KEY,               -- "0x12345678"
		signature  TEXT NOT NULL                    -- "transfer(address,uint256)"
	)`,

	// 4: Safe multisig proposals — one row per proposed Safe transaction, tracked
	// through signature collection and execution. The Safe tx fields are stored so
	// a proposal survives across sessions and signer switches.
	`CREATE TABLE IF NOT EXISTS safe_proposals (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		safe_address     TEXT    NOT NULL,          -- EIP-55 Safe address
		chain_id         INTEGER NOT NULL,
		to_address       TEXT    NOT NULL,          -- inner call target
		value_wei        TEXT    NOT NULL,          -- decimal string
		data             TEXT    NOT NULL,          -- 0x-hex inner calldata ("0x" if none)
		operation        INTEGER NOT NULL DEFAULT 0,-- 0=Call
		safe_nonce       INTEGER NOT NULL,
		safe_tx_hash     TEXT    NOT NULL,          -- canonical hash owners sign
		kind             TEXT    NOT NULL,          -- transfer|add-owner|remove-owner|swap-owner|change-threshold|reject
		description      TEXT,                       -- human summary
		status           TEXT    NOT NULL DEFAULT 'collecting', -- collecting|ready|executed|rejected|failed
		created_at       INTEGER NOT NULL,
		executed_tx_hash TEXT,                       -- outer EOA tx hash once executed
		error            TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_safe_proposals_safe ON safe_proposals(safe_address, chain_id)`,
	`CREATE INDEX IF NOT EXISTS idx_safe_proposals_hash ON safe_proposals(safe_tx_hash)`,

	// 5: collected owner signatures for a proposal (one row per owner). The
	// (proposal, signer) pair is unique so re-signing replaces rather than dupes.
	`CREATE TABLE IF NOT EXISTS safe_signatures (
		proposal_id    INTEGER NOT NULL REFERENCES safe_proposals(id) ON DELETE CASCADE,
		signer_address TEXT    NOT NULL,            -- EIP-55 owner address
		signature      TEXT    NOT NULL,            -- 0x-hex 65-byte signature
		signed_at      INTEGER NOT NULL,
		PRIMARY KEY (proposal_id, signer_address)
	)`,

	// 6: cached token approvals for the Approvals pane, so re-scans are incremental.
	// The PK is ordered (chain_id, owner, …) so listing by (chain_id, owner) is a
	// prefix scan — no separate index needed.
	`CREATE TABLE IF NOT EXISTS approvals (
		chain_id       INTEGER NOT NULL,
		owner          TEXT    NOT NULL,           -- EIP-55 owner address
		layer          INTEGER NOT NULL,           -- 0=direct ERC-20, 1=Permit2
		token          TEXT    NOT NULL,           -- token contract (EIP-55)
		spender        TEXT    NOT NULL,           -- spender contract (EIP-55)
		token_symbol   TEXT,
		token_decimals INTEGER NOT NULL DEFAULT 0,
		amount         TEXT    NOT NULL,           -- decimal string (base units)
		unlimited      INTEGER NOT NULL DEFAULT 0,
		expiration     INTEGER NOT NULL DEFAULT 0, -- Permit2 expiry (unix), 0 if none
		updated_block  INTEGER NOT NULL DEFAULT 0,
		updated_at     INTEGER NOT NULL,
		PRIMARY KEY (chain_id, owner, layer, token, spender)
	)`,

	// 7: per-(chain, owner) scan high-watermark, so a rescan only covers new blocks.
	`CREATE TABLE IF NOT EXISTS approval_scan (
		chain_id   INTEGER NOT NULL,
		owner      TEXT    NOT NULL,
		last_block INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (chain_id, owner)
	)`,

	// 8: auto-discovered tokens per (chain, account) — the set an account has ever
	// received (from Transfer-log scans), so balances populate on launch without a
	// fresh full scan. Balances themselves are always read live; only the token set
	// is cached. The PK is a (chain_id, account) prefix for cheap per-account listing.
	`CREATE TABLE IF NOT EXISTS discovered_tokens (
		chain_id    INTEGER NOT NULL,
		account     TEXT    NOT NULL,            -- EIP-55 account address
		token       TEXT    NOT NULL,            -- token contract (EIP-55)
		found_block INTEGER NOT NULL DEFAULT 0,  -- block the token was first seen at
		updated_at  INTEGER NOT NULL,
		PRIMARY KEY (chain_id, account, token)
	)`,

	// 9: per-(chain, account) token-scan high-watermark, so re-discovery only covers
	// blocks since the last scan (across launches). Separate from discovered_tokens
	// because a watermark exists even for an account that has received nothing.
	`CREATE TABLE IF NOT EXISTS token_scan (
		chain_id   INTEGER NOT NULL,
		account    TEXT    NOT NULL,
		last_block INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (chain_id, account)
	)`,

	// 10: user-hidden ("spam") tokens per (chain, account). A hidden token is kept
	// in discovered_tokens (so unhiding needs no re-scan) but filtered out of the
	// balance view and the Send picker, and its balance isn't fetched each block.
	`CREATE TABLE IF NOT EXISTS hidden_tokens (
		chain_id   INTEGER NOT NULL,
		account    TEXT    NOT NULL,
		token      TEXT    NOT NULL,            -- token contract (EIP-55)
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (chain_id, account, token)
	)`,
}

// migrate applies any not-yet-applied migrations inside a transaction.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	row := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, i+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion returns the highest applied migration number.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}
