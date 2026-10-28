package notes

import (
	"context"
	"io/fs"
	"log"
	"os"
	"strings"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

func (s *Service) UseIndex(ctx context.Context, ix *index.Index) error {
	s.index = ix
	return s.Sync(ctx)
}

func (s *Service) Sync(ctx context.Context) error {
	have, err := s.index.Stat(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var failed error
	err = s.walk(s.vault.Root(), "", func(rel, real string) bool {
		seen[rel] = true
		info, err := os.Stat(real)
		if err != nil {
			return true
		}
		if st, ok := have[rel]; ok && st == statOf(info) {
			return true
		}
		if failed = s.Reindex(ctx, rel); failed != nil {
			return false
		}
		return true
	})
	if err != nil {
		return err
	}
	if failed != nil {
		return failed
	}
	var gone []string
	for p := range have {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	return s.index.Delete(ctx, gone...)
}

func (s *Service) Reindex(ctx context.Context, rel string) error {
	real, clean, err := s.vault.ResolveRead(rel)
	if err != nil {
		return s.index.Delete(ctx, rel)
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return s.index.Delete(ctx, clean)
	}
	lines := st.doc.lines
	spans := splitSections(lines)
	secs := make([]index.Section, len(spans))
	for i, sp := range spans {
		secs[i] = index.Section{Start: sp.start + 1, End: sp.end, Text: strings.Join(lines[sp.start:sp.end], "\n")}
	}
	return s.index.Put(ctx, clean, statOf(st.info), secs)
}

func (s *Service) reindexQuietly(rel string) {
	if s.index == nil {
		return
	}
	if err := s.Reindex(context.Background(), rel); err != nil {
		log.Printf("index %s: %v", rel, err)
	}
}

func statOf(info fs.FileInfo) index.FileStat {
	return index.FileStat{MtimeNS: info.ModTime().UnixNano(), Size: info.Size()}
}
