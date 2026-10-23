package main

// JavaScript runtime behaviour the TypeScript server relies on.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// decodeUTF8 is TextDecoder("utf-8") as Node uses it for child output: each maximal ill-formed
// subsequence becomes one U+FFFD.
func decodeUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	for i := 0; i < len(data); {
		c := data[i]
		if c < 0x80 {
			b.WriteByte(c)
			i++
			continue
		}
		need, lo, hi := 0, byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lo = 2, 0xA0
		case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
			need = 2
		case c == 0xED:
			need, hi = 2, 0x9F
		case c == 0xF0:
			need, lo = 3, 0x90
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		case c == 0xF4:
			need, hi = 3, 0x8F
		default:
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		for k := 0; k < need; k++ {
			if j >= len(data) || data[j] < lo || data[j] > hi {
				break
			}
			lo, hi = 0x80, 0xBF
			j++
		}
		if j-i == need+1 {
			b.Write(data[i:j])
		} else {
			b.WriteRune(utf8.RuneError)
		}
		i = j
	}
	return b.String()
}

// mathRound is Math.round: the nearest integer, halves toward +Infinity.
func mathRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

// percent is Math.round((part / total) * 1000) / 10, or 0 when total is 0.
func percent(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return mathRound(float64(part)/float64(total)*1000) / 10
}

// isoString is new Date(ms).toISOString(); ok is false for an invalid date.
func isoString(ms float64) (string, bool) {
	if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
		return "", false
	}
	t := time.UnixMilli(int64(ms)).UTC()
	year := t.Year()
	var y string
	switch {
	case year >= 0 && year <= 9999:
		y = fmt.Sprintf("%04d", year)
	case year < 0:
		y = fmt.Sprintf("-%06d", -year)
	default:
		y = fmt.Sprintf("+%06d", year)
	}
	return y + t.Format("-01-02T15:04:05.000Z"), true
}

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
// cases U+0130 and final sigma.
func toLowerCase(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		switch r {
		case 0x130:
			b.WriteString("i\u0307")
			continue
		case 0x3A3:
			if finalSigma(runes, i) {
				b.WriteRune(0x3C2)
			} else {
				b.WriteRune(0x3C3)
			}
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
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
