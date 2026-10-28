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
	ID         string   `json:"id,omitempty"`
	URL        string   `json:"url,omitempty" jsonschema:"page where a person applies or rejects it; give it to them"`
	Status     string   `json:"status" jsonschema:"pending, or applied when every edit was inside blocks you may still change"`
	Path       string   `json:"path"`
	Links      int      `json:"links,omitempty" jsonschema:"for a move: links in other notes that will be rewritten"`
	Linked     []string `json:"linked,omitempty" jsonschema:"for a delete: notes that link to it and will have broken links"`
	Unwritable []string `json:"unwritable,omitempty" jsonschema:"notes linking to it that you may not change; their links will break"`
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
		switch p.Kind {
		case "delete":
			return s.applyDelete(p, by, comment)
		case "move":
			return s.applyMove(p, by, comment)
		case "edit":
		default:
			return index.Decision{}, fmt.Errorf("unknown proposal kind %s", p.Kind)
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
