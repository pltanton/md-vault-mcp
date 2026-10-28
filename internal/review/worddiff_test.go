package review

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/notes"
)

func TestWordDiff(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"Купить молоко и хлеб", "Купить кефир и хлеб", "Купить [-молоко-][+кефир+] и хлеб"},
		{"- ДР: 12 марта", "- ДР: 21 марта", "- ДР: [-12-][+21+] марта"},
		{"a\nb", "a\nb\nc", "a\nb[+\nc+]"},
		{"same", "same", "same"},
		{"", "new", "[+new+]"},
	} {
		var b strings.Builder
		for _, p := range wordDiff(c.a, c.b) {
			switch p.Kind {
			case "del":
				b.WriteString("[-" + p.Text + "-]")
			case "ins":
				b.WriteString("[+" + p.Text + "+]")
			default:
				b.WriteString(p.Text)
			}
		}
		if b.String() != c.want {
			t.Errorf("%q → %q: got %q, want %q", c.a, c.b, b.String(), c.want)
		}
	}
}

func shape(blocks []block) string {
	var parts []string
	for _, b := range blocks {
		if b.Hidden {
			parts = append(parts, fmt.Sprintf("(%d hidden)", len(b.Lines)))
			continue
		}
		for _, l := range b.Lines {
			if l.Changed {
				parts = append(parts, "*")
			} else {
				parts = append(parts, ".")
			}
		}
	}
	return strings.Join(parts, " ")
}

func TestCollapse(t *testing.T) {
	var old, new []string
	for i := range 12 {
		old = append(old, fmt.Sprintf("строка %d", i))
		new = append(new, fmt.Sprintf("строка %d", i))
	}
	new[5] = "строка пять"
	got := shape(diffBlocks(strings.Join(old, "\n"), strings.Join(new, "\n")))
	if got != "(4 hidden) . * . (5 hidden)" {
		t.Errorf("got %s", got)
	}
	short := shape(diffBlocks("a\nb\nc", "a\nB\nc"))
	if short != ". * ." {
		t.Errorf("short: %s", short)
	}
}

func TestBuildEditEscapesAndRenders(t *testing.T) {
	v := buildEdit(notes.EditPreview{Line: 3, Old: []string{"<b>было</b>"}, New: []string{"**стало** <script>x</script>"}})
	var html string
	for _, b := range v.Blocks {
		for _, l := range b.Lines {
			html += string(l.HTML)
		}
	}
	if strings.Contains(html, "<b>") || strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("diff html not escaped: %s", html)
	}
	if !strings.Contains(string(v.Preview), "<strong>стало</strong>") || strings.Contains(string(v.Preview), "<script>") {
		t.Errorf("preview: %s", v.Preview)
	}
}
