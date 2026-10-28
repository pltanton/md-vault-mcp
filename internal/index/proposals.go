package index

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrNotPending = errors.New("this proposal has already been decided")
	ErrNoProposal = errors.New("no such proposal")
)

type Proposal struct {
	ID, Kind, Path, NewPath, Status string
	Edits                           json.RawMessage
	LinkEdits                       json.RawMessage
	Reason                          string
	BaseHash                        string
	Result                          json.RawMessage
	CreatedAt                       time.Time
	DecidedAt                       time.Time
	DecidedBy                       string
	Comment                         string
}

type Decision struct {
	Status, By, Comment string
	Result              json.RawMessage
}

const proposalCols = `id, kind, path, coalesce(new_path, ''), status, edits, link_edits, coalesce(reason, ''),
	coalesce(base_hash, ''), result, created_at, decided_at, coalesce(decided_by, ''), coalesce(comment, '')`

func scanProposal(row pgx.Row) (Proposal, error) {
	var p Proposal
	var edits, linkEdits, result []byte
	var decided *time.Time
	err := row.Scan(&p.ID, &p.Kind, &p.Path, &p.NewPath, &p.Status, &edits, &linkEdits, &p.Reason, &p.BaseHash,
		&result, &p.CreatedAt, &decided, &p.DecidedBy, &p.Comment)
	p.Edits, p.LinkEdits, p.Result = edits, linkEdits, result
	if decided != nil {
		p.DecidedAt = *decided
	}
	return p, err
}

func rawJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

func (ix *Index) CreateProposal(ctx context.Context, p Proposal) error {
	_, err := ix.pool.Exec(ctx, `INSERT INTO proposals (id, kind, path, new_path, status, edits, reason, base_hash, link_edits)
		VALUES ($1, $2, $3, nullif($4, ''), 'pending', $5::jsonb, $6, nullif($7, ''), $8::jsonb)`,
		p.ID, p.Kind, p.Path, p.NewPath, rawJSON(p.Edits), p.Reason, p.BaseHash, rawJSON(p.LinkEdits))
	return err
}

func (ix *Index) Proposal(ctx context.Context, id string) (Proposal, bool, error) {
	p, err := scanProposal(ix.pool.QueryRow(ctx, "SELECT "+proposalCols+" FROM proposals WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, false, nil
	}
	return p, err == nil, err
}

func (ix *Index) collect(ctx context.Context, sql string, args ...any) ([]Proposal, error) {
	rows, err := ix.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Proposal, error) { return scanProposal(r) })
}

// Proposals lists newest first; an empty status means every status.
func (ix *Index) Proposals(ctx context.Context, status string, limit int) ([]Proposal, error) {
	return ix.collect(ctx, "SELECT "+proposalCols+` FROM proposals WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC, id LIMIT $2`, status, limit)
}

func (ix *Index) RecentDecisions(ctx context.Context, limit int) ([]Proposal, error) {
	return ix.collect(ctx, "SELECT "+proposalCols+` FROM proposals WHERE status <> 'pending'
		ORDER BY decided_at DESC NULLS LAST, id LIMIT $1`, limit)
}

// Decide locks a pending proposal, runs fn and records its decision; an error from fn leaves it pending.
func (ix *Index) Decide(ctx context.Context, id string, fn func(Proposal) (Decision, error)) (Proposal, error) {
	var out Proposal
	err := pgx.BeginFunc(ctx, ix.pool, func(tx pgx.Tx) error {
		p, err := scanProposal(tx.QueryRow(ctx, "SELECT "+proposalCols+" FROM proposals WHERE id = $1 FOR UPDATE", id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoProposal
		}
		if err != nil {
			return err
		}
		if p.Status != "pending" {
			return ErrNotPending
		}
		d, err := fn(p)
		if err != nil {
			return err
		}
		out, err = scanProposal(tx.QueryRow(ctx, `UPDATE proposals SET status = $2, decided_at = clock_timestamp(),
			decided_by = nullif($3, ''), comment = nullif($4, ''), result = $5::jsonb,
			edits = NULL, link_edits = NULL, reason = NULL
			WHERE id = $1 RETURNING `+proposalCols, id, d.Status, d.By, d.Comment, rawJSON(d.Result)))
		return err
	})
	return out, err
}

func (ix *Index) ExpireProposals(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := ix.pool.Exec(ctx, `UPDATE proposals SET status = 'expired', decided_at = clock_timestamp(),
		edits = NULL, link_edits = NULL, reason = NULL
		WHERE status = 'pending' AND created_at < clock_timestamp() - make_interval(secs => $1)`, olderThan.Seconds())
	return tag.RowsAffected(), err
}
