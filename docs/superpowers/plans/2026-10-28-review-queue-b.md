# Review Queue (Plan B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `file.backlinks` in `base_query`; the agent proposes deleting or moving notes, and a move rewrites the links to the note in the same proposal.

**Architecture:** No links table (spec amendment): `internal/notes/links.go` finds link occurrences, resolves them against the readable notes and rewrites them; proposing walks the vault. `index.Proposal` gains `LinkEdits`. Applying a delete or move checks the note's HMAC against `base_hash`. The review page shows a delete with the note's text and the notes that link to it, and a move with every link rewrite.

**Tech Stack:** Go, existing packages. Spec: `docs/superpowers/specs/2026-10-28-review-queue-design.md`.

**Conventions:** as in plan A (dev shell, no SKIP, no comments, signed commits with the Co-Authored-By line).

---

### Task 1: `file.backlinks`

**Files:** Modify `internal/notes/base_query.go`, `internal/notes/base_test.go`

- [ ] **Step 1: Failing test** — append to `internal/notes/base_test.go`:

```go
func TestBacklinks(t *testing.T) {
	q := &baseQuery{now: time.Now(), formulas: map[string]node{}, byPath: map[string]*baseFile{}, byName: map[string]*baseFile{}}
	add := func(p string, links ...string) *baseFile {
		f := &baseFile{path: p, props: map[string]any{}, formulas: map[string]any{}}
		for _, l := range links {
			f.links = append(f.links, linkVal{target: l})
		}
		q.files = append(q.files, f)
		q.byPath[strings.ToLower(p)] = f
		q.byName[strings.ToLower(f.basename())] = f
		return f
	}
	bob := add("People/Bob.md", "Bob")
	add("A.md", "Bob", "People/Bob")
	add("B.md", "Bob.md")
	add("C.md", "Alice")
	got, ok := evalString(t, q, bob, "file.backlinks").([]any)
	if !ok || len(got) != 2 {
		t.Errorf("got %#v", evalString(t, q, bob, "file.backlinks"))
	}
}
```

- [ ] **Step 2:** `go test ./internal/notes/ -run TestBacklinks` — FAIL (`unsupported`).

- [ ] **Step 3: Implement** — add `backlinks map[*baseFile][]any` to `baseQuery`, replace the `case "backlinks"` branch in `baseFile.field` with `return q.backlinksOf(f), nil`, and add:

```go
func (q *baseQuery) backlinksOf(f *baseFile) []any {
	if q.backlinks == nil {
		q.backlinks = map[*baseFile][]any{}
		for _, g := range q.files {
			seen := map[*baseFile]bool{}
			for _, l := range append(append([]linkVal(nil), g.links...), g.embeds...) {
				if t := q.resolve(l.target); t != nil && t != g && !seen[t] {
					seen[t] = true
					q.backlinks[t] = append(q.backlinks[t], linkVal{target: strings.TrimSuffix(g.path, ".md")})
				}
			}
		}
	}
	return append([]any(nil), q.backlinks[f]...)
}
```

- [ ] **Step 4:** `go test ./internal/notes/ -run 'Base|Backlinks'` — PASS. README: drop `file.backlinks` from "Not supported".
- [ ] **Step 5:** commit `base_query: file.backlinks`.

---

### Task 2: Link occurrences, resolution, rewriting

**Files:** Create `internal/notes/links.go`, `internal/notes/links_test.go`

- [ ] **Step 1: Failing test** — `internal/notes/links_test.go`:

````go
package notes

import (
	"fmt"
	"strings"
	"testing"
)

func TestLinkRefs(t *testing.T) {
	lines := []string{
		"See [[Мама]] and ![[Люди/Мама#ДР|день]] and [мама](Люди/Мама.md#ДР).",
		"`[[Code]]` and [site](https://x.org) and ![img](a.png) and [[ ]]",
		"```",
		"[[Fenced]]",
		"```",
		"[esc](../Люди/Мама%20Ира.md)",
	}
	var got []string
	for _, r := range linkRefs(lines) {
		got = append(got, fmt.Sprintf("%d %v %v %s %s", r.line, r.embed, r.md, r.target, r.suffix))
	}
	want := []string{
		"0 false false Мама ",
		"0 true false Люди/Мама #ДР|день",
		"0 false true Люди/Мама.md #ДР",
		"5 false true ../Люди/Мама Ира.md ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestResolveAndRewrite(t *testing.T) {
	ix := newNoteIndex([]string{"Люди/Мама.md", "Дом/Мама.md", "Люди/Папа.md", "Inbox/Заметка.md", "Корень.md"})
	from := "Inbox/Заметка.md"
	ref := func(s string) linkRef { return linkRefs([]string{s})[0] }
	for _, c := range []struct{ link, resolved string }{
		{"[[Папа]]", "Люди/Папа.md"},
		{"[[Мама]]", ""},
		{"[[Люди/Мама]]", "Люди/Мама.md"},
		{"[x](../Люди/Папа.md)", "Люди/Папа.md"},
		{"[x](Люди/Папа.md)", "Люди/Папа.md"},
		{"[[Нет такой]]", ""},
	} {
		if got := ix.resolve(from, ref(c.link)); got != c.resolved {
			t.Errorf("%s resolved to %q, want %q", c.link, got, c.resolved)
		}
	}
	for _, c := range []struct{ link, newPath, want string }{
		{"[[Папа]]", "Люди/Отец.md", "[[Отец]]"},
		{"[[Папа#ДР|папа]]", "Люди/Отец.md", "[[Отец#ДР|папа]]"},
		{"![[Папа]]", "Архив/Папа.md", "![[Папа]]"},
		{"[[Папа]]", "Дом/Мама.md", "[[Дом/Мама]]"},
		{"[[Люди/Папа]]", "Архив/Папа.md", "[[Архив/Папа]]"},
		{"[[Люди/Папа.md]]", "Архив/Папа.md", "[[Архив/Папа.md]]"},
		{"[x](../Люди/Папа.md#a)", "Архив/Старое/Папа.md", "[x](../Архив/Старое/Папа.md#a)"},
		{"[x](Люди/Папа.md)", "Архив/Папа.md", "[x](Архив/Папа.md)"},
		{"[x](../Люди/Папа.md)", "Inbox/Папа Ира.md", "[x](Папа Ира.md)"},
		{"[x](../Люди/%D0%9F%D0%B0%D0%BF%D0%B0.md)", "Архив/Папа Ира.md", "[x](../Архив/Папа%20Ира.md)"},
	} {
		if got := ix.rewrite(from, ref(c.link), "Люди/Папа.md", c.newPath); got != c.want {
			t.Errorf("%s → %s: got %s, want %s", c.link, c.newPath, got, c.want)
		}
	}
}
````

- [ ] **Step 2:** `go test ./internal/notes/ -run 'TestLinkRefs|TestResolveAndRewrite'` — FAIL, undefined.

- [ ] **Step 3: Implement** — `internal/notes/links.go`:

````go
package notes

import (
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	wikiRef = regexp.MustCompile(`(!?)\[\[([^\]|#^\n]*)([#^][^\]|\n]*)?(\|[^\]\n]*)?\]\]`)
	mdRef   = regexp.MustCompile(`(!?)\[([^\]\n]*)\]\(([^)\s]+)\)`)
)

type linkRef struct {
	line    int
	raw     string
	embed   bool
	md      bool
	target  string
	suffix  string
	text    string
	encoded bool
}

func maskInlineCode(l string) string {
	b := []byte(l)
	in := false
	for i, c := range b {
		if c == '`' {
			in = !in
			continue
		}
		if in {
			b[i] = ' '
		}
	}
	return string(b)
}

func linkRefs(lines []string) []linkRef {
	var out []linkRef
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		masked := maskInlineCode(l)
		for _, m := range wikiRef.FindAllStringSubmatchIndex(masked, -1) {
			target := strings.TrimSpace(l[m[4]:m[5]])
			if target == "" {
				continue
			}
			suffix := ""
			if m[6] >= 0 {
				suffix += l[m[6]:m[7]]
			}
			if m[8] >= 0 {
				suffix += l[m[8]:m[9]]
			}
			out = append(out, linkRef{line: i, raw: l[m[0]:m[1]], embed: m[3] > m[2], target: target, suffix: suffix})
		}
		for _, m := range mdRef.FindAllStringSubmatchIndex(masked, -1) {
			href := l[m[6]:m[7]]
			if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
				continue
			}
			p, frag, hasFrag := strings.Cut(href, "#")
			dec, err := url.PathUnescape(p)
			if err != nil {
				dec = p
			}
			if ext := strings.ToLower(path.Ext(dec)); ext != "" && ext != ".md" {
				continue
			}
			suffix := ""
			if hasFrag {
				suffix = "#" + frag
			}
			out = append(out, linkRef{line: i, raw: l[m[0]:m[1]], embed: m[3] > m[2], md: true, target: dec,
				suffix: suffix, text: l[m[4]:m[5]], encoded: dec != p})
		}
	}
	return out
}

type noteIndex struct {
	byPath map[string]string
	byName map[string][]string
}

func noteName(p string) string { return strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p))) }

func newNoteIndex(paths []string) noteIndex {
	ix := noteIndex{byPath: map[string]string{}, byName: map[string][]string{}}
	for _, p := range paths {
		ix.byPath[strings.ToLower(p)] = p
		ix.byName[noteName(p)] = append(ix.byName[noteName(p)], p)
	}
	return ix
}

func (ix noteIndex) lookup(p string) string {
	p = strings.ToLower(strings.TrimPrefix(path.Clean("/"+p), "/"))
	if q, ok := ix.byPath[p]; ok {
		return q
	}
	return ix.byPath[p+".md"]
}

func (ix noteIndex) resolve(from string, r linkRef) string {
	t := strings.TrimPrefix(r.target, "/")
	dirRel := path.Join(path.Dir(from), t)
	if r.md {
		if p := ix.lookup(dirRel); p != "" {
			return p
		}
		return ix.lookup(t)
	}
	if p := ix.lookup(t); p != "" {
		return p
	}
	if strings.Contains(t, "/") {
		return ix.lookup(dirRel)
	}
	if ps := ix.byName[noteName(t)]; len(ps) == 1 {
		return ps[0]
	}
	return ""
}

func (ix noteIndex) uniqueAfterMove(oldPath, newPath string) bool {
	n := 1
	for _, p := range ix.byName[noteName(newPath)] {
		if p != oldPath && p != newPath {
			n++
		}
	}
	return n == 1
}

func (ix noteIndex) rewrite(from string, r linkRef, oldPath, newPath string) string {
	bang := ""
	if r.embed {
		bang = "!"
	}
	if r.md {
		href := newPath
		if ix.lookup(path.Join(path.Dir(from), strings.TrimPrefix(r.target, "/"))) == oldPath {
			if rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(newPath)); err == nil {
				href = filepath.ToSlash(rel)
			}
		}
		if !strings.EqualFold(path.Ext(r.target), ".md") {
			href = strings.TrimSuffix(href, ".md")
		}
		if r.encoded {
			href = strings.ReplaceAll(href, " ", "%20")
		}
		return bang + "[" + r.text + "](" + href + r.suffix + ")"
	}
	noExt := strings.TrimSuffix(newPath, ".md")
	target := noExt
	if !strings.Contains(r.target, "/") && ix.uniqueAfterMove(oldPath, newPath) {
		target = path.Base(noExt)
	}
	if strings.EqualFold(path.Ext(r.target), ".md") {
		target += ".md"
	}
	return bang + "[[" + target + r.suffix + "]]"
}
````

- [ ] **Step 4:** same command — PASS.
- [ ] **Step 5:** commit `notes: find, resolve and rewrite links`.

---

### Task 3: `LinkEdits` in the proposals store

**Files:** Modify `internal/index/proposals.go`, `internal/index/proposals_test.go`

- [ ] **Step 1: Test** — in `TestProposalLifecycle` create the proposal with `LinkEdits: json.RawMessage(`{"notes":[]}`)` and assert `got.LinkEdits != nil` before the decision and `decided.LinkEdits == nil` after.
- [ ] **Step 2: Implement** — add `LinkEdits json.RawMessage` after `Edits` in `Proposal`; in `proposalCols` put `link_edits` after `edits`; scan it into a `[]byte` like `edits`; in `CreateProposal` add `link_edits` with `$9::jsonb` and pass `rawJSON(p.LinkEdits)`.
- [ ] **Step 3:** `go test ./internal/index/` — PASS; commit `index: proposals keep link edits`.

---

### Task 4: Delete and move proposals

**Files:** Create `internal/notes/moves.go`, `internal/notes/moves_test.go`; modify `internal/notes/proposals.go` (`ApplyProposal` dispatch, `ProposeResult`), `internal/notes/tools.go`

- [ ] **Step 1: Failing test** — `internal/notes/moves_test.go`:

```go
package notes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposeMoveRewritesLinks(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	f.write("Tasks.md", "корневая заметка про подарок [[Alice]] и снова [[Alice|её]]\n")
	f.write("00. Inbox/Покупки.md", "# Купить\n- для [Алисы](../02.%20Areas/Personal/People/Alice.md)\n")
	f.useIndex(accessRules())
	add, err := f.svc.Add(julia, "- Варежки", "## Идеи для подарков")
	if err != nil {
		t.Fatal(err)
	}
	newPath := "02. Areas/Personal/People/Алиса.md"
	res, err := f.svc.ProposeMove(ctx, julia, newPath, "переименовать по-русски")
	if err != nil || res.Status != "pending" || res.Links != 3 {
		t.Fatalf("propose %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(f.vault, filepath.FromSlash(julia))); err != nil {
		t.Fatal("moved before approval")
	}
	p, err := f.svc.ApplyProposal(ctx, res.ID, "anton", "")
	if err != nil || p.Status != "applied" {
		t.Fatalf("apply %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(f.vault, filepath.FromSlash(julia))); !os.IsNotExist(err) {
		t.Error("old file still there")
	}
	if got := f.read("Tasks.md"); !strings.Contains(got, "[[Алиса]]") || !strings.Contains(got, "[[Алиса|её]]") {
		t.Errorf("Tasks.md: %s", got)
	}
	if got := f.read("00. Inbox/Покупки.md"); !strings.Contains(got, "[Алисы](../02.%20Areas/Personal/People/Алиса.md)") {
		t.Errorf("Покупки.md: %s", got)
	}
	rec, _ := f.svc.Recent(0)
	if len(rec.Blocks) != 1 || rec.Blocks[0].ID != add.ID || rec.Blocks[0].Path != newPath {
		t.Errorf("journal did not follow the move: %+v", rec.Blocks)
	}
	if got := matchKeys(f.search("варежки", "")); got != newPath+":6" {
		t.Errorf("search after move: %s", got)
	}
}

func TestMoveRefusals(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	for _, c := range []struct{ from, to string }{
		{julia, "Tasks.md"},
		{julia, julia},
		{julia, "02. Areas/Work/Alice.md"},
		{"02. Areas/Work/Job.md", "Job.md"},
	} {
		if _, err := f.svc.ProposeMove(ctx, c.from, c.to, "r"); err == nil {
			t.Errorf("%s → %s accepted", c.from, c.to)
		}
	}
	res, err := f.svc.ProposeMove(ctx, julia, "Alice.md", "r")
	if err != nil {
		t.Fatal(err)
	}
	f.write(julia, f.read(julia)+"- ещё\n")
	if p, err := f.svc.ApplyProposal(ctx, res.ID, "anton", ""); err != nil || p.Status != "stale" {
		t.Errorf("changed source: %+v %v", p, err)
	}
}

func TestProposeDelete(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	f.write("Tasks.md", "ссылка на [[Покупки]]\n")
	f.useIndex(accessRules())
	res, err := f.svc.ProposeDelete(ctx, "00. Inbox/Покупки.md", "дубль")
	if err != nil || res.Status != "pending" || len(res.Linked) != 1 || res.Linked[0] != "Tasks.md" {
		t.Fatalf("propose %+v %v", res, err)
	}
	p, err := f.svc.ApplyProposal(ctx, res.ID, "julsa", "")
	if err != nil || p.Status != "applied" {
		t.Fatalf("apply %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(f.vault, "00. Inbox", "Покупки.md")); !os.IsNotExist(err) {
		t.Error("still there")
	}
	if got := matchKeys(f.search("молоко", "")); got != "" {
		t.Errorf("still indexed: %s", got)
	}
}
```

with a helper in `internal/notes/search_test.go`:

```go
func accessRules() access.RuleSet { return access.RuleSet{} }
```

(`useIndex` with an empty rule set re-syncs the existing service.)

- [ ] **Step 2:** `go test ./internal/notes/ -run 'Move|Delete'` — FAIL, undefined.

- [ ] **Step 3: Result fields** — in `internal/notes/proposals.go` add to `ProposeResult`:

```go
	Links      int      `json:"links,omitempty" jsonschema:"for a move: links in other notes that will be rewritten"`
	Linked     []string `json:"linked,omitempty" jsonschema:"for a delete: notes that link to it and will have broken links"`
	Unwritable []string `json:"unwritable,omitempty" jsonschema:"notes linking to it that you may not change; their links will break"`
```

and at the top of the `Decide` callback in `ApplyProposal`, replace the kind check with:

```go
		switch p.Kind {
		case "delete":
			return s.applyDelete(p, by, comment)
		case "move":
			return s.applyMove(p, by, comment)
		case "edit":
		default:
			return index.Decision{}, fmt.Errorf("unknown proposal kind %s", p.Kind)
		}
```

and set `applied = true` in a new `defer`-free way: after `Decide` returns, reindex is done by the delete/move helpers themselves, so keep the existing `if err == nil && applied` for edits only.

- [ ] **Step 4: Implement** — `internal/notes/moves.go`:

```go
package notes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

type LinkEdit struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

type LinkChanges struct {
	Notes      []LinkEdit `json:"notes,omitempty"`
	Linked     []string   `json:"linked,omitempty"`
	Unwritable []string   `json:"unwritable,omitempty"`
	Skipped    []string   `json:"skipped,omitempty"`
}

func (s *Service) readableNotes() []string {
	var out []string
	s.walk(s.vault.Root(), "", func(rel, _ string) bool {
		out = append(out, rel)
		return true
	})
	return out
}

func (s *Service) linksTo(target string, newPath string) LinkChanges {
	paths := s.readableNotes()
	ix := newNoteIndex(paths)
	var ch LinkChanges
	for _, from := range paths {
		real, clean, err := s.vault.ResolveRead(from)
		if err != nil {
			continue
		}
		st, err := s.readFile(real, clean)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		var edits []Edit
		for _, r := range linkRefs(st.doc.lines) {
			if seen[r.raw] || ix.resolve(from, r) != target {
				continue
			}
			seen[r.raw] = true
			if newPath != "" {
				if nr := ix.rewrite(from, r, target, newPath); nr != r.raw {
					edits = append(edits, Edit{Old: r.raw, New: nr})
				}
			} else {
				edits = append(edits, Edit{Old: r.raw})
			}
		}
		if len(edits) == 0 || from == target {
			continue
		}
		ch.Linked = append(ch.Linked, from)
		if !s.vault.Policy.CanWrite(from) {
			ch.Unwritable = append(ch.Unwritable, from)
		} else if newPath != "" {
			ch.Notes = append(ch.Notes, LinkEdit{Path: from, Edits: edits})
		}
	}
	sort.Strings(ch.Linked)
	sort.Strings(ch.Unwritable)
	return ch
}

func (s *Service) proposeWhole(ctx context.Context, kind, rel, newRel, reason string) (ProposeResult, error) {
	if s.reviewURL == "" || s.index == nil {
		return ProposeResult{}, errors.New("review is not configured on this server")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ProposeResult{}, errors.New("reason must not be empty: the person reviewing it reads it")
	}
	real, clean, err := s.vault.ResolveWrite(rel)
	if err != nil {
		return ProposeResult{}, err
	}
	newClean := ""
	if kind == "move" {
		if _, newClean, err = s.vault.ResolveCreate(newRel); err != nil {
			return ProposeResult{}, err
		}
		if strings.EqualFold(newClean, clean) {
			return ProposeResult{}, errors.New("the new path is the same note")
		}
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return ProposeResult{}, err
	}
	ch := s.linksTo(clean, newClean)
	payload, err := json.Marshal(ch)
	if err != nil {
		return ProposeResult{}, err
	}
	id := newProposalID()
	if err := s.index.CreateProposal(ctx, index.Proposal{ID: id, Kind: kind, Path: clean, NewPath: newClean,
		LinkEdits: payload, Reason: reason, BaseHash: s.journal.hash(st.doc.lines)}); err != nil {
		return ProposeResult{}, err
	}
	res := ProposeResult{ID: id, URL: s.proposalURL(id), Status: "pending", Path: clean, Unwritable: ch.Unwritable}
	if kind == "move" {
		for _, n := range ch.Notes {
			res.Links += len(n.Edits)
		}
	} else {
		res.Linked = ch.Linked
	}
	return res, nil
}

func (s *Service) ProposeDelete(ctx context.Context, rel, reason string) (ProposeResult, error) {
	return s.proposeWhole(ctx, "delete", rel, "", reason)
}

func (s *Service) ProposeMove(ctx context.Context, rel, newRel, reason string) (ProposeResult, error) {
	return s.proposeWhole(ctx, "move", rel, newRel, reason)
}

func (s *Service) unchanged(rel, hash string) (string, fileState, bool, error) {
	real, clean, err := s.vault.ResolveWrite(rel)
	if err != nil {
		return "", fileState{}, false, nil
	}
	st, err := s.readFile(real, clean)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fileState{}, false, nil
	}
	if err != nil {
		return "", fileState{}, false, err
	}
	return real, st, s.journal.hash(st.doc.lines) == hash, nil
}

func (s *Service) applyDelete(p index.Proposal, by, comment string) (index.Decision, error) {
	status := "stale"
	err := s.journal.update(func(entries []entry) ([]entry, error) {
		real, _, ok, err := s.unchanged(p.Path, p.BaseHash)
		if err != nil || !ok {
			return entries, err
		}
		if err := os.Remove(real); err != nil {
			return nil, err
		}
		status = "applied"
		kept := entries[:0:0]
		for _, e := range entries {
			if e.Path != p.Path {
				kept = append(kept, e)
			}
		}
		return kept, nil
	})
	if err != nil {
		return index.Decision{}, err
	}
	if status == "applied" {
		s.reindexQuietly(p.Path)
	}
	return index.Decision{Status: status, By: by, Comment: comment}, nil
}

func (s *Service) applyMove(p index.Proposal, by, comment string) (index.Decision, error) {
	var ch LinkChanges
	if err := json.Unmarshal(p.LinkEdits, &ch); err != nil {
		return index.Decision{}, err
	}
	status := "stale"
	var touched []string
	err := s.journal.update(func(entries []entry) ([]entry, error) {
		real, _, ok, err := s.unchanged(p.Path, p.BaseHash)
		if err != nil || !ok {
			return entries, err
		}
		dst, _, err := s.vault.ResolveCreate(p.NewPath)
		if err != nil {
			return entries, nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(real, dst); err != nil {
			return nil, err
		}
		status = "applied"
		touched = append(touched, p.Path, p.NewPath)
		for i := range entries {
			if entries[i].Path == p.Path {
				entries[i].Path = p.NewPath
			}
		}
		var skipped []string
		for _, n := range ch.Notes {
			path := n.Path
			if path == p.Path {
				path = p.NewPath
			}
			if err := s.rewriteLinks(path, n.Edits); err != nil {
				skipped = append(skipped, path)
				continue
			}
			touched = append(touched, path)
		}
		ch = LinkChanges{Skipped: skipped}
		return entries, nil
	})
	if err != nil {
		return index.Decision{}, err
	}
	for _, t := range touched {
		s.reindexQuietly(t)
	}
	d := index.Decision{Status: status, By: by, Comment: comment}
	if len(ch.Skipped) > 0 {
		d.Result, _ = json.Marshal(ch)
	}
	return d, nil
}

func (s *Service) rewriteLinks(rel string, edits []Edit) error {
	real, clean, err := s.vault.ResolveWrite(rel)
	if err != nil {
		return err
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return err
	}
	text := docText(st.doc)
	for _, e := range edits {
		if !strings.Contains(text, e.Old) {
			return fmt.Errorf("%s no longer has %s", clean, e.Old)
		}
		text = strings.ReplaceAll(text, e.Old, e.New)
	}
	return s.writeFile(real, clean, st.info, withText(st.doc, text))
}
```

- [ ] **Step 5: Tools** — in `tools.go` add inputs:

```go
type proposeDeleteInput struct {
	Path   string `json:"path" jsonschema:"note to delete"`
	Reason string `json:"reason" jsonschema:"why, for the person who reviews it"`
}

type proposeMoveInput struct {
	Path    string `json:"path" jsonschema:"note to move or rename"`
	NewPath string `json:"new_path" jsonschema:"where it goes, e.g. 02. Areas/People/Мама.md; must not exist"`
	Reason  string `json:"reason" jsonschema:"why, for the person who reviews it"`
}
```

and, after `note_propose`:

```go
	mcp.AddTool(server, &mcp.Tool{
		Name: "note_propose_delete",
		Description: "Propose deleting a note you may write. A person applies or rejects it on the review page; give " +
			"them the url. The result lists the notes that link to it, whose links would break.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposeDeleteInput) (*mcp.CallToolResult, ProposeResult, error) {
		out, err := s.ProposeDelete(ctx, in.Path, in.Reason)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_propose_move",
		Description: "Propose moving or renaming a note you may write. The links to it in other notes you may write " +
			"are rewritten in the same proposal; notes you may not change are listed in unwritable. A person applies " +
			"or rejects it on the review page; give them the url.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposeMoveInput) (*mcp.CallToolResult, ProposeResult, error) {
		out, err := s.ProposeMove(ctx, in.Path, in.NewPath, in.Reason)
		return nil, out, err
	})
```

- [ ] **Step 6:** `go test -race -count=1 ./internal/...` — PASS; commit `note_propose_delete, note_propose_move`.

---

### Task 5: Review page for deletes and moves

**Files:** Modify `internal/review/server.go` (`Queue`), `internal/review/pages.go`, `internal/review/templates/list.html`, `internal/review/templates/proposal.html`, `internal/review/server_test.go`; `internal/notes/moves.go` (`ProposalNote`)

- [ ] **Step 1:** add to `Queue`: `ProposalNote(p index.Proposal) (string, bool)`; implement in notes:

```go
func (s *Service) ProposalNote(p index.Proposal) (string, bool) {
	real, clean, err := s.vault.ResolveRead(p.Path)
	if err != nil {
		return "", false
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return "", false
	}
	return docText(st.doc), true
}
```

and in the fake queue: `func (q *fakeQueue) ProposalNote(index.Proposal) (string, bool) { return "# Старое\nтекст", true }`.

- [ ] **Step 2:** `proposalPage` gets `Note template.HTML`, `Changes notes.LinkChanges`; `showProposal` fills them for pending `delete`/`move` (render the note with `renderMarkdown`, decode `p.LinkEdits`), and for any decided proposal decodes `p.Result` into `Changes` so skipped notes show.

- [ ] **Step 3:** templates — in `list.html` after the path: `{{if ne .Kind "edit"}} <span class="muted">· {{.Kind}}{{if .NewPath}} → {{.NewPath}}{{end}}</span>{{end}}`. In `proposal.html`, inside the pending branch, before the edits: for `delete` a heading "Delete this note", the list of `Changes.Linked` ("These notes link to it; their links will break"), and `<details class="render" open><summary>Note text</summary><div class="md">{{.Note}}</div></details>`; for `move` "Move to <code>{{.P.NewPath}}</code>", every `Changes.Notes` path with its edits as `<del>` old / `<ins>` new link text, `Changes.Unwritable` as a warning, and the note text in a closed `<details>`. After a decision, `Changes.Skipped` as "Links not rewritten in: …".

- [ ] **Step 4:** tests — `TestDeleteAndMovePages`: fake proposals of kind `delete` (with `LinkEdits` `{"linked":["Tasks.md"]}`) and `move` (`NewPath` `Архив/Старое.md`, `LinkEdits` `{"notes":[{"path":"Tasks.md","edits":[{"old":"[[Старое]]","new":"[[Архив/Старое]]"}]}],"unwritable":["Work.md"]}`); the pages contain "Delete this note", "Tasks.md", "<h1>Старое</h1>", "Архив/Старое.md", `<del>[[Старое]]</del>`, `<ins>[[Архив/Старое]]</ins>`, "Work.md". The fake queue needs a map of proposals by id.

- [ ] **Step 5:** `go test -race ./internal/review/` — PASS; commit `review: delete and move pages`.

---

### Task 6: Docs, deploy, restart Hermes

- [ ] README: tool rows for `note_propose_delete`, `note_propose_move`; in Review: "a move rewrites wikilinks, embeds and Markdown links to the note in notes the agent may write; links from notes it may not read are not seen; relative Markdown links inside the moved note itself are not adjusted"; `file.backlinks` supported.
- [ ] `vendorHash` unchanged (no new deps) — `nix build .#md-vault-mcp` to confirm.
- [ ] Full check, merge to master, push; bump `md-vault-mcp` in `~/dev/nixos-config-flake/servers` (pull first; never deploy someone else's uncommitted work), commit, push, `just home-server`; then `systemctl restart hermes-agent` on the server, because Hermes keeps the tool list from its start.
