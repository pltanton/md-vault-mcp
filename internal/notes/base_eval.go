package notes

import (
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type (
	dateVal struct {
		t       time.Time
		hasTime bool
	}
	durationVal struct {
		months int
		ms     float64
	}
	linkVal struct {
		target, display string
	}
	regexVal struct {
		re     *regexp.Regexp
		global bool
	}
)

// errUnsupported fails the whole query rather than quietly producing a wrong table.
type errUnsupported struct{ what string }

func (e errUnsupported) Error() string {
	return e.what + " is not supported by this server"
}

type scope struct {
	q     *baseQuery
	file  *baseFile
	vars  map[string]any
	depth int
}

func (sc *scope) with(vars map[string]any) *scope {
	n := *sc
	n.vars = vars
	return &n
}

func (sc *scope) eval(n node) (any, error) {
	switch x := n.(type) {
	case numLit:
		return x.v, nil
	case strLit:
		return x.v, nil
	case regexLit:
		return newRegex(x.pattern, x.flags)
	case listLit:
		out := make([]any, 0, len(x.items))
		for _, it := range x.items {
			v, err := sc.eval(it)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case objLit:
		out := make(map[string]any, len(x.keys))
		for i, k := range x.keys {
			v, err := sc.eval(x.vals[i])
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case ident:
		return sc.ident(x.name)
	case member:
		if id, ok := x.obj.(ident); ok {
			switch id.name {
			case "note":
				if _, shadowed := sc.vars["note"]; !shadowed {
					return sc.file.prop(x.name), nil
				}
			case "formula":
				if _, shadowed := sc.vars["formula"]; !shadowed {
					return sc.formula(x.name)
				}
			}
		}
		obj, err := sc.eval(x.obj)
		if err != nil {
			return nil, err
		}
		return sc.field(obj, x.name)
	case index:
		if id, ok := x.obj.(ident); ok && (id.name == "note" || id.name == "formula") {
			k, err := sc.eval(x.key)
			if err != nil {
				return nil, err
			}
			if id.name == "note" {
				return sc.file.prop(toString(k)), nil
			}
			return sc.formula(toString(k))
		}
		obj, err := sc.eval(x.obj)
		if err != nil {
			return nil, err
		}
		k, err := sc.eval(x.key)
		if err != nil {
			return nil, err
		}
		return indexValue(obj, k)
	case call:
		return sc.call(x)
	case unary:
		v, err := sc.eval(x.x)
		if err != nil {
			return nil, err
		}
		if x.op == "!" {
			return !truthy(v), nil
		}
		switch y := v.(type) {
		case float64:
			return -y, nil
		case durationVal:
			return durationVal{months: -y.months, ms: -y.ms}, nil
		}
		f, ok := toNumber(v)
		if !ok {
			return nil, nil
		}
		return -f, nil
	case binary:
		if x.op == "&&" || x.op == "||" {
			l, err := sc.eval(x.l)
			if err != nil {
				return nil, err
			}
			if (x.op == "&&") != truthy(l) {
				return l, nil
			}
			return sc.eval(x.r)
		}
		l, err := sc.eval(x.l)
		if err != nil {
			return nil, err
		}
		r, err := sc.eval(x.r)
		if err != nil {
			return nil, err
		}
		return binaryOp(x.op, l, r)
	}
	return nil, fmt.Errorf("cannot evaluate %T", n)
}

func (sc *scope) ident(name string) (any, error) {
	if v, ok := sc.vars[name]; ok {
		return v, nil
	}
	switch name {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	case "file":
		return sc.file, nil
	case "this":
		return sc.q.this, nil
	case "note":
		return copyMap(sc.file.props), nil
	case "formula":
		return nil, errors.New("use formula.<name>")
	case "values":
		return nil, errUnsupported{"values outside a summary"}
	}
	return sc.file.prop(name), nil
}

func (sc *scope) formula(name string) (any, error) {
	if sc.depth > 32 {
		return nil, fmt.Errorf("formula %s refers to itself", name)
	}
	if v, ok := sc.file.formulas[name]; ok {
		return v, nil
	}
	expr, ok := sc.q.formulas[name]
	if !ok {
		return nil, nil
	}
	inner := &scope{q: sc.q, file: sc.file, depth: sc.depth + 1}
	v, err := inner.eval(expr)
	if err != nil {
		var unsupported errUnsupported
		if errors.As(err, &unsupported) {
			return nil, fmt.Errorf("formula %s: %w", name, err)
		}
		sc.q.warn(fmt.Sprintf("formula %s in %s: %v", name, sc.file.path, err))
		v = nil
	}
	sc.file.formulas[name] = v
	return v, nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func indexValue(obj, k any) (any, error) {
	switch o := obj.(type) {
	case []any:
		f, ok := toNumber(k)
		if !ok {
			return nil, nil
		}
		i := int(f)
		if i < 0 {
			i += len(o)
		}
		if i < 0 || i >= len(o) {
			return nil, nil
		}
		return o[i], nil
	case map[string]any:
		return o[toString(k)], nil
	case string:
		f, ok := toNumber(k)
		r := []rune(o)
		if !ok || int(f) < 0 || int(f) >= len(r) {
			return nil, nil
		}
		return string(r[int(f)]), nil
	}
	return nil, nil
}

func (sc *scope) field(obj any, name string) (any, error) {
	switch o := obj.(type) {
	case *baseFile:
		return o.field(sc.q, name)
	case dateVal:
		switch name {
		case "year":
			return float64(o.t.Year()), nil
		case "month":
			return float64(o.t.Month()), nil
		case "day":
			return float64(o.t.Day()), nil
		case "hour":
			return float64(o.t.Hour()), nil
		case "minute":
			return float64(o.t.Minute()), nil
		case "second":
			return float64(o.t.Second()), nil
		case "millisecond":
			return float64(o.t.Nanosecond() / 1e6), nil
		}
	case string:
		if name == "length" {
			return float64(len([]rune(o))), nil
		}
	case []any:
		if name == "length" {
			return float64(len(o)), nil
		}
	case map[string]any:
		return o[name], nil
	case linkVal:
		if f := sc.q.resolve(o.target); f != nil {
			return f.field(sc.q, name)
		}
	}
	return nil, nil
}

func binaryOp(op string, l, r any) (any, error) {
	switch op {
	case "==":
		return looseEqual(l, r), nil
	case "!=":
		return !looseEqual(l, r), nil
	case "<", ">", "<=", ">=":
		c, ok := order(l, r)
		if !ok {
			return false, nil
		}
		switch op {
		case "<":
			return c < 0, nil
		case ">":
			return c > 0, nil
		case "<=":
			return c <= 0, nil
		}
		return c >= 0, nil
	case "+":
		return add(l, r, 1)
	case "-":
		if dl, ok := l.(dateVal); ok {
			if dr, ok := r.(dateVal); ok {
				return float64(dl.t.Sub(dr.t).Milliseconds()), nil
			}
		}
		return add(l, r, -1)
	case "*", "/", "%":
		if d, ok := l.(durationVal); ok {
			f, ok := toNumber(r)
			if !ok || op == "%" {
				return nil, nil
			}
			if op == "/" {
				f = 1 / f
			}
			return durationVal{months: int(math.Round(float64(d.months) * f)), ms: d.ms * f}, nil
		}
		a, ok1 := toNumber(l)
		b, ok2 := toNumber(r)
		if !ok1 || !ok2 {
			return nil, nil
		}
		switch op {
		case "*":
			return a * b, nil
		case "/":
			return a / b, nil
		}
		return math.Mod(a, b), nil
	}
	return nil, fmt.Errorf("unknown operator %s", op)
}

func add(l, r any, sign int) (any, error) {
	if d, ok := l.(dateVal); ok {
		dur, ok := asDuration(r)
		if !ok {
			if sign > 0 {
				if s, isStr := r.(string); isStr {
					return toString(l) + s, nil
				}
			}
			return nil, nil
		}
		return d.plus(dur, sign), nil
	}
	if dl, ok := l.(durationVal); ok {
		if dr, ok := asDuration(r); ok {
			return durationVal{months: dl.months + sign*dr.months, ms: dl.ms + float64(sign)*dr.ms}, nil
		}
	}
	if sign > 0 {
		_, ls := l.(string)
		_, rs := r.(string)
		if ls || rs {
			return toString(l) + toString(r), nil
		}
		if a, ok := l.([]any); ok {
			if b, ok := r.([]any); ok {
				return append(slices.Clone(a), b...), nil
			}
		}
	}
	a, ok1 := toNumber(l)
	b, ok2 := toNumber(r)
	if !ok1 || !ok2 {
		return nil, nil
	}
	return a + float64(sign)*b, nil
}

func (d dateVal) plus(dur durationVal, sign int) dateVal {
	t := d.t.AddDate(0, sign*dur.months, 0).Add(time.Duration(float64(sign) * dur.ms * float64(time.Millisecond)))
	hasTime := d.hasTime || math.Mod(dur.ms, 86400000) != 0
	return dateVal{t: t, hasTime: hasTime}
}

var durationPart = regexp.MustCompile(`^\s*(-?\d+(?:\.\d+)?)\s*([a-zA-Z]+)`)

func parseDuration(s string) (durationVal, bool) {
	var d durationVal
	s = strings.TrimSpace(s)
	if s == "" {
		return d, false
	}
	for s != "" {
		m := durationPart.FindStringSubmatch(s)
		if m == nil {
			return d, false
		}
		n, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "y", "year", "years":
			d.months += int(n * 12)
		case "M", "month", "months":
			d.months += int(n)
		case "w", "week", "weeks":
			d.ms += n * 7 * 86400000
		case "d", "day", "days":
			d.ms += n * 86400000
		case "h", "hour", "hours":
			d.ms += n * 3600000
		case "m", "minute", "minutes":
			d.ms += n * 60000
		case "s", "second", "seconds":
			d.ms += n * 1000
		default:
			return d, false
		}
		s = strings.TrimLeft(s[len(m[0]):], " ,")
	}
	return d, true
}

func asDuration(v any) (durationVal, bool) {
	switch x := v.(type) {
	case durationVal:
		return x, true
	case string:
		return parseDuration(x)
	case float64:
		return durationVal{ms: x}, true
	}
	return durationVal{}, false
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case dateVal:
		return float64(x.t.UnixMilli()), true
	case durationVal:
		return x.ms + float64(x.months)*30*86400000, true
	}
	return 0, false
}

func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return formatNumber(x)
	case dateVal:
		if x.hasTime {
			return x.t.Format("2006-01-02 15:04:05")
		}
		return x.t.Format("2006-01-02")
	case durationVal:
		return formatNumber(x.ms + float64(x.months)*30*86400000)
	case linkVal:
		return x.target
	case *baseFile:
		return x.path
	case []any:
		parts := make([]string, len(x))
		for i, it := range x {
			if it != nil {
				parts[i] = toString(it)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	case regexVal:
		return "/" + x.re.String() + "/"
	}
	return fmt.Sprint(v)
}

func looseEqual(l, r any) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	switch a := l.(type) {
	case dateVal:
		if b, ok := asDate(r); ok {
			return a.t.Equal(b.t)
		}
		return false
	case linkVal:
		return linkMatches(a, r)
	case *baseFile:
		if b, ok := r.(*baseFile); ok {
			return a == b
		}
		if b, ok := r.(linkVal); ok {
			return linkMatches(b, a)
		}
		return false
	case []any:
		b, ok := r.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !looseEqual(a[i], b[i]) {
				return false
			}
		}
		return true
	case float64:
		b, ok := toNumber(r)
		return ok && a == b
	case bool:
		if b, ok := r.(bool); ok {
			return a == b
		}
		f, ok := toNumber(r)
		return ok && f == boolNum(a)
	case string:
		switch b := r.(type) {
		case string:
			return a == b
		case linkVal, dateVal, *baseFile:
			return looseEqual(r, l)
		case float64:
			f, ok := toNumber(a)
			return ok && f == b
		}
		return false
	}
	return fmt.Sprint(l) == fmt.Sprint(r)
}

func boolNum(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func linkMatches(l linkVal, other any) bool {
	norm := func(s string) string {
		s = strings.TrimSuffix(strings.TrimSpace(s), ".md")
		return strings.ToLower(s)
	}
	switch o := other.(type) {
	case linkVal:
		return norm(l.target) == norm(o.target)
	case string:
		return norm(l.target) == norm(strings.Trim(o, "[]"))
	case *baseFile:
		t := norm(l.target)
		return t == norm(o.path) || t == norm(o.basename())
	}
	return false
}

func asDate(v any) (dateVal, bool) {
	switch x := v.(type) {
	case dateVal:
		return x, true
	case string:
		return parseDateVal(x)
	}
	return dateVal{}, false
}

func parseDateVal(s string) (dateVal, bool) {
	t, ok := parseDate(s, time.Local)
	if !ok {
		return dateVal{}, false
	}
	return dateVal{t: t, hasTime: len(strings.TrimSpace(s)) > 10}, true
}

func order(l, r any) (int, bool) {
	if l == nil || r == nil {
		return 0, false
	}
	if a, ok := l.(dateVal); ok {
		if b, ok := asDate(r); ok {
			return a.t.Compare(b.t), true
		}
	}
	if b, ok := r.(dateVal); ok {
		if a, ok := asDate(l); ok {
			return a.t.Compare(b.t), true
		}
	}
	ls, lIsStr := l.(string)
	rs, rIsStr := r.(string)
	if lIsStr && rIsStr {
		return strings.Compare(strings.ToLower(ls), strings.ToLower(rs)), true
	}
	a, ok1 := toNumber(l)
	b, ok2 := toNumber(r)
	if ok1 && ok2 {
		switch {
		case a < b:
			return -1, true
		case a > b:
			return 1, true
		}
		return 0, true
	}
	return strings.Compare(strings.ToLower(toString(l)), strings.ToLower(toString(r))), true
}

func newRegex(pattern, flags string) (regexVal, error) {
	prefix := ""
	if strings.ContainsRune(flags, 'i') {
		prefix += "i"
	}
	if strings.ContainsRune(flags, 'm') {
		prefix += "m"
	}
	if strings.ContainsRune(flags, 's') {
		prefix += "s"
	}
	if prefix != "" {
		pattern = "(?" + prefix + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return regexVal{}, fmt.Errorf("regular expression: %w", err)
	}
	return regexVal{re: re, global: strings.ContainsRune(flags, 'g')}, nil
}

func (sc *scope) call(c call) (any, error) {
	switch fn := c.fn.(type) {
	case ident:
		if _, isVar := sc.vars[fn.name]; !isVar {
			return sc.global(fn.name, c.args)
		}
	case member:
		recv, err := sc.eval(fn.obj)
		if err != nil {
			return nil, err
		}
		return sc.method(recv, fn.name, c.args)
	}
	return nil, errUnsupported{"calling a computed value"}
}

func (sc *scope) args(nodes []node) ([]any, error) {
	out := make([]any, len(nodes))
	for i, n := range nodes {
		v, err := sc.eval(n)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func argN(a []any, i int) any {
	if i < len(a) {
		return a[i]
	}
	return nil
}

func (sc *scope) global(name string, nodes []node) (any, error) {
	if name == "if" {
		if len(nodes) < 2 {
			return nil, errors.New("if() needs a condition and a result")
		}
		cond, err := sc.eval(nodes[0])
		if err != nil {
			return nil, err
		}
		if truthy(cond) {
			return sc.eval(nodes[1])
		}
		if len(nodes) > 2 {
			return sc.eval(nodes[2])
		}
		return nil, nil
	}
	a, err := sc.args(nodes)
	if err != nil {
		return nil, err
	}
	switch name {
	case "date":
		if d, ok := argN(a, 0).(dateVal); ok {
			return d, nil
		}
		d, ok := parseDateVal(toString(argN(a, 0)))
		if !ok {
			return nil, nil
		}
		return d, nil
	case "duration":
		d, ok := asDuration(argN(a, 0))
		if !ok {
			return nil, nil
		}
		return d, nil
	case "now":
		return dateVal{t: sc.q.now, hasTime: true}, nil
	case "today":
		return dateVal{t: dateOnly(sc.q.now)}, nil
	case "number":
		f, ok := toNumber(argN(a, 0))
		if !ok {
			return nil, fmt.Errorf("number(%q): not a number", toString(argN(a, 0)))
		}
		return f, nil
	case "list":
		if l, ok := argN(a, 0).([]any); ok {
			return l, nil
		}
		return []any{argN(a, 0)}, nil
	case "max", "min":
		var best any
		for _, v := range a {
			f, ok := toNumber(v)
			if !ok {
				continue
			}
			if b, ok := best.(float64); !ok || name == "max" && f > b || name == "min" && f < b {
				best = f
			}
		}
		return best, nil
	case "link":
		target := argN(a, 0)
		display := ""
		if len(a) > 1 {
			display = toString(a[1])
		}
		switch t := target.(type) {
		case *baseFile:
			return linkVal{target: strings.TrimSuffix(t.path, ".md"), display: display}, nil
		case linkVal:
			t.display = display
			return t, nil
		}
		return parseLink(toString(target), display), nil
	case "file":
		switch t := argN(a, 0).(type) {
		case *baseFile:
			return t, nil
		case linkVal:
			return sc.q.resolve(t.target), nil
		}
		f := sc.q.resolve(toString(argN(a, 0)))
		if f == nil {
			return nil, nil
		}
		return f, nil
	case "escapeHTML":
		return html.EscapeString(toString(argN(a, 0))), nil
	case "html", "icon", "image":
		return toString(argN(a, 0)), nil
	case "random":
		return nil, errUnsupported{"random()"}
	}
	return nil, errUnsupported{name + "()"}
}

var wikiLink = regexp.MustCompile(`^!?\[\[([^\]|#^]*)(?:[#^][^\]|]*)?(?:\|([^\]]*))?\]\]$`)

func parseLink(s, display string) linkVal {
	s = strings.TrimSpace(s)
	if m := wikiLink.FindStringSubmatch(s); m != nil {
		if display == "" {
			display = m[2]
		}
		return linkVal{target: strings.TrimSpace(m[1]), display: display}
	}
	return linkVal{target: s, display: display}
}

func (sc *scope) method(recv any, name string, nodes []node) (any, error) {
	if l, ok := recv.([]any); ok {
		switch name {
		case "map", "filter", "reduce":
			return sc.listLambda(l, name, nodes)
		}
	}
	if f, ok := recv.(*baseFile); ok {
		return f.method(sc, name, nodes)
	}
	a, err := sc.args(nodes)
	if err != nil {
		return nil, err
	}
	switch name {
	case "isTruthy":
		return truthy(recv), nil
	case "isType":
		return typeName(recv) == strings.ToLower(toString(argN(a, 0))), nil
	case "toString":
		return toString(recv), nil
	case "isEmpty":
		switch x := recv.(type) {
		case nil:
			return true, nil
		case string:
			return x == "", nil
		case []any:
			return len(x) == 0, nil
		case map[string]any:
			return len(x) == 0, nil
		}
		return false, nil
	}
	switch x := recv.(type) {
	case nil:
		return nil, nil
	case dateVal:
		return dateMethod(sc, x, name, a)
	case string:
		return stringMethod(x, name, a)
	case float64:
		return numberMethod(x, name, a)
	case []any:
		return listMethod(x, name, a)
	case map[string]any:
		switch name {
		case "keys":
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]any, len(keys))
			for i, k := range keys {
				out[i] = k
			}
			return out, nil
		case "values":
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]any, len(keys))
			for i, k := range keys {
				out[i] = x[k]
			}
			return out, nil
		}
	case linkVal:
		switch name {
		case "asFile":
			if f := sc.q.resolve(x.target); f != nil {
				return f, nil
			}
			return nil, nil
		case "linksTo":
			f := sc.q.resolve(x.target)
			if f == nil {
				return false, nil
			}
			return f.hasLink(sc.q, argN(a, 0)), nil
		}
	case regexVal:
		if name == "matches" {
			return x.re.MatchString(toString(argN(a, 0))), nil
		}
	case durationVal:
		return nil, errUnsupported{"duration." + name + "()"}
	}
	return nil, errUnsupported{typeName(recv) + "." + name + "()"}
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case dateVal:
		return "date"
	case durationVal:
		return "duration"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	case linkVal:
		return "link"
	case *baseFile:
		return "file"
	case regexVal:
		return "regexp"
	}
	return "unknown"
}

func (sc *scope) listLambda(l []any, name string, nodes []node) (any, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("%s() needs an expression", name)
	}
	var acc any
	if name == "reduce" && len(nodes) > 1 {
		v, err := sc.eval(nodes[1])
		if err != nil {
			return nil, err
		}
		acc = v
	}
	out := []any{}
	for i, v := range l {
		vars := copyMap(sc.vars)
		vars["value"], vars["index"], vars["acc"] = v, float64(i), acc
		r, err := sc.with(vars).eval(nodes[0])
		if err != nil {
			return nil, err
		}
		switch name {
		case "map":
			out = append(out, r)
		case "filter":
			if truthy(r) {
				out = append(out, v)
			}
		case "reduce":
			acc = r
		}
	}
	if name == "reduce" {
		return acc, nil
	}
	return out, nil
}

func dateMethod(sc *scope, d dateVal, name string, a []any) (any, error) {
	switch name {
	case "date":
		return dateVal{t: dateOnly(d.t)}, nil
	case "time":
		return d.t.Format("15:04:05"), nil
	case "format":
		return momentFormat(d.t, toString(argN(a, 0))), nil
	case "relative":
		return relativeTime(d.t, sc.q.now), nil
	}
	return nil, errUnsupported{"date." + name + "()"}
}

func stringMethod(s, name string, a []any) (any, error) {
	switch name {
	case "contains":
		return strings.Contains(s, toString(argN(a, 0))), nil
	case "containsAll", "containsAny":
		for _, v := range a {
			has := strings.Contains(s, toString(v))
			if name == "containsAny" && has {
				return true, nil
			}
			if name == "containsAll" && !has {
				return false, nil
			}
		}
		return name == "containsAll", nil
	case "startsWith":
		return strings.HasPrefix(s, toString(argN(a, 0))), nil
	case "endsWith":
		return strings.HasSuffix(s, toString(argN(a, 0))), nil
	case "lower":
		return strings.ToLower(s), nil
	case "upper":
		return strings.ToUpper(s), nil
	case "trim":
		return strings.TrimSpace(s), nil
	case "title":
		prev := ' '
		return strings.Map(func(r rune) rune {
			out := r
			if unicode.IsSpace(prev) {
				out = unicode.ToUpper(r)
			}
			prev = r
			return out
		}, s), nil
	case "reverse":
		r := []rune(s)
		slices.Reverse(r)
		return string(r), nil
	case "repeat":
		n, _ := toNumber(argN(a, 0))
		if n < 0 || n > 10000 {
			return nil, nil
		}
		return strings.Repeat(s, int(n)), nil
	case "slice":
		r := []rune(s)
		start, end := sliceBounds(len(r), a)
		return string(r[start:end]), nil
	case "split":
		var parts []string
		if re, ok := argN(a, 0).(regexVal); ok {
			parts = re.re.Split(s, -1)
		} else {
			parts = strings.Split(s, toString(argN(a, 0)))
		}
		if n, ok := toNumber(argN(a, 1)); ok && len(a) > 1 && int(n) >= 0 && int(n) < len(parts) {
			parts = parts[:int(n)]
		}
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = p
		}
		return out, nil
	case "replace":
		repl := toString(argN(a, 1))
		if re, ok := argN(a, 0).(regexVal); ok {
			if re.global {
				return re.re.ReplaceAllString(s, repl), nil
			}
			loc := re.re.FindStringSubmatchIndex(s)
			if loc == nil {
				return s, nil
			}
			var out []byte
			out = re.re.ExpandString(out, repl, s, loc)
			return s[:loc[0]] + string(out) + s[loc[1]:], nil
		}
		return strings.ReplaceAll(s, toString(argN(a, 0)), repl), nil
	}
	return nil, errUnsupported{"string." + name + "()"}
}

func sliceBounds(n int, a []any) (int, int) {
	clamp := func(v any, def int) int {
		f, ok := toNumber(v)
		if !ok || v == nil {
			return def
		}
		i := int(f)
		if i < 0 {
			i += n
		}
		return max(0, min(i, n))
	}
	start := clamp(argN(a, 0), 0)
	end := clamp(argN(a, 1), n)
	if end < start {
		end = start
	}
	return start, end
}

func numberMethod(f float64, name string, a []any) (any, error) {
	switch name {
	case "abs":
		return math.Abs(f), nil
	case "ceil":
		return math.Ceil(f), nil
	case "floor":
		return math.Floor(f), nil
	case "round":
		digits, _ := toNumber(argN(a, 0))
		p := math.Pow(10, digits)
		return math.Floor(f*p+0.5) / p, nil
	case "toFixed":
		digits, _ := toNumber(argN(a, 0))
		return strconv.FormatFloat(f, 'f', int(digits), 64), nil
	}
	return nil, errUnsupported{"number." + name + "()"}
}

func listMethod(l []any, name string, a []any) (any, error) {
	contains := func(v any) bool {
		for _, it := range l {
			if looseEqual(it, v) || isTagEqual(it, v) {
				return true
			}
		}
		return false
	}
	switch name {
	case "contains":
		return contains(argN(a, 0)), nil
	case "containsAll", "containsAny":
		for _, v := range a {
			has := contains(v)
			if name == "containsAny" && has {
				return true, nil
			}
			if name == "containsAll" && !has {
				return false, nil
			}
		}
		return name == "containsAll", nil
	case "join":
		sep := ","
		if len(a) > 0 {
			sep = toString(a[0])
		}
		parts := make([]string, len(l))
		for i, it := range l {
			parts[i] = toString(it)
		}
		return strings.Join(parts, sep), nil
	case "reverse":
		out := slices.Clone(l)
		slices.Reverse(out)
		return out, nil
	case "slice":
		start, end := sliceBounds(len(l), a)
		return slices.Clone(l[start:end]), nil
	case "sort":
		out := slices.Clone(l)
		slices.SortStableFunc(out, func(x, y any) int {
			c, _ := order(x, y)
			return c
		})
		return out, nil
	case "unique":
		var out []any
		for _, it := range l {
			if !slices.ContainsFunc(out, func(o any) bool { return looseEqual(o, it) }) {
				out = append(out, it)
			}
		}
		return append([]any{}, out...), nil
	case "flat":
		var out []any
		for _, it := range l {
			if sub, ok := it.([]any); ok {
				out = append(out, sub...)
			} else {
				out = append(out, it)
			}
		}
		return append([]any{}, out...), nil
	}
	return nil, errUnsupported{"list." + name + "()"}
}

// isTagEqual lets file.tags.contains("person") match a tag stored as "#person".
func isTagEqual(a, b any) bool {
	as, ok1 := a.(string)
	bs, ok2 := b.(string)
	return ok1 && ok2 && strings.EqualFold(strings.TrimPrefix(as, "#"), strings.TrimPrefix(bs, "#"))
}

var momentToken = regexp.MustCompile(`\[[^\]]*\]|YYYY|YY|MMMM|MMM|MM|M|Do|DD|D|dddd|ddd|dd|d|HH|H|hh|h|mm|m|ss|s|A|a|SSS|X|x|ZZ|Z`)

func momentFormat(t time.Time, layout string) string {
	if layout == "" {
		layout = "YYYY-MM-DDTHH:mm:ssZ"
	}
	return momentToken.ReplaceAllStringFunc(layout, func(tok string) string {
		h12 := t.Hour() % 12
		if h12 == 0 {
			h12 = 12
		}
		switch tok {
		case "YYYY":
			return fmt.Sprintf("%04d", t.Year())
		case "YY":
			return fmt.Sprintf("%02d", t.Year()%100)
		case "MMMM":
			return t.Month().String()
		case "MMM":
			return t.Month().String()[:3]
		case "MM":
			return fmt.Sprintf("%02d", int(t.Month()))
		case "M":
			return strconv.Itoa(int(t.Month()))
		case "Do":
			return strconv.Itoa(t.Day()) + ordinal(t.Day())
		case "DD":
			return fmt.Sprintf("%02d", t.Day())
		case "D":
			return strconv.Itoa(t.Day())
		case "dddd":
			return t.Weekday().String()
		case "ddd":
			return t.Weekday().String()[:3]
		case "dd":
			return t.Weekday().String()[:2]
		case "d":
			return strconv.Itoa(int(t.Weekday()))
		case "HH":
			return fmt.Sprintf("%02d", t.Hour())
		case "H":
			return strconv.Itoa(t.Hour())
		case "hh":
			return fmt.Sprintf("%02d", h12)
		case "h":
			return strconv.Itoa(h12)
		case "mm":
			return fmt.Sprintf("%02d", t.Minute())
		case "m":
			return strconv.Itoa(t.Minute())
		case "ss":
			return fmt.Sprintf("%02d", t.Second())
		case "s":
			return strconv.Itoa(t.Second())
		case "SSS":
			return fmt.Sprintf("%03d", t.Nanosecond()/1e6)
		case "A":
			if t.Hour() < 12 {
				return "AM"
			}
			return "PM"
		case "a":
			if t.Hour() < 12 {
				return "am"
			}
			return "pm"
		case "X":
			return strconv.FormatInt(t.Unix(), 10)
		case "x":
			return strconv.FormatInt(t.UnixMilli(), 10)
		case "Z":
			return t.Format("-07:00")
		case "ZZ":
			return t.Format("-0700")
		}
		return strings.Trim(tok, "[]")
	})
}

func ordinal(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// relativeTime follows Moment's fromNow() thresholds, which Obsidian uses.
func relativeTime(t, now time.Time) string {
	diff := t.Sub(now)
	future := diff >= 0
	secs := math.Abs(diff.Seconds())
	mins, hours, days := secs/60, secs/3600, secs/86400
	var s string
	switch {
	case secs < 45:
		s = "a few seconds"
	case secs < 90:
		s = "a minute"
	case mins < 45:
		s = fmt.Sprintf("%d minutes", int(math.Round(mins)))
	case mins < 90:
		s = "an hour"
	case hours < 22:
		s = fmt.Sprintf("%d hours", int(math.Round(hours)))
	case hours < 36:
		s = "a day"
	case days < 26:
		s = fmt.Sprintf("%d days", int(math.Round(days)))
	case days < 45:
		s = "a month"
	case days < 320:
		s = fmt.Sprintf("%d months", int(math.Round(days/30.4)))
	case days < 548:
		s = "a year"
	default:
		s = fmt.Sprintf("%d years", int(math.Round(days/365.25)))
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}
