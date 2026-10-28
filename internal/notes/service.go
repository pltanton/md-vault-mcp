package notes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pltanton/md-vault-mcp/internal/access"
	"github.com/pltanton/md-vault-mcp/internal/index"
)

const (
	defaultEditWindow  = 48 * time.Hour
	defaultMaxFileSize = 1 << 20
	maxSearchResults   = 50
	maxSnippetRunes    = 200
	maxRecent          = 20
)

var errOwnerChanged = errors.New("changed or removed by the owner — it's his now; leave it as it is")

type Service struct {
	vault   *access.Vault
	journal *journal
	index   *index.Index
	window  time.Duration
	maxSize int64
	now     func() time.Time
}

func NewService(cfg Config) (*Service, error) {
	policy, err := access.NewPolicy(cfg.Read, cfg.Write, cfg.Extensions)
	if err != nil {
		return nil, err
	}
	vault, err := access.OpenVault(cfg.Vault, policy)
	if err != nil {
		return nil, err
	}
	j, err := openJournal(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	s := &Service{vault: vault, journal: j, window: cfg.EditWindow, maxSize: cfg.MaxFileSize, now: time.Now}
	if s.window <= 0 {
		s.window = defaultEditWindow
	}
	if s.maxSize <= 0 {
		s.maxSize = defaultMaxFileSize
	}
	return s, nil
}

type fileState struct {
	info fs.FileInfo
	doc  doc
}

func (s *Service) readFile(real, rel string) (fileState, error) {
	info, err := os.Stat(real)
	if err != nil {
		return fileState{}, err
	}
	if info.Size() > s.maxSize {
		return fileState{}, fmt.Errorf("%s is %d bytes, over the %d byte limit", rel, info.Size(), s.maxSize)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return fileState{}, err
	}
	return fileState{info: info, doc: parseDoc(data)}, nil
}

func (s *Service) writeFile(real, rel string, before fs.FileInfo, d doc) error {
	data := d.bytes()
	if int64(len(data)) > s.maxSize {
		return fmt.Errorf("%s would grow to %d bytes, over the %d byte limit", rel, len(data), s.maxSize)
	}
	tmp, err := os.CreateTemp(filepath.Dir(real), "."+filepath.Base(real)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	mode := os.FileMode(0o644)
	if before != nil {
		mode = before.Mode().Perm()
		if st, ok := before.Sys().(*syscall.Stat_t); ok {
			if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
				tmp.Close()
				return fmt.Errorf("%s: cannot keep its owner: %w", rel, err)
			}
		}
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if before != nil {
		now, err := os.Stat(real)
		if err != nil {
			return err
		}
		if !now.ModTime().Equal(before.ModTime()) || now.Size() != before.Size() {
			return fmt.Errorf("%s changed while it was being edited; read it again and retry", rel)
		}
	} else if _, err := os.Lstat(real); err == nil {
		return fmt.Errorf("%s already exists", rel)
	}
	return os.Rename(tmp.Name(), real)
}

type ReadResult struct {
	Path       string `json:"path"`
	TotalLines int    `json:"total_lines"`
	Content    string `json:"content" jsonschema:"lines prefixed with their 1-based number and a tab"`
}

func (s *Service) Read(rel string, offset, limit int) (ReadResult, error) {
	real, clean, err := s.vault.ResolveRead(rel)
	if err != nil {
		return ReadResult{}, err
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return ReadResult{}, err
	}
	lines := st.doc.lines
	start := 0
	if offset > 1 {
		start = min(offset-1, len(lines))
	}
	end := len(lines)
	if limit > 0 {
		end = min(start+limit, end)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
	}
	return ReadResult{Path: clean, TotalLines: len(lines), Content: b.String()}, nil
}

func (s *Service) walk(root, base string, visit func(rel, real string) bool) error {
	stop := errors.New("stop")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		rel := relJoin(base, root, p)
		if d.IsDir() {
			if p != root && !s.vault.Policy.MayHoldReadable(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !s.vault.Policy.CanRead(rel) {
			return nil
		}
		if !visit(rel, p) {
			return stop
		}
		return nil
	})
	if errors.Is(err, stop) {
		return nil
	}
	return err
}

func relJoin(base, root, p string) string {
	r, _ := filepath.Rel(root, p)
	r = filepath.ToSlash(r)
	if r == "." {
		return base
	}
	if base == "" {
		return r
	}
	return base + "/" + r
}

type ListEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind" jsonschema:"dir or note"`
}

type ListResult struct {
	Dir     string      `json:"dir"`
	Entries []ListEntry `json:"entries"`
}

func (s *Service) List(dir string) (ListResult, error) {
	real, clean, err := s.vault.ResolveDir(dir)
	if err != nil {
		return ListResult{}, err
	}
	items, err := os.ReadDir(real)
	if err != nil {
		return ListResult{}, err
	}
	out := ListResult{Dir: clean, Entries: []ListEntry{}}
	for _, it := range items {
		rel := it.Name()
		if clean != "" {
			rel = clean + "/" + rel
		}
		switch {
		case it.IsDir():
			if !s.vault.Policy.MayHoldReadable(rel) {
				continue
			}
			found := false
			s.walk(filepath.Join(real, it.Name()), rel, func(string, string) bool {
				found = true
				return false
			})
			if found {
				out.Entries = append(out.Entries, ListEntry{Name: it.Name(), Path: rel, Kind: "dir"})
			}
		case it.Type().IsRegular() && s.vault.Policy.CanRead(rel):
			out.Entries = append(out.Entries, ListEntry{Name: it.Name(), Path: rel, Kind: "note"})
		}
	}
	if clean != "" && len(out.Entries) == 0 {
		return ListResult{}, access.ErrNotReadable
	}
	return out, nil
}

type WriteResult struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Line      int       `json:"line" jsonschema:"1-based first line of the block"`
	Lines     int       `json:"lines"`
	ExpiresAt time.Time `json:"editable_until"`
}

func (s *Service) Add(rel, text, headingLine string) (WriteResult, error) {
	add := textLines(text)
	if len(add) == 0 {
		return WriteResult{}, errors.New("text must not be empty")
	}
	real, clean, err := s.vault.ResolveWrite(rel)
	if err != nil {
		return WriteResult{}, err
	}
	var res WriteResult
	err = s.journal.update(func(entries []entry) ([]entry, error) {
		st, err := s.readFile(real, clean)
		if err != nil {
			return nil, err
		}
		lines := st.doc.lines
		start, end := 0, len(lines)
		heading := ""
		if strings.TrimSpace(headingLine) != "" {
			h, err := findHeading(lines, headingLine)
			if err != nil {
				return nil, err
			}
			start, end = section(lines, h)
			heading = lines[h.line]
		} else {
			start = frontMatterEnd(lines)
		}
		at := appendPoint(lines, start, end)
		d := st.doc
		d.lines = insertLines(lines, at, add)
		if len(lines) == 0 {
			d.trailingNL = true
		}
		if err := s.writeFile(real, clean, st.info, d); err != nil {
			return nil, err
		}
		now := s.now()
		e := entry{ID: newID(), Path: clean, Heading: strings.TrimSpace(heading), Lines: len(add), Hash: s.journal.hash(add), Created: now, Updated: now}
		res = WriteResult{ID: e.ID, Path: clean, Line: at + 1, Lines: len(add), ExpiresAt: now.Add(s.window)}
		return append(s.live(entries), e), nil
	})
	if err == nil {
		s.reindexQuietly(res.Path)
	}
	return res, err
}

func frontMatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for j := 1; j < len(lines); j++ {
		if t := strings.TrimSpace(lines[j]); t == "---" || t == "..." {
			return j + 1
		}
	}
	return 0
}

func (s *Service) Create(rel, text string) (WriteResult, error) {
	content := textLines(text)
	if len(content) == 0 {
		return WriteResult{}, errors.New("text must not be empty")
	}
	real, clean, err := s.vault.ResolveCreate(rel)
	if err != nil {
		return WriteResult{}, err
	}
	var res WriteResult
	err = s.journal.update(func(entries []entry) ([]entry, error) {
		if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
			return nil, err
		}
		if err := s.writeFile(real, clean, nil, doc{lines: content, trailingNL: true}); err != nil {
			return nil, err
		}
		now := s.now()
		e := entry{ID: newID(), Path: clean, Lines: len(content), Whole: true, Hash: s.journal.hash(content), Created: now, Updated: now}
		res = WriteResult{ID: e.ID, Path: clean, Line: 1, Lines: len(content), ExpiresAt: now.Add(s.window)}
		return append(s.live(entries), e), nil
	})
	if err == nil {
		s.reindexQuietly(res.Path)
	}
	return res, err
}

func (s *Service) live(entries []entry) []entry {
	cutoff := s.now().Add(-s.window)
	out := entries[:0:0]
	for _, e := range entries {
		if e.Created.After(cutoff) {
			out = append(out, e)
		}
	}
	return out
}

func (s *Service) entryFor(entries []entry, id string) (int, error) {
	id = strings.TrimSpace(id)
	for i, e := range entries {
		if e.ID != id {
			continue
		}
		if !e.Created.After(s.now().Add(-s.window)) {
			return 0, fmt.Errorf("block %s was written more than %s ago; it's the owner's now", id, s.window)
		}
		return i, nil
	}
	return 0, fmt.Errorf("no block %s among what you wrote in the last %s; it may have expired, see note_recent", id, s.window)
}

func (s *Service) locate(lines []string, e entry) (int, error) {
	var all []int
	for i := 0; i+e.Lines <= len(lines); i++ {
		if s.journal.hash(lines[i:i+e.Lines]) == e.Hash {
			all = append(all, i)
		}
	}
	pick := func(found []int) (int, error) {
		switch len(found) {
		case 0:
			return 0, fmt.Errorf("block %s in %s was %w", e.ID, e.Path, errOwnerChanged)
		case 1:
			return found[0], nil
		}
		nums := make([]string, len(found))
		for i, n := range found {
			nums[i] = fmt.Sprint(n + 1)
		}
		return 0, fmt.Errorf("block %s appears %d times in %s (lines %s); ask the owner which one he means — he can edit it himself",
			e.ID, len(found), e.Path, strings.Join(nums, ", "))
	}
	if e.Heading != "" && len(all) > 1 {
		if h, err := findHeading(lines, e.Heading); err == nil {
			start, end := section(lines, h)
			var under []int
			for _, n := range all {
				if n >= start && n+e.Lines <= end {
					under = append(under, n)
				}
			}
			if len(under) > 0 {
				return pick(under)
			}
		}
	}
	return pick(all)
}

func (s *Service) changeBlock(id string, with []string) (WriteResult, error) {
	var res WriteResult
	err := s.journal.update(func(entries []entry) ([]entry, error) {
		i, err := s.entryFor(entries, id)
		if err != nil {
			return nil, err
		}
		e := entries[i]
		if e.Property != "" {
			if with != nil {
				return nil, fmt.Errorf("%s changed property %q; use note_set_property to change it again or note_remove to undo", e.ID, e.Property)
			}
			r, err := s.revertProperty(e)
			if err != nil {
				return nil, err
			}
			res = r
			return append(entries[:i:i], entries[i+1:]...), nil
		}
		real, clean, err := s.vault.ResolveWrite(e.Path)
		if err != nil {
			if errors.Is(err, access.ErrNotReadable) {
				return nil, fmt.Errorf("block %s in %s was %w", e.ID, e.Path, errOwnerChanged)
			}
			return nil, err
		}
		st, err := s.readFile(real, clean)
		if err != nil {
			return nil, err
		}
		at, err := s.locate(st.doc.lines, e)
		if err != nil {
			return nil, err
		}
		d := st.doc
		d.lines = replaceLines(st.doc.lines, at, e.Lines, with)
		if e.Whole && len(textLines(strings.Join(d.lines, "\n"))) == 0 {
			if err := os.Remove(real); err != nil {
				return nil, err
			}
		} else if err := s.writeFile(real, clean, st.info, d); err != nil {
			return nil, err
		}
		res = WriteResult{ID: e.ID, Path: e.Path, Line: at + 1, Lines: len(with), ExpiresAt: e.Created.Add(s.window)}
		if len(with) == 0 {
			return append(entries[:i:i], entries[i+1:]...), nil
		}
		e.Lines, e.Hash, e.Updated = len(with), s.journal.hash(with), s.now()
		entries[i] = e
		return s.live(entries), nil
	})
	if err == nil {
		s.reindexQuietly(res.Path)
	}
	return res, err
}

func (s *Service) Edit(id, text string) (WriteResult, error) {
	with := textLines(text)
	if len(with) == 0 {
		return WriteResult{}, errors.New("text must not be empty; use note_remove to delete the block")
	}
	return s.changeBlock(id, with)
}

func (s *Service) Remove(id string) (WriteResult, error) {
	return s.changeBlock(id, nil)
}

type RecentBlock struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Property  string    `json:"property,omitempty" jsonschema:"set for a property change; note_remove undoes it"`
	Heading   string    `json:"heading,omitempty"`
	Line      int       `json:"line"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"editable_until"`
}

type RecentResult struct {
	Blocks []RecentBlock `json:"blocks"`
}

func (s *Service) Recent(limit int) (RecentResult, error) {
	if limit <= 0 || limit > maxRecent {
		limit = maxRecent
	}
	entries, err := s.journal.view()
	if err != nil {
		return RecentResult{}, err
	}
	entries = s.live(entries)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Updated.After(entries[j].Updated) })
	out := RecentResult{Blocks: []RecentBlock{}}
	for _, e := range entries {
		if len(out.Blocks) >= limit {
			break
		}
		real, clean, err := s.vault.ResolveWrite(e.Path)
		if err != nil {
			continue
		}
		st, err := s.readFile(real, clean)
		if err != nil {
			continue
		}
		if e.Property != "" {
			fm, ok := findFrontMatter(st.doc.lines)
			if !ok {
				continue
			}
			start, end, found := propertySpan(st.doc.lines, fm, e.Property)
			if !found && e.Lines > 0 || found && s.journal.hash(st.doc.lines[start:end]) != e.Hash {
				continue
			}
			text := ""
			if found {
				text = strings.Join(st.doc.lines[start:end], "\n")
			}
			out.Blocks = append(out.Blocks, RecentBlock{
				ID: e.ID, Path: e.Path, Property: e.Property, Line: start + 1, Text: text,
				CreatedAt: e.Created, ExpiresAt: e.Created.Add(s.window),
			})
			continue
		}
		at, err := s.locate(st.doc.lines, e)
		if err != nil {
			continue
		}
		out.Blocks = append(out.Blocks, RecentBlock{
			ID: e.ID, Path: e.Path, Heading: e.Heading, Line: at + 1,
			Text:      strings.Join(st.doc.lines[at:at+e.Lines], "\n"),
			CreatedAt: e.Created, ExpiresAt: e.Created.Add(s.window),
		})
	}
	return out, nil
}
