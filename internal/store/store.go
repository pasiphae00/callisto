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

	"github.com/pasiphae/callisto/internal/config"
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
