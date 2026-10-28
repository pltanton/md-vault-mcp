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
