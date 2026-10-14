package notes

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultBaseRows = 100
	maxBaseRows     = 500
	maxBaseWarnings = 20
)

type baseFile struct {
	path     string
	props    map[string]any
	rawProps map[string]bool
	tags     []string
	links    []linkVal
	embeds   []linkVal
	size     int64
	mtime    time.Time
	ctime    time.Time
	formulas map[string]any
}

func (f *baseFile) basename() string {
	return strings.TrimSuffix(path.Base(f.path), path.Ext(f.path))
}

func (f *baseFile) prop(name string) any {
	return f.props[name]
}

func (f *baseFile) folder() string {
	d := path.Dir(f.path)
	if d == "." {
		return "/"
	}
	return d
}

func (f *baseFile) field(q *baseQuery, name string) (any, error) {
	switch name {
	case "name":
		return path.Base(f.path), nil
	case "basename":
		return f.basename(), nil
	case "path":
		return f.path, nil
	case "folder":
		return f.folder(), nil
	case "ext":
		return strings.TrimPrefix(path.Ext(f.path), "."), nil
	case "size":
		return float64(f.size), nil
	case "properties":
		return copyMap(f.props), nil
	case "tags":
		out := make([]any, len(f.tags))
		for i, t := range f.tags {
			out[i] = t
		}
		return out, nil
	case "links", "embeds":
		src := f.links
		if name == "embeds" {
			src = f.embeds
		}
		out := make([]any, len(src))
		for i, l := range src {
			out[i] = l
		}
		return out, nil
	case "mtime":
		return dateVal{t: f.mtime, hasTime: true}, nil
	case "ctime":
		return dateVal{t: f.ctime, hasTime: true}, nil
	case "file":
		return f, nil
	case "backlinks":
		return nil, errUnsupported{"file.backlinks"}
	}
	return nil, nil
}

func (f *baseFile) method(sc *scope, name string, nodes []node) (any, error) {
	a, err := sc.args(nodes)
	if err != nil {
		return nil, err
	}
	switch name {
	case "asLink":
		return linkVal{target: strings.TrimSuffix(f.path, ".md"), display: toString(argN(a, 0))}, nil
	case "hasLink":
		return f.hasLink(sc.q, argN(a, 0)), nil
	case "hasProperty":
		return f.rawProps[toString(argN(a, 0))], nil
	case "hasTag":
		for _, v := range a {
			if hasTag(f.tags, toString(v)) {
				return true, nil
			}
		}
		return false, nil
	case "inFolder":
		want := strings.Trim(toString(argN(a, 0)), "/")
		folder := f.folder()
		return want == "" || folder == want || strings.HasPrefix(folder, want+"/"), nil
	case "isEmpty":
		return false, nil
	case "isTruthy":
		return true, nil
	case "toString":
		return f.path, nil
	case "isType":
		return strings.EqualFold(toString(argN(a, 0)), "file"), nil
	}
	return nil, errUnsupported{"file." + name + "()"}
}

func (f *baseFile) hasLink(q *baseQuery, other any) bool {
	var target *baseFile
	switch o := other.(type) {
	case *baseFile:
		target = o
	case linkVal:
		target = q.resolve(o.target)
	case string:
		target = q.resolve(o)
	}
	for _, l := range f.links {
		if target != nil && q.resolve(l.target) == target {
			return true
		}
		if target == nil && linkMatches(l, other) {
			return true
		}
	}
	return false
}

type baseQuery struct {
	now      time.Time
	this     *baseFile
	files    []*baseFile
	byPath   map[string]*baseFile
	byName   map[string]*baseFile
	formulas map[string]node
	warnings []string
}

func (q *baseQuery) warn(msg string) {
	if len(q.warnings) < maxBaseWarnings {
		q.warnings = append(q.warnings, msg)
	}
}

// resolve finds a note the way Obsidian resolves a wikilink: by path, then by name.
func (q *baseQuery) resolve(target string) *baseFile {
	t := strings.TrimSpace(parseLink(target, "").target)
	t = strings.TrimPrefix(t, "/")
	if f := q.byPath[strings.ToLower(t)]; f != nil {
		return f
	}
	if f := q.byPath[strings.ToLower(t+".md")]; f != nil {
		return f
	}
	return q.byName[strings.ToLower(path.Base(strings.TrimSuffix(t, ".md")))]
}

type baseSpec struct {
	Filters    yaml.Node                 `yaml:"filters"`
	Formulas   map[string]string         `yaml:"formulas"`
	Properties map[string]map[string]any `yaml:"properties"`
	Summaries  map[string]string         `yaml:"summaries"`
	Views      []baseView                `yaml:"views"`
	Extra      map[string]yaml.Node      `yaml:",inline"`
}

type baseView struct {
	Type      string            `yaml:"type"`
	Name      string            `yaml:"name"`
	Limit     int               `yaml:"limit"`
	Filters   yaml.Node         `yaml:"filters"`
	Order     []string          `yaml:"order"`
	Sort      []baseSort        `yaml:"sort"`
	GroupBy   *baseSort         `yaml:"groupBy"`
	Summaries map[string]string `yaml:"summaries"`
}

type baseSort struct {
	Property  string `yaml:"property"`
	Direction string `yaml:"direction"`
}

type filterNode struct {
	op    string
	expr  node
	src   string
	items []filterNode
}

func compileFilter(n *yaml.Node) (*filterNode, error) {
	if n == nil || n.Kind == 0 || n.Tag == "!!null" {
		return nil, nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		e, err := parseExpr(n.Value)
		if err != nil {
			return nil, err
		}
		return &filterNode{expr: e, src: n.Value}, nil
	case yaml.MappingNode:
		if len(n.Content) != 2 {
			return nil, errors.New("a filter group must have exactly one of and, or, not")
		}
		op := n.Content[0].Value
		if op != "and" && op != "or" && op != "not" {
			return nil, fmt.Errorf("unknown filter group %q", op)
		}
		list := n.Content[1]
		if list.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("filter group %q must hold a list", op)
		}
		out := &filterNode{op: op}
		for _, c := range list.Content {
			f, err := compileFilter(c)
			if err != nil {
				return nil, err
			}
			if f != nil {
				out.items = append(out.items, *f)
			}
		}
		return out, nil
	}
	return nil, errors.New("a filter must be an expression or an and/or/not group")
}

func (q *baseQuery) matches(f *filterNode, file *baseFile) (bool, error) {
	if f == nil {
		return true, nil
	}
	if f.expr != nil {
		v, err := (&scope{q: q, file: file}).eval(f.expr)
		if err != nil {
			var unsupported errUnsupported
			if errors.As(err, &unsupported) {
				return false, fmt.Errorf("filter %s: %w", f.src, err)
			}
			q.warn(fmt.Sprintf("filter %s on %s: %v", f.src, file.path, err))
			return false, nil
		}
		return truthy(v), nil
	}
	for _, it := range f.items {
		ok, err := q.matches(&it, file)
		if err != nil {
			return false, err
		}
		switch {
		case f.op == "and" && !ok:
			return false, nil
		case f.op == "or" && ok:
			return true, nil
		case f.op == "not" && ok:
			return false, nil
		}
	}
	return f.op != "or", nil
}

func (q *baseQuery) value(file *baseFile, id string) (any, error) {
	sc := &scope{q: q, file: file}
	switch {
	case strings.HasPrefix(id, "file."):
		return file.field(q, strings.TrimPrefix(id, "file."))
	case strings.HasPrefix(id, "formula."):
		return sc.formula(strings.TrimPrefix(id, "formula."))
	case strings.HasPrefix(id, "note."):
		return file.prop(strings.TrimPrefix(id, "note.")), nil
	}
	return file.prop(id), nil
}

func noteValue(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case string:
		if plainDate.MatchString(strings.TrimSpace(x)) {
			if d, ok := parseDateVal(x); ok {
				return d
			}
		}
		if wikiLink.MatchString(strings.TrimSpace(x)) {
			return parseLink(x, "")
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, it := range x {
			out[i] = noteValue(it)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, it := range x {
			out[k] = noteValue(it)
		}
		return out
	}
	return v
}

var (
	bodyWikiLink = regexp.MustCompile(`(!?)\[\[([^\]\n]+)\]\]`)
	bodyMdLink   = regexp.MustCompile(`(!?)\[[^\]\n]*\]\(([^)\s]+)\)`)
)

func extractLinks(lines []string, props map[string]any) (links, embeds []linkVal) {
	add := func(embed bool, l linkVal) {
		if l.target == "" {
			return
		}
		if embed {
			embeds = append(embeds, l)
		} else {
			links = append(links, l)
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for _, m := range bodyWikiLink.FindAllStringSubmatch(x, -1) {
				add(false, parseLink("[["+m[2]+"]]", ""))
			}
		case []any:
			for _, it := range x {
				walk(it)
			}
		case map[string]any:
			for _, it := range x {
				walk(it)
			}
		}
	}
	walk(props)
	for _, l := range lines[frontMatterEnd(lines):] {
		for _, m := range bodyWikiLink.FindAllStringSubmatch(l, -1) {
			add(m[1] == "!", parseLink("[["+m[2]+"]]", ""))
		}
		for _, m := range bodyMdLink.FindAllStringSubmatch(l, -1) {
			target := m[2]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
				continue
			}
			if dec, err := url.PathUnescape(target); err == nil {
				target = dec
			}
			add(m[1] == "!", linkVal{target: strings.TrimSuffix(target, ".md")})
		}
	}
	return links, embeds
}

func (s *Service) loadBaseFiles(q *baseQuery) error {
	q.byPath, q.byName = map[string]*baseFile{}, map[string]*baseFile{}
	root, base, err := s.vault.ResolveDir("")
	if err != nil {
		return err
	}
	return s.walk(root, base, func(rel, real string) bool {
		st, err := s.readFile(real, rel)
		if err != nil {
			return true
		}
		raw, err := properties(st.doc.lines)
		if err != nil {
			q.warn(fmt.Sprintf("%s: front matter is not valid YAML, its properties are ignored", rel))
			raw = map[string]any{}
		}
		f := &baseFile{
			path:     rel,
			props:    make(map[string]any, len(raw)),
			rawProps: make(map[string]bool, len(raw)),
			tags:     noteTags(st.doc.lines, raw),
			size:     st.info.Size(),
			mtime:    st.info.ModTime(),
			formulas: map[string]any{},
		}
		f.ctime = birthTime(real, f.mtime)
		for k, v := range raw {
			f.props[k] = noteValue(v)
			f.rawProps[k] = true
		}
		f.links, f.embeds = extractLinks(st.doc.lines, raw)
		q.files = append(q.files, f)
		q.byPath[strings.ToLower(rel)] = f
		if _, taken := q.byName[strings.ToLower(f.basename())]; !taken {
			q.byName[strings.ToLower(f.basename())] = f
		}
		return true
	})
}

type BaseColumn struct {
	ID   string `json:"id" jsonschema:"file.*, formula.* or a note property"`
	Name string `json:"name,omitempty" jsonschema:"display name from the base, when set"`
}

type BaseRow struct {
	Path   string         `json:"path"`
	Group  any            `json:"group,omitempty" jsonschema:"value of the view's groupBy property"`
	Values map[string]any `json:"values"`
}

type BaseResult struct {
	View      string         `json:"view"`
	Views     []string       `json:"views" jsonschema:"all views of this base; the first is the default"`
	Columns   []BaseColumn   `json:"columns"`
	Rows      []BaseRow      `json:"rows"`
	Total     int            `json:"total" jsonschema:"rows matching the filters, before any limit"`
	Truncated bool           `json:"truncated,omitempty"`
	Summaries map[string]any `json:"summaries,omitempty"`
	Warnings  []string       `json:"warnings,omitempty" jsonschema:"values that could not be computed for some notes; they show as null"`
}

func (s *Service) QueryBase(rel, viewName string, limit int) (BaseResult, error) {
	if limit <= 0 {
		limit = defaultBaseRows
	}
	limit = min(limit, maxBaseRows)
	if !strings.EqualFold(path.Ext(rel), ".base") {
		return BaseResult{}, errors.New("path must point to a .base file")
	}
	real, clean, err := s.vault.ResolveReadAny(rel)
	if err != nil {
		return BaseResult{}, err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return BaseResult{}, err
	}
	var spec baseSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return BaseResult{}, fmt.Errorf("%s: %w", clean, err)
	}
	if len(spec.Views) == 0 {
		spec.Views = []baseView{{Type: "table", Name: "Table"}}
	}
	view := spec.Views[0]
	names := make([]string, len(spec.Views))
	for i, v := range spec.Views {
		names[i] = v.Name
		if viewName != "" && strings.EqualFold(v.Name, viewName) {
			view = v
		}
	}
	if viewName != "" && !strings.EqualFold(view.Name, viewName) {
		return BaseResult{}, fmt.Errorf("no view %q; views: %s", viewName, strings.Join(names, ", "))
	}

	q := &baseQuery{now: s.now(), formulas: map[string]node{}}
	for name, src := range spec.Formulas {
		e, err := parseExpr(src)
		if err != nil {
			return BaseResult{}, fmt.Errorf("formula %s: %w", name, err)
		}
		q.formulas[name] = e
	}
	global, err := compileFilter(&spec.Filters)
	if err != nil {
		return BaseResult{}, fmt.Errorf("filters: %w", err)
	}
	local, err := compileFilter(&view.Filters)
	if err != nil {
		return BaseResult{}, fmt.Errorf("view %s filters: %w", view.Name, err)
	}
	if err := s.loadBaseFiles(q); err != nil {
		return BaseResult{}, err
	}
	q.this = &baseFile{path: clean, props: map[string]any{}, rawProps: map[string]bool{}, formulas: map[string]any{}}
	if info, err := os.Stat(real); err == nil {
		q.this.size, q.this.mtime, q.this.ctime = info.Size(), info.ModTime(), birthTime(real, info.ModTime())
	}

	var rows []*baseFile
	for _, f := range q.files {
		ok, err := q.matches(global, f)
		if err != nil {
			return BaseResult{}, err
		}
		if ok {
			if ok, err = q.matches(local, f); err != nil {
				return BaseResult{}, err
			}
		}
		if ok {
			rows = append(rows, f)
		}
	}

	type keyed struct {
		f     *baseFile
		group any
		sort  []any
	}
	items := make([]keyed, len(rows))
	for i, f := range rows {
		k := keyed{f: f}
		if view.GroupBy != nil {
			if k.group, err = q.value(f, view.GroupBy.Property); err != nil {
				return BaseResult{}, err
			}
		}
		for _, so := range view.Sort {
			v, err := q.value(f, so.Property)
			if err != nil {
				return BaseResult{}, err
			}
			k.sort = append(k.sort, v)
		}
		items[i] = k
	}
	cmpVals := func(a, b any, desc bool) int {
		if blank(a) || blank(b) {
			return cmpBlank(a, b)
		}
		c, _ := order(a, b)
		if desc {
			return -c
		}
		return c
	}
	sort.SliceStable(items, func(i, j int) bool {
		if view.GroupBy != nil {
			if c := cmpVals(items[i].group, items[j].group, isDesc(view.GroupBy.Direction)); c != 0 {
				return c < 0
			}
		}
		for n, so := range view.Sort {
			if c := cmpVals(items[i].sort[n], items[j].sort[n], isDesc(so.Direction)); c != 0 {
				return c < 0
			}
		}
		return false
	})

	columns := view.Order
	if len(columns) == 0 {
		columns = defaultColumns(rows)
	}
	out := BaseResult{View: view.Name, Views: names, Total: len(items), Rows: []BaseRow{}}
	for _, c := range columns {
		col := BaseColumn{ID: c}
		if p, ok := spec.Properties[c]; ok {
			col.Name, _ = p["displayName"].(string)
		}
		out.Columns = append(out.Columns, col)
	}
	if len(view.Summaries) > 0 {
		if out.Summaries, err = q.summaries(rows, view.Summaries, spec.Summaries); err != nil {
			return BaseResult{}, err
		}
	}
	if view.Limit > 0 && len(items) > view.Limit {
		items = items[:view.Limit]
	}
	if len(items) > limit {
		items, out.Truncated = items[:limit], true
	}
	for _, it := range items {
		row := BaseRow{Path: it.f.path, Values: map[string]any{}}
		if view.GroupBy != nil {
			row.Group = exportValue(it.group)
		}
		for _, c := range columns {
			v, err := q.value(it.f, c)
			if err != nil {
				return BaseResult{}, err
			}
			row.Values[c] = exportValue(v)
		}
		out.Rows = append(out.Rows, row)
	}
	out.Warnings = q.warnings
	return out, nil
}

func blank(v any) bool {
	return v == nil || v == "" || isEmptyList(v)
}

func cmpBlank(a, b any) int {
	switch {
	case blank(a) && blank(b):
		return 0
	case blank(a):
		return 1
	}
	return -1
}

func isDesc(dir string) bool {
	return strings.EqualFold(dir, "desc")
}

func defaultColumns(rows []*baseFile) []string {
	seen := map[string]bool{}
	cols := []string{"file.name"}
	for _, f := range rows {
		for k := range f.props {
			if !seen[k] {
				seen[k] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return append(cols, keys...)
}

func exportValue(v any) any {
	switch x := v.(type) {
	case dateVal, durationVal, regexVal:
		return toString(x)
	case linkVal:
		if x.display != "" {
			return "[[" + x.target + "|" + x.display + "]]"
		}
		return "[[" + x.target + "]]"
	case *baseFile:
		return x.path
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, it := range x {
			out[i] = exportValue(it)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, it := range x {
			out[k] = exportValue(it)
		}
		return out
	}
	return v
}

func (q *baseQuery) summaries(rows []*baseFile, wanted, custom map[string]string) (map[string]any, error) {
	out := map[string]any{}
	for col, name := range wanted {
		values := make([]any, 0, len(rows))
		for _, f := range rows {
			v, err := q.value(f, col)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		if src, ok := custom[name]; ok {
			e, err := parseExpr(src)
			if err != nil {
				return nil, fmt.Errorf("summary %s: %w", name, err)
			}
			v, err := (&scope{q: q, file: q.this, vars: map[string]any{"values": values}}).eval(e)
			if err != nil {
				return nil, fmt.Errorf("summary %s: %w", name, err)
			}
			out[col] = exportValue(v)
			continue
		}
		v, err := defaultSummary(name, values)
		if err != nil {
			return nil, err
		}
		out[col] = exportValue(v)
	}
	return out, nil
}

func defaultSummary(name string, values []any) (any, error) {
	var nums []float64
	var dates []dateVal
	for _, v := range values {
		if d, ok := v.(dateVal); ok {
			dates = append(dates, d)
		} else if f, ok := v.(float64); ok {
			nums = append(nums, f)
		}
	}
	switch strings.ToLower(name) {
	case "average", "sum", "min", "max", "range", "median", "stddev":
		if len(nums) == 0 {
			if strings.EqualFold(name, "range") && len(dates) > 0 {
				break
			}
			return nil, nil
		}
		slices.Sort(nums)
		sum := 0.0
		for _, n := range nums {
			sum += n
		}
		mean := sum / float64(len(nums))
		switch strings.ToLower(name) {
		case "average":
			return mean, nil
		case "sum":
			return sum, nil
		case "min":
			return nums[0], nil
		case "max":
			return nums[len(nums)-1], nil
		case "range":
			return nums[len(nums)-1] - nums[0], nil
		case "median":
			m := len(nums) / 2
			if len(nums)%2 == 0 {
				return (nums[m-1] + nums[m]) / 2, nil
			}
			return nums[m], nil
		case "stddev":
			v := 0.0
			for _, n := range nums {
				v += (n - mean) * (n - mean)
			}
			return math.Sqrt(v / float64(len(nums))), nil
		}
	}
	switch strings.ToLower(name) {
	case "earliest", "latest", "range":
		if len(dates) == 0 {
			return nil, nil
		}
		slices.SortFunc(dates, func(a, b dateVal) int { return a.t.Compare(b.t) })
		switch strings.ToLower(name) {
		case "earliest":
			return dates[0], nil
		case "latest":
			return dates[len(dates)-1], nil
		}
		return float64(dates[len(dates)-1].t.Sub(dates[0].t).Milliseconds()), nil
	case "checked", "unchecked":
		n := 0.0
		for _, v := range values {
			if b, ok := v.(bool); ok && b == strings.EqualFold(name, "checked") {
				n++
			}
		}
		return n, nil
	case "empty", "filled":
		n := 0.0
		for _, v := range values {
			if blank(v) == strings.EqualFold(name, "empty") {
				n++
			}
		}
		return n, nil
	case "unique":
		var seen []any
		for _, v := range values {
			if !slices.ContainsFunc(seen, func(o any) bool { return looseEqual(o, v) }) {
				seen = append(seen, v)
			}
		}
		return float64(len(seen)), nil
	}
	return nil, errUnsupported{"summary " + name}
}

func isEmptyList(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 0
}
