package main

// Domain to ASCII for special-scheme hosts.

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// domainToASCII approximates UTS #46 processing with the URL standard's options: case
// folding, fullwidth forms and ideographic full stops mapped, disallowed characters and
// broken punycode rejected.
func domainToASCII(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(unicode.ToLower(r))
		case r >= 0xFF01 && r <= 0xFF5E:
			b.WriteRune(unicode.ToLower(r - 0xFEE0))
		case r == 0x3002 || r == 0xFF61:
			b.WriteRune('.')
		case r == utf8.RuneError || unicode.In(r, unicode.Cc, unicode.Co, unicode.Cs, unicode.Zs, unicode.Zl, unicode.Zp):
			return "", false
		case !unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Cf):
			return "", false
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	out := b.String()
	for _, label := range strings.Split(out, ".") {
		if strings.HasPrefix(label, "xn--") {
			if _, ok := punycodeDecode(label[4:]); !ok {
				return "", false
			}
		}
	}
	return out, true
}

func punycodeDecode(s string) ([]rune, bool) {
	const base, tMin, tMax, skew, damp = 36, 1, 26, 38, 700
	var out []rune
	if k := strings.LastIndexByte(s, '-'); k >= 0 {
		for _, r := range s[:k] {
			if r >= 0x80 {
				return nil, false
			}
			out = append(out, r)
		}
		s = s[k+1:]
	}
	n, bias, i := 128, 72, 0
	for pos := 0; pos < len(s); {
		oldi, w := i, 1
		for k := base; ; k += base {
			if pos >= len(s) {
				return nil, false
			}
			c := s[pos]
			pos++
			var digit int
			switch {
			case c >= '0' && c <= '9':
				digit = int(c-'0') + 26
			case c >= 'a' && c <= 'z':
				digit = int(c - 'a')
			case c >= 'A' && c <= 'Z':
				digit = int(c - 'A')
			default:
				return nil, false
			}
			i += digit * w
			if i > 0x10FFFF*4 {
				return nil, false
			}
			t := k - bias
			if t < tMin {
				t = tMin
			} else if t > tMax {
				t = tMax
			}
			if digit < t {
				break
			}
			w *= base - t
		}
		length := len(out) + 1
		delta := i - oldi
		if oldi == 0 {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / length
		k := 0
		for delta > ((base-tMin)*tMax)/2 {
			delta /= base - tMin
			k += base
		}
		bias = k + (base-tMin+1)*delta/(delta+skew)
		n += i / length
		i %= length
		if n > 0x10FFFF {
			return nil, false
		}
		out = append(out[:i], append([]rune{rune(n)}, out[i:]...)...)
		i++
	}
	return out, true
}
