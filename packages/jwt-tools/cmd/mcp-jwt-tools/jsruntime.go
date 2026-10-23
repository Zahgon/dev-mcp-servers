package main

// JavaScript and Node runtime behaviour the JWT code relies on: Buffer base64 and
// UTF-8 decoding, Number#toFixed, Math.round, Date#toISOString and the
// non-unicode case-insensitive RegExp used to spot sensitive claim names.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// errInvalidTime is the RangeError toISOString throws for an invalid Date.
var errInvalidTime = errors.New("Invalid time value")

// base64urlDecode is Buffer.from(padded, "base64") for text already checked to hold only
// base64url letters: four characters give three bytes, a trailing three give two, a
// trailing two give one and a trailing single character gives nothing.
func base64urlDecode(s string) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, len(s)*3/4)
	var acc uint32
	bits := 0
	for i := 0; i < len(s); i++ {
		acc = acc<<6 | uint32(strings.IndexByte(alphabet, s[i]))
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
		}
	}
	return out
}

func base64urlEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// decodeUTF8 is Buffer#toString("utf8"): every maximal invalid subpart becomes U+FFFD,
// as the WHATWG decoder does.
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
		need, lower, upper := 0, byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lower = 2, 0xA0
		case c == 0xED:
			need, upper = 2, 0x9F
		case c >= 0xE1 && c <= 0xEF:
			need = 2
		case c == 0xF0:
			need, lower = 3, 0x90
		case c == 0xF4:
			need, upper = 3, 0x8F
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		default:
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		valid := true
		for k := 0; k < need; k++ {
			if j >= len(data) || data[j] < lower || data[j] > upper {
				valid = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if valid {
			b.Write(data[i:j])
		} else {
			b.WriteRune(utf8.RuneError)
		}
		i = j
	}
	return b.String()
}

// encodeUTF8 is Buffer.from(s, "utf8"): a lone surrogate becomes U+FFFD.
func encodeUTF8(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, n := jsv.DecodeWTF8(s, i)
		i += n
		if r >= 0xD800 && r <= 0xDFFF {
			r = utf8.RuneError
		}
		out = utf8.AppendRune(out, r)
	}
	return out
}

// jsRound is Math.round.
func jsRound(x float64) float64 {
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

// toFixed1 is Number#toFixed(1) for a non-negative finite x: the exact binary value is
// rounded, and a tie goes to the larger neighbour.
func toFixed1(x float64) string {
	if x >= 1e21 {
		return jsv.FormatNumber(x)
	}
	v := new(big.Float).SetPrec(2000).SetFloat64(x)
	v.Mul(v, big.NewFloat(10))
	v.Add(v, big.NewFloat(0.5))
	n, _ := v.Int(nil)
	if v.Sign() < 0 {
		n.SetInt64(0)
	}
	digits := n.String()
	if len(digits) < 2 {
		digits = "0" + digits
	}
	return digits[:len(digits)-1] + "." + digits[len(digits)-1:]
}

// isoSeconds is new Date(seconds * 1000).toISOString().
func isoSeconds(seconds float64) (string, error) {
	ms := seconds * 1000
	if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
		return "", errInvalidTime
	}
	t := time.UnixMilli(int64(ms)).UTC()
	y := t.Year()
	var year string
	switch {
	case y >= 0 && y <= 9999:
		year = fmt.Sprintf("%04d", y)
	case y < 0:
		year = fmt.Sprintf("-%06d", -y)
	default:
		year = fmt.Sprintf("+%06d", y)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", year, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e6), nil
}

var sensitiveClaim = regexp.MustCompile(`password|passwd|secret|ssn|social.?security|credit.?card|card.?number|cvv|private.?key|api.?key`)

// isSensitiveClaim is SENSITIVE_CLAIM.test(name) for the JavaScript pattern with the i
// flag and no u flag: it works on UTF-16 code units, only ASCII letters fold, and "."
// matches any unit but a line terminator. Each non-ASCII unit becomes U+FFFD (still
// matched by ".") so that the Go pattern sees one rune per unit.
func isSensitiveClaim(name string) bool {
	var b strings.Builder
	for _, u := range jsv.UTF16Units(name) {
		switch {
		case u == '\n', u == '\r', u == 0x2028, u == 0x2029:
			b.WriteByte('\n')
		case u >= 'A' && u <= 'Z':
			b.WriteByte(byte(u) + 'a' - 'A')
		case u < 0x80:
			b.WriteByte(byte(u))
		default:
			b.WriteRune(utf8.RuneError)
		}
	}
	return sensitiveClaim.MatchString(b.String())
}

// asciiLower is String#toLowerCase for the comparison with "none", where only the ASCII
// letters can produce a match.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}
