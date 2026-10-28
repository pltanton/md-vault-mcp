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
