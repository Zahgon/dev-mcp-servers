package jsregex

// atomEscape parses an escape outside a character class (the backslash is at p.pos).
func (p *parser) atomEscape() *node {
	p.pos++ // backslash
	if p.eof() {
		p.fail("\\ at end of pattern")
		return nil
	}
	c := p.peek()
	switch {
	case c >= '1' && c <= '9':
		save := p.pos
		n := 0
		for !p.eof() && isDigit(p.peek()) {
			n = n*10 + int(p.peek()-'0')
			if n > 1<<20 {
				n = 1 << 20
			}
			p.pos++
		}
		if n <= p.backrefMax {
			return &node{kind: nBackref, index: n}
		}
		if p.unicode {
			p.fail("Invalid escape")
			return nil
		}
		p.pos = save
		if c >= '8' {
			p.pos++
			return &node{kind: nChar, ch: c}
		}
		return &node{kind: nChar, ch: p.legacyOctal()}
	case c == 'k':
		if p.unicode || p.named {
			p.pos++
			if p.peek() != '<' {
				p.fail("Invalid named reference")
				return nil
			}
			p.pos++
			name, ok := p.groupName()
			if !ok {
				p.fail("Invalid capture group name")
				return nil
			}
			ref := &node{kind: nBackref, name: name}
			p.nameRefs = append(p.nameRefs, ref)
			return ref
		}
		p.pos++
		return &node{kind: nChar, ch: 'k'}
	}
	if set := p.classEscape(false); set != nil {
		return &node{kind: nClass, set: set}
	}
	if p.reason != "" {
		return nil
	}
	ch, ok := p.characterEscape(false)
	if !ok {
		return nil
	}
	return &node{kind: nChar, ch: ch}
}

// legacyOctal reads up to three octal digits (Annex B LegacyOctalEscapeSequence) at p.pos.
func (p *parser) legacyOctal() rune {
	var v rune
	n := 0
	for !p.eof() && n < 3 && p.peek() >= '0' && p.peek() <= '7' {
		nv := v*8 + (p.peek() - '0')
		if nv > 0377 {
			break
		}
		v = nv
		p.pos++
		n++
	}
	return v
}

// classEscape parses \d \D \s \S \w \W and, in unicode mode, \p{...} \P{...} at p.pos (after the
// backslash). It returns nil, leaving the position unchanged, for any other escape.
func (p *parser) classEscape(inClass bool) *charSet {
	c := p.peek()
	switch c {
	case 'd':
		p.pos++
		return &charSet{has: isDigit}
	case 'D':
		p.pos++
		return &charSet{has: not(isDigit)}
	case 's':
		p.pos++
		return &charSet{has: isSpace}
	case 'S':
		p.pos++
		return &charSet{has: not(isSpace)}
	case 'w', 'W':
		p.pos++
		f := wordFunc(p.unicode && p.icase)
		if c == 'W' {
			f = not(f)
		}
		return &charSet{has: f}
	case 'p', 'P':
		if !p.unicode {
			return nil
		}
		p.pos++
		if p.peek() != '{' {
			p.fail("Invalid property name")
			return nil
		}
		end := p.pos + 1
		for end < len(p.src) && p.src[end] != '}' {
			end++
		}
		if end >= len(p.src) {
			p.fail("Invalid property name")
			return nil
		}
		body := string(utf16Digits(p.src[p.pos+1 : end]))
		f := property(body)
		if f == nil {
			p.fail("Invalid property name")
			return nil
		}
		p.pos = end + 1
		if c == 'P' {
			f = not(f)
		}
		return &charSet{has: f}
	}
	return nil
}

// characterEscape parses a CharacterEscape at p.pos (after the backslash).
func (p *parser) characterEscape(inClass bool) (rune, bool) {
	c := p.peek()
	switch c {
	case 'f':
		p.pos++
		return '\f', true
	case 'n':
		p.pos++
		return '\n', true
	case 'r':
		p.pos++
		return '\r', true
	case 't':
		p.pos++
		return '\t', true
	case 'v':
		p.pos++
		return '\v', true
	case 'c':
		l := p.peekAt(1)
		if l >= 'a' && l <= 'z' || l >= 'A' && l <= 'Z' {
			p.pos += 2
			return l % 32, true
		}
		if inClass && !p.unicode && (isDigit(l) || l == '_') {
			p.pos += 2
			return l % 32, true
		}
		if p.unicode {
			p.fail("Invalid Unicode escape")
			return 0, false
		}
		// Annex B: the backslash stands for itself and "c" is read next.
		return '\\', true
	case '0':
		if !isDigit(p.peekAt(1)) {
			p.pos++
			return 0, true
		}
		if p.unicode {
			p.fail("Invalid decimal escape")
			return 0, false
		}
		return p.legacyOctal(), true
	case 'x':
		p.pos++
		if v, ok := p.hexN(2); ok {
			return v, true
		}
		if p.unicode {
			p.fail("Invalid escape")
			return 0, false
		}
		return 'x', true
	case 'u':
		p.pos++
		if v, ok := p.unicodeEscapeBody(p.unicode); ok {
			return v, true
		}
		if p.unicode {
			p.fail("Invalid Unicode escape")
			return 0, false
		}
		return 'u', true
	}
	if p.unicode {
		if isSyntaxChar(c) || c == '-' && inClass {
			p.pos++
			return c, true
		}
		p.fail("Invalid escape")
		return 0, false
	}
	if inClass && c >= '1' && c <= '9' {
		if c >= '8' {
			p.pos++
			return c, true
		}
		return p.legacyOctal(), true
	}
	return p.next(), true
}

// class parses a character class at p.pos ('[').
func (p *parser) class() *node {
	p.pos++
	negate := false
	if p.peek() == '^' {
		negate = true
		p.pos++
	}
	var f func(rune) bool
	if p.sets {
		f = p.classSetContents()
	} else {
		f = p.classRanges()
	}
	if p.reason != "" {
		return nil
	}
	if p.peek() != ']' {
		p.fail("Unterminated character class")
		return nil
	}
	p.pos++
	return &node{kind: nClass, set: &charSet{has: f, negate: negate}}
}

// classAtom reads one class atom: a character (ok, set nil) or a class escape (set non-nil).
func (p *parser) classAtom() (rune, *charSet, bool) {
	if p.eof() {
		p.fail("Unterminated character class")
		return 0, nil, false
	}
	if p.peek() != '\\' {
		return p.next(), nil, true
	}
	p.pos++
	if p.eof() {
		p.fail("\\ at end of pattern")
		return 0, nil, false
	}
	switch p.peek() {
	case 'b':
		p.pos++
		return '\b', nil, true
	case '-':
		if p.unicode {
			p.pos++
			return '-', nil, true
		}
	case 'B':
		if p.unicode {
			p.fail("Invalid escape")
			return 0, nil, false
		}
		p.pos++
		return 'B', nil, true
	case 'k':
		if !p.unicode {
			p.pos++
			return 'k', nil, true
		}
		p.fail("Invalid escape")
		return 0, nil, false
	}
	if set := p.classEscape(true); set != nil {
		return 0, set, true
	}
	if p.reason != "" {
		return 0, nil, false
	}
	c, ok := p.characterEscape(true)
	return c, nil, ok
}

func (p *parser) classRanges() func(rune) bool {
	var parts []func(rune) bool
	for !p.eof() && p.peek() != ']' {
		lo, loSet, ok := p.classAtom()
		if !ok {
			return nil
		}
		if p.peek() == '-' && p.peekAt(1) != ']' && p.peekAt(1) != -1 {
			p.pos++
			hi, hiSet, ok := p.classAtom()
			if !ok {
				return nil
			}
			if loSet != nil || hiSet != nil {
				if p.unicode {
					p.fail("Invalid character class")
					return nil
				}
				for _, x := range []struct {
					c   rune
					set *charSet
				}{{lo, loSet}, {'-', nil}, {hi, hiSet}} {
					if x.set != nil {
						parts = append(parts, x.set.has)
					} else {
						parts = append(parts, single(x.c))
					}
				}
				continue
			}
			if hi < lo {
				p.fail("Range out of order in character class")
				return nil
			}
			parts = append(parts, ranges([2]rune{lo, hi}))
			continue
		}
		if loSet != nil {
			parts = append(parts, loSet.has)
		} else {
			parts = append(parts, single(lo))
		}
	}
	return union(parts...)
}

// classSetContents parses the body of a v-mode class: a union of characters, ranges, nested classes
// and escapes, or a chain of && intersections or -- subtractions.
func (p *parser) classSetContents() func(rune) bool {
	first := p.classSetOperand()
	if p.reason != "" {
		return nil
	}
	op := ""
	if p.peek() == '&' && p.peekAt(1) == '&' {
		op = "&&"
	} else if p.peek() == '-' && p.peekAt(1) == '-' {
		op = "--"
	}
	if op == "" {
		parts := []func(rune) bool{first}
		for p.reason == "" && !p.eof() && p.peek() != ']' {
			if p.peek() == '&' && p.peekAt(1) == '&' || p.peek() == '-' && p.peekAt(1) == '-' {
				p.fail("Invalid set operation in character class")
				return nil
			}
			parts = append(parts, p.classSetOperand())
		}
		return union(parts...)
	}
	acc := first
	for p.reason == "" && !p.eof() && p.peek() != ']' {
		if !(op == "&&" && p.peek() == '&' && p.peekAt(1) == '&') && !(op == "--" && p.peek() == '-' && p.peekAt(1) == '-') {
			p.fail("Invalid set operation in character class")
			return nil
		}
		p.pos += 2
		rhs := p.classSetOperand()
		l, r := acc, rhs
		if op == "&&" {
			acc = func(c rune) bool { return l(c) && r(c) }
		} else {
			acc = func(c rune) bool { return l(c) && !r(c) }
		}
	}
	return acc
}

func (p *parser) classSetOperand() func(rune) bool {
	if p.peek() == '[' {
		p.pos++
		negate := p.peek() == '^'
		if negate {
			p.pos++
		}
		inner := p.classSetContents()
		if p.reason != "" {
			return nil
		}
		if p.peek() != ']' {
			p.fail("Unterminated character class")
			return nil
		}
		p.pos++
		if negate {
			return not(inner)
		}
		return inner
	}
	if c := p.peek(); c != '\\' && isClassSetReserved(c) {
		p.fail("Invalid character in character class")
		return nil
	}
	lo, set, ok := p.classAtom()
	if !ok {
		return nil
	}
	if set != nil {
		return set.has
	}
	if p.peek() == '-' && p.peekAt(1) != '-' {
		p.pos++
		hi, hiSet, ok := p.classAtom()
		if !ok {
			return nil
		}
		if hiSet != nil || hi < lo {
			p.fail("Invalid character class")
			return nil
		}
		return ranges([2]rune{lo, hi})
	}
	return single(lo)
}

func isClassSetReserved(c rune) bool {
	switch c {
	case '(', ')', '{', '}', '/', '|', '-':
		return true
	}
	return false
}
