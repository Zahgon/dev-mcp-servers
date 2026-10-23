// Package jsregex is an ECMAScript (V8, Node 22) regular expression engine over UTF-16 strings: the
// syntax V8 accepts with the flags d g i m s u v y, backtracking matching with captures, lookaround and
// backreferences, and cancellation through a deadline.
package jsregex

import (
	"fmt"
	"strconv"
	"strings"
)

type nodeKind int

const (
	nEmpty nodeKind = iota
	nChar           // one character (code unit, or code point in unicode mode)
	nClass          // character class or class escape
	nAny            // .
	nSeq
	nAlt
	nGroup   // capturing group
	nRepeat  // quantified
	nBOL     // ^
	nEOL     // $
	nWordB   // \b
	nNWordB  // \B
	nLook    // lookaround
	nBackref // \1, \k<name>
)

type node struct {
	kind     nodeKind
	ch       rune
	set      *charSet
	kids     []*node
	index    int // capture index for nGroup, target for nBackref (0 = by name, resolved later)
	name     string
	min, max int // max < 0: unbounded
	lazy     bool
	ahead    bool // lookahead (else lookbehind)
	negate   bool
	firstCap int // first capture index inside a repeat or lookaround
	lastCap  int // one past the last capture index inside
}

// syntaxError is a pattern V8 rejects; Error() is the message V8 reports.
type syntaxError struct {
	Pattern, Flags, Reason string
}

func (e *syntaxError) Error() string {
	return fmt.Sprintf("Invalid regular expression: /%s/%s: %s", e.Pattern, e.Flags, e.Reason)
}

type parser struct {
	src        []uint16
	pos        int
	unicode    bool // u or v
	sets       bool // v
	icase      bool // i
	named      bool // the pattern has named groups (enables \k in non-unicode mode)
	groups     int
	names      []string // names[i] is the name of capture i+1, "" when unnamed
	backrefMax int
	reason     string
	nameRefs   []*node
}

func (p *parser) fail(reason string) {
	if p.reason == "" {
		p.reason = reason
	}
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() rune {
	if p.eof() {
		return -1
	}
	return rune(p.src[p.pos])
}

func (p *parser) peekAt(k int) rune {
	if p.pos+k >= len(p.src) {
		return -1
	}
	return rune(p.src[p.pos+k])
}

// next reads one pattern character: a code point in unicode mode, a code unit otherwise.
func (p *parser) next() rune {
	c := rune(p.src[p.pos])
	p.pos++
	if p.unicode && isHigh(c) && !p.eof() && isLow(rune(p.src[p.pos])) {
		c = combine(c, rune(p.src[p.pos]))
		p.pos++
	}
	return c
}

func isHigh(c rune) bool { return c >= 0xD800 && c <= 0xDBFF }
func isLow(c rune) bool  { return c >= 0xDC00 && c <= 0xDFFF }
func combine(h, l rune) rune {
	return 0x10000 + (h-0xD800)<<10 + (l - 0xDC00)
}

// scanGroups counts capturing groups and collects names before parsing, as V8 does, so that
// backreferences can refer forward.
func (p *parser) scanGroups() {
	inClass := false
	for i := 0; i < len(p.src); i++ {
		c := p.src[i]
		switch {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			if i+1 < len(p.src) && p.src[i+1] == '?' {
				if i+2 < len(p.src) && p.src[i+2] == '<' && i+3 < len(p.src) && p.src[i+3] != '=' && p.src[i+3] != '!' {
					p.named = true
					p.backrefMax++
				}
				continue
			}
			p.backrefMax++
		}
	}
}

func parse(src []uint16, flags string) (*node, *parser) {
	p := &parser{src: src, unicode: strings.ContainsAny(flags, "uv"), sets: strings.Contains(flags, "v"),
		icase: strings.Contains(flags, "i")}
	p.scanGroups()
	root := p.disjunction()
	if p.reason == "" && !p.eof() {
		if p.peek() == ')' {
			p.fail("Unmatched ')'")
		} else {
			p.fail("Unexpected character")
		}
	}
	if p.reason == "" {
		for _, ref := range p.nameRefs {
			ref.index = -1
			for i, n := range p.names {
				if n == ref.name {
					ref.index = i + 1
				}
			}
			if ref.index < 0 {
				p.fail("Invalid named capture referenced")
			}
		}
	}
	return root, p
}

func (p *parser) disjunction() *node {
	alts := []*node{p.alternative()}
	for p.reason == "" && p.peek() == '|' {
		p.pos++
		alts = append(alts, p.alternative())
	}
	if len(alts) == 1 {
		return alts[0]
	}
	return &node{kind: nAlt, kids: alts}
}

func (p *parser) alternative() *node {
	seq := &node{kind: nSeq}
	for p.reason == "" && !p.eof() && p.peek() != '|' && p.peek() != ')' {
		t := p.term()
		if t != nil {
			seq.kids = append(seq.kids, t)
		}
	}
	return seq
}

func (p *parser) term() *node {
	start := p.groups
	c := p.peek()
	var atom *node
	quantifiable := ""
	switch c {
	case '^':
		p.pos++
		atom, quantifiable = &node{kind: nBOL}, "Nothing to repeat"
	case '$':
		p.pos++
		atom, quantifiable = &node{kind: nEOL}, "Nothing to repeat"
	case '\\':
		if p.peekAt(1) == 'b' || p.peekAt(1) == 'B' {
			kind := nWordB
			if p.peekAt(1) == 'B' {
				kind = nNWordB
			}
			p.pos += 2
			atom, quantifiable = &node{kind: kind}, "Nothing to repeat"
		} else {
			atom = p.atomEscape()
		}
	case '(':
		atom, quantifiable = p.group()
	case '.':
		p.pos++
		atom = &node{kind: nAny}
	case '[':
		atom = p.class()
	case '*', '+', '?':
		p.fail("Nothing to repeat")
		return nil
	case '{':
		if _, _, ok := p.braceQuantifier(false); ok {
			p.fail("Nothing to repeat")
			return nil
		}
		if p.unicode {
			p.fail("Lone quantifier brackets")
			return nil
		}
		p.pos++
		atom = &node{kind: nChar, ch: '{'}
	case '}', ']':
		if p.unicode {
			p.fail("Lone quantifier brackets")
			return nil
		}
		p.pos++
		atom = &node{kind: nChar, ch: c}
	default:
		atom = &node{kind: nChar, ch: p.next()}
	}
	if p.reason != "" {
		return nil
	}
	return p.quantifier(atom, quantifiable, start)
}

// braceQuantifier reads {n}, {n,} or {n,m} at the current position; ok is false (and the position
// unchanged) when the text there is not one. With consume it advances past it.
func (p *parser) braceQuantifier(consume bool) (min, max int, ok bool) {
	i := p.pos + 1
	readInt := func() (int, bool) {
		s := i
		for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
			i++
		}
		if i == s {
			return 0, false
		}
		n, err := strconv.Atoi(string(utf16Digits(p.src[s:i])))
		if err != nil {
			n = 1<<31 - 1
		}
		return n, true
	}
	min, ok1 := readInt()
	if !ok1 {
		return 0, 0, false
	}
	max = min
	if i < len(p.src) && p.src[i] == ',' {
		i++
		if i < len(p.src) && p.src[i] == '}' {
			max = -1
		} else {
			m, ok2 := readInt()
			if !ok2 {
				return 0, 0, false
			}
			max = m
		}
	}
	if i >= len(p.src) || p.src[i] != '}' {
		return 0, 0, false
	}
	if consume {
		p.pos = i + 1
	}
	return min, max, true
}

func utf16Digits(u []uint16) []byte {
	b := make([]byte, len(u))
	for i, c := range u {
		b[i] = byte(c)
	}
	return b
}

// quantifier reads a quantifier after atom; notQuantifiable is the error V8 reports when the atom
// cannot be repeated ("" when it can).
func (p *parser) quantifier(atom *node, notQuantifiable string, firstCap int) *node {
	min, max := -1, -1
	switch p.peek() {
	case '*':
		min, max = 0, -1
		p.pos++
	case '+':
		min, max = 1, -1
		p.pos++
	case '?':
		min, max = 0, 1
		p.pos++
	case '{':
		lo, hi, ok := p.braceQuantifier(true)
		if !ok {
			if p.unicode {
				p.fail("Incomplete quantifier")
			}
			return atom
		}
		if hi >= 0 && hi < lo {
			p.fail("numbers out of order in {} quantifier")
			return nil
		}
		min, max = lo, hi
	default:
		return atom
	}
	if notQuantifiable != "" {
		p.fail(notQuantifiable)
		return nil
	}
	lazy := false
	if p.peek() == '?' {
		lazy = true
		p.pos++
	}
	return &node{kind: nRepeat, kids: []*node{atom}, min: min, max: max, lazy: lazy, firstCap: firstCap + 1, lastCap: p.groups + 1}
}

func (p *parser) group() (*node, string) {
	p.pos++ // (
	if p.peek() != '?' {
		p.groups++
		idx := p.groups
		p.names = append(p.names, "")
		body := p.disjunction()
		if !p.closeGroup() {
			return nil, ""
		}
		return &node{kind: nGroup, index: idx, kids: []*node{body}}, ""
	}
	switch p.peekAt(1) {
	case ':':
		p.pos += 2
		body := p.disjunction()
		if !p.closeGroup() {
			return nil, ""
		}
		return body, ""
	case '=', '!':
		neg := p.peekAt(1) == '!'
		p.pos += 2
		first := p.groups
		body := p.disjunction()
		if !p.closeGroup() {
			return nil, ""
		}
		look := &node{kind: nLook, ahead: true, negate: neg, kids: []*node{body}, firstCap: first + 1, lastCap: p.groups + 1}
		if p.unicode {
			return look, "Invalid quantifier"
		}
		// Annex B: lookaheads are quantifiable in non-unicode mode.
		return look, ""
	case '<':
		if p.peekAt(2) == '=' || p.peekAt(2) == '!' {
			neg := p.peekAt(2) == '!'
			p.pos += 3
			first := p.groups
			body := p.disjunction()
			if !p.closeGroup() {
				return nil, ""
			}
			return &node{kind: nLook, ahead: false, negate: neg, kids: []*node{body}, firstCap: first + 1, lastCap: p.groups + 1}, "Invalid quantifier"
		}
		p.pos += 2
		name, ok := p.groupName()
		if !ok {
			p.fail("Invalid capture group name")
			return nil, ""
		}
		for _, n := range p.names {
			if n == name {
				p.fail("Duplicate capture group name")
				return nil, ""
			}
		}
		p.groups++
		idx := p.groups
		p.names = append(p.names, name)
		body := p.disjunction()
		if !p.closeGroup() {
			return nil, ""
		}
		return &node{kind: nGroup, index: idx, name: name, kids: []*node{body}}, ""
	}
	p.fail("Invalid group")
	return nil, ""
}

func (p *parser) closeGroup() bool {
	if p.reason != "" {
		return false
	}
	if p.peek() != ')' {
		p.fail("Unterminated group")
		return false
	}
	p.pos++
	return true
}

// groupName reads IdentifierName '>' (with \u escapes allowed), returning the name.
func (p *parser) groupName() (string, bool) {
	var b []rune
	for {
		if p.eof() {
			return "", false
		}
		var c rune
		if p.peek() == '\\' {
			p.pos++
			if p.peek() != 'u' {
				return "", false
			}
			p.pos++
			v, ok := p.unicodeEscapeBody(true)
			if !ok {
				return "", false
			}
			c = v
		} else {
			c = rune(p.src[p.pos])
			p.pos++
			if isHigh(c) && !p.eof() && isLow(rune(p.src[p.pos])) {
				c = combine(c, rune(p.src[p.pos]))
				p.pos++
			}
		}
		if c == '>' {
			break
		}
		if len(b) == 0 && !isIDStart(c) || len(b) > 0 && !isIDPart(c) {
			return "", false
		}
		b = append(b, c)
	}
	if len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// unicodeEscapeBody reads the part after \u: XXXX (with a following \uXXXX low surrogate joined in
// unicode mode) or {X...} when braces are allowed.
func (p *parser) unicodeEscapeBody(braces bool) (rune, bool) {
	if braces && p.peek() == '{' {
		i := p.pos + 1
		var v rune
		n := 0
		for i < len(p.src) && isHex(rune(p.src[i])) {
			v = v*16 + hexVal(rune(p.src[i]))
			if v > 0x10FFFF {
				return 0, false
			}
			i++
			n++
		}
		if n == 0 || i >= len(p.src) || p.src[i] != '}' {
			return 0, false
		}
		p.pos = i + 1
		return v, true
	}
	v, ok := p.hexN(4)
	if !ok {
		return 0, false
	}
	if braces && isHigh(v) && p.peek() == '\\' && p.peekAt(1) == 'u' {
		save := p.pos
		p.pos += 2
		if lo, ok := p.hexN(4); ok && isLow(lo) {
			return combine(v, lo), true
		}
		p.pos = save
	}
	return v, true
}

func (p *parser) hexN(n int) (rune, bool) {
	if p.pos+n > len(p.src) {
		return 0, false
	}
	var v rune
	for k := 0; k < n; k++ {
		c := rune(p.src[p.pos+k])
		if !isHex(c) {
			return 0, false
		}
		v = v*16 + hexVal(c)
	}
	p.pos += n
	return v, true
}

func isHex(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c rune) rune {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

func isSyntaxChar(c rune) bool { return strings.ContainsRune("^$\\.*+?()[]{}|/", c) }
