package review

import (
	"bytes"
	"html"
	"html/template"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/pltanton/md-vault-mcp/internal/notes"
)

const (
	contextLines = 1
	tokenBase    = 0x10000
	pairSimilar  = 0.5
	rewriteBelow = 0.5
)

var (
	tokenRe  = regexp.MustCompile(`[\p{L}\p{N}_]+|\s|[^\p{L}\p{N}_\s]`)
	markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))
	opKinds  = map[diffmatchpatch.Operation]string{
		diffmatchpatch.DiffEqual: "eq", diffmatchpatch.DiffDelete: "del", diffmatchpatch.DiffInsert: "ins",
	}
)

type piece struct{ Kind, Text string }

type line struct {
	Kind    string
	Changed bool
	HTML    template.HTML
}

type block struct {
	Hidden bool
	Lines  []line
}

type editView struct {
	Line          int
	Stale         bool
	Before, After []string
	Old, New      []string
	Blocks        []block
	Preview       template.HTML
	Rewrite       bool
	OldPreview    template.HTML
}

func wordDiff(a, b string) []piece {
	index := map[string]rune{}
	var tokens []string
	encode := func(s string) []rune {
		var out []rune
		for _, t := range tokenRe.FindAllString(s, -1) {
			r, ok := index[t]
			if !ok {
				r = rune(tokenBase + len(tokens))
				index[t] = r
				tokens = append(tokens, t)
			}
			out = append(out, r)
		}
		return out
	}
	ra, rb := encode(a), encode(b)
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffCleanupSemantic(dmp.DiffMainRunes(ra, rb, false))
	var out []piece
	for _, d := range diffs {
		var sb strings.Builder
		for _, r := range d.Text {
			sb.WriteString(tokens[r-tokenBase])
		}
		kind := opKinds[d.Type]
		if n := len(out); n > 0 && out[n-1].Kind == kind {
			out[n-1].Text += sb.String()
			continue
		}
		if sb.Len() > 0 {
			out = append(out, piece{Kind: kind, Text: sb.String()})
		}
	}
	return out
}

func similarity(pieces []piece) float64 {
	same, total := 0, 0
	for _, p := range pieces {
		n := utf8.RuneCountInString(strings.TrimSpace(p.Text))
		if p.Kind == "eq" {
			same += 2 * n
			total += 2 * n
		} else {
			total += n
		}
	}
	if total == 0 {
		return 1
	}
	return float64(same) / float64(total)
}

func piecesHTML(pieces []piece) template.HTML {
	var b strings.Builder
	for _, p := range pieces {
		text := html.EscapeString(p.Text)
		switch p.Kind {
		case "del":
			b.WriteString("<del>" + text + "</del>")
		case "ins":
			b.WriteString("<ins>" + text + "</ins>")
		default:
			b.WriteString(text)
		}
	}
	return template.HTML(b.String())
}

func plainLine(kind, text string) line {
	return line{Kind: kind, Changed: kind != "eq", HTML: template.HTML(html.EscapeString(text))}
}

func splitText(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func diffLines(a, b string) []line {
	dmp := diffmatchpatch.New()
	ra, rb, table := dmp.DiffLinesToRunes(a+"\n", b+"\n")
	diffs := dmp.DiffCharsToLines(dmp.DiffMainRunes(ra, rb, false), table)
	var removed, added []string
	var lines []line
	flush := func() {
		paired := len(removed) > 0 && len(added) > 0
		n := min(len(removed), len(added))
		for i := 0; paired && i < n; i++ {
			paired = similarity(wordDiff(removed[i], added[i])) >= pairSimilar
		}
		if paired {
			for i := 0; i < n; i++ {
				lines = append(lines, line{Kind: "chg", Changed: true, HTML: piecesHTML(wordDiff(removed[i], added[i]))})
			}
			removed, added = removed[n:], added[n:]
		}
		for _, r := range removed {
			lines = append(lines, plainLine("rm", r))
		}
		for _, a := range added {
			lines = append(lines, plainLine("add", a))
		}
		removed, added = nil, nil
	}
	for _, d := range diffs {
		text := splitText(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			removed = append(removed, text...)
		case diffmatchpatch.DiffInsert:
			added = append(added, text...)
		default:
			flush()
			for _, t := range text {
				lines = append(lines, plainLine("eq", t))
			}
		}
	}
	flush()
	return lines
}

func collapse(lines []line) []block {
	var out []block
	show := func(ls ...line) {
		if len(ls) == 0 {
			return
		}
		if n := len(out); n > 0 && !out[n-1].Hidden {
			out[n-1].Lines = append(out[n-1].Lines, ls...)
			return
		}
		out = append(out, block{Lines: append([]line(nil), ls...)})
	}
	for i := 0; i < len(lines); {
		if lines[i].Changed {
			show(lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && !lines[j].Changed {
			j++
		}
		lead, trail := contextLines, contextLines
		if i == 0 {
			lead = 0
		}
		if j == len(lines) {
			trail = 0
		}
		if j-i-lead-trail >= 2 {
			show(lines[i : i+lead]...)
			out = append(out, block{Hidden: true, Lines: lines[i+lead : j-trail]})
			show(lines[j-trail : j]...)
		} else {
			show(lines[i:j]...)
		}
		i = j
	}
	return out
}

func diffBlocks(a, b string) []block { return collapse(diffLines(a, b)) }

func renderMarkdown(src string) template.HTML {
	var b bytes.Buffer
	if err := markdown.Convert([]byte(src), &b); err != nil {
		return template.HTML(html.EscapeString(src))
	}
	return template.HTML(b.String())
}

func buildEdit(e notes.EditPreview) editView {
	v := editView{Line: e.Line, Stale: e.Stale, Before: e.Before, After: e.After, Old: e.Old, New: e.New}
	old, new := strings.Join(e.Old, "\n"), strings.Join(e.New, "\n")
	v.Preview = renderMarkdown(new)
	if e.Stale {
		return v
	}
	if similarity(wordDiff(old, new)) < rewriteBelow {
		v.Rewrite = true
		v.OldPreview = renderMarkdown(old)
		return v
	}
	v.Blocks = diffBlocks(old, new)
	return v
}
