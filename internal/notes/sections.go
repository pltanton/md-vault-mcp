package notes

import "strings"

const maxSectionLines = 40

// span is 0-based, end exclusive.
type span struct{ start, end int }

func splitSections(lines []string) []span {
	starts := []int{0}
	if fm := frontMatterEnd(lines); fm > 0 && fm < len(lines) {
		starts = append(starts, fm)
	}
	for _, h := range headings(lines) {
		if h.line != starts[len(starts)-1] {
			starts = append(starts, h.line)
		}
	}
	var out []span
	for i, s := range starts {
		e := len(lines)
		if i+1 < len(starts) {
			e = starts[i+1]
		}
		for a := s; a < e; a += maxSectionLines {
			b := min(a+maxSectionLines, e)
			if hasText(lines[a:b]) {
				out = append(out, span{a, b})
			}
		}
	}
	return out
}

func hasText(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}
