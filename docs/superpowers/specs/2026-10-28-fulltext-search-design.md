# Full-text search with stemming (Postgres)

Status: approved 2026-10-28. Sub-project 1 of 2; the second one (agent edits of
foreign text through an owner review queue with a web UI) gets its own spec and
reuses the Postgres setup introduced here.

## Goal

Replace the substring `note_search` with ranked full-text search that matches
word forms (Russian and English), finds query words spread across lines of one
section, and keeps itself current while the owner edits notes in Obsidian. Leave
room for vector search (pgvector, RAG) without a redesign.

## Decisions

- Postgres is required. The substring search is removed; there is no fallback
  mode without a database.
- The database stores no note text, not even headings: only lexemes
  (`tsvector`), paths, sizes, mtimes and line ranges. Headings and snippets are
  read from the files at query time.
- The unit of search is a section, not a line or a note.
- Only notes the instance may read are indexed. Hidden paths (`access.Hidden`,
  e.g. `.stversions`, `.syncthing.*.tmp`) never are.
- Text search configuration `md_vault`, created by a migration: a copy of the
  built-in `russian` where Cyrillic words (`word`, `hword`, `hword_part`) go
  first through the synonym dictionary `ru_stem_fixes`
  (github:pltanton/ru-stem-fixes: every OpenCorpora form whose Snowball stem
  differs from its lemma's, mapped onto the lemma's stem) and fall back to
  `russian_stem`; ASCII words keep `english_stem`. Snowball alone was measured
  to miss fleeting vowels (`подарок`/`подарки`, `день`/`дня`). The hunspell
  `ru_RU` dictionary was tried first and dropped: it lists oblique forms such as
  `ковра` and `сна` as words of their own.
- Text and queries are normalized in Go before they reach Postgres: lowercased,
  `ё` replaced with `е`, invalid
  UTF-8 and NUL bytes dropped (Postgres rejects them). Lowercasing in Go also
  keeps Cyrillic case folding independent of the cluster's locale.
- Query syntax: `websearch_to_tsquery` (quoted phrases, `-word`, `or`).
- Index freshness: full mtime/size scan at startup plus an fsnotify watcher;
  the agent's own writes are reindexed synchronously.

## Deployment

### Config

New required key `database_url` in the TOML config (a libpq/pgx connection
string, e.g. `host=/run/postgresql dbname=md_vault_mcp_anton`).

### Dictionary package

The flake input `ru-stem-fixes` provides
`share/postgresql/tsearch_data/ru_stem_fixes.syn`. Postgres reads dictionaries
only from its share directory, which `postgresql.withPackages` merges, so the
package is added the same way as an extension; Postgres itself is not rebuilt.
The module takes it through `services.md-vault-mcp.stemFixesPackage`, which the
flake's `nixosModules.default` sets.

### NixOS module

The module adds the dictionary package to `services.postgresql.extensions`.
For every instance `<name>` it:

- enables `services.postgresql` and adds, via `ensureDatabases` and
  `ensureUsers` with `ensureDBOwnership = true`, a role and a database both
  named `md_vault_mcp_<name>` (dashes in `<name>` become underscores);
- maps the instance's unix user to that role with `services.postgresql.identMap`
  and a `local <db> <role> peer map=md-vault-mcp` line in
  `services.postgresql.authentication`, so each instance can open only its own
  database and no password exists;
- sets `database_url` to `host=/run/postgresql dbname=md_vault_mcp_<name>
user=md_vault_mcp_<name>`;
- orders the service `after` and `requires` `postgresql.service`.

Instance option `database.createLocally` (default `true`) turns the above off;
then `database.url` must be set and is passed through as is.

## Schema

Migrations are SQL files embedded in the binary and applied at startup inside a
transaction, tracked in `schema_migrations(version)`. Driver: `pgx/v5`.

```sql
CREATE TABLE files (
  path     text PRIMARY KEY,
  mtime_ns bigint NOT NULL,
  size     bigint NOT NULL,
  name_tsv tsvector NOT NULL
);

CREATE TABLE sections (
  path       text NOT NULL REFERENCES files(path) ON DELETE CASCADE,
  start_line int  NOT NULL,
  end_line   int  NOT NULL,
  tsv        tsvector NOT NULL,
  PRIMARY KEY (path, start_line)
);

CREATE INDEX sections_tsv ON sections USING gin (tsv);
CREATE INDEX files_name_tsv ON files USING gin (name_tsv);
```

`name_tsv` is built from the file name without extension. Lines are 1-based and
inclusive. Vector search later adds `sections.embedding vector(N)` in one more
migration.

## Sections

A note is split into non-overlapping sections:

- the front matter, if present, is one section;
- every heading line (any level, outside front matter and fenced code — the
  rules `markdown.go` already implements) starts a new section that runs to the
  line before the next heading;
- lines before the first heading form a section of their own;
- a section longer than 40 lines is cut into consecutive pieces of at most 40
  lines;
- sections with no non-blank lines are dropped.

The splitter is a pure function `[]string -> []Section{Start, End}` in
`internal/notes` (it needs the Markdown parsing there).

## Components

### `internal/index`

Owns the database. No knowledge of access rules or Markdown.

- `Open(ctx, url) (*Index, error)` — connect and migrate.
- `Stat(ctx) (map[path]FileStat, error)` — what is indexed.
- `Put(ctx, path, stat, sections []Text)` — in one transaction delete the
  file's rows and insert the file and its sections; `to_tsvector('md_vault', $n)`
  is computed by Postgres from parameters, nothing textual is stored.
- `Delete(ctx, paths...)`.
- `Search(ctx, query, under, limit) ([]Hit, error)` — see below.
- `MatchingLines(ctx, query, lines []string) ([]int, error)` — indexes of the
  given lines whose own `to_tsvector` shares a lexeme with the query, one round
  trip; used for snippets.
- Empty query after parsing (`numnode(websearch_to_tsquery(...)) = 0`) returns
  `ErrNoSearchableWords`.

### Indexer (in `internal/notes`)

Bridges files and the index; uses `Service.walk`, `readFile` and the splitter.

- `Sync(ctx)` — walk readable notes, reindex those whose mtime or size differ
  from `Stat`, delete rows for paths that are gone or no longer readable. Runs at
  startup before the server accepts requests; a startup failure stops the
  process.
- `Reindex(ctx, rel)` — read one file and `Put` it, or `Delete` it if it is gone,
  unreadable by policy, too large or unparsable.
- Watcher: fsnotify on every directory that `MayHoldReadable`; new directories
  are added as they appear. Events per path are debounced for 300 ms, then
  `Reindex`. A rename counts as remove of the old path and create of the new one.
  Queue overflow (`fsnotify.ErrEventOverflow`) triggers `Sync`.
- Every tool that writes a note (`note_add`, `note_create`, `note_edit`,
  `note_remove`, `note_set_property`, `note_unset_property`) calls `Reindex` for
  that path before returning. An indexing error is logged and does not fail the
  tool: the file is written, and the next event or `Sync` repairs the index.

## `note_search(query, under?, limit?)`

1. Parse with `websearch_to_tsquery('md_vault', query)`; no searchable words →
   error `query has no searchable words`.
2. Candidates: sections whose `tsv` matches, plus files whose `name_tsv`
   matches. Rank: `ts_rank_cd(tsv, q)`; a section of a file whose name also
   matches gets its rank doubled. A file that matches by name only yields one
   hit with no section.
3. Filter by `under` as a path prefix (`under` resolved through the vault the
   same way as today), order by rank, cut to `limit` (default and max 50) and
   set `truncated` when more exist.
4. Each hit is checked with `Policy.CanRead` before it is returned.
5. For each section hit read the file, take the section's lines, get matching
   lines with `MatchingLines` and return up to 3 as snippets (`snippet()` as
   today). The heading is the section's first line if it is a heading, else
   empty. If the file changed since indexing and the range no longer fits, the
   file is reindexed and the hit is returned without snippets.

Result:

```json
{
  "matches": [
    {
      "path": "02. Areas/Personal/Gifts.md",
      "heading": "## Gift ideas",
      "start_line": 12,
      "end_line": 30,
      "rank": 0.42,
      "lines": [{ "line": 15, "text": "подарки маме: ..." }]
    }
  ],
  "truncated": false
}
```

For a name-only hit `heading`, `start_line`, `end_line` and `lines` are absent.

## Errors

- Database unreachable or migration failure at startup: exit with the error;
  systemd restarts the service.
- Database error during a search: returned to the client as is.
- Database error while reindexing after a write: logged, tool succeeds.
- Unreadable or oversized file during `Sync`/`Reindex`: its rows are deleted,
  the file is skipped.

## Testing

- Splitter: table tests — headings of all levels, headings in fenced code and
  front matter, text before the first heading, 40-line cuts, blank sections,
  CRLF.
- Index and search: integration tests against a real Postgres. A test helper
  runs `initdb` in `t.TempDir()` and starts a server on a unix socket there; the
  tests skip when `initdb` is not on `PATH`. Cases: stemming (`gifts` finds
  `gift`), words on different lines of one section,
  `under`, ranking order, name-only match, deleted file, a file that became
  unreadable after a rule change, stop-words-only query, agent write visible to
  the next search.
- Watcher: write a file and poll search until it appears; Syncthing-style write
  to a hidden temp file followed by rename; new subdirectory.
- Word forms: `подарок`/`подарки`, `день`/`дня`, `ковёр`/`ковра`, `рот`/`рту`,
  `идти`/`шла`, `ребёнок`/`ребенка`.
- Migrations run as a non-superuser role that owns its database, as in
  production.
- `devShell` gets `postgresql.withPackages (_: [ru-stem-fixes])`, so `initdb` on
  `PATH` has the dictionary. The package build keeps running no tests
  (`subPackages` is `cmd/md-vault-mcp`, which has none); run `go test ./...` in
  the dev shell.

## Docs

- README: new `note_search` row and description; replace "No copy of your notes"
  with what is stored (lexemes, paths, line ranges, sizes, mtimes; no text);
  `database_url` and the Postgres part of the NixOS module.
- `md-vault-mcp.example.toml`: `database_url`.

## Out of scope

Embeddings and vector search; highlighting inside a line; search over
attachments or `.base` files; the review queue.
