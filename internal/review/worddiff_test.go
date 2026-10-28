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
	if short != ". * * ." {
		t.Errorf("short: %s", short)
	}
}

func TestDiffLines(t *testing.T) {
	var kinds []string
	for _, l := range diffLines("a one\nb two\nc three", "a one\nb two too\nc three\nd new") {
		kinds = append(kinds, l.Kind+":"+string(l.HTML))
	}
	if got := strings.Join(kinds, " | "); got != "eq:a one | chg:b two<ins> too</ins> | eq:c three | add:d new" {
		t.Errorf("got %s", got)
	}
}

func TestBuildEdit(t *testing.T) {
	small := buildEdit(notes.EditPreview{Old: []string{"- <b>был</b> текст тут"}, New: []string{"- <b>был</b> текст тут и там"}})
	if small.Rewrite || len(small.Blocks) != 1 || !strings.Contains(string(small.Blocks[0].Lines[0].HTML), "&lt;b&gt;") {
		t.Errorf("small edit %+v", small)
	}
	rewrite := buildEdit(notes.EditPreview{Old: []string{"Пиши ему, как человеку."}, New: []string{"**Совсем** другой <script>x</script>"}})
	if !rewrite.Rewrite || rewrite.Blocks == nil || !strings.Contains(string(rewrite.Preview), "<strong>Совсем</strong>") ||
		strings.Contains(string(rewrite.Preview), "<script>") || !strings.Contains(string(rewrite.OldPreview), "как человеку") {
		t.Errorf("rewrite %+v", rewrite)
	}
}
