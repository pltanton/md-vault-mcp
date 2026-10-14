package notes

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/access"
)

const personNote = `---
tags:
  - person
birthday: 1990-10-13
doctor: "[[Dr. House]]"
rating: 4
score: 4.5
# a comment the owner wrote
aliases: []
---
# Bob
`

func paths(rows []PropertyRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Path
	}
	return out
}

func TestPropertiesKeepScalarsAsWritten(t *testing.T) {
	props, err := properties(strings.Split(personNote, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"tags":     []any{"person"},
		"birthday": "1990-10-13",
		"doctor":   "[[Dr. House]]",
		"rating":   int64(4),
		"score":    4.5,
		"aliases":  []any{},
	}
	if !reflect.DeepEqual(props, want) {
		t.Fatalf("got %#v", props)
	}
}

func TestPropertiesQuery(t *testing.T) {
	f := newFixture(t)
	f.write("02. Areas/Personal/People/Bob.md", personNote)
	f.write("02. Areas/Personal/People/Carol.md", "---\nbirthday: 1992-10-11\n---\nInline #person tag.\n")
	f.write("02. Areas/Personal/People/Leap.md", "---\ntags: [person/family]\nbirthday: 2000-02-29\n---\n")
	f.write("02. Areas/Personal/People/NoYear.md", "---\ntags: [person]\nbirthday: \"\"\n---\n")
	f.write("02. Areas/Work/Boss.md", "---\ntags: [person]\nbirthday: 1980-10-12\n---\n")
	f.write("02. Areas/Personal/Broken.md", "---\nbirthday: [unclosed\n---\n")

	t.Run("tag from property, inline and nested", func(t *testing.T) {
		res, err := f.svc.Properties(PropertyQuery{Tag: "person", Sort: "birthday"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"02. Areas/Personal/People/Bob.md",
			"02. Areas/Personal/People/Carol.md",
			"02. Areas/Personal/People/Leap.md",
			"02. Areas/Personal/People/Alice.md",
			"02. Areas/Personal/People/NoYear.md",
		}
		if got := paths(res.Notes); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v", got)
		}
		if !reflect.DeepEqual(res.Invalid, []string{"02. Areas/Personal/Broken.md"}) {
			t.Fatalf("invalid = %v", res.Invalid)
		}
	})

	t.Run("upcoming yearly dates, soonest first, with years", func(t *testing.T) {
		res, err := f.svc.Properties(PropertyQuery{Upcoming: &Upcoming{Field: "birthday", Days: 14}, Fields: []string{"birthday"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Notes) != 1 {
			t.Fatalf("got %v", paths(res.Notes))
		}
		row := res.Notes[0]
		if row.Path != "02. Areas/Personal/People/Bob.md" || *row.Next != (NextDate{Date: "2026-10-13", InDays: 1, Years: 36}) {
			t.Fatalf("got %+v %+v", row, row.Next)
		}
		if !reflect.DeepEqual(row.Properties, map[string]any{"birthday": "1990-10-13"}) {
			t.Fatalf("fields not narrowed: %v", row.Properties)
		}
	})

	t.Run("a date already passed this year comes next year", func(t *testing.T) {
		res, err := f.svc.Properties(PropertyQuery{Upcoming: &Upcoming{Field: "birthday", Days: 366}})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]NextDate{}
		for _, r := range res.Notes {
			got[r.Path] = *r.Next
		}
		if got["02. Areas/Personal/People/Carol.md"] != (NextDate{Date: "2027-10-11", InDays: 364, Years: 35}) {
			t.Fatalf("carol %+v", got["02. Areas/Personal/People/Carol.md"])
		}
		if got["02. Areas/Personal/People/Leap.md"] != (NextDate{Date: "2027-02-28", InDays: 139, Years: 27}) {
			t.Fatalf("leap %+v", got["02. Areas/Personal/People/Leap.md"])
		}
	})

	t.Run("has and equals", func(t *testing.T) {
		res, err := f.svc.Properties(PropertyQuery{Has: []string{"birthday"}, Equals: map[string]string{"tags": "PERSON"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := paths(res.Notes); !reflect.DeepEqual(got, []string{"02. Areas/Personal/People/Bob.md"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("descending sort by number", func(t *testing.T) {
		f.write("00. Inbox/Low.md", "---\nrating: 2\n---\n")
		f.write("00. Inbox/High.md", "---\nrating: 10\n---\n")
		res, err := f.svc.Properties(PropertyQuery{Under: "00. Inbox", Has: []string{"rating"}, Sort: "-rating"})
		if err != nil {
			t.Fatal(err)
		}
		if got := paths(res.Notes); !reflect.DeepEqual(got, []string{"00. Inbox/High.md", "00. Inbox/Low.md"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("unreadable folder", func(t *testing.T) {
		if _, err := f.svc.Properties(PropertyQuery{Under: "02. Areas/Work"}); !errors.Is(err, access.ErrNotReadable) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSetPropertyKeepsTheRestIntact(t *testing.T) {
	f := newFixture(t)
	rel := "02. Areas/Personal/People/Bob.md"
	f.write(rel, personNote)

	res, err := f.svc.SetProperty(rel, "birthday", "1990-10-14")
	if err != nil {
		t.Fatal(err)
	}
	if res.Previous != "1990-10-13" {
		t.Fatalf("previous = %v", res.Previous)
	}
	want := strings.Replace(personNote, "birthday: 1990-10-13", "birthday: 1990-10-14", 1)
	if got := f.read(rel); got != want {
		t.Fatalf("got\n%s", got)
	}

	if _, err := f.svc.SetProperty(rel, "tags", []any{"person", "friend"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetProperty(rel, "clinic", "[[Evangelismos]]"); err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(want, "tags:\n  - person\n", "tags:\n  - person\n  - friend\n", 1)
	want = strings.Replace(want, "aliases: []\n---", "aliases: []\nclinic: \"[[Evangelismos]]\"\n---", 1)
	if got := f.read(rel); got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestSetPropertyAddsFrontMatter(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetProperty("00. Inbox/Покупки.md", "status", "open"); err != nil {
		t.Fatal(err)
	}
	if got := f.read("00. Inbox/Покупки.md"); got != "---\nstatus: open\n---\n# Купить\n- [ ] молоко\n" {
		t.Fatalf("got %q", got)
	}
}

func TestUnsetAndUndo(t *testing.T) {
	f := newFixture(t)
	rel := "02. Areas/Personal/People/Bob.md"
	f.write(rel, personNote)

	ch, err := f.svc.UnsetProperty(rel, "tags")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.read(rel), "person") {
		t.Fatalf("tags still there:\n%s", f.read(rel))
	}
	recent, err := f.svc.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Blocks) != 1 || recent.Blocks[0].Property != "tags" {
		t.Fatalf("recent = %+v", recent.Blocks)
	}
	if _, err := f.svc.Remove(ch.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.read(rel); got != personNote {
		t.Fatalf("not restored:\n%s", got)
	}

	if _, err := f.svc.UnsetProperty(rel, "nope"); err == nil {
		t.Fatal("unset of a missing property succeeded")
	}
}

func TestUndoRefusesAfterOwnerChange(t *testing.T) {
	f := newFixture(t)
	rel := "02. Areas/Personal/People/Bob.md"
	f.write(rel, personNote)
	ch, err := f.svc.SetProperty(rel, "rating", 5)
	if err != nil {
		t.Fatal(err)
	}
	f.write(rel, strings.Replace(f.read(rel), "rating: 5", "rating: 3", 1))
	if _, err := f.svc.Remove(ch.ID); !errors.Is(err, errOwnerChanged) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.read(rel), "rating: 3") {
		t.Fatal("owner's value was overwritten")
	}
}

func TestSetPropertyRespectsWriteRules(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetProperty("02. Areas/Work/Job.md", "status", "done"); !errors.Is(err, access.ErrNotWritable) {
		t.Fatalf("err = %v", err)
	}
}
