package main

// Date.parse as V8 12.4 (Node 22) implements it: the ES5 ISO 8601 date-time format
// first, then the legacy (Safari-compatible) grammar for whatever the ISO scan left.
// This follows src/date/dateparser*.{h,cc} and ParseDateTimeString in builtins-date.cc.

import (
	"math"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	dpNone             = math.MaxInt32
	dpMaxSignificant   = 9
	dpSmiMax           = math.MaxInt32
	msPerDay           = 86400000.0
	maxTimeMs          = 8.64e15
	maxTimeBeforeUTCMs = maxTimeMs + 30*msPerDay
)

// dpReader walks UTF-16 code units; a NUL unit or the end of input reads as 0.
type dpReader struct {
	units []uint16
	index int
	ch    uint32
}

func newReader(s string) *dpReader {
	r := &dpReader{units: jsv.UTF16Units(s)}
	r.next()
	return r
}

func (r *dpReader) next() {
	if r.index < len(r.units) {
		r.ch = uint32(r.units[r.index])
	} else {
		r.ch = 0
	}
	r.index++
}

func (r *dpReader) isDigit() bool { return r.ch >= '0' && r.ch <= '9' }

// isWhiteSpace is the ECMAScript WhiteSpace production (no line terminators).
func isWhiteSpace(c uint32) bool {
	switch c {
	case '\t', '\v', '\f', ' ', 0xa0, 0x1680, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return c >= 0x2000 && c <= 0x200a
}

func isLineTerminator(c uint32) bool {
	return c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029
}

func (r *dpReader) wordChar() bool { return r.ch >= 'A' && !isWhiteSpace(r.ch) }

func (r *dpReader) readUnsignedNumeral() int {
	n, i := 0, 0
	for r.ch == '0' {
		r.next()
	}
	for r.isDigit() {
		if i < dpMaxSignificant {
			n = n*10 + int(r.ch-'0')
		}
		i++
		r.next()
	}
	return n
}

func (r *dpReader) readWord(prefix *[3]uint32) int {
	length := 0
	for ; r.wordChar(); r.next() {
		if length < len(prefix) {
			prefix[length] = r.ch | 0x20
		}
		length++
	}
	for i := length; i < len(prefix); i++ {
		prefix[i] = 0
	}
	return length
}

func (r *dpReader) skip(c uint32) bool {
	if r.ch == c {
		r.next()
		return true
	}
	return false
}

func (r *dpReader) skipParentheses() bool {
	if r.ch != '(' {
		return false
	}
	balance := 0
	for {
		switch r.ch {
		case ')':
			balance--
		case '(':
			balance++
		}
		r.next()
		if balance <= 0 || r.ch == 0 {
			return true
		}
	}
}

type keywordType int

const (
	kwInvalid keywordType = iota
	kwMonthName
	kwTimeZoneName
	kwTimeSeparator
	kwAmPm
)

type tokenTag int

const (
	tagInvalid tokenTag = -6 + iota
	tagUnknown
	tagWhiteSpace
	tagNumber
	tagSymbol
	tagEndOfInput
)

// dpToken is a DateToken: tags >= 0 are keywords of that keywordType.
type dpToken struct {
	tag    tokenTag
	length int
	value  int
}

func (t dpToken) isNumber() bool                   { return t.tag == tagNumber }
func (t dpToken) isSymbol(c byte) bool             { return t.tag == tagSymbol && t.value == int(c) }
func (t dpToken) isKeyword() bool                  { return t.tag >= 0 }
func (t dpToken) isKeywordType(k keywordType) bool { return t.tag == tokenTag(k) }
func (t dpToken) isFixedNumber(n int) bool         { return t.isNumber() && t.length == n }
func (t dpToken) isSign() bool                     { return t.tag == tagSymbol && (t.value == '-' || t.value == '+') }
func (t dpToken) sign() int                        { return 44 - t.value }
func (t dpToken) isKeywordZ() bool {
	return t.isKeywordType(kwTimeZoneName) && t.length == 1 && t.value == 0
}

type keyword struct {
	prefix [3]uint32
	kind   keywordType
	value  int
}

var keywords = []keyword{
	{[3]uint32{'j', 'a', 'n'}, kwMonthName, 1}, {[3]uint32{'f', 'e', 'b'}, kwMonthName, 2},
	{[3]uint32{'m', 'a', 'r'}, kwMonthName, 3}, {[3]uint32{'a', 'p', 'r'}, kwMonthName, 4},
	{[3]uint32{'m', 'a', 'y'}, kwMonthName, 5}, {[3]uint32{'j', 'u', 'n'}, kwMonthName, 6},
	{[3]uint32{'j', 'u', 'l'}, kwMonthName, 7}, {[3]uint32{'a', 'u', 'g'}, kwMonthName, 8},
	{[3]uint32{'s', 'e', 'p'}, kwMonthName, 9}, {[3]uint32{'o', 'c', 't'}, kwMonthName, 10},
	{[3]uint32{'n', 'o', 'v'}, kwMonthName, 11}, {[3]uint32{'d', 'e', 'c'}, kwMonthName, 12},
	{[3]uint32{'a', 'm', 0}, kwAmPm, 0}, {[3]uint32{'p', 'm', 0}, kwAmPm, 12},
	{[3]uint32{'u', 't', 0}, kwTimeZoneName, 0}, {[3]uint32{'u', 't', 'c'}, kwTimeZoneName, 0},
	{[3]uint32{'z', 0, 0}, kwTimeZoneName, 0}, {[3]uint32{'g', 'm', 't'}, kwTimeZoneName, 0},
	{[3]uint32{'c', 'd', 't'}, kwTimeZoneName, -5}, {[3]uint32{'c', 's', 't'}, kwTimeZoneName, -6},
	{[3]uint32{'e', 'd', 't'}, kwTimeZoneName, -4}, {[3]uint32{'e', 's', 't'}, kwTimeZoneName, -5},
	{[3]uint32{'m', 'd', 't'}, kwTimeZoneName, -6}, {[3]uint32{'m', 's', 't'}, kwTimeZoneName, -7},
	{[3]uint32{'p', 'd', 't'}, kwTimeZoneName, -7}, {[3]uint32{'p', 's', 't'}, kwTimeZoneName, -8},
	{[3]uint32{'t', 0, 0}, kwTimeSeparator, 0},
}

func lookupKeyword(prefix [3]uint32, length int) (keywordType, int) {
	for _, k := range keywords {
		if k.prefix == prefix && (length <= 3 || k.kind == kwMonthName) {
			return k.kind, k.value
		}
	}
	return kwInvalid, 0
}

type dpScanner struct {
	in   *dpReader
	peek dpToken
}

func newScanner(in *dpReader) *dpScanner {
	s := &dpScanner{in: in}
	s.peek = s.scan()
	return s
}

func (s *dpScanner) next() dpToken {
	t := s.peek
	s.peek = s.scan()
	return t
}

func (s *dpScanner) skipSymbol(c byte) bool {
	if s.peek.isSymbol(c) {
		s.peek = s.scan()
		return true
	}
	return false
}

func (s *dpScanner) scan() dpToken {
	in := s.in
	pre := in.index
	if in.ch == 0 {
		return dpToken{tag: tagEndOfInput, value: -1}
	}
	if in.isDigit() {
		n := in.readUnsignedNumeral()
		return dpToken{tag: tagNumber, length: in.index - pre, value: n}
	}
	for _, c := range []uint32{':', '-', '+', '.', ')'} {
		if in.skip(c) {
			return dpToken{tag: tagSymbol, length: 1, value: int(c)}
		}
	}
	if in.wordChar() {
		var prefix [3]uint32
		length := in.readWord(&prefix)
		kind, value := lookupKeyword(prefix, length)
		return dpToken{tag: tokenTag(kind), length: length, value: value}
	}
	if isWhiteSpace(in.ch) || isLineTerminator(in.ch) {
		in.next()
		return dpToken{tag: tagWhiteSpace, length: in.index - pre, value: -1}
	}
	in.skipParentheses()
	if in.index == pre {
		in.next()
	}
	return dpToken{tag: tagUnknown, length: 1, value: -1}
}

func between(x, lo, hi int) bool { return uint32(x-lo) <= uint32(hi-lo) }

func isMinute(x int) bool { return between(x, 0, 59) }
func isHour(x int) bool   { return between(x, 0, 23) }
func isMonth(x int) bool  { return between(x, 1, 12) }
func isDay(x int) bool    { return between(x, 1, 31) }

type tzComposer struct{ sign, hour, minute int }

func (z *tzComposer) set(hours int) {
	z.sign = 1
	if hours < 0 {
		z.sign = -1
	}
	z.hour = hours * z.sign
	z.minute = 0
}

func (z *tzComposer) setSign(sign int) {
	z.sign = 1
	if sign < 0 {
		z.sign = -1
	}
}

func (z *tzComposer) expecting(n int) bool {
	return z.hour != dpNone && z.minute == dpNone && isMinute(n)
}

func (z *tzComposer) isUTC() bool { return z.hour == 0 && z.minute == 0 }

// offset is the UTC offset in seconds, or NaN when the text names none.
func (z *tzComposer) offset() (float64, bool) {
	if z.sign == dpNone {
		return math.NaN(), true
	}
	if z.hour == dpNone {
		z.hour = 0
	}
	if z.minute == dpNone {
		z.minute = 0
	}
	total := uint32(z.hour)*3600 + uint32(z.minute)*60
	if total > dpSmiMax {
		return 0, false
	}
	seconds := int(total)
	if z.sign < 0 {
		seconds = -seconds
	}
	return float64(seconds), true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (c *timeComposer) empty() bool { return c.index == 0 }

func (c *timeComposer) expecting(n int) bool {
	return (c.index == 1 && isMinute(n)) || (c.index == 2 && isMinute(n)) || (c.index == 3 && between(n, 0, 999))
}

func (c *timeComposer) add(n int) bool {
	if c.index < len(c.comp) {
		c.comp[c.index] = n
		c.index++
		return true
	}
	return false
}

func (c *timeComposer) addFinal(n int) bool {
	if !c.add(n) {
		return false
	}
	for c.index < len(c.comp) {
		c.comp[c.index] = 0
		c.index++
	}
	return true
}

func (c *timeComposer) write() (h, m, s, ms int, ok bool) {
	for c.index < len(c.comp) {
		c.comp[c.index] = 0
		c.index++
	}
	h, m, s, ms = c.comp[0], c.comp[1], c.comp[2], c.comp[3]
	if c.hourOffset != dpNone {
		if !between(h, 0, 12) {
			return 0, 0, 0, 0, false
		}
		h = h%12 + c.hourOffset
	}
	if !isHour(h) || !isMinute(m) || !isMinute(s) || !between(ms, 0, 999) {
		if h != 24 || m != 0 || s != 0 || ms != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return h, m, s, ms, true
}

type dayComposer struct {
	comp       [3]int
	index      int
	namedMonth int
	iso        bool
}

func (c *dayComposer) empty() bool { return c.index == 0 }

func (c *dayComposer) add(n int) bool {
	if c.index < len(c.comp) {
		c.comp[c.index] = n
		c.index++
		return true
	}
	return false
}

// write mirrors DayComposer::Write, which pads the components to three before it
// inspects how many were given, so the one- and two-component branches never apply.
func (c *dayComposer) write() (year, month, day int, ok bool) {
	if c.index < 1 {
		return 0, 0, 0, false
	}
	for c.index < len(c.comp) {
		c.comp[c.index] = 1
		c.index++
	}
	month, day = dpNone, dpNone
	if c.namedMonth == dpNone {
		if c.iso || !isDay(c.comp[0]) {
			year, month, day = c.comp[0], c.comp[1], c.comp[2]
		} else {
			month, day, year = c.comp[0], c.comp[1], c.comp[2]
		}
	} else {
		month = c.namedMonth
		if !isDay(c.comp[0]) {
			year, day = c.comp[0], c.comp[1]
		} else {
			day, year = c.comp[0], c.comp[1]
		}
	}
	if !c.iso {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	if !isMonth(month) || !isDay(day) {
		return 0, 0, 0, false
	}
	return year, month - 1, day, true
}

func readMilliseconds(t dpToken) int {
	number, length := t.value, t.length
	switch {
	case length == 1:
		number *= 100
	case length == 2:
		number *= 10
	case length > 3:
		if length > dpMaxSignificant {
			length = dpMaxSignificant
		}
		factor := 1
		for length > 3 {
			factor *= 10
			length--
		}
		number /= factor
	}
	return number
}

var invalidToken = dpToken{tag: tagInvalid, value: -1}

// parseES5 reads as much of an ES5 date-time string as it can and returns the first
// token it did not consume (end of input on full success, invalid on a hard failure).
func parseES5(s *dpScanner, day *dayComposer, tm *timeComposer, tz *tzComposer) dpToken {
	switch {
	case s.peek.isSign():
		signTok := s.next()
		if !s.peek.isFixedNumber(6) {
			return signTok
		}
		sign := signTok.sign()
		year := s.next().value
		if sign < 0 && year == 0 {
			return signTok
		}
		day.add(sign * year)
	case s.peek.isFixedNumber(4):
		day.add(s.next().value)
	default:
		return s.next()
	}
	if s.skipSymbol('-') {
		if !s.peek.isFixedNumber(2) || !isMonth(s.peek.value) {
			return s.next()
		}
		day.add(s.next().value)
		if s.skipSymbol('-') {
			if !s.peek.isFixedNumber(2) || !isDay(s.peek.value) {
				return s.next()
			}
			day.add(s.next().value)
		}
	}
	if !s.peek.isKeywordType(kwTimeSeparator) {
		if s.peek.tag != tagEndOfInput {
			return s.next()
		}
	} else {
		s.next()
		if !s.peek.isFixedNumber(2) || !between(s.peek.value, 0, 24) {
			return invalidToken
		}
		hour24 := s.peek.value == 24
		tm.add(s.next().value)
		if !s.skipSymbol(':') {
			return invalidToken
		}
		if !s.peek.isFixedNumber(2) || !isMinute(s.peek.value) || (hour24 && s.peek.value > 0) {
			return invalidToken
		}
		tm.add(s.next().value)
		if s.skipSymbol(':') {
			if !s.peek.isFixedNumber(2) || !isMinute(s.peek.value) || (hour24 && s.peek.value > 0) {
				return invalidToken
			}
			tm.add(s.next().value)
			if s.skipSymbol('.') {
				if !s.peek.isNumber() || (hour24 && s.peek.value > 0) {
					return invalidToken
				}
				tm.add(readMilliseconds(s.next()))
			}
		}
		if s.peek.isKeywordZ() {
			s.next()
			tz.set(0)
		} else if s.peek.isSymbol('+') || s.peek.isSymbol('-') {
			if s.next().value == '+' {
				tz.setSign(1)
			} else {
				tz.setSign(-1)
			}
			if s.peek.isFixedNumber(4) {
				hm := s.next().value
				if !isHour(hm/100) || !isMinute(hm%100) {
					return invalidToken
				}
				tz.hour, tz.minute = hm/100, hm%100
			} else {
				if !s.peek.isFixedNumber(2) || !isHour(s.peek.value) {
					return invalidToken
				}
				tz.hour = s.next().value
				if !s.skipSymbol(':') {
					return invalidToken
				}
				if !s.peek.isFixedNumber(2) || !isMinute(s.peek.value) {
					return invalidToken
				}
				tz.minute = s.next().value
			}
		}
		if s.peek.tag != tagEndOfInput {
			return invalidToken
		}
	}
	if tz.hour == dpNone && tm.empty() {
		tz.set(0)
	}
	day.iso = true
	return dpToken{tag: tagEndOfInput, value: -1}
}

type parsedDate struct {
	year, month, day, hour, minute, second, ms int
	offset                                     float64
}

func parseFields(text string) (parsedDate, bool) {
	s := newScanner(newReader(text))
	tz := &tzComposer{sign: dpNone, hour: dpNone, minute: dpNone}
	tm := &timeComposer{hourOffset: dpNone}
	day := &dayComposer{namedMonth: dpNone}
	token := parseES5(s, day, tm, tz)
	if token.tag == tagInvalid {
		return parsedDate{}, false
	}
	hasNumber := !day.empty()
	for ; token.tag != tagEndOfInput; token = s.next() {
		switch {
		case token.isNumber():
			hasNumber = true
			n := token.value
			if s.skipSymbol(':') {
				if s.skipSymbol(':') {
					if !tm.empty() {
						return parsedDate{}, false
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return parsedDate{}, false
					}
					if s.peek.isSymbol('.') {
						s.next()
					}
				}
			} else if s.skipSymbol('.') && tm.expecting(n) {
				tm.add(n)
				if !s.peek.isNumber() {
					return parsedDate{}, false
				}
				ms := readMilliseconds(s.next())
				if ms < 0 {
					return parsedDate{}, false
				}
				tm.addFinal(ms)
			} else if tz.expecting(n) {
				tz.minute = n
			} else if tm.expecting(n) {
				tm.addFinal(n)
				p := s.peek
				if p.tag != tagEndOfInput && p.tag != tagWhiteSpace && !p.isKeywordZ() && !p.isSign() {
					return parsedDate{}, false
				}
			} else {
				if !day.add(n) {
					return parsedDate{}, false
				}
				s.skipSymbol('-')
			}
		case token.isKeyword():
			kind, value := keywordType(token.tag), token.value
			switch {
			case kind == kwAmPm && !tm.empty():
				tm.hourOffset = value
			case kind == kwMonthName:
				day.namedMonth = value
				s.skipSymbol('-')
			case kind == kwTimeZoneName && hasNumber:
				tz.set(value)
			default:
				if hasNumber || s.peek.isNumber() {
					return parsedDate{}, false
				}
			}
		case token.isSign() && (tz.isUTC() || !tm.empty()):
			tz.setSign(token.sign())
			n, length := 0, 0
			if s.peek.isNumber() {
				t := s.next()
				n, length = t.value, t.length
			}
			hasNumber = true
			switch {
			case s.peek.isSymbol(':'):
				tz.hour, tz.minute = n, dpNone
			case length == 1 || length == 2:
				tz.hour, tz.minute = n, 0
			case length == 3 || length == 4:
				tz.hour, tz.minute = n/100, n%100
			default:
				return parsedDate{}, false
			}
		case (token.isSign() || token.isSymbol(')')) && hasNumber:
			return parsedDate{}, false
		}
	}
	var d parsedDate
	var ok bool
	if d.year, d.month, d.day, ok = day.write(); !ok {
		return d, false
	}
	if d.hour, d.minute, d.second, d.ms, ok = tm.write(); !ok {
		return d, false
	}
	if d.offset, ok = tz.offset(); !ok {
		return d, false
	}
	return d, true
}

// parseDate is Date.parse(text): epoch milliseconds, or NaN.
func parseDate(text string) float64 {
	d, ok := parseFields(text)
	if !ok {
		return math.NaN()
	}
	date := makeDate(makeDay(float64(d.year), float64(d.month), float64(d.day)),
		float64(d.hour)*3600000+float64(d.minute)*60000+float64(d.second)*1000+float64(d.ms))
	if math.IsNaN(d.offset) {
		if !(date >= -maxTimeBeforeUTCMs && date <= maxTimeBeforeUTCMs) {
			return math.NaN()
		}
		date = localToUTC(date)
	} else {
		date -= d.offset * 1000
	}
	return timeClip(date)
}
