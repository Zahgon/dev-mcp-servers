package main

// Port of src/ast.ts: a small parser for JavaScript regular expression syntax used to explain a pattern
// and to look for backtracking hazards. It works on UTF-16 code units, as the TypeScript does.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsregex"
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

type astKind int

const (
	kAlternation astKind = iota
	kSequence
	kGroup
	kQuantified
	kChar
	kClass
	kEscape
	kAnchor
)

type astNode struct {
	kind         astKind
	alternatives []*astNode // alternation
	items        []*astNode // sequence
	groupKind    string     // group: capture, non-capture, named, lookahead, ...
	name         string
	index        int
	body         *astNode // group, quantified
	min          float64
	max          float64 // -1 for unbounded
	lazy         bool
	raw          string
	value        string   // char
	negated      bool     // class
	members      []string // class
	escapeKind   string   // escape kind; anchor kind
	description  string
}

type regexSyntaxError struct {
	message  string
	position int
}

func (e *regexSyntaxError) Error() string { return e.message }

type astParser struct {
	src    []uint16
	i      int
	groups int
}

func (p *astParser) eof() bool { return p.i >= len(p.src) }

// at is src[i] as a one-unit string, "" past the end (JavaScript undefined compares unequal to every char).
func (p *astParser) at(i int) string {
	if i < 0 || i >= len(p.src) {
		return ""
	}
	return jsregex.String(p.src[i : i+1])
}

func (p *astParser) peek() string { return p.at(p.i) }

func (p *astParser) slice(a, b int) string {
	if b > len(p.src) {
		b = len(p.src)
	}
	if a > b {
		return ""
	}
	return jsregex.String(p.src[a:b])
}

func (p *astParser) startsWith(s string, at int) bool {
	u := jsregex.Units(s)
	if at+len(u) > len(p.src) {
		return false
	}
	for k, c := range u {
		if p.src[at+k] != c {
			return false
		}
	}
	return true
}

func parseRegex(pattern string) (*astNode, int, error) {
	p := &astParser{src: jsregex.Units(pattern)}
	ast, err := p.alternation()
	if err != nil {
		return nil, 0, err
	}
	if !p.eof() && p.peek() == ")" {
		return nil, 0, &regexSyntaxError{"unmatched )", p.i}
	}
	if !p.eof() {
		return nil, 0, &regexSyntaxError{"unexpected " + jsv.Stringify(p.peek(), 0), p.i}
	}
	return ast, p.groups, nil
}

func (p *astParser) alternation() (*astNode, error) {
	first, err := p.sequence()
	if err != nil {
		return nil, err
	}
	alts := []*astNode{first}
	for !p.eof() && p.peek() == "|" {
		p.i++
		s, err := p.sequence()
		if err != nil {
			return nil, err
		}
		alts = append(alts, s)
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return &astNode{kind: kAlternation, alternatives: alts}, nil
}

func (p *astParser) sequence() (*astNode, error) {
	n := &astNode{kind: kSequence, items: []*astNode{}}
	for !p.eof() && p.peek() != "|" && p.peek() != ")" {
		atom, err := p.atom()
		if err != nil {
			return nil, err
		}
		q, err := p.quantifier(atom)
		if err != nil {
			return nil, err
		}
		n.items = append(n.items, q)
	}
	return n, nil
}

// braceQuantifier matches /^\{(\d+)(?:(,)(\d*))?\}/ at i: the digits, whether a comma is present, the
// digits after it and the length of the match.
func (p *astParser) braceQuantifier(i int) (string, bool, string, int, bool) {
	if p.at(i) != "{" {
		return "", false, "", 0, false
	}
	j := i + 1
	digits := func() string {
		s := j
		for j < len(p.src) && p.src[j] >= '0' && p.src[j] <= '9' {
			j++
		}
		return p.slice(s, j)
	}
	lo := digits()
	if lo == "" {
		return "", false, "", 0, false
	}
	comma, hi := false, ""
	if p.at(j) == "," {
		comma = true
		j++
		hi = digits()
	}
	if p.at(j) != "}" {
		return "", false, "", 0, false
	}
	return lo, comma, hi, j + 1 - i, true
}

func jsNumberOf(digits string) float64 {
	f, _ := strconv.ParseFloat(digits, 64)
	return f
}

func (p *astParser) quantifier(atom *astNode) (*astNode, error) {
	if p.eof() {
		return atom, nil
	}
	start := p.i
	var min, max float64
	switch p.peek() {
	case "*":
		min, max = 0, -1
		p.i++
	case "+":
		min, max = 1, -1
		p.i++
	case "?":
		min, max = 0, 1
		p.i++
	case "{":
		lo, comma, hi, n, ok := p.braceQuantifier(p.i)
		if !ok {
			return atom, nil
		}
		min = jsNumberOf(lo)
		switch {
		case !comma:
			max = min
		case hi == "":
			max = -1
		default:
			max = jsNumberOf(hi)
		}
		if max >= 0 && max < min {
			return nil, &regexSyntaxError{fmt.Sprintf("quantifier {%s,%s} has max below min", jsv.FormatNumber(min), jsv.FormatNumber(max)), p.i}
		}
		p.i += n
	default:
		return atom, nil
	}
	if atom.kind == kAnchor || atom.kind == kQuantified {
		return nil, &regexSyntaxError{"nothing to repeat", start}
	}
	lazy := false
	if !p.eof() && p.peek() == "?" {
		lazy = true
		p.i++
	}
	return &astNode{kind: kQuantified, body: atom, min: min, max: max, lazy: lazy, raw: p.slice(start, p.i)}, nil
}

func isNameStart(c uint16) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || c == '$'
}

func isNamePart(c uint16) bool { return isNameStart(c) || c >= '0' && c <= '9' }

func (p *astParser) atom() (*astNode, error) {
	c := p.peek()
	start := p.i
	switch c {
	case "(":
		p.i++
		kind, name := "capture", ""
		switch {
		case p.startsWith("?:", p.i):
			kind = "non-capture"
			p.i += 2
		case p.startsWith("?=", p.i):
			kind = "lookahead"
			p.i += 2
		case p.startsWith("?!", p.i):
			kind = "negative-lookahead"
			p.i += 2
		case p.startsWith("?<=", p.i):
			kind = "lookbehind"
			p.i += 3
		case p.startsWith("?<!", p.i):
			kind = "negative-lookbehind"
			p.i += 3
		case p.startsWith("?<", p.i):
			j := p.i + 2
			if j < len(p.src) && isNameStart(p.src[j]) {
				j++
				for j < len(p.src) && isNamePart(p.src[j]) {
					j++
				}
				if j < len(p.src) && p.src[j] == '>' {
					kind, name = "named", p.slice(p.i+2, j)
					p.i = j + 1
					break
				}
			}
			if kind != "named" {
				return nil, &regexSyntaxError{"invalid group name", p.i}
			}
		case p.peek() == "?":
			return nil, &regexSyntaxError{"unsupported group modifier", p.i}
		}
		index := 0
		if kind == "capture" || kind == "named" {
			p.groups++
			index = p.groups
		}
		body, err := p.alternation()
		if err != nil {
			return nil, err
		}
		if p.eof() || p.peek() != ")" {
			return nil, &regexSyntaxError{"unterminated group", start}
		}
		p.i++
		return &astNode{kind: kGroup, groupKind: kind, name: name, body: body, index: index}, nil
	case "[":
		return p.class()
	case "\\":
		return p.escape()
	case "^":
		p.i++
		return &astNode{kind: kAnchor, escapeKind: "start", raw: "^"}, nil
	case "$":
		p.i++
		return &astNode{kind: kAnchor, escapeKind: "end", raw: "$"}, nil
	case ".":
		p.i++
		return &astNode{kind: kEscape, escapeKind: "any", raw: ".", description: "any character except line terminators (any character at all with the s flag)"}, nil
	case ")":
		return nil, &regexSyntaxError{"unmatched )", p.i}
	case "*", "+", "?":
		return nil, &regexSyntaxError{"nothing to repeat", p.i}
	}
	p.i++
	return &astNode{kind: kChar, value: c, raw: c}, nil
}

func (p *astParser) class() (*astNode, error) {
	start := p.i
	p.i++
	negated := false
	if p.peek() == "^" {
		negated = true
		p.i++
	}
	members := []string{}
	first := true
	for !p.eof() && (p.peek() != "]" || first) {
		first = false
		var item string
		if p.peek() == "\\" {
			e, err := p.escape()
			if err != nil {
				return nil, err
			}
			if e.kind == kEscape {
				item = e.description
			} else {
				item = e.raw
			}
		} else {
			item = p.at(p.i)
			p.i++
		}
		if !p.eof() && p.peek() == "-" && p.at(p.i+1) != "]" && p.i+1 < len(p.src) {
			p.i++
			var hi string
			if p.peek() == "\\" {
				e, err := p.escape()
				if err != nil {
					return nil, err
				}
				hi = e.raw
			} else {
				hi = p.at(p.i)
				p.i++
			}
			members = append(members, item+" to "+hi)
		} else {
			members = append(members, item)
		}
	}
	if p.eof() {
		return nil, &regexSyntaxError{"unterminated character class", start}
	}
	p.i++
	return &astNode{kind: kClass, negated: negated, raw: p.slice(start, p.i), members: members}, nil
}

var simpleEscapes = map[string][2]string{
	"d": {"digit", "a digit 0-9"},
	"D": {"non-digit", "any character that is not a digit"},
	"w": {"word", "a word character (letter, digit or underscore)"},
	"W": {"non-word", "any character that is not a word character"},
	"s": {"space", "a whitespace character"},
	"S": {"non-space", "any character that is not whitespace"},
}

var namedEscapes = map[string]string{"n": "a newline", "r": "a carriage return", "t": "a tab", "f": "a form feed", "v": "a vertical tab", "0": "a NUL character"}

func (p *astParser) escape() (*astNode, error) {
	start := p.i
	p.i++
	if p.eof() {
		return nil, &regexSyntaxError{"pattern ends with a backslash", start}
	}
	c := p.at(p.i)
	p.i++
	raw := func() string { return p.slice(start, p.i) }
	if s, ok := simpleEscapes[c]; ok {
		return &astNode{kind: kEscape, escapeKind: s[0], description: s[1], raw: raw()}, nil
	}
	switch c {
	case "b":
		return &astNode{kind: kAnchor, escapeKind: "word-boundary", raw: "\\b"}, nil
	case "B":
		return &astNode{kind: kAnchor, escapeKind: "non-word-boundary", raw: "\\B"}, nil
	case "p", "P":
		prop := "?"
		if p.peek() == "{" {
			j := p.i + 1
			for j < len(p.src) && p.src[j] != '}' {
				j++
			}
			if j < len(p.src) && j > p.i+1 {
				prop = p.slice(p.i+1, j)
				p.i = j + 1
			}
		}
		what := "a character with"
		if c == "P" {
			what = "any character without"
		}
		return &astNode{kind: kEscape, escapeKind: "unicode-property", raw: raw(), description: what + " the Unicode property " + prop + " (needs the u or v flag)"}, nil
	case "k":
		name := "?"
		if p.peek() == "<" {
			j := p.i + 1
			for j < len(p.src) && p.src[j] != '>' {
				j++
			}
			if j < len(p.src) && j > p.i+1 {
				name = p.slice(p.i+1, j)
				p.i = j + 1
			}
		}
		return &astNode{kind: kEscape, escapeKind: "named-backreference", raw: raw(), description: "the same text as group " + name + " matched"}, nil
	}
	if len(c) == 1 && c[0] >= '1' && c[0] <= '9' {
		for !p.eof() && p.src[p.i] >= '0' && p.src[p.i] <= '9' {
			p.i++
		}
		return &astNode{kind: kEscape, escapeKind: "backreference", raw: raw(), description: "the same text as group " + strings.TrimPrefix(raw(), "\\") + " matched"}, nil
	}
	if v, ok := namedEscapes[c]; ok {
		return &astNode{kind: kChar, value: v, raw: raw()}, nil
	}
	switch c {
	case "x":
		p.i += 2
		return &astNode{kind: kChar, value: "the character with hex code " + p.slice(start+2, p.i), raw: raw()}, nil
	case "u":
		if p.peek() == "{" {
			end := -1
			for j := p.i; j < len(p.src); j++ {
				if p.src[j] == '}' {
					end = j
					break
				}
			}
			if end == -1 {
				p.i = len(p.src)
			} else {
				p.i = end + 1
			}
		} else {
			p.i += 4
		}
		return &astNode{kind: kChar, value: "the character with code point " + p.slice(start+2, p.i), raw: raw()}, nil
	case "c":
		p.i++
		return &astNode{kind: kChar, value: "control character " + p.slice(start+2, p.i), raw: raw()}, nil
	}
	return &astNode{kind: kChar, value: c, raw: raw()}, nil
}

func describeQuantifier(min, max float64, lazy bool) string {
	var s string
	n, x := jsv.FormatNumber(min), jsv.FormatNumber(max)
	switch {
	case min == 0 && max < 0:
		s = "zero or more times"
	case min == 1 && max < 0:
		s = "one or more times"
	case min == 0 && max == 1:
		s = "optionally (zero or one time)"
	case max < 0:
		s = "at least " + n + " times"
	case min == max:
		plural := "s"
		if min == 1 {
			plural = ""
		}
		s = "exactly " + n + " time" + plural
	default:
		s = "between " + n + " and " + x + " times"
	}
	if lazy {
		return s + ", as few as possible (lazy)"
	}
	return s
}

var groupLabels = map[string]string{
	"non-capture":         "non-capturing group",
	"lookahead":           "lookahead: assert that what follows matches",
	"negative-lookahead":  "negative lookahead: assert that what follows does not match",
	"lookbehind":          "lookbehind: assert that what precedes matches",
	"negative-lookbehind": "negative lookbehind: assert that what precedes does not match",
}

var anchorTexts = map[string]string{
	"start":             "start of input (start of a line with the m flag)",
	"end":               "end of input (end of a line with the m flag)",
	"word-boundary":     "a word boundary",
	"non-word-boundary": "a position that is not a word boundary",
}

func explainNode(n *astNode, depth int, lines []any) []any {
	pad := strings.Repeat("  ", depth)
	switch n.kind {
	case kAlternation:
		lines = append(lines, fmt.Sprintf("%sone of the following %d alternatives:", pad, len(n.alternatives)))
		for k, a := range n.alternatives {
			lines = append(lines, fmt.Sprintf("%s  alternative %d:", pad, k+1))
			lines = explainNode(a, depth+2, lines)
		}
	case kSequence:
		if len(n.items) == 0 {
			lines = append(lines, pad+"(empty)")
		}
		for _, item := range n.items {
			lines = explainNode(item, depth, lines)
		}
	case kGroup:
		label := groupLabels[n.groupKind]
		switch n.groupKind {
		case "capture":
			label = fmt.Sprintf("capturing group %d", n.index)
		case "named":
			label = fmt.Sprintf("named capturing group \"%s\" (group %d)", n.name, n.index)
		}
		lines = append(lines, pad+label+":")
		lines = explainNode(n.body, depth+1, lines)
	case kQuantified:
		lines = append(lines, fmt.Sprintf("%srepeat %s (%s):", pad, describeQuantifier(n.min, n.max, n.lazy), n.raw))
		lines = explainNode(n.body, depth+1, lines)
	case kChar:
		if n.raw == n.value {
			lines = append(lines, fmt.Sprintf("%sthe character \"%s\"", pad, n.value))
		} else {
			lines = append(lines, fmt.Sprintf("%s%s (%s)", pad, n.value, n.raw))
		}
	case kClass:
		what := "one character from"
		if n.negated {
			what = "any character NOT in"
		}
		quoted := make([]string, len(n.members))
		for k, m := range n.members {
			quoted[k] = jsv.Stringify(m, 0)
		}
		lines = append(lines, fmt.Sprintf("%s%s the set: %s (%s)", pad, what, strings.Join(quoted, ", "), n.raw))
	case kEscape:
		lines = append(lines, fmt.Sprintf("%s%s (%s)", pad, n.description, n.raw))
	case kAnchor:
		lines = append(lines, fmt.Sprintf("%s%s (%s)", pad, anchorTexts[n.escapeKind], n.raw))
	}
	return lines
}
