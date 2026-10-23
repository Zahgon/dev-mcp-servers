package main

// JavaScript string semantics for the parser and the rules. The TypeScript code measures,
// slices and matches strings in UTF-16 code units, so text is processed in a "unit form":
// every UTF-16 code unit is one rune. BMP characters are unchanged; each surrogate code
// unit becomes a private-use rune of plane 16. Regular expressions written in JavaScript
// syntax are translated to RE2 with JavaScript's \s, \S, '.' and ASCII-only /i.

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const surrogateRune = 0x10F000 - 0xD800

// toUnits converts a WTF-8 string to unit form.
func toUnits(s string) string {
	var b strings.Builder
	for _, u := range jsv.UTF16Units(s) {
		if u >= 0xD800 && u <= 0xDFFF {
			b.WriteRune(rune(u) + surrogateRune)
		} else {
			b.WriteRune(rune(u))
		}
	}
	return b.String()
}

// fromUnits converts a unit-form string back to WTF-8, pairing surrogates again.
func fromUnits(s string) string {
	units := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0xD800+surrogateRune && r <= 0xDFFF+surrogateRune {
			r -= surrogateRune
		}
		units = append(units, uint16(r))
	}
	return jsv.FromUnits(units)
}

// unitLen is String.prototype.length of a unit-form string.
func unitLen(s string) int { return utf8.RuneCountInString(s) }

// deepFromUnits converts every string in a result value back from unit form.
func deepFromUnits(v any) any {
	switch x := v.(type) {
	case string:
		return fromUnits(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepFromUnits(item)
		}
		return out
	case *jsv.Obj:
		out := jsv.NewObj()
		for _, k := range x.Keys() {
			out.Set(fromUnits(k), deepFromUnits(jsv.Prop(x, k)))
		}
		return out
	}
	return v
}

const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsRegexp compiles a JavaScript regular expression source with flags "i" and "s" (and
// "g", which only callers act on). It supports the subset the server's patterns use and
// panics on anything else, so an unsupported pattern fails at start-up.
func jsRegexp(source, flags string) *regexp.Regexp {
	fold := strings.Contains(flags, "i")
	dotAll := strings.Contains(flags, "s")
	if strings.ContainsAny(flags, "muyd") {
		panic("jsRegexp: unsupported flags " + flags)
	}
	src := []rune(source)
	var out strings.Builder
	inClass := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\':
			i++
			e := src[i]
			switch {
			case e == 's' && inClass:
				out.WriteString(jsSpace)
			case e == 's':
				out.WriteString("[" + jsSpace + "]")
			case e == 'S' && !inClass:
				out.WriteString("[^" + jsSpace + "]")
			case e == 'b' && inClass:
				out.WriteString(`\x08`)
			case strings.ContainsRune("dDwWbBnrtfv", e) && !(inClass && e == 'B'):
				out.WriteRune('\\')
				out.WriteRune(e)
			case e == '/':
				out.WriteRune('/')
			case strings.ContainsRune(`\.^$|?*+()[]{}-`, e):
				out.WriteRune('\\')
				out.WriteRune(e)
			default:
				panic("jsRegexp: unsupported escape in " + source)
			}
		case inClass:
			if c == ']' {
				inClass = false
			} else if fold && c < 0x80 && unicode.IsLetter(c) {
				panic("jsRegexp: letter in a class with /i in " + source)
			}
			out.WriteRune(c)
		case c == '[':
			rest := string(src[i:])
			if strings.HasPrefix(rest, `[\s\S]`) || strings.HasPrefix(rest, `[\S\s]`) {
				out.WriteString(`(?s:.)`)
				i += 5
				continue
			}
			inClass = true
			out.WriteRune(c)
			if i+1 < len(src) && src[i+1] == '^' {
				out.WriteRune('^')
				i++
			}
		case c == '.':
			if dotAll {
				out.WriteString(`(?s:.)`)
			} else {
				out.WriteString(`[^\n\r\x{2028}\x{2029}]`)
			}
		case c == '(' && i+1 < len(src) && src[i+1] == '?':
			if i+2 >= len(src) || src[i+2] != ':' {
				panic("jsRegexp: unsupported group in " + source)
			}
			out.WriteString("(?:")
			i += 2
		case fold && c < 0x80 && unicode.IsLetter(c):
			out.WriteString("[" + string(unicode.ToLower(c)) + string(unicode.ToUpper(c)) + "]")
		default:
			out.WriteRune(c)
		}
	}
	return regexp.MustCompile(out.String())
}

// jsTrim, jsTrimStart and jsTrimEnd are String.prototype.trim and friends.
func jsTrim(s string) string      { return strings.TrimFunc(s, jsv.IsSpace) }
func jsTrimStart(s string) string { return strings.TrimLeftFunc(s, jsv.IsSpace) }
func jsTrimEnd(s string) string   { return strings.TrimRightFunc(s, jsv.IsSpace) }

// jsUpper is String.prototype.toUpperCase for unit-form text, including the
// one-to-many mappings of SpecialCasing.txt.
func jsUpper(s string) string {
	var b strings.Builder
	for _, r := range s {
		if m, ok := upperSpecial[r]; ok {
			b.WriteString(m)
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

var upperSpecial = map[rune]string{
	0xDF: "SS", 0x149: "\u02BCN", 0x1F0: "J\u030C", 0x390: "\u0399\u0308\u0301", 0x3B0: "\u03A5\u0308\u0301",
	0x587: "\u0535\u0552", 0x1E96: "H\u0331", 0x1E97: "T\u0308", 0x1E98: "W\u030A", 0x1E99: "Y\u030A",
	0x1E9A: "A\u02BE", 0xFB00: "FF", 0xFB01: "FI", 0xFB02: "FL", 0xFB03: "FFI", 0xFB04: "FFL", 0xFB05: "ST",
	0xFB06: "ST", 0xFB13: "\u0544\u0546", 0xFB14: "\u0544\u0535", 0xFB15: "\u0544\u053B", 0xFB16: "\u054E\u0546",
	0xFB17: "\u0544\u053D",
}
