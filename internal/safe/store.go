package safe

import (
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// ProposalStatus is the lifecycle stage of a Safe proposal.
type ProposalStatus string

const (
	StatusCollecting ProposalStatus = "collecting" // gathering owner signatures
	StatusReady      ProposalStatus = "ready"       // threshold met, awaiting execution
	StatusExecuted   ProposalStatus = "executed"    // execTransaction mined successfully
	StatusRejected   ProposalStatus = "rejected"    // superseded by an executed rejection
	StatusFailed     ProposalStatus = "failed"      // execution reverted or errored
)

// ProposalKind classifies what a proposal does (for display and history).
type ProposalKind string

const (
	KindTransfer        ProposalKind = "transfer"
	KindAddOwner        ProposalKind = "add-owner"
	KindRemoveOwner     ProposalKind = "remove-owner"
	KindSwapOwner       ProposalKind = "swap-owner"
	KindChangeThreshold ProposalKind = "change-threshold"
	KindReject          ProposalKind = "reject"
	KindContractCall    ProposalKind = "contract-call" // a prepared/advanced action
)

// Signature is one owner's collected signature over a proposal's safeTxHash.
type Signature struct {
	Signer   common.Address
	Sig      []byte // 65 bytes
	SignedAt int64
}

// Proposal is a persisted Safe transaction proposal plus its collected signatures.
// The SafeTx fields are stored so the proposal is fully reconstructable across
// sessions and signer switches, independent of any live connection.
type Proposal struct {
	ID             int64
	SafeAddress    common.Address
	ChainID        uint64
	To             common.Address
	Value          *big.Int
	Data           []byte
	Operation      Operation
	SafeNonce      uint64
	SafeTxHash     common.Hash
	Kind           ProposalKind
	Description    string
	Status         ProposalStatus
	CreatedAt      int64
	ExecutedTxHash string
	Error          string
	Signatures     []Signature
}

// SafeTx reconstructs the SafeTx a proposal represents (for signing/execution).
func (p Proposal) SafeTx() SafeTx {
	value := p.Value
	if value == nil {
		value = big.NewInt(0)
	}
	return SafeTx{
		To:             p.To,
		Value:          new(big.Int).Set(value),
		Data:           p.Data,
		Operation:      p.Operation,
		SafeTxGas:      big.NewInt(0),
		BaseGas:        big.NewInt(0),
		GasPrice:       big.NewInt(0),
		GasToken:       common.Address{},
		RefundReceiver: common.Address{},
		Nonce:          new(big.Int).SetUint64(p.SafeNonce),
	}
}

// SignedBy reports whether an owner has already signed this proposal.
func (p Proposal) SignedBy(owner common.Address) bool {
	for _, s := range p.Signatures {
		if s.Signer == owner {
			return true
		}
	}
	return false
}

// SignatureMap returns the collected signatures keyed by signer, for PackSignatures.
func (p Proposal) SignatureMap() map[common.Address][]byte {
	out := make(map[common.Address][]byte, len(p.Signatures))
	for _, s := range p.Signatures {
		out[s.Signer] = s.Sig
	}
	return out
}

// ProposalRepo persists Safe proposals and their signatures. It takes a *sql.DB
// directly (rather than the store package) to avoid an import cycle with config.
type ProposalRepo struct {
	db *sql.DB
}

// NewProposalRepo returns a repo backed by the given database handle.
func NewProposalRepo(db *sql.DB) *ProposalRepo {
	return &ProposalRepo{db: db}
}

// Insert records a new proposal (status defaults to collecting) and returns its id.
func (r *ProposalRepo) Insert(p Proposal) (int64, error) {
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().Unix()
	}
	if p.Status == "" {
		p.Status = StatusCollecting
	}
	value := "0"
	if p.Value != nil {
		value = p.Value.String()
	}
	res, err := r.db.Exec(`
		INSERT INTO safe_proposals
			(safe_address, chain_id, to_address, value_wei, data, operation, safe_nonce,
			 safe_tx_hash, kind, description, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.SafeAddress.Hex(), p.ChainID, p.To.Hex(), value, hexutil.Encode(p.Data),
		uint8(p.Operation), p.SafeNonce, p.SafeTxHash.Hex(), string(p.Kind),
		p.Description, string(p.Status), p.CreatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddSignature records (or replaces) an owner's signature for a proposal.
func (r *ProposalRepo) AddSignature(proposalID int64, signer common.Address, sig []byte) error {
	_, err := r.db.Exec(`
		INSERT INTO safe_signatures (proposal_id, signer_address, signature, signed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(proposal_id, signer_address) DO UPDATE SET signature=excluded.signature, signed_at=excluded.signed_at`,
		proposalID, signer.Hex(), hexutil.Encode(sig), time.Now().Unix(),
	)
	return err
}

// SetStatus updates a proposal's status. executedTxHash and errMsg may be "".
func (r *ProposalRepo) SetStatus(id int64, status ProposalStatus, executedTxHash, errMsg string) error {
	_, err := r.db.Exec(
		`UPDATE safe_proposals SET status=?, executed_tx_hash=?, error=? WHERE id=?`,
		string(status), nullString(executedTxHash), nullString(errMsg), id,
	)
	return err
}

// MarkRejectedByNonce marks all still-open proposals at a Safe's given nonce as
// rejected, except the one that executed the rejection (keepID). Used after an
// on-chain rejection consumes the nonce, invalidating siblings.
func (r *ProposalRepo) MarkRejectedByNonce(safeAddr common.Address, chainID, nonce uint64, keepID int64) error {
	_, err := r.db.Exec(`
		UPDATE safe_proposals SET status=?
		WHERE safe_address=? AND chain_id=? AND safe_nonce=? AND id<>? AND status IN (?, ?)`,
		string(StatusRejected), safeAddr.Hex(), chainID, nonce, keepID,
		string(StatusCollecting), string(StatusReady),
	)
	return err
}

// ListBySafe returns the proposals for a Safe (newest first) with signatures loaded.
// CountActive returns the number of proposals still awaiting action (collecting or
// ready) across all Safes — used for the Safe nav-badge count.
func (r *ProposalRepo) CountActive() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM safe_proposals WHERE status IN (?, ?)`,
		string(StatusCollecting), string(StatusReady)).Scan(&n)
	return n, err
}

func (r *ProposalRepo) ListBySafe(safeAddr common.Address, chainID uint64) ([]Proposal, error) {
	rows, err := r.db.Query(`
		SELECT id, safe_address, chain_id, to_address, value_wei, data, operation, safe_nonce,
		       safe_tx_hash, kind, COALESCE(description,''), status, created_at,
		       COALESCE(executed_tx_hash,''), COALESCE(error,'')
		FROM safe_proposals
		WHERE safe_address=? AND chain_id=?
		ORDER BY created_at DESC, id DESC`,
		safeAddr.Hex(), chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		sigs, err := r.signaturesFor(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Signatures = sigs
	}
	return out, nil
}

// Get returns one proposal by id with its signatures loaded.
func (r *ProposalRepo) Get(id int64) (Proposal, error) {
	row := r.db.QueryRow(`
		SELECT id, safe_address, chain_id, to_address, value_wei, data, operation, safe_nonce,
		       safe_tx_hash, kind, COALESCE(description,''), status, created_at,
		       COALESCE(executed_tx_hash,''), COALESCE(error,'')
		FROM safe_proposals WHERE id=?`, id)
	p, err := scanProposal(row)
	if err != nil {
		return Proposal{}, err
	}
	sigs, err := r.signaturesFor(id)
	if err != nil {
		return Proposal{}, err
	}
	p.Signatures = sigs
	return p, nil
}

func (r *ProposalRepo) signaturesFor(proposalID int64) ([]Signature, error) {
	rows, err := r.db.Query(
		`SELECT signer_address, signature, signed_at FROM safe_signatures WHERE proposal_id=? ORDER BY signed_at ASC`,
		proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Signature
	for rows.Next() {
		var addr, sigHex string
		var at int64
		if err := rows.Scan(&addr, &sigHex, &at); err != nil {
			return nil, err
		}
		sig, err := hexutil.Decode(sigHex)
		if err != nil {
			return nil, fmt.Errorf("decode stored signature: %w", err)
		}
		out = append(out, Signature{Signer: common.HexToAddress(addr), Sig: sig, SignedAt: at})
	}
	return out, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanProposal(s scanner) (Proposal, error) {
	var (
		p          Proposal
		safeAddr   string
		to         string
		valueWei   string
		dataHex    string
		operation  uint8
		txHash     string
		kind       string
		status     string
		executedTx string
	)
	if err := s.Scan(
		&p.ID, &safeAddr, &p.ChainID, &to, &valueWei, &dataHex, &operation, &p.SafeNonce,
		&txHash, &kind, &p.Description, &status, &p.CreatedAt, &executedTx, &p.Error,
	); err != nil {
		return Proposal{}, err
	}
	p.SafeAddress = common.HexToAddress(safeAddr)
	p.To = common.HexToAddress(to)
	v, ok := new(big.Int).SetString(valueWei, 10)
	if !ok {
		v = big.NewInt(0)
	}
	p.Value = v
	data, err := hexutil.Decode(dataHex)
	if err != nil {
		return Proposal{}, fmt.Errorf("decode stored calldata: %w", err)
	}
	p.Data = data
	p.Operation = Operation(operation)
	p.SafeTxHash = common.HexToHash(txHash)
	p.Kind = ProposalKind(kind)
	p.Status = ProposalStatus(status)
	p.ExecutedTxHash = executedTx
	return p, nil
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
