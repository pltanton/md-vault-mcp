package index

import (
	"context"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
)

type FileStat struct {
	MtimeNS int64
	Size    int64
}

// Section lines are 1-based and inclusive.
type Section struct {
	Start, End int
	Text       string
}

func (ix *Index) Put(ctx context.Context, p string, st FileStat, sections []Section) error {
	name := strings.TrimSuffix(path.Base(p), path.Ext(p))
	return pgx.BeginFunc(ctx, ix.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", p); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM files WHERE path = $1", p); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO files (path, mtime_ns, size, name_tsv) VALUES ($1, $2, $3, to_tsvector('md_vault', $4))",
			p, st.MtimeNS, st.Size, normalize(name)); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for _, s := range sections {
			batch.Queue("INSERT INTO sections (path, start_line, end_line, tsv) VALUES ($1, $2, $3, to_tsvector('md_vault', $4))",
				p, s.Start, s.End, normalize(s.Text))
		}
		return tx.SendBatch(ctx, batch).Close()
	})
}

func (ix *Index) Stat(ctx context.Context) (map[string]FileStat, error) {
	rows, err := ix.pool.Query(ctx, "SELECT path, mtime_ns, size FROM files")
	if err != nil {
		return nil, err
	}
	out := map[string]FileStat{}
	var p string
	var st FileStat
	_, err = pgx.ForEachRow(rows, []any{&p, &st.MtimeNS, &st.Size}, func() error {
		out[p] = st
		return nil
	})
	return out, err
}

func (ix *Index) Delete(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := ix.pool.Exec(ctx, "DELETE FROM files WHERE path = ANY($1)", paths)
	return err
}
