package main

// Punycode (RFC 3492) for IDNA labels.

import "strings"

const (
	pcBase        = 36
	pcTMin        = 1
	pcTMax        = 26
	pcSkew        = 38
	pcDamp        = 700
	pcInitialBias = 72
	pcInitialN    = 128
	pcMaxInt      = 1<<31 - 1
)

func pcAdapt(delta, numPoints int, first bool) int {
	if first {
		delta /= pcDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	for delta > ((pcBase-pcTMin)*pcTMax)/2 {
		delta /= pcBase - pcTMin
		k += pcBase
	}
	return k + (pcBase-pcTMin+1)*delta/(delta+pcSkew)
}

func pcDigit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

func pcThreshold(k, bias int) int {
	switch {
	case k <= bias:
		return pcTMin
	case k >= bias+pcTMax:
		return pcTMax
	}
	return k - bias
}

func punycodeEncode(input []rune) (string, bool) {
	var out strings.Builder
	for _, r := range input {
		if r < 0x80 {
			out.WriteRune(r)
		}
	}
	basic := out.Len()
	h := basic
	if basic > 0 {
		out.WriteByte('-')
	}
	n, delta, bias := pcInitialN, 0, pcInitialBias
	for h < len(input) {
		m := pcMaxInt
		for _, r := range input {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		if (m - n) > (pcMaxInt-delta)/(h+1) {
			return "", false
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range input {
			if int(r) < n {
				delta++
				if delta > pcMaxInt {
					return "", false
				}
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := pcBase; ; k += pcBase {
				t := pcThreshold(k, bias)
				if q < t {
					break
				}
				out.WriteByte(pcDigit(t + (q-t)%(pcBase-t)))
				q = (q - t) / (pcBase - t)
			}
			out.WriteByte(pcDigit(q))
			bias = pcAdapt(delta, h+1, h == basic)
			delta = 0
			h++
		}
		delta++
		n++
	}
	return out.String(), true
}

func punycodeDecode(s string) ([]rune, bool) {
	var out []rune
	pos := 0
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		for _, r := range s[:i] {
			if r >= 0x80 {
				return nil, false
			}
			out = append(out, r)
		}
		pos = i + 1
	}
	n, i, bias := pcInitialN, 0, pcInitialBias
	for pos < len(s) {
		oldi, w := i, 1
		for k := pcBase; ; k += pcBase {
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
			if digit > (pcMaxInt-i)/w {
				return nil, false
			}
			i += digit * w
			t := pcThreshold(k, bias)
			if digit < t {
				break
			}
			if w > pcMaxInt/(pcBase-t) {
				return nil, false
			}
			w *= pcBase - t
		}
		count := len(out) + 1
		bias = pcAdapt(i-oldi, count, oldi == 0)
		if i/count > pcMaxInt-n {
			return nil, false
		}
		n += i / count
		i %= count
		if n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
			return nil, false
		}
		out = append(out[:i], append([]rune{rune(n)}, out[i:]...)...)
		i++
	}
	return out, true
}
