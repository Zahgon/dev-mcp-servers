package jsregex

import (
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// Units is the UTF-16 view of a JavaScript string held as WTF-8.
func Units(s string) []uint16 { return jsv.UTF16Units(s) }

// String is the JavaScript string (WTF-8) of UTF-16 code units.
func String(u []uint16) string { return jsv.FromUnits(u) }

// Test is RegExp.prototype.test on a fresh RegExp (lastIndex 0).
func (re *Regexp) Test(s string, deadline time.Time) (bool, bool) {
	mt, _, aborted := re.Exec(Units(s), 0, deadline)
	return mt != nil, aborted
}

// Replace is String.prototype.replace(re, replacement) on a fresh RegExp: every match when the g flag
// is set, otherwise the first, with the $ patterns of GetSubstitution.
func (re *Regexp) Replace(s, replacement string, deadline time.Time) (string, bool) {
	in, repl := Units(s), Units(replacement)
	var matches []*Match
	last := 0
	for {
		mt, next, aborted := re.Exec(in, last, deadline)
		if aborted {
			return "", true
		}
		if mt == nil {
			break
		}
		matches = append(matches, mt)
		if !re.global {
			break
		}
		if mt.Caps[1] == mt.Caps[0] {
			next = re.advance(in, next)
		}
		last = next
	}
	var out []uint16
	pos := 0
	for _, mt := range matches {
		if mt.Caps[0] < pos {
			continue
		}
		out = append(out, in[pos:mt.Caps[0]]...)
		out = append(out, re.substitution(in, mt, repl)...)
		pos = mt.Caps[1]
	}
	out = append(out, in[pos:]...)
	return String(out), false
}

// substitution is GetSubstitution for one match.
func (re *Regexp) substitution(in []uint16, mt *Match, repl []uint16) []uint16 {
	var out []uint16
	m := re.groups
	capture := func(k int) []uint16 {
		s, e := mt.Caps[2*k], mt.Caps[2*k+1]
		if s < 0 {
			return nil
		}
		return in[s:e]
	}
	hasNames := len(re.Names()) > 0
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c != '$' || i+1 >= len(repl) {
			out = append(out, c)
			continue
		}
		d := repl[i+1]
		switch {
		case d == '$':
			out = append(out, '$')
			i++
		case d == '&':
			out = append(out, capture(0)...)
			i++
		case d == '`':
			out = append(out, in[:mt.Caps[0]]...)
			i++
		case d == '\'':
			out = append(out, in[mt.Caps[1]:]...)
			i++
		case d >= '0' && d <= '9':
			n1 := int(d - '0')
			if i+2 < len(repl) && repl[i+2] >= '0' && repl[i+2] <= '9' {
				n2 := n1*10 + int(repl[i+2]-'0')
				if n2 >= 1 && n2 <= m {
					out = append(out, capture(n2)...)
					i += 2
					continue
				}
			}
			if n1 >= 1 && n1 <= m {
				out = append(out, capture(n1)...)
				i++
				continue
			}
			out = append(out, c)
		case d == '<' && hasNames:
			end := -1
			for j := i + 2; j < len(repl); j++ {
				if repl[j] == '>' {
					end = j
					break
				}
			}
			if end < 0 {
				out = append(out, c)
				continue
			}
			name := String(repl[i+2 : end])
			for _, nm := range re.Names() {
				if nm.Name == name {
					out = append(out, capture(nm.Index)...)
				}
			}
			i = end
		default:
			out = append(out, c)
		}
	}
	return out
}
