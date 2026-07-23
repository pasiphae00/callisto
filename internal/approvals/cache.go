package approvals

import (
	"database/sql"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/pasiphae00/callisto/internal/store"
)

// Cache persists discovered approvals + a per-(chain, owner) scan watermark so
// re-scans only need to cover new blocks. It is a thin domain layer over the
// store's approvals / approval_scan tables (mirrors internal/history).
type Cache struct {
	db *sql.DB
}

// NewCache returns a Cache backed by the given store.
func NewCache(s *store.Store) *Cache { return &Cache{db: s.DB()} }

// List returns the cached approvals for (chainID, owner), with spender labels
// re-derived from the current bundled map.
func (c *Cache) List(chainID uint64, owner common.Address) ([]Approval, error) {
	rows, err := c.db.Query(`
		SELECT layer, token, spender, token_symbol, token_decimals, amount, unlimited, expiration
		FROM approvals WHERE chain_id=? AND owner=? ORDER BY layer, token, spender`,
		chainID, owner.Hex())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Approval
	for rows.Next() {
		var (
			layer, decimals, unlimited int
			expiration                 int64
			token, spender, symbol     string
			amountStr                  string
		)
		if err := rows.Scan(&layer, &token, &spender, &symbol, &decimals, &amountStr, &unlimited, &expiration); err != nil {
			return nil, err
		}
		amount, ok := new(big.Int).SetString(amountStr, 10)
		if !ok {
			amount = big.NewInt(0)
		}
		sp := common.HexToAddress(spender)
		out = append(out, Approval{
			Layer:         Layer(layer),
			Token:         common.HexToAddress(token),
			TokenSymbol:   symbol,
			TokenDecimals: uint8(decimals),
			Spender:       sp,
			SpenderLabel:  spenderLabel(chainID, sp),
			Amount:        amount,
			Unlimited:     unlimited != 0,
			Expiration:    expiration,
		})
	}
	return out, rows.Err()
}

// Watermark returns the last scanned block for (chainID, owner) and whether one
// exists.
func (c *Cache) Watermark(chainID uint64, owner common.Address) (uint64, bool, error) {
	var block int64
	err := c.db.QueryRow(`SELECT last_block FROM approval_scan WHERE chain_id=? AND owner=?`,
		chainID, owner.Hex()).Scan(&block)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return uint64(block), true, nil
}

// Save replaces the cached approvals for (chainID, owner) with the given set and
// records the watermark, transactionally (used after a full or incremental scan).
func (c *Cache) Save(chainID uint64, owner common.Address, approvals []Approval, watermark uint64) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM approvals WHERE chain_id=? AND owner=?`, chainID, owner.Hex()); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, a := range approvals {
		if err := upsertTx(tx, chainID, owner, a, watermark, now); err != nil {
			return err
		}
	}
	if err := setWatermarkTx(tx, chainID, owner, watermark, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Upsert records or updates one approval (used by the live watcher).
func (c *Cache) Upsert(chainID uint64, owner common.Address, a Approval, block uint64) error {
	return upsertTx(c.db, chainID, owner, a, block, time.Now().Unix())
}

// Delete removes one approval (used when it is revoked / drops to zero).
func (c *Cache) Delete(chainID uint64, owner common.Address, a Approval) error {
	_, err := c.db.Exec(`DELETE FROM approvals WHERE chain_id=? AND owner=? AND layer=? AND token=? AND spender=?`,
		chainID, owner.Hex(), int(a.Layer), a.Token.Hex(), a.Spender.Hex())
	return err
}

// SetWatermark advances the scan watermark for (chainID, owner).
func (c *Cache) SetWatermark(chainID uint64, owner common.Address, block uint64) error {
	return setWatermarkTx(c.db, chainID, owner, block, time.Now().Unix())
}

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func upsertTx(e execer, chainID uint64, owner common.Address, a Approval, block uint64, now int64) error {
	amount := "0"
	if a.Amount != nil {
		amount = a.Amount.String()
	}
	unlimited := 0
	if a.Unlimited {
		unlimited = 1
	}
	_, err := e.Exec(`
		INSERT INTO approvals
			(chain_id, owner, layer, token, spender, token_symbol, token_decimals, amount, unlimited, expiration, updated_block, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chain_id, owner, layer, token, spender) DO UPDATE SET
			token_symbol=excluded.token_symbol, token_decimals=excluded.token_decimals,
			amount=excluded.amount, unlimited=excluded.unlimited, expiration=excluded.expiration,
			updated_block=excluded.updated_block, updated_at=excluded.updated_at`,
		chainID, owner.Hex(), int(a.Layer), a.Token.Hex(), a.Spender.Hex(),
		a.TokenSymbol, int(a.TokenDecimals), amount, unlimited, a.Expiration, int64(block), now)
	return err
}

func setWatermarkTx(e execer, chainID uint64, owner common.Address, block uint64, now int64) error {
	_, err := e.Exec(`
		INSERT INTO approval_scan (chain_id, owner, last_block, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chain_id, owner) DO UPDATE SET last_block=excluded.last_block, updated_at=excluded.updated_at`,
		chainID, owner.Hex(), int64(block), now)
	return err
}
