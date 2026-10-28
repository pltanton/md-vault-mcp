package index

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

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
