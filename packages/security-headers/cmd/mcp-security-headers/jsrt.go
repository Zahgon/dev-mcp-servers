package main

// JavaScript runtime behaviour the TypeScript server relies on.

import (
	"math"
	"regexp"
	"strconv"
	"unicode"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var decimalLiteral = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// jsNumber is Number(s) for the decimal strings git prints: blank is 0, a decimal literal is its
// value, Infinity is infinite, anything else is NaN.
func jsNumber(s string) float64 {
	t := jsv.Trim(s)
	switch t {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if !decimalLiteral.MatchString(t) {
		return math.NaN()
	}
	// Out-of-range literals come back as an infinity or zero, as in JavaScript.
	f, _ := strconv.ParseFloat(t, 64)
	return f
}

// splitSpace is s.split(/\s+/).
func splitSpace(s string) []string {
	var out []string
	start := 0
	inSpace := false
	for i, r := range s {
		switch {
		case jsv.IsSpace(r) && !inSpace:
			out = append(out, s[start:i])
			inSpace = true
		case !jsv.IsSpace(r) && inSpace:
			start = i
			inSpace = false
		}
	}
	if inSpace {
		return append(out, "")
	}
	return append(out, s[start:])
}

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) || unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', 0xA8, 0xAD, 0xAF, 0xB4, 0xB7, 0xB8, 0x2018, 0x2019, 0x2024, 0x2027:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// toLowerCase is String.prototype.toLowerCase: Unicode default lower-casing with the special
// cases U+0130 and final sigma. Lone surrogates pass through unchanged.
func toLowerCase(s string) string {
	runes := wtf8Runes(s)
	var b []byte
	for i, r := range runes {
		switch r {
		case 0x130:
			b = append(b, "i̇"...)
			continue
		case 0x3A3:
			if finalSigma(runes, i) {
				b = jsv.AppendWTF8(b, 0x3C2)
			} else {
				b = jsv.AppendWTF8(b, 0x3C3)
			}
			continue
		}
		if r >= 0xD800 && r <= 0xDFFF {
			b = jsv.AppendWTF8(b, r)
			continue
		}
		b = jsv.AppendWTF8(b, unicode.ToLower(r))
	}
	return string(b)
}

// wtf8Runes decodes a JavaScript string held as WTF-8 into code points, lone surrogates included.
func wtf8Runes(s string) []rune {
	var out []rune
	for i := 0; i < len(s); {
		r, n := jsv.DecodeWTF8(s, i)
		out = append(out, r)
		i += n
	}
	return out
}

func finalSigma(runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		before = isCased(runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		return !isCased(runes[j])
	}
	return true
}

// toUpperCase is String.prototype.toUpperCase for the comparisons this server makes: the simple
// Unicode mappings plus the unconditional special casings that expand to several letters.
func toUpperCase(s string) string {
	var b []byte
	for _, r := range wtf8Runes(s) {
		switch exp, special := upperSpecial[r]; {
		case special:
			b = append(b, exp...)
		case r >= 0xD800 && r <= 0xDFFF:
			b = jsv.AppendWTF8(b, r)
		default:
			b = jsv.AppendWTF8(b, unicode.ToUpper(r))
		}
	}
	return string(b)
}

var upperSpecial = map[rune]string{
	0xDF: "SS", 0x149: "ʼN", 0x1F0: "J̌", 0x390: "Ϊ́", 0x3B0: "Ϋ́", 0x587: "ԵՒ",
	0x1E96: "H̱", 0x1E97: "T̈", 0x1E98: "W̊", 0x1E99: "Y̊", 0x1E9A: "Aʾ",
	0xFB00: "FF", 0xFB01: "FI", 0xFB02: "FL", 0xFB03: "FFI", 0xFB04: "FFL", 0xFB05: "ST", 0xFB06: "ST",
}
