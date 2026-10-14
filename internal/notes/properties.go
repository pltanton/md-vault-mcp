package notes

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	defaultPropertyRows = 100
	maxPropertyRows     = 500
	maxInvalidReported  = 10
)

type PropertyQuery struct {
	Under    string            `json:"under,omitempty" jsonschema:"directory to look in; omit for every readable note"`
	Tag      string            `json:"tag,omitempty" jsonschema:"only notes with this tag (from the tags property or inline #tag); nested tags match too, person matches person/friend"`
	Has      []string          `json:"has,omitempty" jsonschema:"only notes where all these properties are set and not empty"`
	Equals   map[string]string `json:"equals,omitempty" jsonschema:"only notes whose property equals this text, case-insensitive; for a list property, one of its items must equal it"`
	Fields   []string          `json:"fields,omitempty" jsonschema:"properties to return; omit for all of them"`
	Upcoming *Upcoming         `json:"upcoming,omitempty" jsonschema:"only notes whose yearly date (birthday, anniversary) falls in the next days, soonest first"`
	Sort     string            `json:"sort,omitempty" jsonschema:"property to sort by; prefix with - for descending; dates sort as dates, numbers as numbers"`
	Limit    int               `json:"limit,omitempty" jsonschema:"at most this many notes, up to 500"`
}

type Upcoming struct {
	Field string `json:"field" jsonschema:"property holding the date, e.g. birthday"`
	Days  int    `json:"days" jsonschema:"how many days ahead, today included as day 0"`
}

type NextDate struct {
	Date    string `json:"date" jsonschema:"next occurrence, YYYY-MM-DD"`
	InDays  int    `json:"in_days" jsonschema:"0 is today"`
	Weekday string `json:"weekday" jsonschema:"day of the week of the next occurrence, in English"`
	Years   int    `json:"years,omitempty" jsonschema:"years since the original date on that day, e.g. the age turned"`
}

type PropertyRow struct {
	Path       string         `json:"path"`
	Properties map[string]any `json:"properties"`
	Next       *NextDate      `json:"next,omitempty"`
}

type PropertyResult struct {
	Notes     []PropertyRow `json:"notes"`
	Truncated bool          `json:"truncated,omitempty"`
	Invalid   []string      `json:"invalid,omitempty" jsonschema:"notes skipped because their front matter is not valid YAML"`
}

func (s *Service) Properties(q PropertyQuery) (PropertyResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPropertyRows
	}
	limit = min(limit, maxPropertyRows)
	if q.Upcoming != nil {
		if strings.TrimSpace(q.Upcoming.Field) == "" {
			return PropertyResult{}, errors.New("upcoming.field must name a date property")
		}
		if q.Upcoming.Days < 0 || q.Upcoming.Days > 366 {
			return PropertyResult{}, errors.New("upcoming.days must be between 0 and 366")
		}
	}
	root, base, err := s.vault.ResolveDir(q.Under)
	if err != nil {
		return PropertyResult{}, err
	}
	today := dateOnly(s.now())
	out := PropertyResult{Notes: []PropertyRow{}}
	var all []PropertyRow
	err = s.walk(root, base, func(rel, real string) bool {
		st, err := s.readFile(real, rel)
		if err != nil {
			return true
		}
		props, err := properties(st.doc.lines)
		if err != nil {
			if len(out.Invalid) < maxInvalidReported {
				out.Invalid = append(out.Invalid, rel)
			}
			return true
		}
		if q.Tag != "" && !hasTag(noteTags(st.doc.lines, props), q.Tag) {
			return true
		}
		for _, h := range q.Has {
			if isEmpty(props[h]) {
				return true
			}
		}
		for k, want := range q.Equals {
			if !valueEquals(props[k], want) {
				return true
			}
		}
		row := PropertyRow{Path: rel, Properties: props}
		if q.Upcoming != nil {
			next, ok := nextOccurrence(props[q.Upcoming.Field], today)
			if !ok || next.InDays > q.Upcoming.Days {
				return true
			}
			row.Next = &next
		}
		all = append(all, row)
		return true
	})
	if err != nil {
		return PropertyResult{}, err
	}
	switch {
	case q.Sort != "":
		field, desc := strings.CutPrefix(q.Sort, "-")
		slices.SortStableFunc(all, func(a, b PropertyRow) int {
			c := compareValues(a.Properties[field], b.Properties[field])
			if desc {
				return -c
			}
			return c
		})
	case q.Upcoming != nil:
		slices.SortStableFunc(all, func(a, b PropertyRow) int { return cmp.Compare(a.Next.InDays, b.Next.InDays) })
	}
	if len(all) > limit {
		all, out.Truncated = all[:limit], true
	}
	for i := range all {
		all[i].Properties = pick(all[i].Properties, q.Fields)
	}
	out.Notes = append(out.Notes, all...)
	return out, nil
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func nextOccurrence(v any, today time.Time) (NextDate, bool) {
	d, ok := parseDate(v, today.Location())
	if !ok {
		return NextDate{}, false
	}
	on := func(year int) time.Time {
		day := d.Day()
		// Feb 29 is celebrated on Feb 28 in other years.
		if d.Month() == time.February && day == 29 && !isLeap(year) {
			day = 28
		}
		return time.Date(year, d.Month(), day, 0, 0, 0, 0, today.Location())
	}
	next := on(today.Year())
	if next.Before(today) {
		next = on(today.Year() + 1)
	}
	return NextDate{
		Date:    next.Format("2006-01-02"),
		InDays:  int(next.Sub(today).Hours()+12) / 24,
		Weekday: next.Weekday().String(),
		Years:   next.Year() - d.Year(),
	}, true
}

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func valueEquals(v any, want string) bool {
	if items, ok := v.([]any); ok {
		for _, it := range items {
			if valueEquals(it, want) {
				return true
			}
		}
		return false
	}
	if v == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(v)), strings.TrimSpace(want))
}

func compareValues(a, b any) int {
	if isEmpty(a) || isEmpty(b) {
		return cmp.Compare(boolInt(isEmpty(a)), boolInt(isEmpty(b)))
	}
	if da, ok := parseDate(a, time.UTC); ok {
		if db, ok := parseDate(b, time.UTC); ok {
			return da.Compare(db)
		}
	}
	if na, ok := number(a); ok {
		if nb, ok := number(b); ok {
			return cmp.Compare(na, nb)
		}
	}
	return strings.Compare(strings.ToLower(fmt.Sprint(a)), strings.ToLower(fmt.Sprint(b)))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func pick(props map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return props
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := props[f]; ok {
			out[f] = v
		}
	}
	return out
}

type PropertyChange struct {
	ID        string    `json:"id" jsonschema:"undo it with note_remove(id)"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Previous  any       `json:"previous" jsonschema:"value before the change; null when the property was not there"`
	Value     any       `json:"value,omitempty" jsonschema:"value now set; absent after note_unset_property"`
	ExpiresAt time.Time `json:"undoable_until"`
}

func (s *Service) SetProperty(rel, name string, value any) (PropertyChange, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return PropertyChange{}, errors.New("name must not be empty")
	}
	with, err := renderProperty(name, value)
	if err != nil {
		return PropertyChange{}, err
	}
	return s.changeProperty(rel, name, with, value)
}

func (s *Service) UnsetProperty(rel, name string) (PropertyChange, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return PropertyChange{}, errors.New("name must not be empty")
	}
	return s.changeProperty(rel, name, nil, nil)
}

func (s *Service) changeProperty(rel, name string, with []string, value any) (PropertyChange, error) {
	real, clean, err := s.vault.ResolveWrite(rel)
	if err != nil {
		return PropertyChange{}, err
	}
	var res PropertyChange
	err = s.journal.update(func(entries []entry) ([]entry, error) {
		st, err := s.readFile(real, clean)
		if err != nil {
			return nil, err
		}
		props, err := properties(st.doc.lines)
		if err != nil {
			return nil, fmt.Errorf("%s: front matter is not valid YAML, fix it by hand first: %w", clean, err)
		}
		lines, old, at, err := spliceProperty(st.doc.lines, name, with, -1)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", clean, err)
		}
		if old == nil && with == nil {
			return nil, fmt.Errorf("%s has no property %q", clean, name)
		}
		d := st.doc
		d.lines = lines
		if len(st.doc.lines) == 0 {
			d.trailingNL = true
		}
		if err := s.writeFile(real, clean, st.info, d); err != nil {
			return nil, err
		}
		now := s.now()
		e := entry{ID: newID(), Path: clean, Property: name, Old: old, At: at, Lines: len(with), Hash: s.journal.hash(with), Created: now, Updated: now}
		res = PropertyChange{ID: e.ID, Path: clean, Name: name, Previous: props[name], Value: value, ExpiresAt: now.Add(s.window)}
		return append(s.live(entries), e), nil
	})
	return res, err
}

// Other front matter lines stay byte for byte; at = -1 adds a new key last.
func spliceProperty(lines []string, name string, with []string, at int) ([]string, []string, int, error) {
	fm, ok := findFrontMatter(lines)
	if !ok {
		if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
			return nil, nil, 0, errors.New("front matter has no closing ---")
		}
		if with == nil {
			return lines, nil, 0, nil
		}
		head := append(append([]string{"---"}, with...), "---")
		return append(head, lines...), nil, 0, nil
	}
	start, end, found := propertySpan(lines, fm, name)
	if !found {
		if with == nil {
			return lines, nil, 0, nil
		}
		if at < 1 || at > fm.close {
			at = fm.close
		}
		return insertLines(lines, at, with), nil, 0, nil
	}
	old := slices.Clone(lines[start:end])
	return replaceLines(lines, start, end-start, with), old, start, nil
}

func (s *Service) revertProperty(e entry) (WriteResult, error) {
	real, clean, err := s.vault.ResolveWrite(e.Path)
	if err != nil {
		return WriteResult{}, err
	}
	st, err := s.readFile(real, clean)
	if err != nil {
		return WriteResult{}, err
	}
	fm, ok := findFrontMatter(st.doc.lines)
	var current []string
	if ok {
		if start, end, found := propertySpan(st.doc.lines, fm, e.Property); found {
			current = st.doc.lines[start:end]
		}
	}
	if s.journal.hash(current) != e.Hash {
		return WriteResult{}, fmt.Errorf("property %q in %s was %w", e.Property, e.Path, errOwnerChanged)
	}
	lines, _, _, err := spliceProperty(st.doc.lines, e.Property, e.Old, e.At)
	if err != nil {
		return WriteResult{}, err
	}
	d := st.doc
	d.lines = lines
	if err := s.writeFile(real, clean, st.info, d); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{ID: e.ID, Path: e.Path, Lines: len(e.Old), ExpiresAt: e.Created.Add(s.window)}, nil
}
