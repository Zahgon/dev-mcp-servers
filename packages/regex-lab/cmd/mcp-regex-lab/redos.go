package main

// Port of src/redos.ts: static heuristics for catastrophic backtracking.

import (
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsregex"
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var alphabet = func() []string {
	var a []string
	for c := 32; c < 127; c++ {
		a = append(a, string(rune(c)))
	}
	return append(a, "\n", "\t", "é", "中")
}()

// charSet is a JavaScript Set of one-character strings: insertion order is kept.
type charSet struct {
	order []string
	has   map[string]bool
}

func newSet() *charSet { return &charSet{has: map[string]bool{}} }

func (s *charSet) add(c string) {
	if !s.has[c] {
		s.has[c] = true
		s.order = append(s.order, c)
	}
}

func singleCharTester(raw, flags string) func(string) bool {
	re, err := jsregex.Compile("^(?:"+raw+")$", stripFlags(flags, "gmyd"))
	if err != nil {
		return func(string) bool { return false }
	}
	return func(c string) bool {
		ok, _ := re.Test(c, noDeadline)
		return ok
	}
}

func stripFlags(flags, drop string) string {
	out := make([]rune, 0, len(flags))
	for _, f := range flags {
		keep := true
		for _, d := range drop {
			if f == d {
				keep = false
			}
		}
		if keep {
			out = append(out, f)
		}
	}
	return string(out)
}

var literalEscapes = map[string]string{"\\n": "\n", "\\r": "\r", "\\t": "\t", "\\f": "\f", "\\v": "\v", "\\0": "\x00"}

func literalChar(n *astNode) (string, bool) {
	if n.raw == n.value {
		return n.value, true
	}
	if v, ok := literalEscapes[n.raw]; ok {
		return v, true
	}
	if u := jsregex.Units(n.raw); len(u) == 2 && u[0] == '\\' {
		return jsregex.String(u[1:]), true
	}
	re, err := jsregex.Compile("^"+n.raw+"$", "u")
	if err != nil {
		return "", false
	}
	for _, c := range alphabet {
		if ok, _ := re.Test(c, noDeadline); ok {
			return c, true
		}
	}
	return "", false
}

// escapeRegexSource is c.replace(/[.*+?^${}()|[\]\\\/]/g, "\\$&").
func escapeRegexSource(c string) string {
	var out []uint16
	for _, u := range jsregex.Units(c) {
		switch u {
		case '.', '*', '+', '?', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\', '/':
			out = append(out, '\\')
		}
		out = append(out, u)
	}
	return jsregex.String(out)
}

func filterAlphabet(test func(string) bool) *charSet {
	s := newSet()
	for _, c := range alphabet {
		if test(c) {
			s.add(c)
		}
	}
	return s
}

func firstSet(n *astNode, flags string) (*charSet, bool) {
	switch n.kind {
	case kChar:
		c, ok := literalChar(n)
		if !ok {
			return newSet(), false
		}
		return filterAlphabet(singleCharTester(escapeRegexSource(c), flags)), false
	case kClass, kEscape:
		if n.kind == kEscape && (n.escapeKind == "backreference" || n.escapeKind == "named-backreference") {
			return filterAlphabet(func(string) bool { return true }), true
		}
		return filterAlphabet(singleCharTester(n.raw, flags)), false
	case kAnchor:
		return newSet(), true
	case kGroup:
		if isLook(n) {
			return newSet(), true
		}
		return firstSet(n.body, flags)
	case kQuantified:
		chars, nullable := firstSet(n.body, flags)
		return chars, nullable || n.min == 0
	case kAlternation:
		chars, nullable := newSet(), false
		for _, a := range n.alternatives {
			f, nl := firstSet(a, flags)
			for _, c := range f.order {
				chars.add(c)
			}
			nullable = nullable || nl
		}
		return chars, nullable
	case kSequence:
		chars := newSet()
		for _, item := range n.items {
			f, nl := firstSet(item, flags)
			for _, c := range f.order {
				chars.add(c)
			}
			if !nl {
				return chars, false
			}
		}
		return chars, true
	}
	return newSet(), false
}

func isLook(n *astNode) bool {
	switch n.groupKind {
	case "lookahead", "negative-lookahead", "lookbehind", "negative-lookbehind":
		return true
	}
	return false
}

func intersect(a, b *charSet) []string {
	var out []string
	for _, c := range a.order {
		if b.has[c] {
			out = append(out, c)
		}
	}
	return out
}

func unbounded(n *astNode) bool { return n.kind == kQuantified && (n.max < 0 || n.max >= 25) }

var groupOpen = map[string]string{"capture": "(", "non-capture": "(?:", "lookahead": "(?=", "negative-lookahead": "(?!",
	"lookbehind": "(?<=", "negative-lookbehind": "(?<!"}

func source(n *astNode) string {
	switch n.kind {
	case kChar, kClass, kEscape, kAnchor:
		return n.raw
	case kQuantified:
		return source(n.body) + n.raw
	case kGroup:
		open := groupOpen[n.groupKind]
		if n.groupKind == "named" {
			open = "(?<" + n.name + ">"
		}
		return open + source(n.body) + ")"
	case kAlternation:
		s := ""
		for k, a := range n.alternatives {
			if k > 0 {
				s += "|"
			}
			s += source(a)
		}
		return s
	case kSequence:
		s := ""
		for _, item := range n.items {
			s += source(item)
		}
		return s
	}
	return ""
}

func allUnbounded(n *astNode) []*astNode {
	switch n.kind {
	case kQuantified:
		var out []*astNode
		if unbounded(n) {
			out = append(out, n)
		}
		return append(out, allUnbounded(n.body)...)
	case kGroup:
		if isLook(n) {
			return nil
		}
		return allUnbounded(n.body)
	case kAlternation:
		var out []*astNode
		for _, a := range n.alternatives {
			out = append(out, allUnbounded(a)...)
		}
		return out
	case kSequence:
		var out []*astNode
		for _, item := range n.items {
			out = append(out, allUnbounded(item)...)
		}
		return out
	}
	return nil
}

func trailingUnbounded(n *astNode) []*astNode {
	switch {
	case n.kind == kQuantified:
		if unbounded(n) {
			return []*astNode{n}
		}
		return nil
	case n.kind == kGroup && !isLook(n):
		return trailingUnbounded(n.body)
	case n.kind == kAlternation:
		var out []*astNode
		for _, a := range n.alternatives {
			out = append(out, trailingUnbounded(a)...)
		}
		return out
	case n.kind == kSequence:
		var out []*astNode
		for i := len(n.items) - 1; i >= 0; i-- {
			item := n.items[i]
			out = append(out, trailingUnbounded(item)...)
			_, nullable := firstSet(item, "")
			if !(item.kind == kQuantified && item.min == 0) && !nullable {
				break
			}
		}
		return out
	}
	return nil
}

type finding struct {
	kind, severity, construct, message string
	probeChar                          *string
}

func (f finding) value() *jsv.Obj {
	var probe any
	if f.probeChar != nil {
		probe = *f.probeChar
	}
	return jsv.O("kind", f.kind, "severity", f.severity, "construct", f.construct, "message", f.message, "probe_char", probe)
}

func analyse(pattern, flags string) ([]finding, error) {
	ast, _, err := parseRegex(pattern)
	if err != nil {
		return nil, err
	}
	var findings []finding
	seen := map[string]bool{}
	push := func(f finding) {
		key := f.kind + ":" + f.construct
		if !seen[key] {
			seen[key] = true
			findings = append(findings, f)
		}
	}
	first := func(s []string) *string {
		if len(s) == 0 {
			return nil
		}
		return &s[0]
	}
	var walk func(n *astNode, enclosing int)
	walk = func(n *astNode, enclosing int) {
		switch n.kind {
		case kQuantified:
			if unbounded(n) {
				outer := n
				trailing := map[*astNode]bool{}
				for _, q := range trailingUnbounded(outer.body) {
					trailing[q] = true
				}
				for _, q := range allUnbounded(outer.body) {
					if q == outer {
						continue
					}
					innerFirst, _ := firstSet(q.body, flags)
					outerFirst, _ := firstSet(outer.body, flags)
					var overlap []string
					if trailing[q] {
						overlap = intersect(innerFirst, outerFirst)
					}
					f := finding{kind: "nested-quantifier", construct: source(outer), probeChar: first(overlap)}
					if len(overlap) > 0 {
						f.severity = "high"
						f.message = "An unbounded repeat (" + source(q) + ") ends the body of another unbounded repeat (" + outer.raw + ") and both can consume the same characters, so a non-matching input of n such characters can be split in about 2^n ways."
					} else {
						f.severity = "medium"
						f.message = "An unbounded repeat (" + source(q) + ") sits inside another unbounded repeat (" + outer.raw + "). Something that must match in between limits the ambiguity, but nested repeats are worth a probe."
					}
					push(f)
				}
				body := outer.body
				if body.kind == kGroup {
					body = body.body
				}
				if body.kind == kAlternation {
					for a := 0; a < len(body.alternatives); a++ {
						for b := a + 1; b < len(body.alternatives); b++ {
							fa, _ := firstSet(body.alternatives[a], flags)
							fb, _ := firstSet(body.alternatives[b], flags)
							overlap := intersect(fa, fb)
							if len(overlap) > 0 {
								push(finding{kind: "overlapping-alternation", severity: "high", construct: source(outer),
									message: "Alternatives " + source(body.alternatives[a]) + " and " + source(body.alternatives[b]) + " can both start with " +
										jsv.Stringify(overlap[0], 0) + " and the group repeats without bound, so the engine tries every assignment of characters to alternatives before failing.",
									probeChar: first(overlap)})
								break
							}
						}
					}
				}
			}
			next := enclosing
			if unbounded(n) {
				next++
			}
			walk(n.body, next)
		case kSequence:
			for i, item := range n.items {
				if unbounded(item) {
					for j := i + 1; j < len(n.items); j++ {
						nxt := n.items[j]
						if unbounded(nxt) {
							fa, _ := firstSet(item.body, flags)
							fb, _ := firstSet(nxt.body, flags)
							overlap := intersect(fa, fb)
							if len(overlap) > 0 {
								severity, tail := "medium", ""
								if enclosing > 0 {
									severity, tail = "high", ", and the sequence itself is repeated, which makes it exponential"
								}
								push(finding{kind: "adjacent-overlap", severity: severity, construct: source(item) + source(nxt),
									message:   "Two consecutive unbounded repeats (" + source(item) + " then " + source(nxt) + ") accept the same characters, so a non-matching input of n such characters is tried in about n^2 ways" + tail + ".",
									probeChar: first(overlap)})
							}
						}
						if _, nullable := firstSet(nxt, flags); !nullable {
							break
						}
					}
				}
				walk(item, enclosing)
			}
		case kAlternation:
			for _, a := range n.alternatives {
				walk(a, enclosing)
			}
		case kGroup:
			walk(n.body, enclosing)
		}
	}
	walk(ast, 0)
	return findings, nil
}
