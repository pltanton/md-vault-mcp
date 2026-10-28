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
