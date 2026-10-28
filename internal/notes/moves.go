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
