package notes

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type frontMatter struct {
	close int
}

func findFrontMatter(lines []string) (frontMatter, bool) {
	end := frontMatterEnd(lines)
	if end == 0 {
		return frontMatter{}, false
	}
	return frontMatter{close: end - 1}, true
}

func (fm frontMatter) body(lines []string) []string {
	return lines[1:fm.close]
}

// Scalars keep the text as written: dates stay "2026-05-07", not timestamps.
func properties(lines []string) (map[string]any, error) {
	fm, ok := findFrontMatter(lines)
	if !ok {
		return map[string]any{}, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(fm.body(lines), "\n")), &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return map[string]any{}, nil
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		if root.Kind == yaml.DocumentNode && len(root.Content) == 1 && root.Content[0].Tag == "!!null" {
			return map[string]any{}, nil
		}
		return nil, errors.New("front matter is not a mapping")
	}
	v, err := nodeValue(root.Content[0])
	if err != nil {
		return nil, err
	}
	return v.(map[string]any), nil
}

func nodeValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return nodeValue(n.Alias)
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err == nil {
				return b, nil
			}
		case "!!int":
			if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
				return i, nil
			}
		case "!!float":
			var f float64
			if err := n.Decode(&f); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
				return f, nil
			}
		}
		return n.Value, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeValue(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := nodeValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[n.Content[i].Value] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported YAML node at line %d", n.Line)
}

var topKey = regexp.MustCompile(`^("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s#'"\-?:][^:]*?|-[^\s:][^:]*?)\s*:(\s|$)`)

func unquoteKey(k string) string {
	if len(k) >= 2 && (k[0] == '"' || k[0] == '\'') {
		var s string
		if yaml.Unmarshal([]byte(k), &s) == nil {
			return s
		}
	}
	return k
}

func propertySpan(lines []string, fm frontMatter, name string) (int, int, bool) {
	for i := 1; i < fm.close; i++ {
		m := topKey.FindStringSubmatch(lines[i])
		if m == nil || unquoteKey(m[1]) != name {
			continue
		}
		j := i + 1
		for ; j < fm.close; j++ {
			l := lines[j]
			if l == "" || l[0] == ' ' || l[0] == '\t' || strings.HasPrefix(l, "- ") || l == "-" {
				continue
			}
			break
		}
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		return i, j, true
	}
	return 0, 0, false
}

var plainDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2})?)?$`)

// renderProperty writes name: value in the shape Obsidian itself writes:
// dates unquoted, lists as block sequences indented by two spaces.
func renderProperty(name string, value any) ([]string, error) {
	key, err := scalarNode(name)
	if err != nil {
		return nil, err
	}
	val, err := valueNode(value)
	if err != nil {
		return nil, err
	}
	doc := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{key, val}}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	enc.Close()
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n"), nil
}

func scalarNode(s string) (*yaml.Node, error) {
	if strings.ContainsAny(s, "\n\r") {
		return nil, errors.New("property names and list items must be one line")
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if plainDate.MatchString(s) {
		n.Tag = ""
		return n, nil
	}
	if out, err := yaml.Marshal(n); err == nil && len(out) > 0 && out[0] == '\'' {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n, nil
}

func valueNode(v any) (*yaml.Node, error) {
	switch x := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}, nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(int64(x), 10)}, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(x, 'f', -1, 64)}, nil
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(x)}, nil
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(x, 10)}, nil
	case string:
		return scalarNode(x)
	case []any:
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range x {
			switch it.(type) {
			case []any, map[string]any:
				return nil, errors.New("list items must be plain values")
			}
			n, err := valueNode(it)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, n)
		}
		if len(seq.Content) == 0 {
			seq.Style = yaml.FlowStyle
		}
		return seq, nil
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return valueNode(items)
	}
	return nil, fmt.Errorf("unsupported value %T: use text, a number, true/false, null or a list of those", v)
}

// noteTags gathers tags the way Obsidian does: the tags property plus inline #tags outside code.
func noteTags(lines []string, props map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimPrefix(strings.TrimSpace(t), "#")
		if t == "" || seen[strings.ToLower(t)] {
			return
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	for _, key := range []string{"tags", "tag"} {
		switch v := props[key].(type) {
		case string:
			for _, t := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
				add(t)
			}
		case []any:
			for _, t := range v {
				if s, ok := t.(string); ok {
					add(s)
				}
			}
		}
	}
	start := frontMatterEnd(lines)
	fence := ""
	for _, l := range lines[start:] {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		for _, m := range inlineTag.FindAllStringSubmatch(stripInlineCode(l), -1) {
			add(m[2])
		}
	}
	return out
}

var inlineTag = regexp.MustCompile(`(^|[\s(,;])#([\p{L}\p{N}_/\-]*[\p{L}_/\-][\p{L}\p{N}_/\-]*)`)

func stripInlineCode(l string) string {
	var b strings.Builder
	in := false
	for _, r := range l {
		if r == '`' {
			in = !in
			continue
		}
		if !in {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hasTag matches nested tags the way Obsidian does: "person" also matches "person/friend".
func hasTag(tags []string, want string) bool {
	want = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(want), "#"))
	for _, t := range tags {
		t = strings.ToLower(t)
		if t == want || strings.HasPrefix(t, want+"/") {
			return true
		}
	}
	return false
}

var dateLayouts = []string{
	"2006-01-02",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05Z07:00",
	"02.01.2006",
}

func parseDate(v any, loc *time.Location) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
