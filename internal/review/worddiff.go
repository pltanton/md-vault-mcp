package review

import (
	"bytes"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/pltanton/md-vault-mcp/internal/notes"
)

const (
	contextLines = 1
	tokenBase    = 0x10000
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
	Changed bool
	HTML    template.HTML
	pieces  []piece
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

func splitLines(pieces []piece) []line {
	lines := []line{{}}
	for _, p := range pieces {
		for i, part := range strings.Split(p.Text, "\n") {
			if i > 0 {
				if p.Kind != "eq" {
					lines[len(lines)-1].Changed = true
				}
				lines = append(lines, line{})
			}
			if part == "" {
				continue
			}
			cur := &lines[len(lines)-1]
			cur.pieces = append(cur.pieces, piece{Kind: p.Kind, Text: part})
			if p.Kind != "eq" {
				cur.Changed = true
			}
		}
	}
	for i := range lines {
		var b strings.Builder
		for _, p := range lines[i].pieces {
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
		lines[i].HTML = template.HTML(b.String())
	}
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

func diffBlocks(a, b string) []block { return collapse(splitLines(wordDiff(a, b))) }

func renderMarkdown(src string) template.HTML {
	var b bytes.Buffer
	if err := markdown.Convert([]byte(src), &b); err != nil {
		return template.HTML(html.EscapeString(src))
	}
	return template.HTML(b.String())
}

func buildEdit(e notes.EditPreview) editView {
	v := editView{Line: e.Line, Stale: e.Stale, Before: e.Before, After: e.After, Old: e.Old, New: e.New}
	if !e.Stale {
		v.Blocks = diffBlocks(strings.Join(e.Old, "\n"), strings.Join(e.New, "\n"))
	}
	v.Preview = renderMarkdown(strings.Join(e.New, "\n"))
	return v
}
