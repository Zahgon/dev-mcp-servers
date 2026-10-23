package jsregex

import (
	"strings"
	"time"
)

// Regexp is a compiled pattern.
type Regexp struct {
	root      *node
	groups    int
	names     []string
	Flags     string
	global    bool
	sticky    bool
	icase     bool
	multiline bool
	dotAll    bool
	unicode   bool
}

// Compile parses pattern (a JavaScript string held as WTF-8) with flags, as new RegExp(pattern, flags)
// does; the error is a *syntaxError worded as V8 words it.
func Compile(pattern, flags string) (*Regexp, error) {
	for i, f := range flags {
		if !strings.ContainsRune("dgimsuvy", f) || strings.ContainsRune(flags[i+1:], f) {
			return nil, flagsError(flags)
		}
	}
	if strings.Contains(flags, "u") && strings.Contains(flags, "v") {
		return nil, flagsError(flags)
	}
	canonical := ""
	for _, f := range "dgimsuvy" {
		if strings.ContainsRune(flags, f) {
			canonical += string(f)
		}
	}
	root, p := parse(Units(pattern), flags)
	if p.reason != "" {
		return nil, &syntaxError{Pattern: pattern, Flags: canonical, Reason: p.reason}
	}
	return &Regexp{
		root: root, groups: p.groups, names: p.names, Flags: canonical,
		global: strings.Contains(flags, "g"), sticky: strings.Contains(flags, "y"),
		icase: strings.Contains(flags, "i"), multiline: strings.Contains(flags, "m"),
		dotAll: strings.Contains(flags, "s"), unicode: p.unicode,
	}, nil
}

// flagsError is the error V8 reports for an invalid flags string.
type flagsError string

func (e flagsError) Error() string {
	return "Invalid flags supplied to RegExp constructor '" + string(e) + "'"
}

// Groups is the number of capturing groups.
func (re *Regexp) Groups() int { return re.groups }

// Names lists the group names in pattern order, with the capture index of each.
func (re *Regexp) Names() []Named {
	var out []Named
	for i, n := range re.names {
		if n != "" {
			out = append(out, Named{Name: n, Index: i + 1})
		}
	}
	return out
}

// Named is a named capturing group.
type Named struct {
	Name  string
	Index int
}

// Match is one match: Caps[2k], Caps[2k+1] are the start and end (UTF-16 indexes) of capture k
// (k = 0 is the whole match), -1 when the group did not participate.
type Match struct {
	Caps []int
}

// matcher is one matching attempt: the input, the capture slots and the deadline.
type matcher struct {
	re       *Regexp
	in       []uint16
	caps     []int
	steps    int
	deadline time.Time
	aborted  bool
}

func (m *matcher) tick() bool {
	if m.aborted {
		return false
	}
	m.steps++
	if m.steps&1023 == 0 && !m.deadline.IsZero() && time.Now().After(m.deadline) {
		m.aborted = true
		return false
	}
	return true
}

// charAt reads the character starting at pos (dir 1) or ending at pos (dir -1); w is its width in
// code units, 0 when there is none.
func (m *matcher) charAt(pos, dir int) (rune, int) {
	if dir > 0 {
		if pos >= len(m.in) {
			return 0, 0
		}
		c := rune(m.in[pos])
		if m.re.unicode && isHigh(c) && pos+1 < len(m.in) && isLow(rune(m.in[pos+1])) {
			return combine(c, rune(m.in[pos+1])), 2
		}
		return c, 1
	}
	if pos <= 0 {
		return 0, 0
	}
	c := rune(m.in[pos-1])
	if m.re.unicode && isLow(c) && pos-2 >= 0 && isHigh(rune(m.in[pos-2])) {
		return combine(rune(m.in[pos-2]), c), 2
	}
	return c, 1
}

func (m *matcher) isWordAt(i int) bool {
	if i < 0 || i >= len(m.in) {
		return false
	}
	return wordFunc(m.re.unicode && m.re.icase)(rune(m.in[i]))
}

func (m *matcher) match(n *node, pos, dir int, k func(int) bool) bool {
	if !m.tick() {
		return false
	}
	switch n.kind {
	case nEmpty:
		return k(pos)
	case nSeq:
		return m.seq(n.kids, 0, pos, dir, k)
	case nAlt:
		for _, a := range n.kids {
			if m.match(a, pos, dir, k) {
				return true
			}
			if m.aborted {
				return false
			}
		}
		return false
	case nChar, nAny, nClass:
		c, w := m.charAt(pos, dir)
		if w == 0 {
			return false
		}
		var ok bool
		switch n.kind {
		case nChar:
			ok = sameChar(c, n.ch, m.re.icase, m.re.unicode)
		case nAny:
			ok = m.re.dotAll || !isLineTerminator(c)
		default:
			ok = n.set.matches(c, m.re.icase, m.re.unicode)
		}
		return ok && k(pos+dir*w)
	case nBOL:
		return (pos == 0 || m.re.multiline && isLineTerminator(rune(m.in[pos-1]))) && k(pos)
	case nEOL:
		return (pos == len(m.in) || m.re.multiline && isLineTerminator(rune(m.in[pos]))) && k(pos)
	case nWordB, nNWordB:
		b := m.isWordAt(pos-1) != m.isWordAt(pos)
		return b == (n.kind == nWordB) && k(pos)
	case nGroup:
		i := n.index
		return m.match(n.kids[0], pos, dir, func(e int) bool {
			os, oe := m.caps[2*i], m.caps[2*i+1]
			if dir > 0 {
				m.caps[2*i], m.caps[2*i+1] = pos, e
			} else {
				m.caps[2*i], m.caps[2*i+1] = e, pos
			}
			if k(e) {
				return true
			}
			m.caps[2*i], m.caps[2*i+1] = os, oe
			return false
		})
	case nBackref:
		return m.backref(n.index, pos, dir, k)
	case nLook:
		return m.look(n, pos, k)
	case nRepeat:
		return m.repeat(n, pos, dir, 0, k)
	}
	return false
}

func (m *matcher) seq(items []*node, i, pos, dir int, k func(int) bool) bool {
	if i == len(items) {
		return k(pos)
	}
	idx := i
	if dir < 0 {
		idx = len(items) - 1 - i
	}
	return m.match(items[idx], pos, dir, func(p int) bool { return m.seq(items, i+1, p, dir, k) })
}

func (m *matcher) backref(i, pos, dir int, k func(int) bool) bool {
	s, e := m.caps[2*i], m.caps[2*i+1]
	if s < 0 || e < 0 {
		return k(pos)
	}
	n := e - s
	var from int
	if dir > 0 {
		if pos+n > len(m.in) {
			return false
		}
		from = pos
	} else {
		if pos-n < 0 {
			return false
		}
		from = pos - n
	}
	for j := 0; j < n; j++ {
		a, b := rune(m.in[s+j]), rune(m.in[from+j])
		if !sameChar(a, b, m.re.icase, m.re.unicode) {
			return false
		}
	}
	return k(pos + dir*n)
}

func (m *matcher) look(n *node, pos int, k func(int) bool) bool {
	saved := append([]int(nil), m.caps...)
	dir := 1
	if !n.ahead {
		dir = -1
	}
	found := m.match(n.kids[0], pos, dir, func(int) bool { return true })
	if m.aborted {
		return false
	}
	if n.negate {
		copy(m.caps, saved)
		return !found && k(pos)
	}
	if !found {
		copy(m.caps, saved)
		return false
	}
	if k(pos) {
		return true
	}
	copy(m.caps, saved)
	return false
}

func (m *matcher) repeat(n *node, pos, dir, count int, k func(int) bool) bool {
	if n.max >= 0 && count >= n.max {
		return k(pos)
	}
	iterate := func() bool {
		saved := append([]int(nil), m.caps[2*n.firstCap:2*n.lastCap]...)
		for j := 2 * n.firstCap; j < 2*n.lastCap; j++ {
			m.caps[j] = -1
		}
		ok := m.match(n.kids[0], pos, dir, func(e int) bool {
			if e == pos && count >= n.min {
				return false
			}
			return m.repeat(n, e, dir, count+1, k)
		})
		if !ok {
			copy(m.caps[2*n.firstCap:2*n.lastCap], saved)
		}
		return ok
	}
	if count < n.min {
		return iterate()
	}
	if n.lazy {
		return k(pos) || !m.aborted && iterate()
	}
	return iterate() || !m.aborted && k(pos)
}

// execAt runs the matcher once at position pos (no searching). It returns the match, or nil, and
// whether the deadline stopped it.
func (re *Regexp) execAt(in []uint16, pos int, deadline time.Time) (*Match, bool) {
	m := &matcher{re: re, in: in, caps: make([]int, 2*(re.groups+1)), deadline: deadline}
	for i := range m.caps {
		m.caps[i] = -1
	}
	end := -1
	if m.match(re.root, pos, 1, func(e int) bool { end = e; return true }) {
		m.caps[0], m.caps[1] = pos, end
		return &Match{Caps: m.caps}, false
	}
	return nil, m.aborted
}

// Exec is RegExp.prototype.exec: it searches from lastIndex (from 0 unless the g or y flag is set),
// and returns the match, the new lastIndex and whether the deadline stopped it.
func (re *Regexp) Exec(in []uint16, lastIndex int, deadline time.Time) (*Match, int, bool) {
	if !re.global && !re.sticky {
		lastIndex = 0
	}
	// V8 starts a Unicode-mode search that begins between the halves of a surrogate pair at the pair's start.
	if re.unicode && lastIndex > 0 && lastIndex < len(in) && isHigh(rune(in[lastIndex-1])) && isLow(rune(in[lastIndex])) {
		lastIndex--
	}
	for {
		if lastIndex > len(in) {
			return nil, 0, false
		}
		mt, aborted := re.execAt(in, lastIndex, deadline)
		if aborted {
			return nil, lastIndex, true
		}
		if mt != nil {
			next := lastIndex
			if re.global || re.sticky {
				next = mt.Caps[1]
			}
			return mt, next, false
		}
		if re.sticky {
			return nil, 0, false
		}
		lastIndex = re.advance(in, lastIndex)
	}
}

// advance is AdvanceStringIndex.
func (re *Regexp) advance(in []uint16, i int) int {
	if re.unicode && i+1 < len(in) && isHigh(rune(in[i])) && isLow(rune(in[i+1])) {
		return i + 2
	}
	return i + 1
}
