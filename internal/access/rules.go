// Package access decides which vault paths a client may read and change.
package access

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

type Mode string

const (
	Allowlist Mode = "allowlist"
	Denylist  Mode = "denylist"
)

type RuleSet struct {
	Mode  Mode     `toml:"mode"`
	Allow []string `toml:"allow"`
	Deny  []string `toml:"deny"`
}

type pattern []string

type rules struct {
	mode  Mode
	allow []pattern
	deny  []pattern
}

type Policy struct {
	read       rules
	write      rules
	extensions []string
}

var ErrOutside = errors.New("path must be relative and stay inside the vault")

func NewPolicy(read, write RuleSet, extensions []string) (*Policy, error) {
	r, err := compile(read)
	if err != nil {
		return nil, fmt.Errorf("read rules: %w", err)
	}
	w, err := compile(write)
	if err != nil {
		return nil, fmt.Errorf("write rules: %w", err)
	}
	if len(extensions) == 0 {
		extensions = []string{".md"}
	}
	exts := make([]string, len(extensions))
	for i, e := range extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		exts[i] = e
	}
	return &Policy{read: r, write: w, extensions: exts}, nil
}

func compile(rs RuleSet) (rules, error) {
	mode := rs.Mode
	if mode == "" {
		mode = Allowlist
	}
	if mode != Allowlist && mode != Denylist {
		return rules{}, fmt.Errorf("mode %q: use allowlist or denylist", rs.Mode)
	}
	if mode == Denylist && len(rs.Allow) > 0 {
		return rules{}, errors.New("allow has no effect in denylist mode; remove it or switch to allowlist")
	}
	out := rules{mode: mode}
	for _, src := range rs.Allow {
		p, err := compilePattern(src)
		if err != nil {
			return rules{}, err
		}
		out.allow = append(out.allow, p)
	}
	for _, src := range rs.Deny {
		p, err := compilePattern(src)
		if err != nil {
			return rules{}, err
		}
		out.deny = append(out.deny, p)
	}
	return out, nil
}

func compilePattern(src string) (pattern, error) {
	s := strings.TrimSpace(src)
	if s == "" || strings.HasPrefix(s, "/") {
		return nil, fmt.Errorf("pattern %q: must be a non-empty path relative to the vault", src)
	}
	if strings.HasSuffix(s, "/") {
		s += "**"
	}
	segs := strings.Split(s, "/")
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return nil, fmt.Errorf("pattern %q: empty, . and .. segments are not allowed", src)
		}
		if _, err := path.Match(seg, ""); err != nil {
			return nil, fmt.Errorf("pattern %q: %w", src, err)
		}
	}
	return pattern(segs), nil
}

func Clean(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) || strings.HasPrefix(rel, "/") {
		return "", ErrOutside
	}
	var out []string
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "", ".":
		case "..":
			return "", ErrOutside
		default:
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/"), nil
}

func split(rel string) []string {
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

func Hidden(rel string) bool {
	for _, seg := range split(rel) {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func (p *Policy) IsNote(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	for _, e := range p.extensions {
		if ext == e {
			return true
		}
	}
	return false
}

func (p *Policy) CanRead(rel string) bool {
	return rel != "" && !Hidden(rel) && p.IsNote(rel) && p.read.allows(split(rel))
}

// CanReadFile skips the note extension check: .base files are read but never searched or written.
func (p *Policy) CanReadFile(rel string) bool {
	return rel != "" && !Hidden(rel) && p.read.allows(split(rel))
}

func (p *Policy) CanWrite(rel string) bool {
	return p.CanRead(rel) && p.write.allows(split(rel))
}

func (p *Policy) MayHoldReadable(dir string) bool {
	return !Hidden(dir) && p.read.mayHold(split(dir))
}

func (r rules) allows(segs []string) bool {
	for _, d := range r.deny {
		if covers(d, segs) {
			return false
		}
	}
	if r.mode == Denylist {
		return true
	}
	for _, a := range r.allow {
		if covers(a, segs) {
			return true
		}
	}
	return false
}

func (r rules) mayHold(dir []string) bool {
	for _, d := range r.deny {
		if covers(d, dir) {
			return false
		}
	}
	if r.mode == Denylist {
		return true
	}
	for _, a := range r.allow {
		if covers(a, dir) || couldMatchBelow(a, dir) {
			return true
		}
	}
	return false
}

func covers(p pattern, segs []string) bool {
	for k := 1; k <= len(segs); k++ {
		if match(p, segs[:k]) {
			return true
		}
	}
	return false
}

func match(p pattern, segs []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			for len(p) > 0 && p[0] == "**" {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if match(p, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(p[0], segs[0]); !ok {
			return false
		}
		p, segs = p[1:], segs[1:]
	}
	return len(segs) == 0
}

func couldMatchBelow(p pattern, dir []string) bool {
	for {
		if len(p) == 0 {
			return false
		}
		if p[0] == "**" || len(dir) == 0 {
			return true
		}
		if ok, _ := path.Match(p[0], dir[0]); !ok {
			return false
		}
		p, dir = p[1:], dir[1:]
	}
}
