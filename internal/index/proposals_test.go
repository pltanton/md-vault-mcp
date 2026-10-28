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
