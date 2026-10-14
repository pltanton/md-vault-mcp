package notes

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pltanton/md-vault-mcp/internal/access"
)

func evalString(t *testing.T, q *baseQuery, f *baseFile, src string) any {
	t.Helper()
	e, err := parseExpr(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	v, err := (&scope{q: q, file: f}).eval(e)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return exportValue(v)
}

func TestBaseExpressions(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.Local)
	f := &baseFile{
		path:     "People/Bob.md",
		props:    map[string]any{"birthday": noteValue("1990-10-13"), "price": 1.5, "grams": float64(6), "Юля": float64(3), "tags": []any{"person"}, "doctor": noteValue("[[Dr House]]")},
		rawProps: map[string]bool{"birthday": true},
		tags:     []string{"person/friend"},
		formulas: map[string]any{},
	}
	q := &baseQuery{now: now, formulas: map[string]node{}, byPath: map[string]*baseFile{}, byName: map[string]*baseFile{}}
	q.this = &baseFile{path: "People/Base.base", props: map[string]any{}, formulas: map[string]any{}}

	cases := []struct {
		src  string
		want any
	}{
		{"1 + 2 * 3", 7.0},
		{"(1 + 2) * 3", 9.0},
		{`"a" + 1`, "a1"},
		{"price / grams * 100", 25.0},
		{"birthday.month", 10.0},
		{"birthday.year + 1", 1991.0},
		{`'⭐⭐⭐⭐⭐'.slice(0, Юля)`, "⭐⭐⭐"},
		{`if(price, "yes", "no")`, "yes"},
		{`if(missing, "yes")`, nil},
		{`file.hasTag("person")`, true},
		{`file.tags.contains("person/friend")`, true},
		{`file.inFolder("People")`, true},
		{`file.inFolder("Peo")`, false},
		{`!file.inFolder("_files")`, true},
		{`file.basename`, "Bob"},
		{`file.name`, "Bob.md"},
		{`file.hasProperty("birthday")`, true},
		{`this.file.folder`, "People"},
		{`date("2026-10-13").relative()`, "in 14 hours"},
		{`date("2026-10-20").relative()`, "in 8 days"},
		{`date("2026-01-01") + "1M"`, "2026-02-01"},
		{`(date("2026-01-02") - date("2026-01-01")) / 86400000`, 1.0},
		{`today().format("DD.MM.YYYY")`, "12.10.2026"},
		{`birthday < today()`, true},
		{`doctor == "Dr House"`, true},
		{`[1, 2, 3].map(value * 2)`, []any{2.0, 4.0, 6.0}},
		{`[1, 2, 3, 4].filter(value > 2)`, []any{3.0, 4.0}},
		{`[1, 2, 3].reduce(acc + value, 0)`, 6.0},
		{`"a,b,c".split(",").join("-")`, "a-b-c"},
		{`"John Smith".replace(/(\w+) (\w+)/, "$2, $1")`, "Smith, John"},
		{`(2.3333).round(2)`, 2.33},
		{`(3.14159).toFixed(2)`, "3.14"},
		{`list("x").length`, 1.0},
		{`max(1, 5, 3)`, 5.0},
		{`missing.isEmpty()`, true},
		{`"hello world".title()`, "Hello World"},
	}
	for _, c := range cases {
		if got := evalString(t, q, f, c.src); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
}

func TestBaseUnsupportedFails(t *testing.T) {
	q := &baseQuery{now: time.Now(), formulas: map[string]node{}}
	f := &baseFile{props: map[string]any{}, formulas: map[string]any{}}
	e, _ := parseExpr("random()")
	_, err := (&scope{q: q, file: f}).eval(e)
	var unsupported errUnsupported
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v", err)
	}
}

const peopleBase = `filters:
  and:
    - '!file.inFolder("_files")'
    - not:
        - 'status == "archived"'
formulas:
  next: 'date(if(today().month < birthday.month || (today().month == birthday.month && today().day <= birthday.day), today().year, today().year + 1) + "-" + birthday.format("MM-DD"))'
  days: '((formula.next - today()) / 86400000).round()'
properties:
  formula.days:
    displayName: Days left
summaries:
  oldest: 'values.sort()[0]'
views:
  - type: table
    name: Upcoming
    filters:
      and:
        - file.hasTag("person")
        - birthday
    order:
      - file.basename
      - birthday
      - formula.days
    sort:
      - property: formula.days
        direction: ASC
    summaries:
      formula.days: Min
      birthday: oldest
  - type: table
    name: By city
    groupBy:
      property: city
      direction: DESC
    sort:
      - property: file.basename
        direction: DESC
    order:
      - file.basename
      - city
`

func TestQueryBase(t *testing.T) {
	f := newFixture(t)
	f.clock = time.Date(2026, 10, 12, 10, 0, 0, 0, time.Local)
	f.svc.now = func() time.Time { return f.clock }
	dir := "02. Areas/Personal/People"
	f.write(dir+"/People.base", peopleBase)
	f.write(dir+"/Bob.md", "---\ntags: [person]\nbirthday: 1990-10-13\ncity: Paphos\n---\n")
	f.write(dir+"/Carol.md", "---\nbirthday: 1992-10-11\ncity: Limassol\n---\n#person\n")
	f.write(dir+"/Dan.md", "---\ntags: [person]\nbirthday: 1980-12-01\nstatus: archived\n---\n")
	f.write(dir+"/Eve.md", "---\ntags: [person]\ncity: Paphos\n---\n")
	f.write("02. Areas/Work/Boss.md", "---\ntags: [person]\nbirthday: 1970-10-12\n---\n")

	res, err := f.svc.QueryBase(dir+"/People.base", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.View != "Upcoming" || !reflect.DeepEqual(res.Views, []string{"Upcoming", "By city"}) {
		t.Fatalf("views %q %v", res.View, res.Views)
	}
	var got []string
	for _, r := range res.Rows {
		got = append(got, r.Values["file.basename"].(string)+":"+toString(r.Values["formula.days"]))
	}
	if want := []string{"Bob:1", "Carol:364"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v", got)
	}
	if res.Columns[2] != (BaseColumn{ID: "formula.days", Name: "Days left"}) {
		t.Fatalf("columns %+v", res.Columns)
	}
	if res.Summaries["formula.days"] != 1.0 || res.Summaries["birthday"] != "1990-10-13" {
		t.Fatalf("summaries %v", res.Summaries)
	}

	res, err = f.svc.QueryBase(dir+"/People.base", "by city", 2)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, r := range res.Rows {
		got = append(got, toString(r.Group)+"/"+r.Values["file.basename"].(string))
	}
	if want := []string{"Paphos/Eve", "Paphos/Bob"}; !reflect.DeepEqual(got, want) || !res.Truncated || res.Total != 6 {
		t.Fatalf("rows %v total %d truncated %v", got, res.Total, res.Truncated)
	}

	if _, err := f.svc.QueryBase(dir+"/People.base", "nope", 0); err == nil || !strings.Contains(err.Error(), "Upcoming, By city") {
		t.Fatalf("err = %v", err)
	}
}

func TestQueryBaseAccess(t *testing.T) {
	f := newFixture(t)
	f.write("02. Areas/Work/Secret.base", "views:\n  - type: table\n    name: T\n")
	if _, err := f.svc.QueryBase("02. Areas/Work/Secret.base", "", 0); !errors.Is(err, access.ErrNotReadable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.QueryBase("Tasks.md", "", 0); err == nil {
		t.Fatal("a note was accepted as a base")
	}
	f.write("00. Inbox/Bad.base", "formulas:\n  x: 'random()'\nviews:\n  - type: table\n    name: T\n    order: [formula.x]\n")
	if _, err := f.svc.QueryBase("00. Inbox/Bad.base", "", 0); err == nil || !strings.Contains(err.Error(), "random() is not supported") {
		t.Fatalf("err = %v", err)
	}
}
