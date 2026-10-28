package notes

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Edit struct {
	Old string `json:"old" jsonschema:"exact text to replace, copied from note_read without the line numbers; may span lines"`
	New string `json:"new" jsonschema:"replacement text; empty removes the old text"`
}

type editSpan struct {
	start, end int
	edit       Edit
}

func locateEdits(text string, edits []Edit) ([]editSpan, error) {
	if len(edits) == 0 {
		return nil, errors.New("edits must not be empty")
	}
	spans := make([]editSpan, 0, len(edits))
	for i, e := range edits {
		if e.Old == "" {
			return nil, fmt.Errorf("edit %d: old must not be empty", i+1)
		}
		at := strings.Index(text, e.Old)
		if at < 0 {
			return nil, fmt.Errorf("edit %d: old text not found in the note; read it again and copy the text exactly", i+1)
		}
		if strings.Contains(text[at+1:], e.Old) {
			return nil, fmt.Errorf("edit %d: old text occurs more than once; include more of the surrounding text", i+1)
		}
		spans = append(spans, editSpan{start: at, end: at + len(e.Old), edit: e})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return nil, errors.New("edits overlap; merge them into one")
		}
	}
	return spans, nil
}

func replaceSpans(text string, spans []editSpan) string {
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		b.WriteString(text[prev:sp.start])
		b.WriteString(sp.edit.New)
		prev = sp.end
	}
	b.WriteString(text[prev:])
	return b.String()
}

func normalizeEdits(edits []Edit) []Edit {
	out := make([]Edit, len(edits))
	for i, e := range edits {
		out[i] = Edit{Old: strings.ReplaceAll(e.Old, "\r\n", "\n"), New: strings.ReplaceAll(e.New, "\r\n", "\n")}
	}
	return out
}

func docText(d doc) string { return strings.Join(d.lines, "\n") }

func withText(d doc, text string) doc {
	d.lines = nil
	if text != "" {
		d.lines = strings.Split(text, "\n")
	}
	return d
}

func lineOffset(lines []string, at int) int {
	n := 0
	for _, l := range lines[:at] {
		n += len(l) + 1
	}
	return n
}
