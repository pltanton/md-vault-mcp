# Review Queue (Plan A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The agent proposes edits of text it may not change directly; a person signed in through Kanidm applies or rejects them in a web page.

**Architecture:** `internal/index` stores proposals in Postgres and decides them under a row lock. `internal/notes` validates, applies and previews edits (pure matching in `edits.go`) and registers `note_propose`/`note_proposals`. A new `internal/review` package serves the page on its own listener with its own OIDC login (code flow + PKCE, HMAC-signed cookies). Spec: `docs/superpowers/specs/2026-10-28-review-queue-design.md` (plan B — links, delete, move — is separate).

**Tech Stack:** Go 1.26, pgx/v5, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `github.com/go-jose/go-jose/v4` (tests only), `html/template`, NixOS.

**Conventions:** run everything in `nix develop`; integration tests must not SKIP. No comments unless they record a fact not recoverable from code (`~/.claude/CLAUDE.md`). Commits end with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`; if GPG signing times out, stop and ask.

---

## File Structure

| File                                                        | Responsibility                                                                                                                                                       |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/notes/edits.go` (new)                             | `Edit`, `locateEdits`, `replaceSpans`, `normalizeEdits`, `docText`, `withText`, `lineOffset`.                                                                        |
| `internal/notes/edits_test.go` (new)                        | Table tests for matching.                                                                                                                                            |
| `internal/index/migrations/0002_proposals.sql` (new)        | `proposals` table.                                                                                                                                                   |
| `internal/index/proposals.go` (new)                         | `Proposal`, `Decision`, `CreateProposal`, `Proposal`, `Proposals`, `RecentDecisions`, `Decide`, `ExpireProposals`.                                                   |
| `internal/index/proposals_test.go` (new)                    | Store tests.                                                                                                                                                         |
| `internal/notes/proposals.go` (new)                         | `EnableReview`, `Propose`, own-block shortcut, `Proposals`, `ListProposals`, `GetProposal`, `PreviewProposal`, `ApplyProposal`, `RejectProposal`, `ExpireProposals`. |
| `internal/notes/proposals_test.go` (new)                    | Behaviour tests on pgtest.                                                                                                                                           |
| `internal/notes/tools.go`                                   | `note_propose`, `note_proposals` when review is enabled.                                                                                                             |
| `internal/notes/service.go`                                 | `reviewURL` field.                                                                                                                                                   |
| `internal/notes/config.go`, `config_test.go`                | `[review]`.                                                                                                                                                          |
| `internal/review/key.go` (new)                              | `LoadKey`.                                                                                                                                                           |
| `internal/review/cookies.go` (new)                          | Sealed cookies, sessions, CSRF.                                                                                                                                      |
| `internal/review/server.go` (new)                           | `Queue`, `Config`, `Server`, routing, auth middleware, OIDC client.                                                                                                  |
| `internal/review/login.go` (new)                            | `/login`, `/callback`, `/logout`.                                                                                                                                    |
| `internal/review/pages.go` (new)                            | List, proposal page, apply/reject.                                                                                                                                   |
| `internal/review/templates/*.html` (new)                    | `layout`, `list`, `proposal`, `message`.                                                                                                                             |
| `internal/review/*_test.go` (new)                           | Cookie tests; fake IdP; end-to-end handler tests.                                                                                                                    |
| `cmd/md-vault-mcp/main.go`                                  | Start the review server and expiry loop.                                                                                                                             |
| `nix/module.nix`                                            | `review.*` instance options.                                                                                                                                         |
| `README.md`, `md-vault-mcp.example.toml`, `nix/package.nix` | Docs, example, `vendorHash`.                                                                                                                                         |

---

### Task 1: Edit matching

**Files:** Create `internal/notes/edits.go`, `internal/notes/edits_test.go`

- [ ] **Step 1: Failing test** — `internal/notes/edits_test.go`:

```go
package notes

import (
	"strings"
	"testing"
)

func TestLocateEdits(t *testing.T) {
	text := "# A\n- Книга\n- Шарф\n- Книга про шарф"
	for _, c := range []struct {
		name  string
		edits []Edit
		want  string
		err   string
	}{
		{"one", []Edit{{"- Шарф", "- Шапка"}}, "# A\n- Книга\n- Шапка\n- Книга про шарф", ""},
		{"several out of order", []Edit{{"про шарф", "про шапку"}, {"# A", "# Б"}}, "# Б\n- Книга\n- Шарф\n- Книга про шапку", ""},
		{"multiline", []Edit{{"- Шарф\n- Книга про", "- Книга про"}}, "# A\n- Книга\n- Книга про шарф", ""},
		{"delete", []Edit{{"\n- Шарф", ""}}, "# A\n- Книга\n- Книга про шарф", ""},
		{"missing", []Edit{{"- Варежки", "x"}}, "", "not found"},
		{"duplicate", []Edit{{"- Книга", "x"}}, "", "more than once"},
		{"overlap", []Edit{{"Книга\n- Шарф", "x"}, {"Шарф\n- Книга про", "y"}}, "", "overlap"},
		{"empty old", []Edit{{"", "x"}}, "", "must not be empty"},
		{"none", nil, "", "must not be empty"},
	} {
		spans, err := locateEdits(text, c.edits)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: got %v, want error with %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := replaceSpans(text, spans); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeEdits(t *testing.T) {
	got := normalizeEdits([]Edit{{"a\r\nb", "c\r\nd"}})
	if got[0].Old != "a\nb" || got[0].New != "c\nd" {
		t.Errorf("got %+v", got)
	}
}
```

- [ ] **Step 2:** `go test ./internal/notes/ -run 'TestLocateEdits|TestNormalizeEdits'` — FAIL, `undefined: Edit`.

- [ ] **Step 3: Implement** — `internal/notes/edits.go`:

```go
package notes

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Edit struct {
	Old string `json:"old" jsonschema:"exact text to replace, copied from note_read without the line numbers; may span lines"`
	New string `json:"new" jsonschema:"replacement text; empty removes the old text"`
}

type editSpan struct {
	start, end int
	edit       Edit
}

func locateEdits(text string, edits []Edit) ([]editSpan, error) {
	if len(edits) == 0 {
		return nil, errors.New("edits must not be empty")
	}
	spans := make([]editSpan, 0, len(edits))
	for i, e := range edits {
		if e.Old == "" {
			return nil, fmt.Errorf("edit %d: old must not be empty", i+1)
		}
		at := strings.Index(text, e.Old)
		if at < 0 {
			return nil, fmt.Errorf("edit %d: old text not found in the note; read it again and copy the text exactly", i+1)
		}
		if strings.Contains(text[at+1:], e.Old) {
			return nil, fmt.Errorf("edit %d: old text occurs more than once; include more of the surrounding text", i+1)
		}
		spans = append(spans, editSpan{start: at, end: at + len(e.Old), edit: e})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return nil, errors.New("edits overlap; merge them into one")
		}
	}
	return spans, nil
}

func replaceSpans(text string, spans []editSpan) string {
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		b.WriteString(text[prev:sp.start])
		b.WriteString(sp.edit.New)
		prev = sp.end
	}
	b.WriteString(text[prev:])
	return b.String()
}

func normalizeEdits(edits []Edit) []Edit {
	out := make([]Edit, len(edits))
	for i, e := range edits {
		out[i] = Edit{Old: strings.ReplaceAll(e.Old, "\r\n", "\n"), New: strings.ReplaceAll(e.New, "\r\n", "\n")}
	}
	return out
}

func docText(d doc) string { return strings.Join(d.lines, "\n") }

func withText(d doc, text string) doc {
	d.lines = nil
	if text != "" {
		d.lines = strings.Split(text, "\n")
	}
	return d
}

func lineOffset(lines []string, at int) int {
	n := 0
	for _, l := range lines[:at] {
		n += len(l) + 1
	}
	return n
}
```

- [ ] **Step 4:** same command — PASS.
- [ ] **Step 5:** `git add internal/notes/edits.go internal/notes/edits_test.go && git commit -m "notes: match exact-text edits"`

---

### Task 2: Proposals store

**Files:** Create `internal/index/migrations/0002_proposals.sql`, `internal/index/proposals.go`, `internal/index/proposals_test.go`

- [ ] **Step 1: Failing test** — `internal/index/proposals_test.go`:

```go
package index

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func editsOf(t *testing.T, p Proposal) []map[string]string {
	t.Helper()
	if p.Edits == nil {
		return nil
	}
	var out []map[string]string
	must(t, json.Unmarshal(p.Edits, &out))
	return out
}

func TestProposalLifecycle(t *testing.T) {
	ix := open(t)
	ctx := context.Background()
	must(t, ix.CreateProposal(ctx, Proposal{ID: "p1", Kind: "edit", Path: "a.md",
		Edits: json.RawMessage(`[{"old":"x","new":"y"}]`), Reason: "fix"}))
	got, ok, err := ix.Proposal(ctx, "p1")
	must(t, err)
	if !ok || got.Status != "pending" || got.Reason != "fix" || got.CreatedAt.IsZero() || !got.DecidedAt.IsZero() ||
		len(editsOf(t, got)) != 1 || editsOf(t, got)[0]["new"] != "y" {
		t.Fatalf("got %+v", got)
	}
	decided, err := ix.Decide(ctx, "p1", func(p Proposal) (Decision, error) {
		return Decision{Status: "applied", By: "anton", Comment: "ок"}, nil
	})
	must(t, err)
	if decided.Status != "applied" || decided.DecidedBy != "anton" || decided.Comment != "ок" ||
		decided.Edits != nil || decided.Reason != "" || decided.DecidedAt.IsZero() {
		t.Errorf("decided %+v", decided)
	}
	if _, err := ix.Decide(ctx, "p1", func(Proposal) (Decision, error) { return Decision{Status: "rejected"}, nil }); !errors.Is(err, ErrNotPending) {
		t.Errorf("second decision: %v", err)
	}
	if _, err := ix.Decide(ctx, "nope", func(Proposal) (Decision, error) { return Decision{Status: "rejected"}, nil }); !errors.Is(err, ErrNoProposal) {
		t.Errorf("missing proposal: %v", err)
	}
	if _, ok, err := ix.Proposal(ctx, "nope"); ok || err != nil {
		t.Errorf("missing get: %v %v", ok, err)
	}
}

func TestDecideRollsBackOnError(t *testing.T) {
	ix := open(t)
	ctx := context.Background()
	must(t, ix.CreateProposal(ctx, Proposal{ID: "p1", Kind: "edit", Path: "a.md", Edits: json.RawMessage(`[]`), Reason: "r"}))
	if _, err := ix.Decide(ctx, "p1", func(Proposal) (Decision, error) { return Decision{}, errors.New("disk full") }); err == nil {
		t.Fatal("no error")
	}
	got, _, err := ix.Proposal(ctx, "p1")
	must(t, err)
	if got.Status != "pending" || got.Reason != "r" {
		t.Errorf("got %+v", got)
	}
}

func TestProposalsListing(t *testing.T) {
	ix := open(t)
	ctx := context.Background()
	for _, id := range []string{"p1", "p2"} {
		must(t, ix.CreateProposal(ctx, Proposal{ID: id, Kind: "edit", Path: id + ".md", Edits: json.RawMessage(`[]`), Reason: "r"}))
	}
	all, err := ix.Proposals(ctx, "", 10)
	must(t, err)
	if len(all) != 2 || all[0].ID != "p2" {
		t.Errorf("all %+v", all)
	}
	_, err = ix.Decide(ctx, "p1", func(Proposal) (Decision, error) { return Decision{Status: "rejected", By: "julsa"}, nil })
	must(t, err)
	pending, err := ix.Proposals(ctx, "pending", 10)
	must(t, err)
	decided, err := ix.RecentDecisions(ctx, 10)
	must(t, err)
	if len(pending) != 1 || pending[0].ID != "p2" || len(decided) != 1 || decided[0].ID != "p1" {
		t.Errorf("pending %+v decided %+v", pending, decided)
	}
}

func TestExpireProposals(t *testing.T) {
	ix := open(t)
	ctx := context.Background()
	for _, id := range []string{"old", "new"} {
		must(t, ix.CreateProposal(ctx, Proposal{ID: id, Kind: "edit", Path: "a.md", Edits: json.RawMessage(`[]`), Reason: "r"}))
	}
	_, err := ix.pool.Exec(ctx, "UPDATE proposals SET created_at = now() - interval '31 days' WHERE id = 'old'")
	must(t, err)
	n, err := ix.ExpireProposals(ctx, 30*24*time.Hour)
	must(t, err)
	old, _, _ := ix.Proposal(ctx, "old")
	fresh, _, _ := ix.Proposal(ctx, "new")
	if n != 1 || old.Status != "expired" || old.Edits != nil || old.Reason != "" || fresh.Status != "pending" {
		t.Errorf("n=%d old %+v new %+v", n, old, fresh)
	}
}
```

- [ ] **Step 2:** `go test ./internal/index/ -run Proposal` — FAIL, `undefined: Proposal`.

- [ ] **Step 3: Migration** — `internal/index/migrations/0002_proposals.sql`:

```sql
CREATE TABLE proposals (
  id         text PRIMARY KEY,
  kind       text NOT NULL CHECK (kind IN ('edit', 'delete', 'move')),
  path       text NOT NULL,
  new_path   text,
  status     text NOT NULL CHECK (status IN ('pending', 'applied', 'rejected', 'stale', 'expired')),
  edits      jsonb,
  link_edits jsonb,
  reason     text,
  base_hash  text,
  result     jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  decided_at timestamptz,
  decided_by text,
  comment    text
);

CREATE INDEX proposals_status ON proposals (status, created_at);
```

- [ ] **Step 4: Store** — `internal/index/proposals.go`:

```go
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

const proposalCols = `id, kind, path, coalesce(new_path, ''), status, edits, coalesce(reason, ''),
	coalesce(base_hash, ''), result, created_at, decided_at, coalesce(decided_by, ''), coalesce(comment, '')`

func scanProposal(row pgx.Row) (Proposal, error) {
	var p Proposal
	var edits, result []byte
	var decided *time.Time
	err := row.Scan(&p.ID, &p.Kind, &p.Path, &p.NewPath, &p.Status, &edits, &p.Reason, &p.BaseHash,
		&result, &p.CreatedAt, &decided, &p.DecidedBy, &p.Comment)
	p.Edits, p.Result = edits, result
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
	_, err := ix.pool.Exec(ctx, `INSERT INTO proposals (id, kind, path, new_path, status, edits, reason, base_hash)
		VALUES ($1, $2, $3, nullif($4, ''), 'pending', $5::jsonb, $6, nullif($7, ''))`,
		p.ID, p.Kind, p.Path, p.NewPath, rawJSON(p.Edits), p.Reason, p.BaseHash)
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
```

- [ ] **Step 5:** `go test ./internal/index/ -v` — all PASS (old ones too: migration 0002 applies after 0001).
- [ ] **Step 6:** `git add internal/index && git commit -m "index: proposals store"`

---

### Task 3: Proposals in the notes service and the agent's tools

**Files:** Create `internal/notes/proposals.go`, `internal/notes/proposals_test.go`; modify `internal/notes/service.go` (field), `internal/notes/tools.go`

- [ ] **Step 1: Failing test** — `internal/notes/proposals_test.go`:

```go
package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

func newReviewFixture(t *testing.T) *fixture {
	t.Helper()
	f := newIndexedFixture(t)
	f.svc.EnableReview("https://review.test/")
	return f
}

func TestProposeValidates(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		name, path string
		edits      []Edit
		reason     string
		err        string
	}{
		{"no reason", julia, []Edit{{"- Шарф", "- Шапка"}}, " ", "reason"},
		{"missing", julia, []Edit{{"- Варежки", "x"}}, "r", "not found"},
		{"no edits", julia, nil, "r", "must not be empty"},
		{"unwritable", "02. Areas/Work/Job.md", []Edit{{"секретный", "x"}}, "r", ""},
	} {
		if _, err := f.svc.Propose(ctx, c.path, c.edits, c.reason); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
}

func TestProposeAndApply(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	before := f.read(julia)
	res, err := f.svc.Propose(ctx, julia, []Edit{{"- Шарф", "- Шапка"}}, "в чате сказали про шапку")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "pending" || len(res.ID) != 16 || res.URL != "https://review.test/p/"+res.ID || f.read(julia) != before {
		t.Fatalf("propose %+v", res)
	}
	list, err := f.svc.Proposals(ctx, "pending", 0)
	if err != nil || len(list.Proposals) != 1 || list.Proposals[0].ID != res.ID || list.Proposals[0].URL != res.URL {
		t.Fatalf("list %+v %v", list, err)
	}
	p, ok, err := f.svc.GetProposal(ctx, res.ID)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	pv := f.svc.PreviewProposal(p)
	if len(pv) != 1 || pv[0].Stale || pv[0].Line != 8 || fmt.Sprint(pv[0].Old) != "[- Шарф]" || fmt.Sprint(pv[0].New) != "[- Шапка]" ||
		fmt.Sprint(pv[0].Before) != "[## Идеи для подарков - Книга]" || len(pv[0].After) != 2 {
		t.Errorf("preview %+v", pv)
	}

	f.write(julia, before+"- Ещё строка\n")
	applied, err := f.svc.ApplyProposal(ctx, res.ID, "anton", "ок")
	if err != nil {
		t.Fatal(err)
	}
	now := f.read(julia)
	if applied.Status != "applied" || applied.DecidedBy != "anton" || applied.Edits != nil ||
		!strings.Contains(now, "- Шапка") || strings.Contains(now, "- Шарф") || !strings.Contains(now, "- Ещё строка") {
		t.Errorf("applied %+v\n%s", applied, now)
	}
	if got := matchKeys(f.search("шапка", "")); got != julia+":6" {
		t.Errorf("search after apply: %s", got)
	}
	if _, err := f.svc.ApplyProposal(ctx, res.ID, "anton", ""); !errors.Is(err, index.ErrNotPending) {
		t.Errorf("second apply: %v", err)
	}
}

func TestApplyStaleAndReject(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	scarf, err := f.svc.Propose(ctx, julia, []Edit{{"- Шарф", "- Шапка"}}, "r")
	if err != nil {
		t.Fatal(err)
	}
	book, err := f.svc.Propose(ctx, julia, []Edit{{"- Книга", "- Две книги"}}, "r")
	if err != nil {
		t.Fatal(err)
	}
	f.write(julia, strings.Replace(f.read(julia), "- Шарф", "- Варежки", 1))
	p, _, _ := f.svc.GetProposal(ctx, scarf.ID)
	if pv := f.svc.PreviewProposal(p); len(pv) != 1 || !pv[0].Stale {
		t.Errorf("preview of a stale edit %+v", pv)
	}
	stale, err := f.svc.ApplyProposal(ctx, scarf.ID, "anton", "")
	if err != nil || stale.Status != "stale" || !strings.Contains(f.read(julia), "- Варежки") {
		t.Errorf("stale %+v %v", stale, err)
	}
	rejected, err := f.svc.RejectProposal(ctx, book.ID, "julsa", "не надо")
	if err != nil || rejected.Status != "rejected" || rejected.Comment != "не надо" || !strings.Contains(f.read(julia), "- Книга") {
		t.Errorf("rejected %+v %v", rejected, err)
	}
}

func TestProposeInsideOwnBlocks(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	add, err := f.svc.Add(julia, "- Варежки синие", "## Идеи для подарков")
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := f.svc.Propose(ctx, julia, []Edit{{"Варежки синие", "Варежки красные"}, {"- Книга", "- Книги"}}, "r")
	if err != nil || mixed.Status != "pending" || strings.Contains(f.read(julia), "красные") {
		t.Fatalf("mixed %+v %v", mixed, err)
	}
	own, err := f.svc.Propose(ctx, julia, []Edit{{"Варежки синие", "Варежки красные"}}, "цвет")
	if err != nil || own.Status != "applied" || own.ID != "" || !strings.Contains(f.read(julia), "- Варежки красные") {
		t.Fatalf("own %+v %v", own, err)
	}
	rec, err := f.svc.Recent(0)
	if err != nil || len(rec.Blocks) != 1 || rec.Blocks[0].ID != add.ID || rec.Blocks[0].Text != "- Варежки красные" {
		t.Errorf("recent %+v %v", rec, err)
	}
	if _, err := f.svc.Edit(add.ID, "- Перчатки"); err != nil {
		t.Errorf("edit after the shortcut: %v", err)
	}
}
```

- [ ] **Step 2:** `go test ./internal/notes/ -run 'Propose|Apply'` — FAIL, `f.svc.EnableReview undefined`.

- [ ] **Step 3: Field** — in `internal/notes/service.go` add `reviewURL string` to `Service` after `index   *index.Index`.

- [ ] **Step 4: Implement** — `internal/notes/proposals.go`:

```go
package notes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

const (
	proposalExpiry       = 30 * 24 * time.Hour
	defaultProposalLimit = 20
	maxProposalLimit     = 50
)

type ProposeResult struct {
	ID     string `json:"id,omitempty"`
	URL    string `json:"url,omitempty" jsonschema:"page where a person applies or rejects it; give it to them"`
	Status string `json:"status" jsonschema:"pending, or applied when every edit was inside blocks you may still change"`
	Path   string `json:"path"`
}

type ProposalInfo struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Path      string     `json:"path"`
	Status    string     `json:"status" jsonschema:"pending, applied, rejected, stale (the text changed before it was applied; read the note and propose again) or expired"`
	Comment   string     `json:"comment,omitempty" jsonschema:"the reviewer's comment"`
	DecidedBy string     `json:"decided_by,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	URL       string     `json:"url"`
}

type ProposalsResult struct {
	Proposals []ProposalInfo `json:"proposals"`
}

type EditPreview struct {
	Line   int
	Before []string
	Old    []string
	New    []string
	After  []string
	Stale  bool
}

func (s *Service) EnableReview(publicURL string) { s.reviewURL = strings.TrimRight(publicURL, "/") }

func (s *Service) proposalURL(id string) string { return s.reviewURL + "/p/" + id }

func newProposalID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) Propose(ctx context.Context, rel string, edits []Edit, reason string) (ProposeResult, error) {
	if s.reviewURL == "" || s.index == nil {
		return ProposeResult{}, errors.New("review is not configured on this server")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ProposeResult{}, errors.New("reason must not be empty: the person reviewing it reads it")
	}
	edits = normalizeEdits(edits)
	real, clean, err := s.vault.ResolveWrite(rel)
	if err != nil {
		return ProposeResult{}, err
	}
	res := ProposeResult{Path: clean}
	err = s.journal.update(func(entries []entry) ([]entry, error) {
		st, err := s.readFile(real, clean)
		if err != nil {
			return nil, err
		}
		text := docText(st.doc)
		spans, err := locateEdits(text, edits)
		if err != nil {
			return nil, err
		}
		next, done, err := s.applyInOwnBlocks(entries, real, clean, st, text, spans)
		if err != nil {
			return nil, err
		}
		if done {
			res.Status = "applied"
			return next, nil
		}
		payload, err := json.Marshal(edits)
		if err != nil {
			return nil, err
		}
		id := newProposalID()
		if err := s.index.CreateProposal(ctx, index.Proposal{ID: id, Kind: "edit", Path: clean, Edits: payload, Reason: reason}); err != nil {
			return nil, err
		}
		res.ID, res.URL, res.Status = id, s.proposalURL(id), "pending"
		return entries, nil
	})
	if err == nil && res.Status == "applied" {
		s.reindexQuietly(clean)
	}
	return res, err
}

type ownBlock struct {
	entry      int
	start, end int
}

func (s *Service) applyInOwnBlocks(entries []entry, real, clean string, st fileState, text string, spans []editSpan) ([]entry, bool, error) {
	cutoff := s.now().Add(-s.window)
	var blocks []ownBlock
	for i, e := range entries {
		if e.Path != clean || e.Property != "" || !e.Created.After(cutoff) {
			continue
		}
		at, err := s.locate(st.doc.lines, e)
		if err != nil {
			continue
		}
		start := lineOffset(st.doc.lines, at)
		blocks = append(blocks, ownBlock{entry: i, start: start, end: start + len(strings.Join(st.doc.lines[at:at+e.Lines], "\n"))})
	}
	touched := map[int]bool{}
	for _, sp := range spans {
		inside := -1
		for b, bl := range blocks {
			if sp.start >= bl.start && sp.end <= bl.end {
				inside = b
				break
			}
		}
		if inside < 0 {
			return nil, false, nil
		}
		touched[inside] = true
	}
	newText := replaceSpans(text, spans)
	if err := s.writeFile(real, clean, st.info, withText(st.doc, newText)); err != nil {
		return nil, false, err
	}
	shift := func(off int) int {
		n := off
		for _, sp := range spans {
			if sp.end <= off {
				n += len(sp.edit.New) - (sp.end - sp.start)
			}
		}
		return n
	}
	now := s.now()
	for b := range touched {
		bl := blocks[b]
		lines := strings.Split(newText[shift(bl.start):shift(bl.end)], "\n")
		e := entries[bl.entry]
		e.Lines, e.Hash, e.Updated = len(lines), s.journal.hash(lines), now
		entries[bl.entry] = e
	}
	return s.live(entries), true, nil
}

func (s *Service) Proposals(ctx context.Context, status string, limit int) (ProposalsResult, error) {
	if s.index == nil {
		return ProposalsResult{}, errors.New("review is not configured on this server")
	}
	if limit <= 0 {
		limit = defaultProposalLimit
	}
	limit = min(limit, maxProposalLimit)
	ps, err := s.index.Proposals(ctx, strings.TrimSpace(status), limit)
	if err != nil {
		return ProposalsResult{}, err
	}
	out := ProposalsResult{Proposals: []ProposalInfo{}}
	for _, p := range ps {
		info := ProposalInfo{ID: p.ID, Kind: p.Kind, Path: p.Path, Status: p.Status, Comment: p.Comment,
			DecidedBy: p.DecidedBy, CreatedAt: p.CreatedAt, URL: s.proposalURL(p.ID)}
		if !p.DecidedAt.IsZero() {
			t := p.DecidedAt
			info.DecidedAt = &t
		}
		out.Proposals = append(out.Proposals, info)
	}
	return out, nil
}

func (s *Service) ListProposals(ctx context.Context) ([]index.Proposal, []index.Proposal, error) {
	pending, err := s.index.Proposals(ctx, "pending", 200)
	if err != nil {
		return nil, nil, err
	}
	decided, err := s.index.RecentDecisions(ctx, 20)
	return pending, decided, err
}

func (s *Service) GetProposal(ctx context.Context, id string) (index.Proposal, bool, error) {
	return s.index.Proposal(ctx, id)
}

func (s *Service) PreviewProposal(p index.Proposal) []EditPreview {
	var edits []Edit
	if json.Unmarshal(p.Edits, &edits) != nil {
		return nil
	}
	var lines []string
	text, readable := "", false
	if real, clean, err := s.vault.ResolveRead(p.Path); err == nil {
		if st, err := s.readFile(real, clean); err == nil {
			lines, text, readable = st.doc.lines, docText(st.doc), true
		}
	}
	out := make([]EditPreview, 0, len(edits))
	for _, e := range edits {
		at := -1
		if i := strings.Index(text, e.Old); readable && i >= 0 && !strings.Contains(text[i+1:], e.Old) {
			at = i
		}
		if at < 0 {
			out = append(out, EditPreview{Old: strings.Split(e.Old, "\n"), New: splitText(e.New), Stale: true})
			continue
		}
		first := strings.Count(text[:at], "\n")
		last := strings.Count(text[:at+len(e.Old)], "\n")
		segStart := lineOffset(lines, first)
		seg := text[segStart : lineOffset(lines, last)+len(lines[last])]
		rel := at - segStart
		out = append(out, EditPreview{
			Line:   first + 1,
			Before: lines[max(0, first-2):first],
			Old:    lines[first : last+1],
			New:    splitText(seg[:rel] + e.New + seg[rel+len(e.Old):]),
			After:  lines[last+1 : min(len(lines), last+3)],
		})
	}
	return out
}

func splitText(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (s *Service) ApplyProposal(ctx context.Context, id, by, comment string) (index.Proposal, error) {
	applied := false
	p, err := s.index.Decide(ctx, id, func(p index.Proposal) (index.Decision, error) {
		if p.Kind != "edit" {
			return index.Decision{}, fmt.Errorf("%s proposals cannot be applied by this version", p.Kind)
		}
		var edits []Edit
		if err := json.Unmarshal(p.Edits, &edits); err != nil {
			return index.Decision{}, err
		}
		status := "stale"
		err := s.journal.update(func(entries []entry) ([]entry, error) {
			real, clean, err := s.vault.ResolveWrite(p.Path)
			if err != nil {
				return entries, nil
			}
			st, err := s.readFile(real, clean)
			if errors.Is(err, fs.ErrNotExist) {
				return entries, nil
			}
			if err != nil {
				return nil, err
			}
			text := docText(st.doc)
			spans, err := locateEdits(text, edits)
			if err != nil {
				return entries, nil
			}
			if err := s.writeFile(real, clean, st.info, withText(st.doc, replaceSpans(text, spans))); err != nil {
				return nil, err
			}
			status = "applied"
			return entries, nil
		})
		if err != nil {
			return index.Decision{}, err
		}
		applied = status == "applied"
		return index.Decision{Status: status, By: by, Comment: comment}, nil
	})
	if err == nil && applied {
		s.reindexQuietly(p.Path)
	}
	return p, err
}

func (s *Service) RejectProposal(ctx context.Context, id, by, comment string) (index.Proposal, error) {
	return s.index.Decide(ctx, id, func(index.Proposal) (index.Decision, error) {
		return index.Decision{Status: "rejected", By: by, Comment: comment}, nil
	})
}

func (s *Service) ExpireProposals(ctx context.Context) error {
	if s.index == nil {
		return nil
	}
	_, err := s.index.ExpireProposals(ctx, proposalExpiry)
	return err
}
```

- [ ] **Step 5: Tools** — in `internal/notes/tools.go` add input types after `recentInput`:

```go
type proposeInput struct {
	Path   string `json:"path" jsonschema:"note to change"`
	Edits  []Edit `json:"edits" jsonschema:"replacements; each old must occur exactly once in the note and edits must not overlap"`
	Reason string `json:"reason" jsonschema:"why, for the person who reviews it, in one or two sentences"`
}

type proposalsInput struct {
	Status string `json:"status,omitempty" jsonschema:"pending, applied, rejected, stale or expired; omit for all"`
	Limit  int    `json:"limit,omitempty" jsonschema:"at most this many, up to 50"`
}
```

and at the end of `Register`, after `note_recent`:

```go
	if s.reviewURL == "" {
		return
	}

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_propose",
		Description: "Propose changing text you may not change directly (the owner's text, or yours after " + window +
			") in a note you may write. Each edit replaces one exact piece of text that occurs exactly once. A person " +
			"applies or rejects it on a web page: give them the returned url. If every edit is inside blocks you may " +
			"still change, it is applied at once instead. See the outcome with note_proposals.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposeInput) (*mcp.CallToolResult, ProposeResult, error) {
		out, err := s.Propose(ctx, in.Path, in.Edits, in.Reason)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_proposals",
		Description: "Your proposals, newest first, with their status and the reviewer's comment. A stale one could " +
			"not be applied because the text changed since; read the note and propose again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposalsInput) (*mcp.CallToolResult, ProposalsResult, error) {
		out, err := s.Proposals(ctx, in.Status, in.Limit)
		return nil, out, err
	})
```

- [ ] **Step 6:** `go test -race -count=1 ./internal/... ` — all PASS, no SKIP.
- [ ] **Step 7:** `git add internal/notes && git commit -m "note_propose, note_proposals: edits of foreign text go to review"`

---

### Task 4: `[review]` config

**Files:** Modify `internal/notes/config.go`, `internal/notes/config_test.go`

- [ ] **Step 1: Failing test** — append to `internal/notes/config_test.go`:

```go
func TestConfigReview(t *testing.T) {
	base := "vault = \"/v\"\nstate_dir = \"/s\"\ndatabase_url = \"x\"\n"
	full := base + `[review]
listen = "127.0.0.1:8193"
public_url = "https://notes.example.org/"
allowed_groups = ["admins"]
oidc_issuer = "https://id.example.org/oauth2/openid/notes"
oidc_client_id = "notes"
oidc_client_secret_file = "/run/secret"
`
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte(full), 0o600)
	cfg, err := LoadConfig(p)
	if err != nil || cfg.Review == nil || cfg.Review.PublicURL != "https://notes.example.org" || cfg.Review.OIDCClientID != "notes" {
		t.Fatalf("got %+v %v", cfg.Review, err)
	}
	os.WriteFile(p, []byte(base), 0o600)
	if cfg, err := LoadConfig(p); err != nil || cfg.Review != nil {
		t.Errorf("without review: %+v %v", cfg.Review, err)
	}
	os.WriteFile(p, []byte(strings.Replace(full, "oidc_issuer", "#", 1)), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "review.oidc_issuer") {
		t.Errorf("missing issuer: %v", err)
	}
	os.WriteFile(p, []byte(strings.Replace(full, "https://notes", "notes", 1)), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "public_url") {
		t.Errorf("bad url: %v", err)
	}
}
```

- [ ] **Step 2:** `go test ./internal/notes/ -run TestConfigReview` — FAIL, `cfg.Review undefined`.

- [ ] **Step 3: Implement** — in `internal/notes/config.go` add the type:

```go
type ReviewConfig struct {
	Listen               string   `toml:"listen"`
	PublicURL            string   `toml:"public_url"`
	AllowedGroups        []string `toml:"allowed_groups"`
	OIDCIssuer           string   `toml:"oidc_issuer"`
	OIDCClientID         string   `toml:"oidc_client_id"`
	OIDCClientSecretFile string   `toml:"oidc_client_secret_file"`
}
```

add `Review *ReviewConfig` as the last field of `Config` and `Review *ReviewConfig \`toml:"review"\``as the last field of`fileConfig`, and before the final `return cfg, nil`in`LoadConfig`:

```go
	if r := fc.Review; r != nil {
		r.PublicURL = strings.TrimRight(r.PublicURL, "/")
		var missing []string
		for _, f := range []struct{ key, value string }{
			{"listen", r.Listen}, {"public_url", r.PublicURL}, {"oidc_issuer", r.OIDCIssuer},
			{"oidc_client_id", r.OIDCClientID}, {"oidc_client_secret_file", r.OIDCClientSecretFile},
		} {
			if f.value == "" {
				missing = append(missing, "review."+f.key)
			}
		}
		if len(r.AllowedGroups) == 0 {
			missing = append(missing, "review.allowed_groups")
		}
		if len(missing) > 0 {
			return Config{}, fmt.Errorf("config: %s required", strings.Join(missing, ", "))
		}
		if !strings.HasPrefix(r.PublicURL, "https://") && !strings.HasPrefix(r.PublicURL, "http://") {
			return Config{}, fmt.Errorf("config: review.public_url must start with https://")
		}
		cfg.Review = r
	}
```

- [ ] **Step 4:** `go test ./internal/notes/ -run Config -v` — PASS.
- [ ] **Step 5:** `git add internal/notes/config.go internal/notes/config_test.go && git commit -m "config: [review] section"`

---

### Task 5: Review key and sealed cookies

**Files:** Create `internal/review/key.go`, `internal/review/cookies.go`, `internal/review/cookies_test.go`; add `internal/review/server.go` skeleton (the `Server` type the cookie code hangs on).

- [ ] **Step 1: Dependencies** — `go get github.com/coreos/go-oidc/v3@latest golang.org/x/oauth2@latest github.com/go-jose/go-jose/v4@latest`

- [ ] **Step 2: Failing test** — `internal/review/cookies_test.go`:

```go
package review

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(Config{PublicURL: "https://review.test", Key: []byte("0123456789abcdef0123456789abcdef")}, nil)
}

func TestSealedCookies(t *testing.T) {
	s := testServer(t)
	raw := s.seal(session{Kind: "session", User: "anton", Exp: time.Now().Add(time.Hour).Unix()})
	var got session
	if !s.open(raw, &got) || got.User != "anton" {
		t.Fatalf("round trip: %+v", got)
	}
	if s.open(raw[:len(raw)-2]+"xx", &got) {
		t.Error("tampered signature accepted")
	}
	other := New(Config{Key: []byte("ffffffffffffffffffffffffffffffff")}, nil)
	if other.open(raw, &got) {
		t.Error("another key accepted")
	}
	f := s.seal(flow{Kind: "flow", Exp: time.Now().Add(time.Hour).Unix()})
	if _, ok := s.sessionFrom(f); ok {
		t.Error("a login cookie works as a session")
	}
	expired := s.seal(session{Kind: "session", User: "anton", Exp: time.Now().Add(-time.Minute).Unix()})
	if _, ok := s.sessionFrom(expired); ok {
		t.Error("expired session accepted")
	}
	if s.csrfToken(raw) == s.csrfToken(expired) {
		t.Error("csrf token does not depend on the session")
	}
}

func TestLoadKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "review.key")
	a, err := LoadKey(p)
	if err != nil || len(a) != 32 {
		t.Fatal(len(a), err)
	}
	b, err := LoadKey(p)
	if err != nil || string(a) != string(b) {
		t.Error("key changed between loads")
	}
	os.WriteFile(p, []byte("short\n"), 0o600)
	if _, err := LoadKey(p); err == nil {
		t.Error("corrupt key accepted")
	}
}
```

- [ ] **Step 3:** `go test ./internal/review/` — FAIL, package has no Go files / `undefined: New`.

- [ ] **Step 4: Key** — `internal/review/key.go`:

```go
package review

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

func LoadKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) < 32 {
			return nil, fmt.Errorf("review key %s is corrupt", path)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	rand.Read(key)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return LoadKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}
```

- [ ] **Step 5: Server skeleton** — `internal/review/server.go` (routes come in Tasks 6–7):

```go
package review

import (
	"context"
	"html/template"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
)

type Queue interface {
	ListProposals(ctx context.Context) (pending, decided []index.Proposal, err error)
	GetProposal(ctx context.Context, id string) (index.Proposal, bool, error)
	PreviewProposal(p index.Proposal) []notes.EditPreview
	ApplyProposal(ctx context.Context, id, by, comment string) (index.Proposal, error)
	RejectProposal(ctx context.Context, id, by, comment string) (index.Proposal, error)
}

var _ Queue = (*notes.Service)(nil)

type Config struct {
	PublicURL     string
	AllowedGroups []string
	Issuer        string
	ClientID      string
	ClientSecret  string
	Key           []byte
}

type Server struct {
	cfg    Config
	queue  Queue
	secure bool
	pages  *template.Template
	now    func() time.Time

	mu       sync.Mutex
	provider *oidc.Provider
}

func New(cfg Config, q Queue) *Server {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	return &Server{cfg: cfg, queue: q, secure: strings.HasPrefix(cfg.PublicURL, "https://"), pages: parsePages(), now: time.Now}
}

// The provider keeps the context for later key fetches, so it must not be a request's.
func (s *Server) oidcClient() (*oidc.Provider, *oauth2.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider == nil {
		p, err := oidc.NewProvider(context.Background(), s.cfg.Issuer)
		if err != nil {
			return nil, nil, err
		}
		s.provider = p
	}
	return s.provider, &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		Endpoint:     s.provider.Endpoint(),
		RedirectURL:  s.cfg.PublicURL + "/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "groups"},
	}, nil
}
```

and a temporary `parsePages` so it compiles — `internal/review/pages.go`:

```go
package review

import "html/template"

func parsePages() *template.Template { return template.New("") }
```

- [ ] **Step 6: Cookies** — `internal/review/cookies.go`:

```go
package review

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookie = "mdv_session"
	flowCookie    = "mdv_login"
	sessionTTL    = 12 * time.Hour
	flowTTL       = 10 * time.Minute
)

type session struct {
	Kind   string   `json:"k"`
	User   string   `json:"u"`
	Groups []string `json:"g"`
	Exp    int64    `json:"e"`
}

type flow struct {
	Kind     string `json:"k"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
	Exp      int64  `json:"e"`
}

func (s *Server) mac(payload string) string {
	m := hmac.New(sha256.New, s.cfg.Key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) seal(v any) string {
	b, _ := json.Marshal(v)
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + s.mac(p)
}

func (s *Server) open(raw string, v any) bool {
	p, sig, ok := strings.Cut(raw, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(p))) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (s *Server) sessionFrom(raw string) (session, bool) {
	var sess session
	if !s.open(raw, &sess) || sess.Kind != "session" || sess.Exp < s.now().Unix() {
		return session{}, false
	}
	return sess, true
}

func (s *Server) csrfToken(rawSession string) string { return s.mac("csrf:" + rawSession) }

func (s *Server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
		Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 7:** `go mod tidy && go test ./internal/review/ -v` — PASS.
- [ ] **Step 8:** `git add go.mod go.sum internal/review && git commit -m "review: key and sealed cookies"`

---

### Task 6: OIDC login

**Files:** Create `internal/review/login.go`, `internal/review/fakeidp_test.go`, `internal/review/server_test.go`; modify `internal/review/server.go` (Handler, auth), `internal/review/pages.go` (templates, message page)

- [ ] **Step 1: Fake IdP** — `internal/review/fakeidp_test.go`:

```go
package review

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type fakeIDP struct {
	url       string
	key       *rsa.PrivateKey
	clientID  string
	user      string
	groups    []string
	nonce     string
	challenge string
}

func newFakeIDP(t *testing.T, clientID string) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: key, clientID: clientID, user: "anton", groups: []string{"admins@id.test"}}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.url, "authorization_endpoint": f.url + "/authorize", "token_endpoint": f.url + "/token",
			"jwks_uri": f.url + "/jwks", "userinfo_endpoint": f.url + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		if r.PostFormValue("code") != "good" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300, "id_token": f.idToken(t)})
	})
	return f
}

func (f *fakeIDP) idToken(t *testing.T) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	payload, _ := json.Marshal(map[string]any{
		"iss": f.url, "aud": f.clientID, "sub": "uuid-" + f.user, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"nonce": f.nonce, "preferred_username": f.user, "groups": f.groups,
	})
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
```

- [ ] **Step 2: Failing tests** — `internal/review/server_test.go`:

```go
package review

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
)

type fakeQueue struct {
	p                     index.Proposal
	applied, rejected     string
	by, comment           string
}

func (q *fakeQueue) ListProposals(context.Context) ([]index.Proposal, []index.Proposal, error) {
	return []index.Proposal{q.p}, nil, nil
}

func (q *fakeQueue) GetProposal(_ context.Context, id string) (index.Proposal, bool, error) {
	return q.p, id == q.p.ID, nil
}

func (q *fakeQueue) PreviewProposal(index.Proposal) []notes.EditPreview {
	return []notes.EditPreview{{Line: 14, Before: []string{"## Мама"}, Old: []string{"- ДР: 12 марта"}, New: []string{"- ДР: 21 марта"}}}
}

func (q *fakeQueue) ApplyProposal(_ context.Context, id, by, comment string) (index.Proposal, error) {
	q.applied, q.by, q.comment, q.p.Status = id, by, comment, "applied"
	return q.p, nil
}

func (q *fakeQueue) RejectProposal(_ context.Context, id, by, comment string) (index.Proposal, error) {
	q.rejected, q.by, q.comment, q.p.Status = id, by, comment, "rejected"
	return q.p, nil
}

type browser struct {
	t   *testing.T
	h   http.Handler
	jar map[string]string
}

func (b *browser) do(method, target string, form url.Values) (*http.Response, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	res := rec.Result()
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 {
			delete(b.jar, c.Name)
		} else {
			b.jar[c.Name] = c.Value
		}
	}
	out, _ := io.ReadAll(res.Body)
	return res, string(out)
}

func (b *browser) login(idp *fakeIDP, next string) *http.Response {
	b.t.Helper()
	res, _ := b.do("GET", "/login?next="+url.QueryEscape(next), nil)
	loc, err := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || err != nil || !strings.HasPrefix(loc.String(), idp.url+"/authorize") {
		b.t.Fatalf("login redirect %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" {
		b.t.Fatalf("no PKCE: %s", loc)
	}
	idp.nonce, idp.challenge = q.Get("nonce"), q.Get("code_challenge")
	res, _ = b.do("GET", "/callback?code=good&state="+url.QueryEscape(q.Get("state")), nil)
	return res
}

func setup(t *testing.T, groups ...string) (*browser, *fakeIDP, *fakeQueue) {
	t.Helper()
	idp := newFakeIDP(t, "family-notes")
	q := &fakeQueue{p: index.Proposal{ID: "p1", Kind: "edit", Path: "Дни рождения.md", Status: "pending", Reason: "в чате сказали 21-е"}}
	if len(groups) == 0 {
		groups = []string{"admins", "family"}
	}
	s := New(Config{PublicURL: "http://review.test", AllowedGroups: groups, Issuer: idp.url, ClientID: "family-notes",
		ClientSecret: "secret", Key: []byte("0123456789abcdef0123456789abcdef")}, q)
	return &browser{t: t, h: s.Handler(), jar: map[string]string{}}, idp, q
}

func TestUnauthenticatedGoesToLogin(t *testing.T) {
	b, _, _ := setup(t)
	res, _ := b.do("GET", "/p/p1", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next=%2Fp%2Fp1" {
		t.Errorf("got %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := b.do("POST", "/p/p1/apply", url.Values{}); res.StatusCode != http.StatusForbidden {
		t.Errorf("post without session: %d", res.StatusCode)
	}
}

func TestLoginLandsOnNext(t *testing.T) {
	b, idp, _ := setup(t)
	res := b.login(idp, "/p/p1")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/p/p1" || b.jar[sessionCookie] == "" || b.jar[flowCookie] != "" {
		t.Fatalf("callback %d %s jar %v", res.StatusCode, res.Header.Get("Location"), b.jar)
	}
}

func TestLoginAcceptsShortAndQualifiedGroups(t *testing.T) {
	for _, g := range []string{"family", "family@home.id.test"} {
		b, idp, _ := setup(t)
		idp.user, idp.groups = "julsa", []string{g}
		if res := b.login(idp, "/"); res.StatusCode != http.StatusSeeOther {
			t.Errorf("%s: %d", g, res.StatusCode)
		}
	}
}

func TestLoginRefusesOtherGroups(t *testing.T) {
	b, idp, _ := setup(t, "admins")
	idp.user, idp.groups = "julsa", []string{"family@home.id.test"}
	res := b.login(idp, "/")
	if res.StatusCode != http.StatusForbidden || b.jar[sessionCookie] != "" {
		t.Errorf("got %d jar %v", res.StatusCode, b.jar)
	}
}

func TestLoginRedirectStaysLocal(t *testing.T) {
	for _, next := range []string{"//evil.example/x", "https://evil.example", "/\\evil.example"} {
		b, idp, _ := setup(t)
		if res := b.login(idp, next); res.Header.Get("Location") != "/" {
			t.Errorf("%s: redirected to %s", next, res.Header.Get("Location"))
		}
	}
}

func TestCallbackNeedsLoginCookie(t *testing.T) {
	b, _, _ := setup(t)
	if res, _ := b.do("GET", "/callback?code=good&state=x", nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestLoginWithoutProvider(t *testing.T) {
	s := New(Config{PublicURL: "http://review.test", AllowedGroups: []string{"admins"}, Issuer: "http://127.0.0.1:1",
		ClientID: "c", Key: []byte("0123456789abcdef0123456789abcdef")}, &fakeQueue{})
	b := &browser{t: t, h: s.Handler(), jar: map[string]string{}}
	if res, body := b.do("GET", "/login", nil); res.StatusCode != http.StatusBadGateway || !strings.Contains(body, "Kanidm") {
		t.Errorf("got %d %s", res.StatusCode, body)
	}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
```

- [ ] **Step 3:** `go test ./internal/review/` — FAIL, `s.Handler undefined`.

- [ ] **Step 4: Templates and message page** — replace `internal/review/pages.go` with:

```go
package review

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
)

//go:embed templates/*.html
var templates embed.FS

var statusNames = map[string]string{
	"pending": "ждёт", "applied": "применено", "rejected": "отклонено", "stale": "устарело", "expired": "истекло",
}

func parsePages() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"status": func(s string) string {
			if n, ok := statusNames[s]; ok {
				return n
			}
			return s
		},
		"when": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("02.01.2006 15:04")
		},
	}).ParseFS(templates, "templates/*.html"))
}

type viewer struct {
	User string
	CSRF string
}

type messagePage struct {
	Title  string
	Viewer viewer
	Text   string
}

type listPage struct {
	Title            string
	Viewer           viewer
	Pending, Decided []index.Proposal
}

type proposalPage struct {
	Title  string
	Viewer viewer
	P      index.Proposal
	Edits  []notes.EditPreview
	Error  string
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var b bytes.Buffer
	if err := s.pages.ExecuteTemplate(&b, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(b.Bytes())
}

func (s *Server) message(w http.ResponseWriter, status int, v viewer, title, text string) {
	s.render(w, status, "message.html", messagePage{Title: title, Viewer: v, Text: text})
}
```

`internal/review/templates/layout.html`:

```html
{{define "head"}}<!doctype html>
<html lang="ru">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>{{.Title}}</title>
    <style>
      :root {
        color-scheme: light dark;
        --ground: #f4f6f3;
        --surface: #fff;
        --ink: #1b2320;
        --muted: #5d6a63;
        --line: #dce3de;
        --accent: #2c6b59;
        --add: #e2f2e7;
        --del: #f7e2df;
        --warn: #8a5a12;
      }
      @media (prefers-color-scheme: dark) {
        :root {
          --ground: #121614;
          --surface: #1a201d;
          --ink: #e3e9e5;
          --muted: #94a29a;
          --line: #2c3531;
          --accent: #72bba5;
          --add: #18321f;
          --del: #3a1f1c;
          --warn: #e2b66a;
        }
      }
      body {
        margin: 0;
        background: var(--ground);
        color: var(--ink);
        font:
          16px/1.5 system-ui,
          -apple-system,
          "Segoe UI",
          sans-serif;
        padding: 0 16px;
      }
      header {
        max-width: 860px;
        margin: 0 auto;
        display: flex;
        flex-wrap: wrap;
        gap: 8px 16px;
        justify-content: space-between;
        align-items: center;
        padding: 16px 0;
        border-bottom: 1px solid var(--line);
      }
      header > a {
        color: var(--ink);
        font-weight: 600;
        text-decoration: none;
      }
      main {
        max-width: 860px;
        margin: 0 auto;
        padding: 24px 0 64px;
        display: grid;
        gap: 18px;
      }
      h1 {
        font-size: 1.4rem;
        margin: 0;
        overflow-wrap: anywhere;
      }
      h2 {
        font-size: 1.05rem;
        margin: 8px 0 0;
      }
      p {
        margin: 0;
      }
      a {
        color: var(--accent);
      }
      .muted {
        color: var(--muted);
        font-size: 0.9rem;
      }
      ul.items {
        list-style: none;
        margin: 0;
        padding: 0;
        display: grid;
        gap: 8px;
      }
      ul.items li {
        background: var(--surface);
        border: 1px solid var(--line);
        border-radius: 10px;
        padding: 10px 14px;
        display: flex;
        flex-wrap: wrap;
        gap: 4px 12px;
        justify-content: space-between;
      }
      .diff {
        font:
          13px/1.45 ui-monospace,
          SFMono-Regular,
          Menlo,
          monospace;
        background: var(--surface);
        border: 1px solid var(--line);
        border-radius: 8px;
        overflow-x: auto;
      }
      .diff div {
        padding: 1px 10px;
        white-space: pre;
      }
      .diff .ctx {
        color: var(--muted);
      }
      .diff .del {
        background: var(--del);
      }
      .diff .add {
        background: var(--add);
      }
      .stale,
      .error {
        color: var(--warn);
        font-weight: 600;
      }
      .note {
        border-left: 3px solid var(--line);
        padding-left: 10px;
        color: var(--muted);
      }
      form.decide {
        display: grid;
        gap: 10px;
      }
      textarea {
        width: 100%;
        box-sizing: border-box;
        min-height: 70px;
        font: inherit;
        padding: 8px;
        border: 1px solid var(--line);
        border-radius: 8px;
        background: var(--surface);
        color: var(--ink);
      }
      .buttons {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
      }
      button {
        font: inherit;
        padding: 7px 16px;
        border-radius: 8px;
        border: 1px solid var(--line);
        background: var(--surface);
        color: var(--ink);
        cursor: pointer;
      }
      button.primary {
        background: var(--accent);
        border-color: var(--accent);
        color: var(--surface);
      }
      button:focus-visible,
      a:focus-visible,
      textarea:focus-visible {
        outline: 2px solid var(--accent);
        outline-offset: 2px;
      }
      form.inline {
        display: flex;
        gap: 8px;
        align-items: center;
        margin: 0;
      }
    </style>
  </head>
  <body>
    <header>
      <a href="/">Правки агента</a>{{if .Viewer.User}}
      <form class="inline" method="post" action="/logout">
        <input type="hidden" name="csrf" value="{{.Viewer.CSRF}}" /><span
          class="muted"
          >{{.Viewer.User}}</span
        ><button type="submit">Выйти</button>
      </form>
      {{end}}
    </header>
    <main>{{end}} {{define "foot"}}</main>
  </body>
</html>
{{end}}
```

`internal/review/templates/message.html`:

```html
{{template "head" .}}
<h1>{{.Title}}</h1>
<p>{{.Text}}</p>
<p><a href="/">К списку правок</a></p>
{{template "foot" .}}
```

`internal/review/templates/list.html` and `proposal.html` are written in Task 7; create them now with just the frame so `ParseFS` finds the names:

```html
{{template "head" .}} {{template "foot" .}}
```

(one file each: `list.html`, `proposal.html`).

- [ ] **Step 5: Handler and auth** — append to `internal/review/server.go` (add imports `crypto/hmac`, `net/http`, `net/url`):

```go
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("GET /callback", s.callback)
	mux.Handle("POST /logout", s.auth(s.logout))
	mux.Handle("GET /{$}", s.auth(s.list))
	mux.Handle("GET /p/{id}", s.auth(s.show))
	mux.Handle("POST /p/{id}/apply", s.auth(s.decide(true)))
	mux.Handle("POST /p/{id}/reject", s.auth(s.decide(false)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, viewer)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sess session
		var raw string
		ok := false
		if c, err := r.Cookie(sessionCookie); err == nil {
			raw = c.Value
			sess, ok = s.sessionFrom(raw)
		}
		if !ok {
			if r.Method != http.MethodGet {
				s.message(w, http.StatusForbidden, viewer{}, "Сессия закончилась", "Войди заново и повтори.")
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		v := viewer{User: sess.User, CSRF: s.csrfToken(raw)}
		if r.Method == http.MethodPost && !hmac.Equal([]byte(r.PostFormValue("csrf")), []byte(v.CSRF)) {
			s.message(w, http.StatusForbidden, v, "Форма устарела", "Обнови страницу и повтори.")
			return
		}
		next(w, r, v)
	})
}
```

and stubs so it compiles until Task 7 (in `pages.go`):

```go
func (s *Server) list(w http.ResponseWriter, r *http.Request, v viewer) {}
func (s *Server) show(w http.ResponseWriter, r *http.Request, v viewer) {}
func (s *Server) decide(apply bool) func(http.ResponseWriter, *http.Request, viewer) {
	return func(http.ResponseWriter, *http.Request, viewer) {}
}
```

- [ ] **Step 6: Login** — `internal/review/login.go`:

```go
package review

import (
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func localPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "/\\")
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if !localPath(next) {
		next = "/"
	}
	_, oc, err := s.oidcClient()
	if err != nil {
		s.message(w, http.StatusBadGateway, viewer{}, "Вход недоступен", "Kanidm не отвечает: "+err.Error())
		return
	}
	f := flow{Kind: "flow", State: randomHex(16), Nonce: randomHex(16), Verifier: oauth2.GenerateVerifier(),
		Next: next, Exp: s.now().Add(flowTTL).Unix()}
	s.setCookie(w, flowCookie, s.seal(f), flowTTL)
	http.Redirect(w, r, oc.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier), oidc.Nonce(f.Nonce)), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f flow
	c, err := r.Cookie(flowCookie)
	if err != nil || !s.open(c.Value, &f) || f.Kind != "flow" || f.Exp < s.now().Unix() || q.Get("state") != f.State {
		s.message(w, http.StatusBadRequest, viewer{}, "Вход не удался", "Ссылка входа устарела. Открой страницу заново.")
		return
	}
	s.clearCookie(w, flowCookie)
	if e := q.Get("error"); e != "" {
		s.message(w, http.StatusForbidden, viewer{}, "Вход не удался", "Kanidm отказал: "+e)
		return
	}
	provider, oc, err := s.oidcClient()
	if err != nil {
		s.message(w, http.StatusBadGateway, viewer{}, "Вход недоступен", "Kanidm не отвечает: "+err.Error())
		return
	}
	tok, err := oc.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		s.message(w, http.StatusBadGateway, viewer{}, "Вход не удался", "Kanidm не выдал токен: "+err.Error())
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(r.Context(), raw)
	if err != nil || idt.Nonce != f.Nonce {
		s.message(w, http.StatusForbidden, viewer{}, "Вход не удался", "Токен Kanidm не прошёл проверку.")
		return
	}
	var claims struct {
		User   string   `json:"preferred_username"`
		Groups []string `json:"groups"`
	}
	if err := idt.Claims(&claims); err != nil {
		s.message(w, http.StatusForbidden, viewer{}, "Вход не удался", "Токен Kanidm не читается.")
		return
	}
	if len(claims.Groups) == 0 {
		if ui, err := provider.UserInfo(r.Context(), oauth2.StaticTokenSource(tok)); err == nil {
			var extra struct {
				Groups []string `json:"groups"`
			}
			if ui.Claims(&extra) == nil {
				claims.Groups = extra.Groups
			}
		}
	}
	if claims.User == "" {
		claims.User = idt.Subject
	}
	if !s.allowed(claims.Groups) {
		s.message(w, http.StatusForbidden, viewer{}, "Нет доступа", "У аккаунта "+claims.User+" нет доступа к этому vault.")
		return
	}
	s.setCookie(w, sessionCookie, s.seal(session{Kind: "session", User: claims.User, Groups: claims.Groups,
		Exp: s.now().Add(sessionTTL).Unix()}), sessionTTL)
	http.Redirect(w, r, f.Next, http.StatusSeeOther)
}

func (s *Server) allowed(groups []string) bool {
	for _, g := range groups {
		short, _, _ := strings.Cut(g, "@")
		for _, a := range s.cfg.AllowedGroups {
			if short == a {
				return true
			}
		}
	}
	return false
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, v viewer) {
	s.clearCookie(w, sessionCookie)
	s.message(w, http.StatusOK, viewer{}, "Сессия закрыта", "Чтобы вернуться, открой ссылку на правку ещё раз.")
}
```

- [ ] **Step 7:** `go mod tidy && go test ./internal/review/ -v` — all PASS.
- [ ] **Step 8:** `git add go.mod go.sum internal/review && git commit -m "review: OIDC login with PKCE"`

---

### Task 7: Review pages

**Files:** Modify `internal/review/pages.go` (replace stubs), `internal/review/templates/list.html`, `internal/review/templates/proposal.html`, `internal/review/server_test.go`

- [ ] **Step 1: Failing test** — append to `internal/review/server_test.go`:

```go
func TestReviewAndApply(t *testing.T) {
	b, idp, q := setup(t)
	b.login(idp, "/")
	res, body := b.do("GET", "/", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `href="/p/p1"`) || !strings.Contains(body, "Дни рождения.md") {
		t.Fatalf("list %d\n%s", res.StatusCode, body)
	}
	res, body = b.do("GET", "/p/p1", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "- ДР: 21 марта") || !strings.Contains(body, "в чате сказали 21-е") {
		t.Fatalf("proposal %d\n%s", res.StatusCode, body)
	}
	m := csrfField.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csrf field")
	}
	if res, _ := b.do("POST", "/p/p1/apply", url.Values{"comment": {"ок"}}); res.StatusCode != http.StatusForbidden || q.applied != "" {
		t.Errorf("apply without csrf: %d", res.StatusCode)
	}
	res, _ = b.do("POST", "/p/p1/apply", url.Values{"csrf": {m[1]}, "comment": {" ок "}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/p/p1" || q.applied != "p1" || q.by != "anton" || q.comment != "ок" {
		t.Errorf("apply %d %s %+v", res.StatusCode, res.Header.Get("Location"), q)
	}
	if res, body := b.do("GET", "/p/p1", nil); !strings.Contains(body, "применено") || strings.Contains(body, `name="comment"`) {
		t.Errorf("decided page %d\n%s", res.StatusCode, body)
	}
}

func TestReject(t *testing.T) {
	b, idp, q := setup(t)
	idp.user, idp.groups = "julsa", []string{"family"}
	b.login(idp, "/")
	_, body := b.do("GET", "/p/p1", nil)
	m := csrfField.FindStringSubmatch(body)
	b.do("POST", "/p/p1/reject", url.Values{"csrf": {m[1]}, "comment": {"не надо"}})
	if q.rejected != "p1" || q.by != "julsa" || q.comment != "не надо" {
		t.Errorf("got %+v", q)
	}
}

func TestUnknownProposal(t *testing.T) {
	b, idp, _ := setup(t)
	b.login(idp, "/")
	if res, _ := b.do("GET", "/p/nope", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	b, idp, _ := setup(t)
	b.login(idp, "/")
	_, body := b.do("GET", "/", nil)
	m := csrfField.FindStringSubmatch(body)
	b.do("POST", "/logout", url.Values{"csrf": {m[1]}})
	if res, _ := b.do("GET", "/", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("still signed in: %d", res.StatusCode)
	}
}
```

- [ ] **Step 2:** `go test ./internal/review/ -run 'Review|Reject|Unknown|Logout'` — FAIL (empty pages).

- [ ] **Step 3: Handlers** — in `internal/review/pages.go` replace the three stubs with (add imports `errors`, `strings`):

```go
func (s *Server) list(w http.ResponseWriter, r *http.Request, v viewer) {
	pending, decided, err := s.queue.ListProposals(r.Context())
	if err != nil {
		s.message(w, http.StatusInternalServerError, v, "Ошибка", err.Error())
		return
	}
	s.render(w, http.StatusOK, "list.html", listPage{Title: "Правки агента", Viewer: v, Pending: pending, Decided: decided})
}

func (s *Server) show(w http.ResponseWriter, r *http.Request, v viewer) {
	s.showProposal(w, r, v, http.StatusOK, "")
}

func (s *Server) showProposal(w http.ResponseWriter, r *http.Request, v viewer, status int, errText string) {
	p, ok, err := s.queue.GetProposal(r.Context(), r.PathValue("id"))
	if err != nil {
		s.message(w, http.StatusInternalServerError, v, "Ошибка", err.Error())
		return
	}
	if !ok {
		s.message(w, http.StatusNotFound, v, "Такой правки нет", "Возможно, ссылка неполная.")
		return
	}
	var edits []notes.EditPreview
	if p.Status == "pending" {
		edits = s.queue.PreviewProposal(p)
	}
	s.render(w, status, "proposal.html", proposalPage{Title: p.Path, Viewer: v, P: p, Edits: edits, Error: errText})
}

func (s *Server) decide(apply bool) func(http.ResponseWriter, *http.Request, viewer) {
	return func(w http.ResponseWriter, r *http.Request, v viewer) {
		id := r.PathValue("id")
		comment := strings.TrimSpace(r.PostFormValue("comment"))
		var err error
		if apply {
			_, err = s.queue.ApplyProposal(r.Context(), id, v.User, comment)
		} else {
			_, err = s.queue.RejectProposal(r.Context(), id, v.User, comment)
		}
		if err != nil && !errors.Is(err, index.ErrNotPending) {
			s.showProposal(w, r, v, http.StatusInternalServerError, err.Error())
			return
		}
		http.Redirect(w, r, "/p/"+id, http.StatusSeeOther)
	}
}
```

- [ ] **Step 4: Templates** — `internal/review/templates/list.html`:

```html
{{template "head" .}}
<h1>Ждут решения</h1>
{{if .Pending}}
<ul class="items">
  {{range .Pending}}
  <li>
    <a href="/p/{{.ID}}">{{.Path}}</a
    ><span class="muted">{{when .CreatedAt}}</span>
  </li>
  {{end}}
</ul>
{{else}}
<p class="muted">Ничего не ждёт.</p>
{{end}} {{if .Decided}}
<h2>Недавно решённые</h2>
<ul class="items">
  {{range .Decided}}
  <li>
    <a href="/p/{{.ID}}">{{.Path}}</a
    ><span class="muted"
      >{{status .Status}}{{if .DecidedBy}} · {{.DecidedBy}}{{end}} · {{when
      .DecidedAt}}</span
    >
  </li>
  {{end}}
</ul>
{{end}} {{template "foot" .}}
```

`internal/review/templates/proposal.html`:

```html
{{template "head" .}}
<h1>{{.P.Path}}</h1>
<p class="muted">
  {{status .P.Status}} · предложено {{when .P.CreatedAt}}{{if .P.DecidedBy}} ·
  {{.P.DecidedBy}}, {{when .P.DecidedAt}}{{end}}
</p>
{{if .Error}}
<p class="error">Не получилось: {{.Error}}</p>
{{end}} {{if .P.Comment}}
<p class="note">Комментарий: {{.P.Comment}}</p>
{{end}} {{if eq .P.Status "pending"}}
<p class="note">Причина от агента: {{.P.Reason}}</p>
{{range .Edits}}
<div class="diff">
  {{if .Stale}}
  <div class="stale">
    Этого текста в заметке уже нет или он встречается несколько раз — применить
    не выйдет
  </div>
  {{end}} {{range .Before}}
  <div class="ctx">{{.}}</div>
  {{end}} {{range .Old}}
  <div class="del">- {{.}}</div>
  {{end}} {{range .New}}
  <div class="add">+ {{.}}</div>
  {{end}} {{range .After}}
  <div class="ctx">{{.}}</div>
  {{end}}
</div>
{{if not .Stale}}
<p class="muted">с строки {{.Line}}</p>
{{end}} {{end}}
<form class="decide" method="post" action="/p/{{.P.ID}}/apply">
  <input type="hidden" name="csrf" value="{{.Viewer.CSRF}}" />
  <label class="muted" for="comment"
    >Комментарий для агента (необязательно)</label
  >
  <textarea id="comment" name="comment"></textarea>
  <div class="buttons">
    <button class="primary" type="submit">Применить</button
    ><button type="submit" formaction="/p/{{.P.ID}}/reject">Отклонить</button>
  </div>
</form>
{{end}} {{template "foot" .}}
```

- [ ] **Step 5:** `go test -race ./internal/review/ -v` — all PASS.
- [ ] **Step 6:** `git add internal/review && git commit -m "review: list, proposal page, apply and reject"`

---

### Task 8: Wire it into main

**Files:** Modify `cmd/md-vault-mcp/main.go`, `md-vault-mcp.example.toml`

- [ ] **Step 1: Code** — in `run`, after the `svc.Watch` block and before `server := mcp.NewServer(...)`, add:

```go
	if cfg.Review != nil {
		svc.EnableReview(cfg.Review.PublicURL)
	}
```

after `svc.Register(server)` add:

```go
	if cfg.Review != nil {
		stopReview, err := startReview(ctx, svc, cfg)
		if err != nil {
			return fmt.Errorf("review: %w", err)
		}
		defer stopReview()
	}
```

and add the function (imports `net`, `path/filepath`, `github.com/pltanton/md-vault-mcp/internal/review`):

```go
func startReview(ctx context.Context, svc *notes.Service, cfg notes.Config) (func(), error) {
	r := cfg.Review
	secret, err := os.ReadFile(r.OIDCClientSecretFile)
	if err != nil {
		return nil, err
	}
	key, err := review.LoadKey(filepath.Join(cfg.StateDir, "review.key"))
	if err != nil {
		return nil, err
	}
	rs := review.New(review.Config{
		PublicURL: r.PublicURL, AllowedGroups: r.AllowedGroups, Issuer: r.OIDCIssuer,
		ClientID: r.OIDCClientID, ClientSecret: strings.TrimSpace(string(secret)), Key: key,
	}, svc)
	ln, err := net.Listen("tcp", r.Listen)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: rs.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("review: %v", err)
		}
	}()
	go func() {
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			if err := svc.ExpireProposals(ctx); err != nil && ctx.Err() == nil {
				log.Printf("expire proposals: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	log.Printf("review page on %s", r.Listen)
	return func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}, nil
}
```

- [ ] **Step 2: Example config** — append to `md-vault-mcp.example.toml`:

```toml

# Optional: a page where people apply or reject the agent's proposals.
[review]
listen = "127.0.0.1:8193"
public_url = "https://notes.example.org"
allowed_groups = ["admins"]
oidc_issuer = "https://id.example.org/oauth2/openid/notes"
oidc_client_id = "notes"
oidc_client_secret_file = "/run/secrets/md-vault-mcp-oidc"
```

`TestLoadExampleConfig` must still pass: add `|| cfg.Review == nil || cfg.Review.Listen != "127.0.0.1:8193"` to its condition.

- [ ] **Step 3: Smoke** — inside `nix develop`, with a throwaway cluster as in the search plan (Task 10 Step 6 there), a config whose `[review]` points `oidc_issuer` at `http://127.0.0.1:1` and `oidc_client_secret_file` at a file with `x`, start the binary with `-transport http -listen 127.0.0.1:18093 -token-file <16+ chars>` in the background, then:

```bash
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' http://127.0.0.1:8193/
curl -s -w '\n%{http_code}\n' http://127.0.0.1:8193/login | tail -2
```

Expected: `303 http://127.0.0.1:8193/login?next=%2F`, then a page containing `Kanidm не отвечает` and `502`. Stop the binary and the cluster.

- [ ] **Step 4:** `go vet ./... && go test -count=1 ./...` — PASS.
- [ ] **Step 5:** `git add cmd md-vault-mcp.example.toml internal/notes/config_test.go && git commit -m "Serve the review page next to the MCP server"`

---

### Task 9: NixOS module

**Files:** Modify `nix/module.nix`

- [ ] **Step 1: Options** — in `instanceType`, after the `database` block:

```nix
      review = {
        enable = lib.mkEnableOption "the page where people apply or reject the agent's proposals";
        listen = lib.mkOption {
          type = lib.types.str;
          example = "127.0.0.1:8193";
        };
        publicUrl = lib.mkOption {
          type = lib.types.str;
          example = "https://notes.example.org";
          description = "Address people open; the proposal links and the OIDC redirect use it.";
        };
        allowedGroups = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          description = "Groups whose members may apply or reject, compared with the part of each groups claim before @.";
        };
        oidc = {
          issuer = lib.mkOption {
            type = lib.types.str;
            example = "https://id.example.org/oauth2/openid/notes";
          };
          clientId = lib.mkOption {type = lib.types.str;};
          clientSecretFile = lib.mkOption {
            type = lib.types.str;
            description = "Runtime path of the client secret, passed via LoadCredential; not a Nix path.";
          };
        };
      };
```

- [ ] **Step 2: Config and credential** — in `service`, replace `// inst.extraSettings);` with:

```nix
      // lib.optionalAttrs inst.review.enable {
        review = {
          inherit (inst.review) listen;
          public_url = inst.review.publicUrl;
          allowed_groups = inst.review.allowedGroups;
          oidc_issuer = inst.review.oidc.issuer;
          oidc_client_id = inst.review.oidc.clientId;
          oidc_client_secret_file = "/run/credentials/md-vault-mcp-${name}.service/oidc";
        };
      }
      // inst.extraSettings);
```

and `LoadCredential = ["token:${inst.tokenFile}"];` with:

```nix
        LoadCredential = ["token:${inst.tokenFile}"] ++ lib.optional inst.review.enable "oidc:${inst.review.oidc.clientSecretFile}";
```

- [ ] **Step 3: Evaluate** — the flake-module `nix eval` from the search plan's Task 11, with the instance extended by:

```nix
review = {
  enable = true;
  listen = "127.0.0.1:8193";
  publicUrl = "https://notes.example.org";
  allowedGroups = ["admins"];
  oidc = { issuer = "https://id.example.org/oauth2/openid/notes"; clientId = "notes"; clientSecretFile = "/var/lib/sso-secrets/notes"; };
};
```

and returning `cred = c.systemd.services.md-vault-mcp-my-notes.serviceConfig.LoadCredential; exec = ...ExecStart;`. Expected: `cred` has `oidc:/var/lib/sso-secrets/notes`; the generated TOML (build its path, `cat`) has a `[review]` table with `oidc_client_secret_file = "/run/credentials/md-vault-mcp-my-notes.service/oidc"`. Also evaluate an instance without `review` and check the TOML has no `[review]`.

- [ ] **Step 4:** `git add nix/module.nix && git commit -m "NixOS module: review page options"`

---

### Task 10: Docs, package hash, full check, merge

**Files:** Modify `README.md`, `nix/package.nix`

- [ ] **Step 1: README** — add rows to the tools table after `base_query`:

```markdown
| `note_propose(path, edits, reason)` | Propose replacing exact pieces of text the agent may not change directly; a person applies or rejects it on the review page. Applied at once when every edit is inside the agent's own live blocks. Returns the page's URL. |
| `note_proposals(status?, limit?)` | The agent's proposals with status (`pending`, `applied`, `rejected`, `stale`, `expired`) and the reviewer's comment. |
```

a section before `## Access rules`:

```markdown
### Review

With `[review]` configured, the server also serves a page on its own listener
where people apply or reject the agent's proposals. Login is OpenID Connect
(code flow with PKCE) against the configured issuer; only members of
`allowed_groups` get in (a `groups` claim of `name@domain` counts as `name`),
and the agent's bearer token is never accepted there. Applying checks that
every replaced piece of text still occurs exactly once; otherwise the proposal
becomes `stale`. A decision erases the proposed text and the agent's reason,
keeping who decided, when and the comment; proposals left pending for 30 days
expire.
```

rows in the configuration table for each `review.*` key (`listen`, `public_url`, `allowed_groups`, `oidc_issuer`, `oidc_client_id`, `oidc_client_secret_file`), and in `## NixOS` a sentence: "`review.enable` and its options add the review page; the client secret goes through `LoadCredential` as `oidc`."

- [ ] **Step 2: vendorHash** — `lib.fakeHash`, `nix build .#md-vault-mcp 2>&1 | grep got:`, set it, build again.
- [ ] **Step 3: Full check** — `gofmt -l . && go vet ./... && go test -race -count=1 ./... && go test -count=1 -v ./... 2>&1 | grep -c -- '--- SKIP'` — nothing from gofmt, `ok` everywhere, `0`.
- [ ] **Step 4:** `git add README.md nix/package.nix && git commit -m "README: review queue"`; merge `review-queue` into `master` fast-forward, run tests on `master`, push.

---

### Task 11: Deploy to home-server

**Files (in `~/dev/nixos-config-flake`):** `servers/nix/nixos-configurations/home-server/modules/sso.nix`, `.../md-vault-mcp.nix`, `dns/dnsconfig.js`, `servers/flake.lock`

That repo has someone's uncommitted kena SSO work in `kena.nix`, `sso.nix` and `flake.lock`; never commit or deploy it.

- [ ] **Step 1: Clean worktree** — `git -C ~/dev/nixos-config-flake worktree add --detach <scratch>/nixcfg HEAD`; in `servers/` run `nix flake update md-vault-mcp`; apply only the kena hunk of the main checkout's `flake.lock` diff (the server runs kena `d78b6fa`).
- [ ] **Step 2: DNS** — in `dns/dnsconfig.js` next to `CNAME("kena", …)` add `CNAME("notes", "home.pltanton.dev."), CNAME("family-notes", "home.pltanton.dev."),`.
- [ ] **Step 3: SSO clients** — in `sso.nix` `clients`, right after the `hass` client (the uncommitted kena client sits after `nextcloud`; a different spot keeps the hunks apart):

```nix
      notes = {
        redirects = ["https://notes.pltanton.dev/callback"];
        landing = "https://notes.pltanton.dev";
        groups = [];
        secretGroup = "syncthing";
      };
      family-notes = {
        redirects = ["https://family-notes.pltanton.dev/callback"];
        landing = "https://family-notes.pltanton.dev";
        groups = ["family"];
        secretGroup = "syncthing";
      };
```

- [ ] **Step 4: Instances** — in `md-vault-mcp.nix` add to each vault in `vaults` a `review` attr (`personal`: host `notes`, listen `127.0.0.1:8193`, groups `["admins"]`; `family`: host `family-notes`, `127.0.0.1:8194`, `["admins" "family"]`), pass it in `services.md-vault-mcp.instances` as

```nix
      review = {
        enable = true;
        inherit (v.review) listen;
        publicUrl = "https://${v.review.host}.pltanton.dev";
        allowedGroups = v.review.groups;
        oidc = {
          issuer = "https://home.id.pltanton.dev/oauth2/openid/${v.review.host}";
          clientId = v.review.host;
          clientSecretFile = "/var/lib/sso-secrets/${v.review.host}";
        };
      };
```

order the services after `sso-secrets.service` (`systemd.services."md-vault-mcp-${name}" = {after = ["sso-secrets.service"]; wants = ["sso-secrets.service"];}` for each), and add Caddy hosts `services.caddy.virtualHosts."${v.review.host}.pltanton.dev".extraConfig = "reverse_proxy http://${v.review.listen}";`.

- [ ] **Step 5: Deploy** — `nh os switch ./servers -H home-server -e none --target-host root@home.kaliwe.ru --build-host root@home.kaliwe.ru` from the worktree; check the diff names only md-vault-mcp (and nothing rolls back), both services active, `/var/lib/sso-secrets/notes` exists, `curl -sI https://notes.pltanton.dev/` after DNS answers → `303` to `/login`, and `/login` → `302` to `home.id.pltanton.dev`.
- [ ] **Step 6: DNS push** — `just dns-preview`, then `just dns-push` (only the two CNAMEs may appear in the preview).
- [ ] **Step 7: Commit** — commit the worktree's changes (lock bump, sso.nix, md-vault-mcp.nix, dnsconfig.js) to the main checkout without touching the kena hunks (apply the worktree diff with `git apply --cached` and to the working tree), push, remove the worktree.
- [ ] **Step 8: Ask the owner** to open a proposal link and sign in once; a real Kanidm login cannot be done by the agent.
