package notes

import (
	"fmt"
	"strings"
)

type doc struct {
	lines      []string
	crlf       bool
	trailingNL bool
}

func parseDoc(data []byte) doc {
	s := string(data)
	d := doc{crlf: strings.Contains(s, "\r\n")}
	if d.crlf {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	d.trailingNL = strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	if s != "" || d.trailingNL {
		d.lines = strings.Split(s, "\n")
	}
	return d
}

func (d doc) bytes() []byte {
	s := strings.Join(d.lines, "\n")
	if len(d.lines) > 0 && d.trailingNL {
		s += "\n"
	}
	if d.crlf {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return []byte(s)
}

func textLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type heading struct {
	line  int
	level int
	text  string
}

func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	rest := trimmed[level:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	text := strings.TrimSpace(rest)
	if closing := strings.TrimRight(text, "#"); closing != text && (closing == "" || strings.HasSuffix(closing, " ")) {
		text = strings.TrimSpace(closing)
	}
	return level, text, true
}

func headings(lines []string) []heading {
	var out []heading
	i := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if t := strings.TrimSpace(lines[j]); t == "---" || t == "..." {
				i = j + 1
				break
			}
		}
	}
	fence := ""
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			fence = "```"
			continue
		}
		if strings.HasPrefix(t, "~~~") {
			fence = "~~~"
			continue
		}
		if level, text, ok := parseHeading(lines[i]); ok {
			out = append(out, heading{line: i, level: level, text: text})
		}
	}
	return out
}

func findHeading(lines []string, want string) (heading, error) {
	want = strings.TrimSpace(want)
	level, text, withLevel := parseHeading(want)
	if !withLevel {
		text = want
	}
	var found []heading
	for _, h := range headings(lines) {
		if h.text == text && (!withLevel || h.level == level) {
			found = append(found, h)
		}
	}
	switch len(found) {
	case 0:
		return heading{}, fmt.Errorf("no heading %q in this note; read it and pass a heading line exactly as written", want)
	case 1:
		return found[0], nil
	}
	nums := make([]string, len(found))
	for i, h := range found {
		nums[i] = fmt.Sprint(h.line + 1)
	}
	return heading{}, fmt.Errorf("heading %q appears %d times (lines %s); ask the owner which one", want, len(found), strings.Join(nums, ", "))
}

func section(lines []string, h heading) (int, int) {
	end := len(lines)
	for _, other := range headings(lines) {
		if other.line > h.line && other.level <= h.level {
			end = other.line
			break
		}
	}
	return h.line + 1, end
}

func appendPoint(lines []string, start, end int) int {
	for i := end - 1; i >= start; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i + 1
		}
	}
	return start
}

func insertLines(lines []string, at int, add []string) []string {
	out := make([]string, 0, len(lines)+len(add))
	out = append(out, lines[:at]...)
	out = append(out, add...)
	return append(out, lines[at:]...)
}

func replaceLines(lines []string, at, n int, with []string) []string {
	out := make([]string, 0, len(lines)-n+len(with))
	out = append(out, lines[:at]...)
	out = append(out, with...)
	return append(out, lines[at+n:]...)
}
