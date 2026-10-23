// Package jsv is a JavaScript value model for servers that must answer exactly as a
// Node.js implementation does. Objects keep insertion order and iterate in the order
// Object.keys uses: canonical array-index keys ascending, then string keys in
// insertion order. A missing key is JavaScript undefined; a present key holding
// nil is JavaScript null. Numbers are float64.
//
// Strings are UTF-16 values held in Go strings as WTF-8: valid UTF-8, except that
// an unpaired surrogate code unit is stored as its three-byte generalized UTF-8
// form, so JSON.parse("\"\\ud83d\"") survives a round trip through the server.
package jsv

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Obj struct {
	order []string
	vals  map[string]any
}

func NewObj() *Obj { return &Obj{vals: map[string]any{}} }

// Set assigns like a JavaScript property write: a new key goes last, an existing key keeps its position.
func (o *Obj) Set(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.order = append(o.order, key)
	}
	o.vals[key] = value
}

func (o *Obj) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[key]
	return v, ok
}

func (o *Obj) Has(key string) bool {
	_, ok := o.Get(key)
	return ok
}

// Len is the number of own keys.
func (o *Obj) Len() int {
	if o == nil {
		return 0
	}
	return len(o.order)
}

// O builds an object from key, value pairs in order.
func O(pairs ...any) *Obj {
	o := NewObj()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// Strings converts a Go string slice into a JavaScript array value.
func Strings(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

func ArrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 {
		return 0, false
	}
	if key != "0" && key[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= 4294967295 {
		return 0, false
	}
	return n, true
}

// Keys returns the keys in JavaScript property order.
func (o *Obj) Keys() []string {
	if o == nil {
		return nil
	}
	var ints, rest []string
	for _, k := range o.order {
		if _, ok := ArrayIndex(k); ok {
			ints = append(ints, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.SliceStable(ints, func(i, j int) bool {
		a, _ := ArrayIndex(ints[i])
		b, _ := ArrayIndex(ints[j])
		return a < b
	})
	return append(ints, rest...)
}

func IsObject(v any) bool {
	o, ok := v.(*Obj)
	return ok && o != nil
}

func IsArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

func AsObj(v any) *Obj {
	if o, ok := v.(*Obj); ok {
		return o
	}
	return nil
}

func Prop(o *Obj, key string) any {
	v, _ := o.Get(key)
	return v
}

// ---- UTF-16 view of WTF-8 strings ---------------------------------------------------

// decodeWTF8 decodes one code point, accepting encoded surrogates. Bytes that are not
// WTF-8 decode as U+FFFD, one byte at a time.
func DecodeWTF8(s string, i int) (rune, int) {
	c := s[i]
	if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
		return rune(c&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), 3
	}
	return utf8.DecodeRuneInString(s[i:])
}

func UTF16Units(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		r, n := DecodeWTF8(s, i)
		i += n
		if r >= 0x10000 {
			r -= 0x10000
			units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			units = append(units, uint16(r))
		}
	}
	return units
}

func AppendWTF8(b []byte, r rune) []byte {
	if r >= 0xD800 && r <= 0xDFFF {
		return append(b, 0xED, byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	}
	return utf8.AppendRune(b, r)
}

func FromUnits(units []uint16) string {
	b := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			b = utf8.AppendRune(b, 0x10000+(u-0xD800)<<10+rune(units[i+1]-0xDC00))
			i++
			continue
		}
		b = AppendWTF8(b, u)
	}
	return string(b)
}

func UTF16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := DecodeWTF8(s, i)
		i += size
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// utf16Slice is s.slice(0, limit); the cut may leave an unpaired high surrogate.
func UTF16Slice(s string, limit int) string {
	units := UTF16Units(s)
	if len(units) <= limit {
		return s
	}
	return FromUnits(units[:limit])
}

// codePointStrings is [...s]: one string per code point; an unpaired surrogate is its own item.
func CodePoints(s string) []any {
	var out []any
	for i := 0; i < len(s); {
		_, n := DecodeWTF8(s, i)
		out = append(out, s[i:i+n])
		i += n
	}
	return out
}

// unitStrings is Object.values(s): one string per UTF-16 code unit.
func CodeUnits(s string) []any {
	units := UTF16Units(s)
	out := make([]any, len(units))
	for i, u := range units {
		out[i] = FromUnits([]uint16{u})
	}
	return out
}

// ---- JavaScript conversions ------------------------------------------------------------

// jsNumber is Number.prototype.toString().
func FormatNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	a := math.Abs(f)
	if a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	digits := strings.TrimLeft(exp[1:], "0")
	if digits == "" {
		digits = "0"
	}
	return mant + "e" + exp[:1] + digits
}

// jsString is String(v) for a defined value; undefined is handled by callers.
func ToString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return FormatNumber(x)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item != nil {
				parts[i] = ToString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func StringOrUndefined(v any, ok bool) string {
	if !ok {
		return "undefined"
	}
	return ToString(v)
}

func Truthy(v any, ok bool) bool {
	if !ok {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func Trim(s string) string { return strings.TrimFunc(s, IsSpace) }

func TrimStart(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// ---- JSON output (JSON.stringify) ----------------------------------------------------------

func writeJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, n := DecodeWTF8(s, i)
		switch {
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r == '\b':
			buf.WriteString(`\b`)
		case r == '\f':
			buf.WriteString(`\f`)
		case r == '\n':
			buf.WriteString(`\n`)
		case r == '\r':
			buf.WriteString(`\r`)
		case r == '\t':
			buf.WriteString(`\t`)
		case r < 0x20 || (r >= 0xD800 && r <= 0xDFFF):
			fmt.Fprintf(buf, `\u%04x`, r)
		default:
			buf.WriteString(s[i : i+n])
		}
		i += n
	}
	buf.WriteByte('"')
}

// writeJSON mirrors JSON.stringify(value, null, indent); indent 0 is compact.
func writeJSON(buf *bytes.Buffer, v any, indent int, depth int) {
	newline := func(d int) {
		if indent > 0 {
			buf.WriteByte('\n')
			buf.WriteString(strings.Repeat(" ", indent*d))
		}
	}
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			buf.WriteString("null")
		} else {
			buf.WriteString(FormatNumber(x))
		}
	case string:
		writeJSONString(buf, x)
	case []any:
		if len(x) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			newline(depth + 1)
			writeJSON(buf, item, indent, depth+1)
		}
		newline(depth)
		buf.WriteByte(']')
	case *Obj:
		keys := x.Keys()
		if len(keys) == 0 {
			buf.WriteString("{}")
			return
		}
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			newline(depth + 1)
			writeJSONString(buf, k)
			buf.WriteByte(':')
			if indent > 0 {
				buf.WriteByte(' ')
			}
			writeJSON(buf, x.vals[k], indent, depth+1)
		}
		newline(depth)
		buf.WriteByte('}')
	}
}

func Stringify(v any, indent int) string {
	var buf bytes.Buffer
	writeJSON(&buf, v, indent, 0)
	return buf.String()
}

// ---- JSON input (JSON.parse) ------------------------------------------------------------

type jsonParser struct {
	s   string
	pos int
}

// fail reports a JSON.parse failure at the current byte offset as JSON.parse does: the
// offset, line and column of the first character that cannot continue the text as JSON
// (the end of the text when it ends early), counted in UTF-16 code units, where CR LF,
// CR and LF each end a line.
func (p *jsonParser) fail(what string) error {
	offset, line, column := jsonPosition(p.s, p.pos)
	return fmt.Errorf("%s in JSON at position %d (line %d column %d)", what, offset, line, column)
}

// failAt reports a failure at byte offset pos.
func (p *jsonParser) failAt(pos int, what string) error {
	p.pos = pos
	return p.fail(what)
}

func jsonPosition(s string, pos int) (offset, line, column int) {
	line, column = 1, 1
	for i := 0; i < pos && i < len(s); {
		r, n := DecodeWTF8(s, i)
		i += n
		units := 1
		if r >= 0x10000 {
			units = 2
		}
		offset += units
		switch {
		case r == '\r' && i < pos && i < len(s) && s[i] == '\n':
			i++
			offset++
			line, column = line+1, 1
		case r == '\r' || r == '\n':
			line, column = line+1, 1
		default:
			column += units
		}
	}
	return offset, line, column
}

func (p *jsonParser) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) value(depth int) (any, error) {
	if depth > 10000 {
		return nil, p.fail("Maximum nesting depth exceeded")
	}
	p.skipSpace()
	if p.pos >= len(p.s) {
		return nil, p.fail("Unexpected end of JSON input")
	}
	switch c := p.s[p.pos]; {
	case c == '{':
		p.pos++
		obj := NewObj()
		p.skipSpace()
		if p.pos < len(p.s) && p.s[p.pos] == '}' {
			p.pos++
			return obj, nil
		}
		for {
			p.skipSpace()
			if p.pos >= len(p.s) || p.s[p.pos] != '"' {
				return nil, p.fail("Expected double-quoted property name")
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if p.pos >= len(p.s) || p.s[p.pos] != ':' {
				return nil, p.fail("Expected ':' after property name")
			}
			p.pos++
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			obj.Set(key, v)
			p.skipSpace()
			if p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.pos < len(p.s) && p.s[p.pos] == '}' {
				p.pos++
				return obj, nil
			}
			return nil, p.fail("Expected ',' or '}' after property value")
		}
	case c == '[':
		p.pos++
		arr := []any{}
		p.skipSpace()
		if p.pos < len(p.s) && p.s[p.pos] == ']' {
			p.pos++
			return arr, nil
		}
		for {
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
			p.skipSpace()
			if p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.pos < len(p.s) && p.s[p.pos] == ']' {
				p.pos++
				return arr, nil
			}
			return nil, p.fail("Expected ',' or ']' after array element")
		}
	case c == '"':
		return p.str()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	for _, lit := range []struct {
		text  string
		value any
	}{{"true", true}, {"false", false}, {"null", nil}} {
		if p.s[p.pos] != lit.text[0] {
			continue
		}
		for k := 1; k < len(lit.text); k++ {
			if p.pos+k >= len(p.s) {
				return nil, p.failAt(len(p.s), "Unexpected end of JSON input")
			}
			if p.s[p.pos+k] != lit.text[k] {
				return nil, p.failAt(p.pos+k, "Unexpected token")
			}
		}
		p.pos += len(lit.text)
		return lit.value, nil
	}
	return nil, p.fail("Unexpected token")
}

func (p *jsonParser) number() (any, error) {
	start := p.pos
	digits := func() int {
		n := 0
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.s[p.pos] == '-' {
		p.pos++
	}
	if p.pos < len(p.s) && p.s[p.pos] == '0' {
		p.pos++
	} else if digits() == 0 {
		return nil, p.fail("No number after minus sign")
	}
	if p.pos < len(p.s) && p.s[p.pos] == '.' {
		p.pos++
		if digits() == 0 {
			return nil, p.fail("Unterminated fractional number")
		}
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
			p.pos++
		}
		if digits() == 0 {
			return nil, p.fail("Exponent part is missing a number")
		}
	}
	f, err := strconv.ParseFloat(p.s[start:p.pos], 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, p.fail("Bad number")
	}
	return f, nil
}

// hex4 reads the four hex digits of a \\u escape; when they are not all there it returns
// how many valid digits precede the first bad character or the end of the text.
func hex4(s string) (uint16, int) {
	var n uint16
	for k := 0; k < 4; k++ {
		if k >= len(s) {
			return 0, k
		}
		c := s[k]
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, k
		}
		n = n<<4 | uint16(d)
	}
	return n, 4
}

func (p *jsonParser) str() (string, error) {
	p.pos++ // opening quote
	var units []uint16
	for {
		if p.pos >= len(p.s) {
			return "", p.fail("Unterminated string")
		}
		c := p.s[p.pos]
		switch {
		case c == '"':
			p.pos++
			return FromUnits(units), nil
		case c < 0x20:
			return "", p.fail("Bad control character in string literal")
		case c == '\\':
			if p.pos+1 >= len(p.s) {
				return "", p.failAt(len(p.s), "Unexpected end of JSON input")
			}
			esc := p.s[p.pos+1]
			p.pos += 2
			switch esc {
			case '"', '\\', '/':
				units = append(units, uint16(esc))
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			case 'u':
				u, got := hex4(p.s[p.pos:])
				if got < 4 {
					return "", p.failAt(p.pos+got, "Bad Unicode escape")
				}
				units = append(units, u)
				p.pos += 4
			default:
				return "", p.failAt(p.pos-1, "Bad escaped character")
			}
		default:
			r, n := DecodeWTF8(p.s, p.pos)
			p.pos += n
			if r >= 0x10000 {
				r -= 0x10000
				units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			} else {
				units = append(units, uint16(r))
			}
		}
	}
}

// parseJSON is JSON.parse(text).
func Parse(text string) (any, error) {
	p := &jsonParser{s: text}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		return nil, p.fail("Unexpected non-whitespace character after JSON")
	}
	return v, nil
}

// ValidUTF8 decodes bytes as Node's UTF-8 decoder does: valid sequences are kept and each
// maximal ill-formed subsequence becomes one U+FFFD (the WHATWG Encoding Standard rule).
func ValidUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	for i := 0; i < len(data); {
		r, n := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || n > 1 {
			b.WriteRune(r)
			i += n
			continue
		}
		b.WriteRune(utf8.RuneError)
		i += maximalSubpart(data[i:])
	}
	return b.String()
}

// maximalSubpart is the length of the longest prefix of b that starts a well-formed
// sequence, at least one byte.
func maximalSubpart(b []byte) int {
	lead := b[0]
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 1
	case lead >= 0xE0 && lead <= 0xEF:
		need = 2
		if lead == 0xE0 {
			lo = 0xA0
		} else if lead == 0xED {
			hi = 0x9F
		}
	case lead >= 0xF0 && lead <= 0xF4:
		need = 3
		if lead == 0xF0 {
			lo = 0x90
		} else if lead == 0xF4 {
			hi = 0x8F
		}
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(b); k++ {
		c := b[n]
		if c < lo || c > hi {
			break
		}
		lo, hi = 0x80, 0xBF
		n++
	}
	return n
}
