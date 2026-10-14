package notes

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type tokKind int

const (
	tEOF tokKind = iota
	tNum
	tStr
	tIdent
	tOp
	tRegex
)

type token struct {
	kind tokKind
	text string
	num  float64
	pos  int
}

type lexer struct {
	src  []rune
	pos  int
	prev *token
}

func (l *lexer) errf(format string, args ...any) error {
	return fmt.Errorf("at %d: %s", l.pos+1, fmt.Sprintf(format, args...))
}

// regexAllowed: a "/" starts a regex literal only where a value is expected.
func (l *lexer) regexAllowed() bool {
	if l.prev == nil {
		return true
	}
	switch l.prev.kind {
	case tNum, tStr, tIdent, tRegex:
		return false
	case tOp:
		return l.prev.text != ")" && l.prev.text != "]"
	}
	return true
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) && unicode.IsSpace(l.src[l.pos]) {
		l.pos++
	}
	start := l.pos
	if l.pos >= len(l.src) {
		return token{kind: tEOF, pos: start}, nil
	}
	c := l.src[l.pos]
	var t token
	switch {
	case unicode.IsDigit(c):
		for l.pos < len(l.src) && (unicode.IsDigit(l.src[l.pos]) || l.src[l.pos] == '.' && l.pos+1 < len(l.src) && unicode.IsDigit(l.src[l.pos+1])) {
			l.pos++
		}
		n, err := strconv.ParseFloat(string(l.src[start:l.pos]), 64)
		if err != nil {
			return token{}, l.errf("bad number")
		}
		t = token{kind: tNum, num: n, text: string(l.src[start:l.pos])}
	case c == '"' || c == '\'':
		l.pos++
		var b strings.Builder
		for {
			if l.pos >= len(l.src) {
				return token{}, l.errf("unterminated string")
			}
			r := l.src[l.pos]
			l.pos++
			if r == c {
				break
			}
			if r == '\\' && l.pos < len(l.src) {
				e := l.src[l.pos]
				l.pos++
				switch e {
				case 'n':
					r = '\n'
				case 't':
					r = '\t'
				default:
					r = e
				}
			}
			b.WriteRune(r)
		}
		t = token{kind: tStr, text: b.String()}
	case c == '/' && l.regexAllowed():
		l.pos++
		var b strings.Builder
		inClass := false
		for {
			if l.pos >= len(l.src) {
				return token{}, l.errf("unterminated regular expression")
			}
			r := l.src[l.pos]
			l.pos++
			if r == '\\' && l.pos < len(l.src) {
				b.WriteRune(r)
				b.WriteRune(l.src[l.pos])
				l.pos++
				continue
			}
			if r == '[' {
				inClass = true
			} else if r == ']' {
				inClass = false
			} else if r == '/' && !inClass {
				break
			}
			b.WriteRune(r)
		}
		flags := l.pos
		for l.pos < len(l.src) && unicode.IsLetter(l.src[l.pos]) {
			l.pos++
		}
		t = token{kind: tRegex, text: b.String() + "\x00" + string(l.src[flags:l.pos])}
	case unicode.IsLetter(c) || c == '_' || c == '$':
		for l.pos < len(l.src) && (unicode.IsLetter(l.src[l.pos]) || unicode.IsDigit(l.src[l.pos]) || l.src[l.pos] == '_' || l.src[l.pos] == '$' || unicode.Is(unicode.Mn, l.src[l.pos])) {
			l.pos++
		}
		t = token{kind: tIdent, text: string(l.src[start:l.pos])}
	default:
		for _, op := range []string{"==", "!=", ">=", "<=", "&&", "||"} {
			if strings.HasPrefix(string(l.src[l.pos:min(l.pos+2, len(l.src))]), op) {
				l.pos += 2
				t = token{kind: tOp, text: op}
				break
			}
		}
		if t.kind != tOp {
			if !strings.ContainsRune("+-*/%!<>()[].,{}:", c) {
				return token{}, l.errf("unexpected %q", c)
			}
			l.pos++
			t = token{kind: tOp, text: string(c)}
		}
	}
	t.pos = start
	l.prev = &t
	return t, nil
}

type node interface{}

type (
	numLit   struct{ v float64 }
	strLit   struct{ v string }
	regexLit struct{ pattern, flags string }
	ident    struct{ name string }
	member   struct {
		obj  node
		name string
	}
	index struct{ obj, key node }
	call  struct {
		fn   node
		args []node
	}
	unary struct {
		op string
		x  node
	}
	binary struct {
		op   string
		l, r node
	}
	listLit struct{ items []node }
	objLit  struct {
		keys []string
		vals []node
	}
)

type parser struct {
	toks []token
	i    int
	src  string
}

func parseExpr(src string) (node, error) {
	lx := &lexer{src: []rune(src)}
	var toks []token
	for {
		t, err := lx.next()
		if err != nil {
			return nil, fmt.Errorf("%q: %w", src, err)
		}
		toks = append(toks, t)
		if t.kind == tEOF {
			break
		}
	}
	p := &parser{toks: toks, src: src}
	n, err := p.expr(0)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", src, err)
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("%q: unexpected %q at %d", src, p.peek().text, p.peek().pos+1)
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) take() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) isOp(s string) bool {
	t := p.peek()
	return t.kind == tOp && t.text == s
}

func (p *parser) expect(s string) error {
	if !p.isOp(s) {
		return fmt.Errorf("expected %q at %d", s, p.peek().pos+1)
	}
	p.i++
	return nil
}

var binaryPrec = map[string]int{
	"||": 1, "&&": 2,
	"==": 3, "!=": 3,
	"<": 4, ">": 4, "<=": 4, ">=": 4,
	"+": 5, "-": 5,
	"*": 6, "/": 6, "%": 6,
}

func (p *parser) expr(minPrec int) (node, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		prec, ok := binaryPrec[t.text]
		if t.kind != tOp || !ok || prec <= minPrec {
			return left, nil
		}
		p.i++
		right, err := p.expr(prec)
		if err != nil {
			return nil, err
		}
		left = binary{op: t.text, l: left, r: right}
	}
}

func (p *parser) unary() (node, error) {
	if p.isOp("!") || p.isOp("-") {
		op := p.take().text
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return unary{op: op, x: x}, nil
	}
	return p.postfix()
}

func (p *parser) postfix() (node, error) {
	n, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("."):
			p.i++
			t := p.take()
			if t.kind != tIdent {
				return nil, fmt.Errorf("expected a name after . at %d", t.pos+1)
			}
			n = member{obj: n, name: t.text}
		case p.isOp("["):
			p.i++
			k, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			n = index{obj: n, key: k}
		case p.isOp("("):
			p.i++
			var args []node
			for !p.isOp(")") {
				a, err := p.expr(0)
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if !p.isOp(",") {
					break
				}
				p.i++
			}
			if err := p.expect(")"); err != nil {
				return nil, err
			}
			n = call{fn: n, args: args}
		default:
			return n, nil
		}
	}
}

func (p *parser) primary() (node, error) {
	t := p.take()
	switch t.kind {
	case tNum:
		return numLit{t.num}, nil
	case tStr:
		return strLit{t.text}, nil
	case tRegex:
		pat, flags, _ := strings.Cut(t.text, "\x00")
		return regexLit{pattern: pat, flags: flags}, nil
	case tIdent:
		return ident{t.text}, nil
	case tOp:
		switch t.text {
		case "(":
			n, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			return n, p.expect(")")
		case "[":
			var items []node
			for !p.isOp("]") {
				it, err := p.expr(0)
				if err != nil {
					return nil, err
				}
				items = append(items, it)
				if !p.isOp(",") {
					break
				}
				p.i++
			}
			return listLit{items}, p.expect("]")
		case "{":
			var o objLit
			for !p.isOp("}") {
				k := p.take()
				if k.kind != tStr && k.kind != tIdent {
					return nil, fmt.Errorf("expected an object key at %d", k.pos+1)
				}
				if err := p.expect(":"); err != nil {
					return nil, err
				}
				v, err := p.expr(0)
				if err != nil {
					return nil, err
				}
				o.keys, o.vals = append(o.keys, k.text), append(o.vals, v)
				if !p.isOp(",") {
					break
				}
				p.i++
			}
			return o, p.expect("}")
		}
	case tEOF:
		return nil, fmt.Errorf("unexpected end of expression")
	}
	return nil, fmt.Errorf("unexpected %q at %d", t.text, t.pos+1)
}
