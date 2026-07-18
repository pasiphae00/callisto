// Package history records the transactions Callisto has prepared and tracks them
// through their lifecycle (prepared → submitted → included/failed). It is a thin
// domain layer over the store's tx_history table so the UI can persist and list
// records without embedding SQL.
package history

import (
	"database/sql"
	"time"

	"codeberg.org/pasiphae/callisto/internal/store"
)

// Status is the lifecycle stage of a recorded transaction.
type Status string

const (
	StatusPrepared  Status = "prepared"
	StatusSubmitted Status = "submitted"
	StatusIncluded  Status = "included"
	StatusFailed    Status = "failed"
)

// Record is one row of transaction history. Optional timestamps/fields use their
// zero value when unset.
type Record struct {
	ID            int64
	ChainID       uint64
	WalletAddress string
	Kind          string // e.g. "send-eth", "send-erc20"
	Instructions  string
	ToAddress     string
	ValueWei      string
	TxHash        string
	Status        Status
	BlockNumber   int64
	BlockTime     int64
	PreparedAt    int64
	SubmittedAt   int64
	IncludedAt    int64
	Error         string
}

// Repo persists and queries transaction history.
type Repo struct {
	db *sql.DB
}

// New returns a Repo backed by the given store.
func New(s *store.Store) *Repo {
	return &Repo{db: s.DB()}
}

// Insert records a newly prepared/submitted transaction and returns its id.
func (r *Repo) Insert(rec Record) (int64, error) {
	if rec.PreparedAt == 0 {
		rec.PreparedAt = time.Now().Unix()
	}
	if rec.Status == "" {
		rec.Status = StatusPrepared
	}
	res, err := r.db.Exec(`
		INSERT INTO tx_history
			(chain_id, wallet_address, kind, instructions, to_address, value_wei, tx_hash, status, prepared_at, submitted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ChainID, rec.WalletAddress, rec.Kind, rec.Instructions, rec.ToAddress,
		rec.ValueWei, nullString(rec.TxHash), string(rec.Status), rec.PreparedAt, nullInt(rec.SubmittedAt),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MarkSubmitted records that a transaction was accepted by a node, with its hash.
func (r *Repo) MarkSubmitted(id int64, txHash string) error {
	_, err := r.db.Exec(
		`UPDATE tx_history SET status=?, tx_hash=?, submitted_at=? WHERE id=?`,
		string(StatusSubmitted), txHash, time.Now().Unix(), id,
	)
	return err
}

// MarkIncluded records inclusion in a block with execution outcome.
func (r *Repo) MarkIncluded(id int64, blockNumber, blockTime int64, success bool) error {
	status := StatusIncluded
	if !success {
		status = StatusFailed
	}
	_, err := r.db.Exec(
		`UPDATE tx_history SET status=?, block_number=?, block_time=?, included_at=? WHERE id=?`,
		string(status), blockNumber, blockTime, time.Now().Unix(), id,
	)
	return err
}

// MarkError records a terminal error (e.g. broadcast rejected).
func (r *Repo) MarkError(id int64, msg string) error {
	_, err := r.db.Exec(
		`UPDATE tx_history SET status=?, error=? WHERE id=?`,
		string(StatusFailed), msg, id,
	)
	return err
}

// List returns the most recent records, newest first.
func (r *Repo) List(limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(`
		SELECT id, chain_id, wallet_address, kind, instructions,
		       COALESCE(to_address,''), COALESCE(value_wei,''), COALESCE(tx_hash,''),
		       status, COALESCE(block_number,0), COALESCE(block_time,0),
		       prepared_at, COALESCE(submitted_at,0), COALESCE(included_at,0), COALESCE(error,'')
		FROM tx_history
		ORDER BY prepared_at DESC, id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var rec Record
		var status string
		if err := rows.Scan(
			&rec.ID, &rec.ChainID, &rec.WalletAddress, &rec.Kind, &rec.Instructions,
			&rec.ToAddress, &rec.ValueWei, &rec.TxHash,
			&status, &rec.BlockNumber, &rec.BlockTime,
			&rec.PreparedAt, &rec.SubmittedAt, &rec.IncludedAt, &rec.Error,
		); err != nil {
			return nil, err
		}
		rec.Status = Status(status)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
