# Full-text Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the substring `note_search` with ranked Postgres full-text search over note sections, lemmatizing Russian with hunspell, kept current by a startup scan and an fsnotify watcher.

**Architecture:** New package `internal/index` owns Postgres (migrations, `Put`/`Stat`/`Delete`/`Search`/`MatchingLines`) and knows nothing of Markdown or access rules. `internal/notes` splits notes into sections, feeds the index (`Sync`, `Reindex`, watcher, reindex after the agent's writes) and builds `note_search` results, reading headings and snippets from the files. The database stores no note text. Spec: `docs/superpowers/specs/2026-10-28-fulltext-search-design.md`.

**Tech Stack:** Go 1.26, `github.com/jackc/pgx/v5` (pgxpool), `github.com/fsnotify/fsnotify`, Postgres 17+ (tests use the builtin `C.UTF-8` locale provider), hunspell `ru_RU` from nixpkgs, NixOS module.

**Conventions for every task:**

- Work inside `nix develop` (after Task 1 it provides `initdb` with the dictionary). Integration tests skip when `initdb` is not on `PATH`, so a "PASS" outside the dev shell proves nothing — check the output says `ok`, not `ok ... [no tests to run]` and no `SKIP`.
- Follow `~/.claude/CLAUDE.md`: no comments unless they record a fact not recoverable from the code. The code below already follows this; don't add comments.
- Commits are GPG-signed. If `git commit` fails with `gpg: signing failed: Timeout`, stop and ask the user to unlock the key; never pass `--no-gpg-sign`.
- Every commit message ends with:
  ```
  Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
  ```

---

## File Structure

| File                                            | Responsibility                                                                                                |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `nix/hunspell-ru.nix` (new)                     | Russian hunspell dictionary converted to UTF-8 under `share/postgresql/tsearch_data`.                         |
| `flake.nix`                                     | Export `hunspell-ru`; dev shell gets Postgres with the dictionary.                                            |
| `internal/pgtest/pgtest.go` (new)               | Throwaway Postgres cluster for tests, one per test binary, one database per test.                             |
| `internal/index/index.go` (new)                 | `Open`, migrations, `Close`, `normalize`.                                                                     |
| `internal/index/store.go` (new)                 | `Put`, `Stat`, `Delete`.                                                                                      |
| `internal/index/search.go` (new)                | `Search`, `MatchingLines`, `ErrNoSearchableWords`.                                                            |
| `internal/index/migrations/0001_init.sql` (new) | Text search config `md_vault`, tables, GIN indexes.                                                           |
| `internal/index/index_test.go` (new)            | Integration tests of the index.                                                                               |
| `internal/notes/sections.go` (new)              | `splitSections`.                                                                                              |
| `internal/notes/sections_test.go` (new)         | Splitter table tests.                                                                                         |
| `internal/notes/indexer.go` (new)               | `UseIndex`, `Sync`, `Reindex`, `reindexQuietly`.                                                              |
| `internal/notes/search.go` (new)                | `Search` and its result types (moved out of `service.go`).                                                    |
| `internal/notes/search_test.go` (new)           | Indexed fixture, search and sync tests, `TestMain`.                                                           |
| `internal/notes/watch.go` (new)                 | `Watch`: fsnotify loop with debounce.                                                                         |
| `internal/notes/watch_test.go` (new)            | Watcher tests.                                                                                                |
| `internal/notes/service.go`                     | Drop old `Search`/`SearchMatch`/`SearchResult`; `index` field; reindex after writes.                          |
| `internal/notes/properties.go`                  | Reindex after property writes.                                                                                |
| `internal/notes/tools.go`                       | New `note_search` description and input schema; pass ctx.                                                     |
| `internal/notes/service_test.go`                | Old search assertions removed from `TestSearchAndListShowOnlyReadable` (becomes `TestListShowsOnlyReadable`). |
| `internal/notes/config.go`, `config_test.go`    | `database_url`, required.                                                                                     |
| `cmd/md-vault-mcp/main.go`                      | Open index, `UseIndex`, `Watch`.                                                                              |
| `md-vault-mcp.example.toml`                     | `database_url`.                                                                                               |
| `nix/module.nix`                                | Local Postgres database per instance, dictionary extension, `database.*` options.                             |
| `nix/package.nix`                               | `vendorHash`.                                                                                                 |
| `README.md`                                     | Search, storage promise, config key, NixOS.                                                                   |

---

### Task 1: Dictionary package and dev shell

**Files:**

- Create: `nix/hunspell-ru.nix`
- Modify: `flake.nix`

- [ ] **Step 1: Write the package**

`nix/hunspell-ru.nix`:

```nix
{
  lib,
  runCommand,
  hunspellDicts,
  glibc,
}:
runCommand "md-vault-mcp-hunspell-ru" {
  nativeBuildInputs = [glibc.bin];
  meta = {
    description = "Russian hunspell dictionary as a Postgres text search dictionary (md_vault_ru)";
    platforms = lib.platforms.linux;
  };
} ''
  d=$out/share/postgresql/tsearch_data
  mkdir -p $d
  src=${hunspellDicts.ru_RU}/share/hunspell
  iconv -f KOI8-R -t UTF-8 $src/ru_RU.aff | sed '1s/^SET KOI8-R/SET UTF-8/' > $d/md_vault_ru.affix
  iconv -f KOI8-R -t UTF-8 $src/ru_RU.dic > $d/md_vault_ru.dict
''
```

- [ ] **Step 2: Wire it into the flake**

In `flake.nix` replace the `packages` and `devShells` attributes with:

```nix
    packages = forAllSystems (pkgs: rec {
      md-vault-mcp = pkgs.callPackage ./nix/package.nix {};
      hunspell-ru = pkgs.callPackage ./nix/hunspell-ru.nix {};
      default = md-vault-mcp;
    });

    nixosModules.default = import ./nix/module.nix;

    devShells = forAllSystems (pkgs: {
      default = pkgs.mkShell {
        packages = [
          pkgs.go
          pkgs.gopls
          (pkgs.postgresql.withPackages (_: [(pkgs.callPackage ./nix/hunspell-ru.nix {})]))
        ];
      };
    });
```

- [ ] **Step 3: Verify the dictionary lands where Postgres looks**

Run:

```bash
nix build .#hunspell-ru && head -1 result/share/postgresql/tsearch_data/md_vault_ru.affix
nix develop -c sh -c 'ls "$(dirname "$(dirname "$(readlink -f "$(command -v initdb)")")")/share/postgresql/tsearch_data/" | grep md_vault'
```

Expected: `SET UTF-8`, then `md_vault_ru.affix` and `md_vault_ru.dict`.

- [ ] **Step 4: Commit**

```bash
git add nix/hunspell-ru.nix flake.nix
git commit -m "Russian hunspell dictionary for Postgres; dev shell with Postgres"
```

---

### Task 2: Test Postgres helper and dependencies

**Files:**

- Create: `internal/pgtest/pgtest.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependencies**

Run:

```bash
go get github.com/jackc/pgx/v5@latest github.com/fsnotify/fsnotify@latest
```

- [ ] **Step 2: Write the helper**

`internal/pgtest/pgtest.go`:

```go
package pgtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
)

var (
	once     sync.Once
	dir      string
	port     int
	startErr error
	dbs      atomic.Int64
)

func Main(m *testing.M) {
	code := m.Run()
	if dir != "" {
		exec.Command("pg_ctl", "-D", filepath.Join(dir, "data"), "-m", "immediate", "-w", "stop").Run()
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

// URL returns a fresh database owned by a non-superuser role, like production.
func URL(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("initdb"); err != nil {
		t.Skip("initdb is not on PATH; run the tests in nix develop")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatal(startErr)
	}
	name := fmt.Sprintf("t%d", dbs.Add(1))
	if err := exec1(connString("postgres", "postgres"), "CREATE DATABASE "+name+" OWNER app"); err != nil {
		t.Fatal(err)
	}
	return connString("app", name)
}

func connString(user, db string) string {
	return fmt.Sprintf("host=127.0.0.1 port=%d user=%s dbname=%s sslmode=disable", port, user, db)
}

func exec1(url, sql string) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

func start() {
	var err error
	if dir, err = os.MkdirTemp("", "pgtest"); err != nil {
		startErr = err
		return
	}
	data := filepath.Join(dir, "data")
	out, err := exec.Command("initdb", "-D", data, "-U", "postgres", "--auth=trust", "-E", "UTF8",
		"--locale-provider=builtin", "--locale=C.UTF-8").CombinedOutput()
	if err != nil {
		startErr = fmt.Errorf("initdb: %v\n%s", err, out)
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		startErr = err
		return
	}
	port = l.Addr().(*net.TCPAddr).Port
	l.Close()
	opts := fmt.Sprintf("-k '' -c listen_addresses=127.0.0.1 -p %d -c fsync=off", port)
	out, err = exec.Command("pg_ctl", "-D", data, "-o", opts, "-l", filepath.Join(dir, "log"), "-w", "start").CombinedOutput()
	if err != nil {
		startErr = fmt.Errorf("pg_ctl start: %v\n%s", err, out)
		return
	}
	startErr = exec1(connString("postgres", "postgres"), "CREATE ROLE app LOGIN")
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go vet ./internal/pgtest/`
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/pgtest/pgtest.go
git commit -m "pgtest: throwaway Postgres cluster for tests"
```

---

### Task 3: Index: open and migrate

**Files:**

- Create: `internal/index/index.go`, `internal/index/migrations/0001_init.sql`, `internal/index/index_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/index/index_test.go`:

```go
package index

import (
	"context"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

func open(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(context.Background(), pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ix.Close)
	return ix
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenMigratesOnce(t *testing.T) {
	url := pgtest.URL(t)
	for range 2 {
		ix, err := Open(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		ix.Close()
	}
}

func TestDictionaryLemmatizes(t *testing.T) {
	ix := open(t)
	var got string
	must(t, ix.pool.QueryRow(context.Background(),
		`SELECT to_tsvector('md_vault', 'подарки дня отца gifts ковальски')::text`).Scan(&got))
	if want := `'gift':4 'день':2 'ковальск':5 'отец':3 'подарок':1`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNormalize(t *testing.T) {
	if got := normalize("Ёлка ЁЖ\x00 \xffок"); got != "елка еж ок" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/index/`
Expected: FAIL to compile, `undefined: Open`.

- [ ] **Step 3: Write the migration**

`internal/index/migrations/0001_init.sql`:

```sql
CREATE TEXT SEARCH DICTIONARY md_vault_ru (
  TEMPLATE = ispell,
  DictFile = md_vault_ru,
  AffFile = md_vault_ru,
  StopWords = russian
);

CREATE TEXT SEARCH CONFIGURATION md_vault (COPY = russian);

ALTER TEXT SEARCH CONFIGURATION md_vault
  ALTER MAPPING FOR word, hword, hword_part WITH md_vault_ru, russian_stem;

CREATE TABLE files (
  path     text PRIMARY KEY,
  mtime_ns bigint NOT NULL,
  size     bigint NOT NULL,
  name_tsv tsvector NOT NULL
);

CREATE TABLE sections (
  path       text NOT NULL REFERENCES files (path) ON DELETE CASCADE,
  start_line int NOT NULL,
  end_line   int NOT NULL,
  tsv        tsvector NOT NULL,
  PRIMARY KEY (path, start_line)
);

CREATE INDEX sections_tsv ON sections USING gin (tsv);
CREATE INDEX files_name_tsv ON files USING gin (name_tsv);
```

- [ ] **Step 4: Write `index.go`**

`internal/index/index.go`:

```go
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
```

pgx runs an `Exec` without arguments over the simple protocol, which is what lets one migration file hold several statements.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/index/ -v`
Expected: `TestOpenMigratesOnce`, `TestDictionaryLemmatizes`, `TestNormalize` PASS (not SKIP). If the dictionary test fails with `could not open dictionary file`, the shell's Postgres lacks the dictionary — re-check Task 1 Step 3.

- [ ] **Step 6: Commit**

```bash
git add internal/index
git commit -m "index: Postgres schema with hunspell-backed md_vault config"
```

---

### Task 4: Index: Put, Stat, Delete

**Files:**

- Create: `internal/index/store.go`
- Modify: `internal/index/index_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/index/index_test.go`:

```go
func count(t *testing.T, ix *Index, table string) int {
	t.Helper()
	var n int
	must(t, ix.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func TestPutReplacesAndDeleteCascades(t *testing.T) {
	ix := open(t)
	ctx := context.Background()
	must(t, ix.Put(ctx, "a/Note.md", FileStat{MtimeNS: 1, Size: 10}, []Section{
		{Start: 1, End: 2, Text: "подарки"},
		{Start: 3, End: 3, Text: "шарф"},
	}))
	must(t, ix.Put(ctx, "a/Note.md", FileStat{MtimeNS: 2, Size: 5}, []Section{{Start: 1, End: 1, Text: "книга"}}))
	stats, err := ix.Stat(ctx)
	must(t, err)
	if len(stats) != 1 || stats["a/Note.md"] != (FileStat{MtimeNS: 2, Size: 5}) {
		t.Errorf("stats %+v", stats)
	}
	if n := count(t, ix, "sections"); n != 1 {
		t.Errorf("%d sections after replace", n)
	}
	must(t, ix.Delete(ctx, "a/Note.md", "never/indexed.md"))
	if n := count(t, ix, "files") + count(t, ix, "sections"); n != 0 {
		t.Errorf("%d rows after delete", n)
	}
	must(t, ix.Delete(ctx))
}

func TestStoresNoText(t *testing.T) {
	ix := open(t)
	var cols []string
	rows, err := ix.pool.Query(context.Background(), `
		SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_name IN ('files', 'sections') AND data_type NOT IN ('bigint', 'integer', 'tsvector')
		ORDER BY 1`)
	must(t, err)
	cols, err = pgx.CollectRows(rows, pgx.RowTo[string])
	must(t, err)
	if got := strings.Join(cols, " "); got != "files.path sections.path" {
		t.Errorf("text-like columns: %s", got)
	}
}
```

Add `"strings"` and `"github.com/jackc/pgx/v5"` to the test imports.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/index/`
Expected: FAIL to compile, `ix.Put undefined`.

- [ ] **Step 3: Write `store.go`**

`internal/index/store.go`:

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/index/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/index
git commit -m "index: put, stat and delete files"
```

---

### Task 5: Index: Search and MatchingLines

**Files:**

- Create: `internal/index/search.go`
- Modify: `internal/index/index_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/index/index_test.go`:

```go
func seed(t *testing.T, ix *Index) {
	t.Helper()
	ctx := context.Background()
	st := FileStat{MtimeNS: 1, Size: 1}
	must(t, ix.Put(ctx, "People/Alice.md", st, []Section{
		{Start: 1, End: 3, Text: "---\ntags: [person]\n---"},
		{Start: 4, End: 5, Text: "# Alice\n"},
		{Start: 6, End: 8, Text: "## Идеи для подарков\n- Книга\n- Шарф"},
	}))
	must(t, ix.Put(ctx, "Inbox/Подарки.md", st, []Section{{Start: 1, End: 1, Text: "список"}}))
	must(t, ix.Put(ctx, "Inbox/Календарь.md", st, []Section{{Start: 1, End: 2, Text: "# Даты\nдня рождения мамы"}}))
	must(t, ix.Put(ctx, "Inbox/Шарф.md", st, []Section{{Start: 1, End: 1, Text: "связать шарф"}}))
}

func hitKeys(hits []Hit) string {
	keys := make([]string, len(hits))
	for i, h := range hits {
		keys[i] = fmt.Sprintf("%s:%d-%d", h.Path, h.Start, h.End)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

func TestSearch(t *testing.T) {
	ix := open(t)
	seed(t, ix)
	ctx := context.Background()
	for _, c := range []struct{ query, under, want string }{
		{"подарок", "", "Inbox/Подарки.md:0-0 People/Alice.md:6-8"},
		{"книга шарф", "", "People/Alice.md:6-8"},
		{"день рождения", "", "Inbox/Календарь.md:1-2"},
		{"Шарф", "Inbox", "Inbox/Шарф.md:1-1"},
		{"шарф -связать", "", "Inbox/Шарф.md:0-0 People/Alice.md:6-8"},
		{"person", "", "People/Alice.md:1-3"},
		{"варежки", "", ""},
	} {
		hits, truncated, err := ix.Search(ctx, c.query, c.under, 10)
		must(t, err)
		if got := hitKeys(hits); got != c.want || truncated {
			t.Errorf("%q under %q: %s (truncated %v), want %s", c.query, c.under, got, truncated, c.want)
		}
	}
}

func TestSearchRanksNameMatchesHigher(t *testing.T) {
	ix := open(t)
	seed(t, ix)
	hits, _, err := ix.Search(context.Background(), "шарф", "", 10)
	must(t, err)
	if len(hits) != 2 || hits[0].Path != "Inbox/Шарф.md" || hits[0].Rank <= hits[1].Rank {
		t.Errorf("hits %+v", hits)
	}
}

func TestSearchTruncates(t *testing.T) {
	ix := open(t)
	seed(t, ix)
	hits, truncated, err := ix.Search(context.Background(), "шарф", "", 1)
	must(t, err)
	if len(hits) != 1 || !truncated {
		t.Errorf("%d hits, truncated %v", len(hits), truncated)
	}
}

func TestSearchNoWords(t *testing.T) {
	ix := open(t)
	if _, _, err := ix.Search(context.Background(), "и в", "", 10); !errors.Is(err, ErrNoSearchableWords) {
		t.Errorf("got %v", err)
	}
}

func TestMatchingLines(t *testing.T) {
	ix := open(t)
	got, err := ix.MatchingLines(context.Background(), "подарок -шарф",
		[]string{"## Идеи для подарков", "- Книга", "- Шарф подарочный", "подарки ещё"})
	must(t, err)
	if fmt.Sprint(got) != "[0 3]" {
		t.Errorf("got %v", got)
	}
}
```

Add `"errors"`, `"fmt"` and `"sort"` to the test imports.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/index/`
Expected: FAIL to compile, `ix.Search undefined`.

- [ ] **Step 3: Write `search.go`**

`internal/index/search.go`:

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/index/ -v`
Expected: all PASS. If `TestSearch` fails only on `"подарок"`, print `SELECT to_tsvector('md_vault', 'подарки')` — `'подарок':1` is expected; anything else means the dictionary isn't loaded.

- [ ] **Step 5: Commit**

```bash
git add internal/index
git commit -m "index: ranked section search and matching lines"
```

---

### Task 6: Section splitter

**Files:**

- Create: `internal/notes/sections.go`, `internal/notes/sections_test.go`

- [ ] **Step 1: Write the failing test**

`internal/notes/sections_test.go`:

````go
package notes

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitSections(t *testing.T) {
	long := "# Long\n" + strings.Repeat("x\n", 84)
	for _, c := range []struct {
		name, text string
		want       []span
	}{
		{"front matter, preamble, headings", "---\na: 1\n---\nintro\n# H1\ntext\n## H2\n\n### H3\nmore",
			[]span{{0, 3}, {3, 4}, {4, 6}, {6, 8}, {8, 10}}},
		{"heading in fenced code", "# A\n```\n# not\n```\n# B", []span{{0, 4}, {4, 5}}},
		{"blank preamble dropped", "\n\n# A\nx", []span{{2, 4}}},
		{"no headings", "a\nb", []span{{0, 2}}},
		{"front matter only", "---\na: 1\n---", []span{{0, 3}}},
		{"heading right after front matter", "---\na: 1\n---\n# A\nx", []span{{0, 3}, {3, 5}}},
		{"crlf", "# A\r\nx\r\n# B\r\n", []span{{0, 2}, {2, 3}}},
		{"long section cut", long, []span{{0, 40}, {40, 80}, {80, 85}}},
		{"empty", "", nil},
	} {
		got := splitSections(parseDoc([]byte(c.text)).lines)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
````

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/notes/ -run TestSplitSections`
Expected: FAIL to compile, `undefined: splitSections`.

- [ ] **Step 3: Write `sections.go`**

`internal/notes/sections.go`:

```go
package notes

import "strings"

const maxSectionLines = 40

// span is 0-based, end exclusive.
type span struct{ start, end int }

func splitSections(lines []string) []span {
	starts := []int{0}
	if fm := frontMatterEnd(lines); fm > 0 && fm < len(lines) {
		starts = append(starts, fm)
	}
	for _, h := range headings(lines) {
		if h.line != starts[len(starts)-1] {
			starts = append(starts, h.line)
		}
	}
	var out []span
	for i, s := range starts {
		e := len(lines)
		if i+1 < len(starts) {
			e = starts[i+1]
		}
		for a := s; a < e; a += maxSectionLines {
			b := min(a+maxSectionLines, e)
			if hasText(lines[a:b]) {
				out = append(out, span{a, b})
			}
		}
	}
	return out
}

func hasText(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/notes/ -run TestSplitSections -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notes/sections.go internal/notes/sections_test.go
git commit -m "notes: split notes into sections for the index"
```

---

### Task 7: Indexer and the new note_search

**Files:**

- Create: `internal/notes/indexer.go`, `internal/notes/search.go`, `internal/notes/search_test.go`
- Modify: `internal/notes/service.go` (remove lines 159–220: `SearchMatch`, `SearchResult`, `Search`, `snippet`; add `index` field), `internal/notes/tools.go`, `internal/notes/service_test.go:108-130`

- [ ] **Step 1: Write the failing tests**

Add a field to `fixture` in `internal/notes/service_test.go`:

```go
type fixture struct {
	t     *testing.T
	vault string
	state string
	svc   *Service
	clock time.Time
	dbURL string
}
```

`internal/notes/search_test.go`:

```go
package notes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/access"
	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

func (f *fixture) useIndex(rules access.RuleSet) {
	f.t.Helper()
	ctx := context.Background()
	if rules.Allow != nil || rules.Deny != nil {
		svc, err := NewService(Config{Vault: f.vault, StateDir: f.state, Read: rules, Write: rules})
		if err != nil {
			f.t.Fatal(err)
		}
		svc.now = f.svc.now
		f.svc = svc
	}
	ix, err := index.Open(ctx, f.dbURL)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(ix.Close)
	if err := f.svc.UseIndex(ctx, ix); err != nil {
		f.t.Fatal(err)
	}
}

func newIndexedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.dbURL = pgtest.URL(t)
	f.useIndex(access.RuleSet{})
	return f
}

func (f *fixture) search(query, under string) SearchResult {
	f.t.Helper()
	out, err := f.svc.Search(context.Background(), query, under, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func matchKeys(out SearchResult) string {
	keys := make([]string, len(out.Matches))
	for i, m := range out.Matches {
		keys[i] = fmt.Sprintf("%s:%d", m.Path, m.StartLine)
	}
	return strings.Join(keys, "|")
}

func TestSearchShowsOnlyReadable(t *testing.T) {
	f := newIndexedFixture(t)
	out := f.search("подарки", "")
	if got := matchKeys(out); got != julia+":6|Tasks.md:1" {
		t.Fatalf("got %s", got)
	}
	m := out.Matches[0]
	if m.Heading != "## Идеи для подарков" || m.EndLine != 9 || fmt.Sprint(m.Lines) != "[{6 ## Идеи для подарков}]" {
		t.Errorf("match %+v", m)
	}
	if got := matchKeys(f.search("покупки", "")); got != "00. Inbox/Покупки.md:0" {
		t.Errorf("name match: %s", got)
	}
	if got := matchKeys(f.search("подарок", "00. Inbox")); got != "" {
		t.Errorf("under: %s", got)
	}
	if _, err := f.svc.Search(context.Background(), "x", "02. Areas/Work", 0); err == nil {
		t.Error("search under a denied dir")
	}
}

func TestSearchAcrossLinesAndSnippets(t *testing.T) {
	f := newIndexedFixture(t)
	m := f.search("книга шарф", "").Matches
	if len(m) != 1 || fmt.Sprint(m[0].Lines) != "[{7 - Книга} {8 - Шарф}]" {
		t.Errorf("got %+v", m)
	}
	f.write("00. Inbox/Много.md", strings.Repeat("варежки\n", 5))
	f.useIndex(access.RuleSet{})
	m = f.search("варежки", "").Matches
	if len(m) != 1 || len(m[0].Lines) != 3 || m[0].Heading != "" {
		t.Errorf("got %+v", m)
	}
}

func TestSearchRejectsEmptyQueries(t *testing.T) {
	f := newIndexedFixture(t)
	for _, q := range []string{"", "  ", "и в"} {
		if _, err := f.svc.Search(context.Background(), q, "", 0); err == nil {
			t.Errorf("query %q accepted", q)
		}
	}
}

func TestSyncDropsGoneAndUnreadable(t *testing.T) {
	f := newIndexedFixture(t)
	if err := os.Remove(filepath.Join(f.vault, "Tasks.md")); err != nil {
		t.Fatal(err)
	}
	f.useIndex(access.RuleSet{Allow: []string{"02. Areas/Personal/**"}})
	if got := matchKeys(f.search("подарок", "")); got != julia+":6" {
		t.Errorf("after sync: %s", got)
	}
	f.useIndex(access.RuleSet{Allow: []string{"00. Inbox/**"}})
	if got := matchKeys(f.search("подарок", "")); got != "" {
		t.Errorf("after narrowing rules: %s", got)
	}
}

func TestSearchReindexesStaleRanges(t *testing.T) {
	f := newIndexedFixture(t)
	f.write(julia, "# Alice\n")
	m := f.search("шарф", "").Matches
	if len(m) != 1 || m[0].Path != julia || m[0].StartLine != 0 || m[0].Lines != nil {
		t.Fatalf("stale hit %+v", m)
	}
	if m := f.search("шарф", "").Matches; len(m) != 0 {
		t.Errorf("still found after reindex: %+v", m)
	}
}
```

In `internal/notes/service_test.go`, rename `TestSearchAndListShowOnlyReadable` to `TestListShowsOnlyReadable` and delete its search part: every line from `out, err := f.svc.Search("ПОДАР", "", 0)` through the closing `}` of the `f.svc.Search("x", "02. Areas/Work", 0)` check. The function then starts with `f := newFixture(t)` followed by `root, err := f.svc.List("")`.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/notes/`
Expected: FAIL to compile (`f.svc.UseIndex undefined`, `m.StartLine undefined`).

- [ ] **Step 3: Remove the old search from `service.go`**

Delete from `internal/notes/service.go` the `SearchMatch` and `SearchResult` types, `func (s *Service) Search` and `func snippet` (lines 159–220). Keep `maxSearchResults` and `maxSnippetRunes` constants. Add the index field:

```go
type Service struct {
	vault   *access.Vault
	journal *journal
	index   *index.Index
	window  time.Duration
	maxSize int64
	now     func() time.Time
}
```

with import `"github.com/pltanton/md-vault-mcp/internal/index"`. Remove `"unicode/utf8"` from the imports if nothing else in the file uses it (`go build` tells you).

- [ ] **Step 4: Write `indexer.go`**

`internal/notes/indexer.go`:

```go
package notes

import (
	"context"
	"io/fs"
	"log"
	"os"
	"strings"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

func (s *Service) UseIndex(ctx context.Context, ix *index.Index) error {
	s.index = ix
	return s.Sync(ctx)
}

func (s *Service) Sync(ctx context.Context) error {
	have, err := s.index.Stat(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var failed error
	err = s.walk(s.vault.Root(), "", func(rel, real string) bool {
		seen[rel] = true
		info, err := os.Stat(real)
		if err != nil {
			return true
		}
		if st, ok := have[rel]; ok && st == statOf(info) {
			return true
		}
		if failed = s.Reindex(ctx, rel); failed != nil {
			return false
		}
		return true
	})
	if err != nil {
		return err
	}
	if failed != nil {
		return failed
	}
	var gone []string
	for p := range have {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	return s.index.Delete(ctx, gone...)
}

func (s *Service) Reindex(ctx context.Context, rel string) error {
	real, clean, err := s.vault.ResolveRead(rel)
	if err != nil {
		return s.index.Delete(ctx, rel)
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return s.index.Delete(ctx, clean)
	}
	lines := st.doc.lines
	spans := splitSections(lines)
	secs := make([]index.Section, len(spans))
	for i, sp := range spans {
		secs[i] = index.Section{Start: sp.start + 1, End: sp.end, Text: strings.Join(lines[sp.start:sp.end], "\n")}
	}
	return s.index.Put(ctx, clean, statOf(st.info), secs)
}

func (s *Service) reindexQuietly(rel string) {
	if s.index == nil {
		return
	}
	if err := s.Reindex(context.Background(), rel); err != nil {
		log.Printf("index %s: %v", rel, err)
	}
}

func statOf(info fs.FileInfo) index.FileStat {
	return index.FileStat{MtimeNS: info.ModTime().UnixNano(), Size: info.Size()}
}
```

- [ ] **Step 5: Write `search.go`**

`internal/notes/search.go`:

```go
package notes

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

const maxSnippets = 3

type SearchLine struct {
	Line int    `json:"line" jsonschema:"1-based"`
	Text string `json:"text"`
}

type SearchMatch struct {
	Path      string       `json:"path"`
	Heading   string       `json:"heading,omitempty" jsonschema:"the section's heading line; absent for text before the first heading"`
	StartLine int          `json:"start_line,omitempty" jsonschema:"1-based first line of the section; absent when only the file name matched"`
	EndLine   int          `json:"end_line,omitempty" jsonschema:"1-based last line of the section"`
	Rank      float64      `json:"rank" jsonschema:"higher is more relevant"`
	Lines     []SearchLine `json:"lines,omitempty" jsonschema:"up to 3 lines of the section holding the query words"`
}

type SearchResult struct {
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated,omitempty"`
}

func (s *Service) Search(ctx context.Context, query, under string, limit int) (SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return SearchResult{}, errors.New("query must not be empty")
	}
	if s.index == nil {
		return SearchResult{}, errors.New("search index is not configured")
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	_, base, err := s.vault.ResolveDir(under)
	if err != nil {
		return SearchResult{}, err
	}
	hits, truncated, err := s.index.Search(ctx, query, base, limit)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Matches: []SearchMatch{}, Truncated: truncated}
	for _, h := range hits {
		if !s.vault.Policy.CanRead(h.Path) {
			continue
		}
		m := SearchMatch{Path: h.Path, Rank: h.Rank}
		if h.Start > 0 {
			if err := s.fillSection(ctx, query, h, &m); err != nil {
				return SearchResult{}, err
			}
		}
		out.Matches = append(out.Matches, m)
	}
	return out, nil
}

func (s *Service) fillSection(ctx context.Context, query string, h index.Hit, m *SearchMatch) error {
	real, clean, err := s.vault.ResolveRead(h.Path)
	if err != nil {
		s.reindexQuietly(h.Path)
		return nil
	}
	st, err := s.readFile(real, clean)
	if err != nil || h.End > len(st.doc.lines) {
		s.reindexQuietly(h.Path)
		return nil
	}
	lines := st.doc.lines[h.Start-1 : h.End]
	m.StartLine, m.EndLine = h.Start, h.End
	for _, hd := range headings(st.doc.lines) {
		if hd.line == h.Start-1 {
			m.Heading = strings.TrimSpace(lines[0])
			break
		}
	}
	idx, err := s.index.MatchingLines(ctx, query, lines)
	if err != nil {
		return err
	}
	for _, i := range idx[:min(len(idx), maxSnippets)] {
		m.Lines = append(m.Lines, SearchLine{Line: h.Start + i, Text: snippet(lines[i])})
	}
	return nil
}

func snippet(line string) string {
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > maxSnippetRunes {
		return string([]rune(line)[:maxSnippetRunes]) + "…"
	}
	return line
}
```

- [ ] **Step 6: Update the tool in `tools.go`**

Replace `searchInput`:

```go
type searchInput struct {
	Query string `json:"query" jsonschema:"words to find; all must occur in one section. \"exact phrase\", -word to exclude, a or b for alternatives"`
	Under string `json:"under,omitempty" jsonschema:"directory to search in; omit for every readable note"`
	Limit int    `json:"limit,omitempty" jsonschema:"at most this many matches, up to 50"`
}
```

Replace the `note_search` registration:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name: "note_search",
		Description: "Full-text search over the notes you may read. Word forms match (Russian and English: " +
			"подарки finds подарок), and the words may sit on different lines of one section. Returns sections " +
			"ranked by relevance with their heading, line range and up to 3 matching lines, plus notes whose " +
			"file name matches; read the note from start_line for the rest.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, SearchResult, error) {
		out, err := s.Search(ctx, in.Query, in.Under, in.Limit)
		return nil, out, err
	})
```

- [ ] **Step 7: Run tests**

Run: `go test ./internal/... -v 2>&1 | grep -E '^(=== RUN|--- |ok|FAIL|panic)' | grep -v '=== RUN'`
Expected: every `--- PASS`, no `--- SKIP`, `ok` for `internal/access`, `internal/index`, `internal/notes`.

- [ ] **Step 8: Commit**

```bash
git add internal/notes
git commit -m "note_search: ranked full-text search over sections"
```

---

### Task 8: Reindex after the agent's writes

**Files:**

- Modify: `internal/notes/service.go` (`Add`, `Create`, `changeBlock`), `internal/notes/properties.go` (`changeProperty`)
- Modify: `internal/notes/search_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/notes/search_test.go`:

```go
func TestSearchSeesAgentWrites(t *testing.T) {
	f := newIndexedFixture(t)
	add, err := f.svc.Add(julia, "- Варежки", "## Идеи для подарков")
	if err != nil {
		t.Fatal(err)
	}
	if got := matchKeys(f.search("варежки", "")); got != julia+":6" {
		t.Errorf("after add: %s", got)
	}
	if _, err := f.svc.Edit(add.ID, "- Перчатки"); err != nil {
		t.Fatal(err)
	}
	if got := matchKeys(f.search("варежки", "")); got != "" {
		t.Errorf("after edit: %s", got)
	}
	created, err := f.svc.Create("00. Inbox/Носки.md", "шерстяные носки")
	if err != nil {
		t.Fatal(err)
	}
	if got := matchKeys(f.search("шерстяные", "")); got != "00. Inbox/Носки.md:1" {
		t.Errorf("after create: %s", got)
	}
	if _, err := f.svc.Remove(created.ID); err != nil {
		t.Fatal(err)
	}
	if got := matchKeys(f.search("шерстяные", "")); got != "" {
		t.Errorf("after removing the created note: %s", got)
	}
	if _, err := f.svc.SetProperty("Tasks.md", "status", "срочно"); err != nil {
		t.Fatal(err)
	}
	if got := matchKeys(f.search("срочно", "")); got != "Tasks.md:1" {
		t.Errorf("after set property: %s", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/notes/ -run TestSearchSeesAgentWrites -v`
Expected: FAIL, `after add: ` (empty).

- [ ] **Step 3: Reindex after each write**

In `internal/notes/service.go`, `Add` and `Create` both end with:

```go
	})
	return res, err
}
```

Change both endings to:

```go
	})
	if err == nil {
		s.reindexQuietly(res.Path)
	}
	return res, err
}
```

Make the same change at the end of `changeBlock` in `service.go` and of `changeProperty` in `internal/notes/properties.go` (both end with `})` / `return res, err`).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/notes/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notes
git commit -m "Reindex a note right after the agent writes it"
```

---

### Task 9: Watcher

**Files:**

- Create: `internal/notes/watch.go`, `internal/notes/watch_test.go`

- [ ] **Step 1: Write the failing test**

`internal/notes/watch_test.go`:

```go
package notes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func (f *fixture) waitFor(query, want string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := f.svc.Search(context.Background(), query, "", 0)
		if err == nil && matchKeys(out) == want {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("search %q: got %s (%v), want %s", query, matchKeys(out), err, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWatcherKeepsIndexCurrent(t *testing.T) {
	f := newIndexedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := f.svc.Watch(ctx); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(f.vault, "00. Inbox", "Новое.md")

	f.write("00. Inbox/Новое.md", "варежки\n")
	f.waitFor("варежки", "00. Inbox/Новое.md:1")

	tmp := filepath.Join(f.vault, "00. Inbox", ".syncthing.Новое.md.tmp")
	if err := os.WriteFile(tmp, []byte("перчатки\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, note); err != nil {
		t.Fatal(err)
	}
	f.waitFor("перчатки", "00. Inbox/Новое.md:1")
	f.waitFor("варежки", "")

	f.write("00. Inbox/Глубже/Ещё/Заметка.md", "носки\n")
	f.waitFor("носки", "00. Inbox/Глубже/Ещё/Заметка.md:1")

	if err := os.Remove(note); err != nil {
		t.Fatal(err)
	}
	f.waitFor("перчатки", "")

	if err := os.RemoveAll(filepath.Join(f.vault, "00. Inbox", "Глубже")); err != nil {
		t.Fatal(err)
	}
	f.waitFor("носки", "")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/notes/ -run TestWatcher`
Expected: FAIL to compile, `f.svc.Watch undefined`.

- [ ] **Step 3: Write `watch.go`**

`internal/notes/watch.go`:

```go
package notes

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/pltanton/md-vault-mcp/internal/access"
)

const watchDebounce = 300 * time.Millisecond

// Watch subscribes to the vault and keeps the index current until ctx ends.
func (s *Service) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := s.watchTree(w, s.vault.Root()); err != nil {
		w.Close()
		return err
	}
	go s.watchLoop(ctx, w)
	return nil
}

func (s *Service) watchTree(w *fsnotify.Watcher, top string) error {
	return filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == top {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, ok := s.vaultRel(p)
		if !ok || rel != "" && !s.vault.Policy.MayHoldReadable(rel) {
			return filepath.SkipDir
		}
		return w.Add(p)
	})
}

func (s *Service) vaultRel(real string) (string, bool) {
	r, err := filepath.Rel(s.vault.Root(), real)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	if r == "." {
		return "", true
	}
	return filepath.ToSlash(r), true
}

// An empty path in the queue means a full Sync.
func (s *Service) watchLoop(ctx context.Context, w *fsnotify.Watcher) {
	defer w.Close()
	due := make(chan string)
	timers := map[string]*time.Timer{}
	schedule := func(rel string) {
		if t, ok := timers[rel]; ok {
			t.Reset(watchDebounce)
			return
		}
		timers[rel] = time.AfterFunc(watchDebounce, func() {
			select {
			case due <- rel:
			case <-ctx.Done():
			}
		})
	}
	for {
		select {
		case <-ctx.Done():
			for _, t := range timers {
				t.Stop()
			}
			return
		case rel := <-due:
			delete(timers, rel)
			var err error
			if rel == "" {
				err = s.Sync(ctx)
			} else {
				err = s.Reindex(ctx, rel)
			}
			if err != nil && ctx.Err() == nil {
				log.Printf("index %q: %v", rel, err)
			}
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			rel, ok := s.vaultRel(ev.Name)
			if !ok || rel == "" || access.Hidden(rel) {
				continue
			}
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					if s.vault.Policy.MayHoldReadable(rel) {
						if err := s.watchTree(w, ev.Name); err != nil {
							log.Printf("watch %s: %v", rel, err)
						}
						schedule("")
					}
					continue
				}
			}
			switch {
			case s.vault.Policy.IsNote(rel):
				schedule(rel)
			case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
				schedule("")
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				schedule("")
				continue
			}
			log.Printf("watch: %v", err)
		}
	}
}
```

A new directory schedules a full `Sync` because files can land in it before the watch on it is added; a removed path that is not a note may have been a directory, and `Sync` drops whatever was under it.

- [ ] **Step 4: Run tests with the race detector**

Run: `go test -race ./internal/notes/ -run 'TestWatcher|TestSearch' -v 2>&1 | grep -E '^(--- |ok|FAIL|WARNING: DATA RACE)'`
Expected: all PASS, no `DATA RACE`.

- [ ] **Step 5: Commit**

```bash
git add internal/notes/watch.go internal/notes/watch_test.go
git commit -m "Watch the vault and reindex changed notes"
```

---

### Task 10: Config and main

**Files:**

- Modify: `internal/notes/config.go`, `internal/notes/config_test.go`, `md-vault-mcp.example.toml`, `cmd/md-vault-mcp/main.go`

- [ ] **Step 1: Write the failing tests**

In `internal/notes/config_test.go`, extend the check in `TestLoadExampleConfig`:

```go
	if cfg.EditWindow != 48*time.Hour || cfg.Read.Mode != "allowlist" || len(cfg.Write.Allow) != 2 || cfg.Listen != "127.0.0.1:8093" ||
		cfg.DatabaseURL != "host=/run/postgresql dbname=md_vault_mcp" {
		t.Errorf("got %+v", cfg)
	}
```

and add:

```go
func TestConfigNeedsDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("vault = \"/v\"\nstate_dir = \"/s\"\n"), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "database_url") {
		t.Errorf("got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/notes/ -run 'Config' -v`
Expected: FAIL to compile, `cfg.DatabaseURL undefined`.

- [ ] **Step 3: Add the key**

In `internal/notes/config.go` add `DatabaseURL string` to `Config` (after `StateDir`), `DatabaseURL string \`toml:"database_url"\``to`fileConfig`(after`StateDir`), `DatabaseURL: fc.DatabaseURL,`to the`cfg := Config{...}`literal, and after the`state_dir` check:

```go
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: database_url is required")
	}
```

In `md-vault-mcp.example.toml` after the `state_dir` line add:

```toml
database_url = "host=/run/postgresql dbname=md_vault_mcp"
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/notes/ -run 'Config' -v`
Expected: PASS.

- [ ] **Step 5: Wire the index into `main.go`**

In `cmd/md-vault-mcp/main.go`, in `run`, replace everything from `svc, err := notes.NewService(cfg)` through `defer stop()` with:

```go
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc, err := notes.NewService(cfg)
	if err != nil {
		return err
	}
	ix, err := index.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := svc.UseIndex(ctx, ix); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if err := svc.Watch(ctx); err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "md-vault", Version: version}, nil)
	svc.Register(server)
```

and add the import `"github.com/pltanton/md-vault-mcp/internal/index"`.

- [ ] **Step 6: Smoke-test the binary against a real database**

Run (inside `nix develop`):

```bash
S=$(mktemp -d) && initdb -D $S/data -U postgres --auth=trust -E UTF8 --locale-provider=builtin --locale=C.UTF-8 >/dev/null \
 && pg_ctl -D $S/data -o "-k $S -c listen_addresses=''" -l $S/log -w start >/dev/null \
 && psql -h $S -U postgres -c 'CREATE DATABASE smoke' >/dev/null \
 && mkdir -p $S/vault && printf '# Идеи\nподарки маме\n' > $S/vault/Note.md \
 && printf 'vault = "%s/vault"\nstate_dir = "%s/state"\ndatabase_url = "host=%s user=postgres dbname=smoke"\n[read]\nmode = "denylist"\n[write]\nmode = "denylist"\n' $S $S $S > $S/c.toml \
 && printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"note_search","arguments":{"query":"подарок"}}}' \
   | timeout 5 go run ./cmd/md-vault-mcp -config $S/c.toml | tail -1; pg_ctl -D $S/data stop >/dev/null; echo "dir: $S"
```

Expected: the last line is the `id: 2` response with `"path":"Note.md"`, `"start_line":1` and a line `подарки маме`. If the unix socket path is too long for Postgres (`$TMPDIR` deep), set `TMPDIR=/tmp` for this command.

- [ ] **Step 7: Commit**

```bash
git add internal/notes/config.go internal/notes/config_test.go md-vault-mcp.example.toml cmd/md-vault-mcp/main.go
git commit -m "database_url: open the index and watch the vault at startup"
```

---

### Task 11: NixOS module

**Files:**

- Modify: `nix/module.nix`

- [ ] **Step 1: Add the options**

In `instanceType`, after `extensions`, add:

```nix
      database = {
        createLocally = lib.mkOption {
          type = lib.types.bool;
          default = true;
          description = ''
            Create a role and a database md_vault_mcp_<name> in the local
            services.postgresql and connect over its unix socket with peer
            authentication mapped from the instance's user.
          '';
        };
        url = lib.mkOption {
          type = lib.types.nullOr lib.types.str;
          default = null;
          description = "Connection string used when createLocally is off.";
        };
      };
```

- [ ] **Step 2: Use them in the service**

In the top `let`, after `toml = ...`, add:

```nix
  dbName = name: "md_vault_mcp_" + lib.replaceStrings ["-"] ["_"] name;
  local = lib.filterAttrs (_: inst: inst.database.createLocally) cfg.instances;
```

In `service`, add to the attribute set passed to `toml.generate` (next to `state_dir`):

```nix
        database_url =
          if inst.database.createLocally
          then "host=/run/postgresql dbname=${dbName name} user=${dbName name}"
          else inst.database.url;
```

and replace `after = ["network.target"];` with:

```nix
      after = ["network.target"] ++ lib.optional inst.database.createLocally "postgresql.service";
      requires = lib.optional inst.database.createLocally "postgresql.service";
```

- [ ] **Step 3: Configure Postgres**

In `config = lib.mkIf (cfg.instances != {}) { ... }` add:

```nix
    assertions =
      lib.mapAttrsToList (name: inst: {
        assertion = inst.database.createLocally || inst.database.url != null;
        message = "services.md-vault-mcp.instances.${name}: set database.url or keep database.createLocally";
      })
      cfg.instances;

    services.postgresql = lib.mkIf (local != {}) {
      enable = true;
      extensions = [(pkgs.callPackage ./hunspell-ru.nix {})];
      ensureDatabases = map dbName (lib.attrNames local);
      ensureUsers = map (name: {
        name = dbName name;
        ensureDBOwnership = true;
      }) (lib.attrNames local);
      identMap = lib.concatLines (lib.mapAttrsToList (name: inst: "md-vault-mcp ${inst.user} ${dbName name}") local);
      authentication = lib.concatLines (map (name: "local ${dbName name} ${dbName name} peer map=md-vault-mcp") (lib.attrNames local));
    };
```

- [ ] **Step 4: Evaluate the module**

Run:

```bash
nix eval --impure --json --expr '
let
  nixpkgs = builtins.getFlake "nixpkgs";
  sys = nixpkgs.lib.nixosSystem {
    system = "x86_64-linux";
    modules = [
      ./nix/module.nix
      {
        boot.isContainer = true;
        system.stateVersion = "25.11";
        services.md-vault-mcp.instances.my-notes = {
          user = "anton";
          vault = "/v";
          listen = "127.0.0.1:8093";
          tokenFile = "/t";
        };
      }
    ];
  };
  c = sys.config;
in {
  dbs = c.services.postgresql.ensureDatabases;
  ident = c.services.postgresql.identMap;
  hba = c.services.postgresql.authentication;
  requires = c.systemd.services.md-vault-mcp-my-notes.requires;
  pg = c.services.postgresql.finalPackage.name;
}'
```

Expected: `dbs` is `["md_vault_mcp_my_notes"]`, `ident` contains `md-vault-mcp anton md_vault_mcp_my_notes`, `hba` starts with `local md_vault_mcp_my_notes md_vault_mcp_my_notes peer map=md-vault-mcp`, `requires` is `["postgresql.service"]`, `pg` is `postgresql-and-plugins-…`.

Then add `exec = c.systemd.services.md-vault-mcp-my-notes.serviceConfig.ExecStart;` to the attribute set, run it again, and `nix build` + `cat` the `.toml` store path after `-config`. Expected line: `database_url = "host=/run/postgresql dbname=md_vault_mcp_my_notes user=md_vault_mcp_my_notes"`.

- [ ] **Step 5: Commit**

```bash
git add nix/module.nix
git commit -m "NixOS module: a local Postgres database per instance"
```

---

### Task 12: Package hash, README, final check

**Files:**

- Modify: `nix/package.nix`, `README.md`

- [ ] **Step 1: Fix `vendorHash`**

Set `vendorHash = lib.fakeHash;` in `nix/package.nix`, run `nix build .#md-vault-mcp 2>&1 | grep 'got:'`, put the printed hash into `vendorHash`, run `nix build .#md-vault-mcp` again. Expected: builds, `result/bin/md-vault-mcp -version` prints `0.1.0`.

- [ ] **Step 2: Update the README**

1. Replace the "No copy of your notes" bullet (lines 21–22) with:

```markdown
- **No copy of your text.** The journal of what the agent wrote stores keyed
  hashes and line counts; the search index stores lemmatized words, paths, line
  ranges, sizes and modification times. Neither stores note text.
```

2. Replace the `note_search` row of the tools table with:

```markdown
| `note_search(query, under?, limit?)` | Full-text search: Russian and English word forms match, words may sit on different lines of one section. Sections ranked by relevance with heading, line range and up to 3 matching lines; notes whose file name matches. `"phrase"`, `-word`, `a or b`. |
```

3. Before `### How the agent's blocks are tracked` add:

```markdown
### Search

The index lives in Postgres (`database_url`). A note is cut into sections at
every heading (and at the front matter); a section longer than 40 lines is cut
into 40-line pieces. Russian words are lemmatized with the hunspell `ru_RU`
dictionary (`подарки` → `подарок`, `дня` → `день`), falling back to the Snowball
stemmer for words it lacks; English words use the Snowball stemmer. `ё` counts as
`е`. At startup the server reindexes notes whose size or modification time
changed and drops those that are gone or no longer readable, then follows
changes with inotify; its own writes are reindexed before the tool returns.
Headings and matching lines are read from the files when a search runs.

The dictionary must be in Postgres' `tsearch_data` directory: the flake builds it
as `packages.<system>.hunspell-ru`, and the NixOS module installs it.
```

4. In the Configuration table, after the `state_dir` row, add:

```markdown
| `database_url` | — | Postgres connection string; the role must own the database (migrations create a text search configuration). |
```

5. In `## NixOS`, after the paragraph ending "cannot reach the other vault.", add:

```markdown
By default each instance gets a local Postgres database and role
`md_vault_mcp_<name>` (dashes become underscores), reached over the unix socket
with peer authentication mapped from the instance's `user`, so no password
exists and one instance cannot open another's database. The module enables
`services.postgresql` and adds the dictionary to its extensions, which restarts
Postgres on the first deploy. To use another server, set
`database.createLocally = false` and `database.url`.
```

6. In `## Development`, change the block to:

````markdown
```sh
nix develop
go test ./...
```

The dev shell has Postgres with the dictionary; the tests start a throwaway
cluster with `initdb` and skip the database tests when it is not on `PATH`.
````

- [ ] **Step 3: Full verification**

Run inside `nix develop`:

```bash
gofmt -l . && go vet ./... && go test -race -count=1 ./... 2>&1 | tail -5 && go test -count=1 -v ./... 2>&1 | grep -c -- '--- SKIP'
```

Expected: `gofmt` prints nothing, `vet` is silent, three `ok` lines, and the SKIP count is `0`.

- [ ] **Step 4: Commit**

```bash
git add nix/package.nix README.md
git commit -m "README: full-text search, Postgres and the dictionary"
```
