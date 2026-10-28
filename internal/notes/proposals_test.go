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
