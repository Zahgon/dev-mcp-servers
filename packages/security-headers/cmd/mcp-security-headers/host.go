package main

// WHATWG host parsing: IPv6 and IPv4 literals, opaque hosts and domains.

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func isForbiddenHost(r rune) bool {
	switch r {
	case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}

func isForbiddenDomain(r rune) bool {
	return isForbiddenHost(r) || r <= 0x1F || r == '%' || r == 0x7F
}

// parseHost is the host parser; isOpaque is true for non-special schemes.
func parseHost(input []rune, isOpaque bool) (string, error) {
	if len(input) > 0 && input[0] == '[' {
		if input[len(input)-1] != ']' {
			return "", errInvalidURL
		}
		pieces, err := parseIPv6(input[1 : len(input)-1])
		if err != nil {
			return "", err
		}
		return "[" + serializeIPv6(pieces) + "]", nil
	}
	if isOpaque {
		var b strings.Builder
		for _, r := range input {
			if isForbiddenHost(r) {
				return "", errInvalidURL
			}
			percentEncode(&b, r, c0Set)
		}
		return b.String(), nil
	}
	domain := percentDecodeUTF8(string(input))
	ascii, err := domainToASCII(domain)
	if err != nil {
		return "", err
	}
	for _, r := range ascii {
		if isForbiddenDomain(r) {
			return "", errInvalidURL
		}
	}
	if endsInNumber(ascii) {
		v4, err := parseIPv4(ascii)
		if err != nil {
			return "", err
		}
		return serializeIPv4(v4), nil
	}
	return ascii, nil
}

// percentDecodeUTF8 percent-decodes s and decodes the bytes as UTF-8, invalid sequences as U+FFFD.
func percentDecodeUTF8(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isASCIIHex(rune(s[i+1])) && isASCIIHex(rune(s[i+2])) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			out = append(out, byte(v))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	if utf8.Valid(out) {
		return string(out)
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}

// domainToASCII is UTS #46 ToASCII as the URL standard calls it (CheckHyphens false, not
// transitional), without the full NFKC tables: ASCII is lower-cased and xn-- labels must decode to
// mapped text; elsewhere the simple Unicode lower-case mapping applies, plus the mappings
// of the dot variants, fullwidth ASCII, the Latin ligatures and the space variants, the ignored
// code points are dropped, joiners fail, and labels holding non-ASCII are Punycode-encoded.
func domainToASCII(domain string) (string, error) {
	var mapped strings.Builder
	for _, r := range domain {
		switch r {
		case 0x3002, 0xFF0E, 0xFF61:
			mapped.WriteRune('.')
		case 0xAD, 0x200B, 0x2060, 0xFEFF, 0x034F, 0x180B, 0x180C, 0x180D:
		case 0xFFFD, 0x200C, 0x200D:
			return "", errInvalidURL
		case 0xA0, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A, 0x202F, 0x205F, 0x3000:
			mapped.WriteRune(' ')
		case 0xFB00, 0xFB01, 0xFB02, 0xFB03, 0xFB04, 0xFB05, 0xFB06:
			mapped.WriteString([]string{"ff", "fi", "fl", "ffi", "ffl", "st", "st"}[r-0xFB00])
		case 0x130:
			mapped.WriteString("i̇")
		default:
			if r >= 0xFF01 && r <= 0xFF5E {
				r -= 0xFEE0
			}
			if r < 0x80 {
				mapped.WriteRune(lowerASCII(r))
			} else {
				mapped.WriteRune(unicode.ToLower(r))
			}
		}
	}
	labels := strings.Split(mapped.String(), ".")
	for i, label := range labels {
		ascii := true
		for _, r := range label {
			ascii = ascii && r < 0x80
		}
		switch {
		case !ascii && strings.HasPrefix(label, "xn--"):
			return "", errInvalidURL
		case !ascii:
			enc, ok := punycodeEncode([]rune(label))
			if !ok {
				return "", errInvalidURL
			}
			labels[i] = "xn--" + enc
		case strings.HasPrefix(label, "xn--"):
			dec, ok := punycodeDecode(label[4:])
			if !ok || len(dec) == 0 {
				return "", errInvalidURL
			}
			for _, r := range dec {
				// A decoded label must already be in mapped form: no controls, no upper case.
				if !unicode.IsGraphic(r) || unicode.ToLower(r) != r {
					return "", errInvalidURL
				}
			}
		}
	}
	out := strings.Join(labels, ".")
	if out == "" {
		return "", errInvalidURL
	}
	return out, nil
}

func endsInNumber(s string) bool {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.IndexFunc(last, func(r rune) bool { return !isASCIIDigit(r) }) < 0 {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// ipv4Big caps parsed numbers: anything at or above it is out of range for every check.
const ipv4Big = 1 << 40

func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	radix := uint64(10)
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, radix = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, radix = s[1:], 8
	}
	var n uint64
	for _, r := range s {
		var d uint64
		switch {
		case isASCIIDigit(r):
			d = uint64(r - '0')
		case r >= 'a' && r <= 'f':
			d = uint64(r-'a') + 10
		case r >= 'A' && r <= 'F':
			d = uint64(r-'A') + 10
		default:
			return 0, false
		}
		if d >= radix {
			return 0, false
		}
		if n < ipv4Big {
			n = n*radix + d
		}
	}
	return n, true
}

func parseIPv4(s string) (uint64, error) {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return 0, errInvalidURL
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := parseIPv4Number(p)
		if !ok {
			return 0, errInvalidURL
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return 0, errInvalidURL
		}
	}
	last := nums[len(nums)-1]
	if last >= uint64(1)<<(8*(5-len(nums))) {
		return 0, errInvalidURL
	}
	v := last
	for i, n := range nums[:len(nums)-1] {
		v += n << (8 * (3 - i))
	}
	return v, nil
}

func serializeIPv4(v uint64) string {
	return strconv.FormatUint(v>>24, 10) + "." + strconv.FormatUint(v>>16&255, 10) + "." + strconv.FormatUint(v>>8&255, 10) + "." + strconv.FormatUint(v&255, 10)
}

func hexVal(r rune) uint16 {
	switch {
	case isASCIIDigit(r):
		return uint16(r - '0')
	case r >= 'a' && r <= 'f':
		return uint16(r-'a') + 10
	}
	return uint16(r-'A') + 10
}

// parseIPv6 is the IPv6 parser of the URL standard.
func parseIPv6(in []rune) ([8]uint16, error) {
	var pieces [8]uint16
	at := func(i int) rune {
		if i < len(in) {
			return in[i]
		}
		return eof
	}
	idx, compress, p := 0, -1, 0
	if at(p) == ':' {
		if at(p+1) != ':' {
			return pieces, errInvalidURL
		}
		p += 2
		idx++
		compress = idx
	}
	for at(p) != eof {
		if idx == 8 {
			return pieces, errInvalidURL
		}
		if at(p) == ':' {
			if compress != -1 {
				return pieces, errInvalidURL
			}
			p++
			idx++
			compress = idx
			continue
		}
		var value uint16
		length := 0
		for length < 4 && isASCIIHex(at(p)) {
			value = value*0x10 + hexVal(at(p))
			p++
			length++
		}
		if at(p) == '.' {
			if length == 0 {
				return pieces, errInvalidURL
			}
			p -= length
			if idx > 6 {
				return pieces, errInvalidURL
			}
			seen := 0
			for at(p) != eof {
				v4 := -1
				if seen > 0 {
					if at(p) != '.' || seen >= 4 {
						return pieces, errInvalidURL
					}
					p++
				}
				if !isASCIIDigit(at(p)) {
					return pieces, errInvalidURL
				}
				for isASCIIDigit(at(p)) {
					n := int(at(p) - '0')
					switch {
					case v4 == -1:
						v4 = n
					case v4 == 0:
						return pieces, errInvalidURL
					default:
						v4 = v4*10 + n
					}
					if v4 > 255 {
						return pieces, errInvalidURL
					}
					p++
				}
				pieces[idx] = pieces[idx]*0x100 + uint16(v4)
				seen++
				if seen == 2 || seen == 4 {
					idx++
				}
			}
			if seen != 4 {
				return pieces, errInvalidURL
			}
			break
		}
		if at(p) == ':' {
			p++
			if at(p) == eof {
				return pieces, errInvalidURL
			}
		} else if at(p) != eof {
			return pieces, errInvalidURL
		}
		pieces[idx] = value
		idx++
	}
	if compress != -1 {
		swaps := idx - compress
		idx = 7
		for idx != 0 && swaps > 0 {
			pieces[idx], pieces[compress+swaps-1] = pieces[compress+swaps-1], pieces[idx]
			idx--
			swaps--
		}
	} else if idx != 8 {
		return pieces, errInvalidURL
	}
	return pieces, nil
}

func serializeIPv6(pieces [8]uint16) string {
	best, bestLen := -1, 1
	for i := 0; i < 8; {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && pieces[j] == 0 {
			j++
		}
		if j-i > bestLen {
			best, bestLen = i, j-i
		}
		i = j
	}
	var b strings.Builder
	ignore0 := false
	for i := 0; i < 8; i++ {
		if ignore0 && pieces[i] == 0 {
			continue
		}
		ignore0 = false
		if i == best {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteString(":")
			}
			ignore0 = true
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
		if i != 7 {
			b.WriteString(":")
		}
	}
	return b.String()
}
