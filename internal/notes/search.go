package notes

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

const maxSnippets = 3

type SearchLine struct {
	Line int    `json:"line" jsonschema:"1-based"`
	Text string `json:"text"`
}

type SearchMatch struct {
	Path      string       `json:"path"`
	Heading   string       `json:"heading,omitempty" jsonschema:"the section's heading line; absent for text before the first heading"`
	StartLine int          `json:"start_line,omitempty" jsonschema:"1-based first line of the section; absent when only the file name matched"`
	EndLine   int          `json:"end_line,omitempty" jsonschema:"1-based last line of the section"`
	Rank      float64      `json:"rank" jsonschema:"higher is more relevant"`
	Lines     []SearchLine `json:"lines,omitempty" jsonschema:"up to 3 lines of the section holding the query words"`
}

type SearchResult struct {
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated,omitempty"`
}

func (s *Service) Search(ctx context.Context, query, under string, limit int) (SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return SearchResult{}, errors.New("query must not be empty")
	}
	if s.index == nil {
		return SearchResult{}, errors.New("search index is not configured")
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	_, base, err := s.vault.ResolveDir(under)
	if err != nil {
		return SearchResult{}, err
	}
	hits, truncated, err := s.index.Search(ctx, query, base, limit)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Matches: []SearchMatch{}, Truncated: truncated}
	for _, h := range hits {
		if !s.vault.Policy.CanRead(h.Path) {
			continue
		}
		m := SearchMatch{Path: h.Path, Rank: h.Rank}
		if h.Start > 0 {
			if err := s.fillSection(ctx, query, h, &m); err != nil {
				return SearchResult{}, err
			}
		}
		out.Matches = append(out.Matches, m)
	}
	return out, nil
}

func (s *Service) fillSection(ctx context.Context, query string, h index.Hit, m *SearchMatch) error {
	real, clean, err := s.vault.ResolveRead(h.Path)
	if err != nil {
		s.reindexQuietly(h.Path)
		return nil
	}
	st, err := s.readFile(real, clean)
	if err != nil || h.End > len(st.doc.lines) {
		s.reindexQuietly(h.Path)
		return nil
	}
	lines := st.doc.lines[h.Start-1 : h.End]
	m.StartLine, m.EndLine = h.Start, h.End
	for _, hd := range headings(st.doc.lines) {
		if hd.line == h.Start-1 {
			m.Heading = strings.TrimSpace(lines[0])
			break
		}
	}
	idx, err := s.index.MatchingLines(ctx, query, lines)
	if err != nil {
		return err
	}
	for _, i := range idx[:min(len(idx), maxSnippets)] {
		m.Lines = append(m.Lines, SearchLine{Line: h.Start + i, Text: snippet(lines[i])})
	}
	return nil
}

func snippet(line string) string {
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > maxSnippetRunes {
		return string([]rune(line)[:maxSnippetRunes]) + "…"
	}
	return line
}
