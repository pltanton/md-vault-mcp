package index

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock = 7235101

type Index struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*Index, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return &Index{pool: pool}, nil
}

func (ix *Index) Close() { ix.pool.Close() }

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version int PRIMARY KEY)"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT version FROM schema_migrations")
		if err != nil {
			return err
		}
		done, err := pgx.CollectRows(rows, pgx.RowTo[int])
		if err != nil {
			return err
		}
		applied := map[int]bool{}
		for _, v := range done {
			applied[v] = true
		}
		for _, name := range names {
			base := path.Base(name)
			v, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
			if err != nil {
				return fmt.Errorf("migration %s: %w", base, err)
			}
			if applied[v] {
				continue
			}
			sql, err := migrations.ReadFile(name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("migration %s: %w", base, err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", v); err != nil {
				return err
			}
		}
		return nil
	})
}

var yo = strings.NewReplacer("ё", "е", "\x00", "")

func normalize(s string) string {
	return yo.Replace(strings.ToLower(strings.ToValidUTF8(s, "")))
}
