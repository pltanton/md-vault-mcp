package index

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrNoSearchableWords = errors.New("query has no searchable words")

// Hit lines are 1-based and inclusive; Start is 0 when only the file name matched.
type Hit struct {
	Path       string
	Start, End int
	Rank       float64
}

const searchSQL = `
WITH q AS (SELECT websearch_to_tsquery('md_vault', $1) AS q),
named AS (
	SELECT f.path, ts_rank_cd(f.name_tsv, q.q)::float8 AS rank
	FROM files f, q
	WHERE f.name_tsv @@ q.q AND ($2 = '' OR starts_with(f.path, $2))
),
secs AS (
	SELECT s.path, s.start_line, s.end_line,
		ts_rank_cd(s.tsv, q.q)::float8 * CASE WHEN n.path IS NULL THEN 1 ELSE 2 END AS rank
	FROM sections s CROSS JOIN q LEFT JOIN named n ON n.path = s.path
	WHERE s.tsv @@ q.q AND ($2 = '' OR starts_with(s.path, $2))
)
SELECT path, start_line, end_line, rank FROM secs
UNION ALL
SELECT n.path, 0, 0, n.rank FROM named n WHERE NOT EXISTS (SELECT 1 FROM secs WHERE secs.path = n.path)
ORDER BY rank DESC, path, start_line
LIMIT $3`

// Search returns up to limit hits under the directory under ("" for the whole vault) and whether more exist.
func (ix *Index) Search(ctx context.Context, query, under string, limit int) ([]Hit, bool, error) {
	query = normalize(query)
	var nodes int
	if err := ix.pool.QueryRow(ctx, "SELECT numnode(websearch_to_tsquery('md_vault', $1))", query).Scan(&nodes); err != nil {
		return nil, false, err
	}
	if nodes == 0 {
		return nil, false, ErrNoSearchableWords
	}
	prefix := ""
	if under != "" {
		prefix = under + "/"
	}
	rows, err := ix.pool.Query(ctx, searchSQL, query, prefix, limit+1)
	if err != nil {
		return nil, false, err
	}
	hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Hit, error) {
		var h Hit
		err := r.Scan(&h.Path, &h.Start, &h.End, &h.Rank)
		return h, err
	})
	if err != nil {
		return nil, false, err
	}
	if len(hits) > limit {
		return hits[:limit], true, nil
	}
	return hits, false, nil
}

const matchingLinesSQL = `
WITH lex AS (
	SELECT coalesce(array_agg(DISTINCT replace(m[1], '''''', '''')), '{}') AS l
	FROM regexp_matches(querytree(websearch_to_tsquery('md_vault', $1)), '''((?:[^'']|'''')+)''', 'g') AS m
)
SELECT (t.n - 1)::int
FROM unnest($2::text[]) WITH ORDINALITY AS t(line, n), lex
WHERE tsvector_to_array(to_tsvector('md_vault', t.line)) && lex.l
ORDER BY t.n`

// MatchingLines returns the 0-based indexes of lines holding a word the query looks for; excluded words don't count.
func (ix *Index) MatchingLines(ctx context.Context, query string, lines []string) ([]int, error) {
	norm := make([]string, len(lines))
	for i, l := range lines {
		norm[i] = normalize(l)
	}
	rows, err := ix.pool.Query(ctx, matchingLinesSQL, normalize(query), norm)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}
