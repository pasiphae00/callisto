package ui

import (
	"database/sql"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"codeberg.org/pasiphae/callisto/internal/store"
)

// tokenCache persists the auto-discovered token set + a per-(chain, account) scan
// watermark, so balances populate on launch without a fresh full Transfer-log scan
// and re-discovery only covers new blocks. It is a thin layer over the store's
// discovered_tokens / token_scan tables (mirrors internal/approvals.Cache).
//
// It lives in the ui package rather than assets because assets is imported by
// config, which store imports — putting it in assets would create an import cycle.
// Only the token *set* is cached; balances are always read live (they change every
// block).
type tokenCache struct {
	db *sql.DB
}

func newTokenCache(s *store.Store) *tokenCache { return &tokenCache{db: s.DB()} }

// list returns the cached token addresses for (chainID, account).
func (c *tokenCache) list(chainID uint64, account common.Address) ([]common.Address, error) {
	rows, err := c.db.Query(
		`SELECT token FROM discovered_tokens WHERE chain_id=? AND account=? ORDER BY found_block, token`,
		chainID, account.Hex())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []common.Address
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		out = append(out, common.HexToAddress(token))
	}
	return out, rows.Err()
}

// watermark returns the last scanned block for (chainID, account) and whether one
// exists.
func (c *tokenCache) watermark(chainID uint64, account common.Address) (uint64, bool, error) {
	var block int64
	err := c.db.QueryRow(`SELECT last_block FROM token_scan WHERE chain_id=? AND account=?`,
		chainID, account.Hex()).Scan(&block)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return uint64(block), true, nil
}

// add records any newly-discovered tokens (existing ones are left untouched) and
// advances the scan watermark to block, transactionally. Called after every scan —
// with an empty token list it just advances the watermark, so blocks already
// covered aren't re-scanned next time.
func (c *tokenCache) add(chainID uint64, account common.Address, tokens []common.Address, block uint64) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	for _, token := range tokens {
		if _, err := tx.Exec(`
			INSERT INTO discovered_tokens (chain_id, account, token, found_block, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(chain_id, account, token) DO NOTHING`,
			chainID, account.Hex(), token.Hex(), int64(block), now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO token_scan (chain_id, account, last_block, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chain_id, account) DO UPDATE SET last_block=excluded.last_block, updated_at=excluded.updated_at`,
		chainID, account.Hex(), int64(block), now); err != nil {
		return err
	}
	return tx.Commit()
}

// hiddenList returns the tokens the user has hidden for (chainID, account).
func (c *tokenCache) hiddenList(chainID uint64, account common.Address) ([]common.Address, error) {
	rows, err := c.db.Query(
		`SELECT token FROM hidden_tokens WHERE chain_id=? AND account=? ORDER BY token`,
		chainID, account.Hex())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []common.Address
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		out = append(out, common.HexToAddress(token))
	}
	return out, rows.Err()
}

// hide marks a token hidden for (chainID, account).
func (c *tokenCache) hide(chainID uint64, account, token common.Address) error {
	_, err := c.db.Exec(`
		INSERT INTO hidden_tokens (chain_id, account, token, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chain_id, account, token) DO NOTHING`,
		chainID, account.Hex(), token.Hex(), time.Now().Unix())
	return err
}

// unhide removes a token from the hidden set for (chainID, account).
func (c *tokenCache) unhide(chainID uint64, account, token common.Address) error {
	_, err := c.db.Exec(`DELETE FROM hidden_tokens WHERE chain_id=? AND account=? AND token=?`,
		chainID, account.Hex(), token.Hex())
	return err
}
