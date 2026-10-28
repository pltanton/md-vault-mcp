package notes

import (
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	wikiRef = regexp.MustCompile(`(!?)\[\[([^\]|#^\n]*)([#^][^\]|\n]*)?(\|[^\]\n]*)?\]\]`)
	mdRef   = regexp.MustCompile(`(!?)\[([^\]\n]*)\]\(([^)\s]+)\)`)
)

type linkRef struct {
	line    int
	raw     string
	embed   bool
	md      bool
	target  string
	suffix  string
	text    string
	encoded bool
}

func maskInlineCode(l string) string {
	b := []byte(l)
	in := false
	for i, c := range b {
		if c == '`' {
			in = !in
			continue
		}
		if in {
			b[i] = ' '
		}
	}
	return string(b)
}

func linkRefs(lines []string) []linkRef {
	var out []linkRef
	fence := ""
	for i, l := range lines {
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
		masked := maskInlineCode(l)
		for _, m := range wikiRef.FindAllStringSubmatchIndex(masked, -1) {
			target := strings.TrimSpace(l[m[4]:m[5]])
			if target == "" {
				continue
			}
			suffix := ""
			if m[6] >= 0 {
				suffix += l[m[6]:m[7]]
			}
			if m[8] >= 0 {
				suffix += l[m[8]:m[9]]
			}
			out = append(out, linkRef{line: i, raw: l[m[0]:m[1]], embed: m[3] > m[2], target: target, suffix: suffix})
		}
		for _, m := range mdRef.FindAllStringSubmatchIndex(masked, -1) {
			href := l[m[6]:m[7]]
			if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
				continue
			}
			p, frag, hasFrag := strings.Cut(href, "#")
			dec, err := url.PathUnescape(p)
			if err != nil {
				dec = p
			}
			if ext := strings.ToLower(path.Ext(dec)); ext != "" && ext != ".md" {
				continue
			}
			suffix := ""
			if hasFrag {
				suffix = "#" + frag
			}
			out = append(out, linkRef{line: i, raw: l[m[0]:m[1]], embed: m[3] > m[2], md: true, target: dec,
				suffix: suffix, text: l[m[4]:m[5]], encoded: dec != p})
		}
	}
	return out
}

type noteIndex struct {
	byPath map[string]string
	byName map[string][]string
}

func noteName(p string) string { return strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p))) }

func newNoteIndex(paths []string) noteIndex {
	ix := noteIndex{byPath: map[string]string{}, byName: map[string][]string{}}
	for _, p := range paths {
		ix.byPath[strings.ToLower(p)] = p
		ix.byName[noteName(p)] = append(ix.byName[noteName(p)], p)
	}
	return ix
}

func (ix noteIndex) lookup(p string) string {
	p = strings.ToLower(strings.TrimPrefix(path.Clean("/"+p), "/"))
	if q, ok := ix.byPath[p]; ok {
		return q
	}
	return ix.byPath[p+".md"]
}

func (ix noteIndex) resolve(from string, r linkRef) string {
	t := strings.TrimPrefix(r.target, "/")
	dirRel := path.Join(path.Dir(from), t)
	if r.md {
		if p := ix.lookup(dirRel); p != "" {
			return p
		}
		return ix.lookup(t)
	}
	if p := ix.lookup(t); p != "" {
		return p
	}
	if strings.Contains(t, "/") {
		return ix.lookup(dirRel)
	}
	if ps := ix.byName[noteName(t)]; len(ps) == 1 {
		return ps[0]
	}
	return ""
}

func (ix noteIndex) uniqueAfterMove(oldPath, newPath string) bool {
	n := 1
	for _, p := range ix.byName[noteName(newPath)] {
		if p != oldPath && p != newPath {
			n++
		}
	}
	return n == 1
}

func (ix noteIndex) rewrite(from string, r linkRef, oldPath, newPath string) string {
	bang := ""
	if r.embed {
		bang = "!"
	}
	if r.md {
		href := newPath
		if ix.lookup(path.Join(path.Dir(from), strings.TrimPrefix(r.target, "/"))) == oldPath {
			if rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(newPath)); err == nil {
				href = filepath.ToSlash(rel)
			}
		}
		if !strings.EqualFold(path.Ext(r.target), ".md") {
			href = strings.TrimSuffix(href, ".md")
		}
		if r.encoded {
			href = strings.ReplaceAll(href, " ", "%20")
		}
		return bang + "[" + r.text + "](" + href + r.suffix + ")"
	}
	noExt := strings.TrimSuffix(newPath, ".md")
	target := noExt
	if !strings.Contains(r.target, "/") && ix.uniqueAfterMove(oldPath, newPath) {
		target = path.Base(noExt)
	}
	if strings.EqualFold(path.Ext(r.target), ".md") {
		target += ".md"
	}
	return bang + "[[" + target + r.suffix + "]]"
}
