package notes

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitSections(t *testing.T) {
	long := "# Long\n" + strings.Repeat("x\n", 84)
	for _, c := range []struct {
		name, text string
		want       []span
	}{
		{"front matter, preamble, headings", "---\na: 1\n---\nintro\n# H1\ntext\n## H2\n\n### H3\nmore",
			[]span{{0, 3}, {3, 4}, {4, 6}, {6, 8}, {8, 10}}},
		{"heading in fenced code", "# A\n```\n# not\n```\n# B", []span{{0, 4}, {4, 5}}},
		{"blank preamble dropped", "\n\n# A\nx", []span{{2, 4}}},
		{"no headings", "a\nb", []span{{0, 2}}},
		{"front matter only", "---\na: 1\n---", []span{{0, 3}}},
		{"heading right after front matter", "---\na: 1\n---\n# A\nx", []span{{0, 3}, {3, 5}}},
		{"crlf", "# A\r\nx\r\n# B\r\n", []span{{0, 2}, {2, 3}}},
		{"long section cut", long, []span{{0, 40}, {40, 80}, {80, 85}}},
		{"empty", "", nil},
	} {
		got := splitSections(parseDoc([]byte(c.text)).lines)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
