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

// unitPrefix is s.slice(0, n) for n >= 0.
func unitPrefix(s string, n int) string {
	r := []rune(s)
	if n >= len(r) {
		return s
	}
	return string(r[:n])
}

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

// jsTrim and jsTrimEnd are String.prototype.trim and trimEnd.
func jsTrim(s string) string    { return strings.TrimFunc(s, jsv.IsSpace) }
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, jsv.IsSpace) }

// jsLower is String.prototype.toLowerCase for unit-form text: Unicode simple lowercase
// mappings plus the unconditional İ mapping and the final-sigma rule of SpecialCasing.txt.
func jsLower(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		switch {
		case r == 0x130:
			b.WriteString("i\u0307")
		case r == 0x3A3 && finalSigma(runes, i):
			b.WriteRune(0x3C2)
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// finalSigma reports whether the capital sigma at i ends a word: a cased letter comes
// before it and none follows, skipping case-ignorable characters.
func finalSigma(runes []rune, i int) bool {
	ignorable := func(r rune) bool {
		return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) || r == '\'' || r == '.' || r == ':' || r == 0x2019
	}
	cased := func(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }
	before := false
	for k := i - 1; k >= 0; k-- {
		if ignorable(runes[k]) {
			continue
		}
		before = cased(runes[k])
		break
	}
	for k := i + 1; k < len(runes); k++ {
		if ignorable(runes[k]) {
			continue
		}
		return before && !cased(runes[k])
	}
	return before
}
