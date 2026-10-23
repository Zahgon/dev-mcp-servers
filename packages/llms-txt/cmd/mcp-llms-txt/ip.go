package main

// The URL standard's IPv4 and IPv6 address parsers, reporting only whether an address parses.

import (
	"strconv"
	"strings"
)

func endsInNumber(domain string) bool {
	labels := strings.Split(domain, ".")
	if labels[len(labels)-1] == "" {
		if len(labels) == 1 {
			return false
		}
		labels = labels[:len(labels)-1]
	}
	last := labels[len(labels)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	if strings.HasPrefix(last, "0x") || strings.HasPrefix(last, "0X") {
		return strings.Trim(last[2:], "0123456789abcdefABCDEF") == ""
	}
	return false
}

func validIPv4(domain string) bool {
	parts := strings.Split(domain, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return false
	}
	var numbers []uint64
	for _, p := range parts {
		if p == "" {
			return false
		}
		radix := 10
		switch {
		case strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X"):
			radix, p = 16, p[2:]
		case len(p) >= 2 && p[0] == '0':
			radix, p = 8, p[1:]
		}
		if p == "" {
			numbers = append(numbers, 0)
			continue
		}
		v, err := strconv.ParseUint(p, radix, 64)
		if err != nil {
			if ne, isNum := err.(*strconv.NumError); isNum && ne.Err == strconv.ErrRange {
				v = 1 << 40
			} else {
				return false
			}
		}
		numbers = append(numbers, v)
	}
	for _, v := range numbers[:len(numbers)-1] {
		if v > 255 {
			return false
		}
	}
	limit := uint64(1) << (8 * (5 - len(numbers)))
	return numbers[len(numbers)-1] < limit
}

// validIPv6 is the URL standard's IPv6 parser, reporting only success.
func validIPv6(s string) bool {
	in := []rune(s)
	pieces, compress, p := 0, -1, 0
	if p < len(in) && in[p] == ':' {
		if p+1 >= len(in) || in[p+1] != ':' {
			return false
		}
		p += 2
		pieces++
		compress = pieces
	}
	for p < len(in) {
		if pieces == 8 {
			return false
		}
		if in[p] == ':' {
			if compress >= 0 {
				return false
			}
			p++
			pieces++
			compress = pieces
			continue
		}
		length := 0
		for length < 4 && p < len(in) && in[p] < 0x80 && isHex(byte(in[p])) {
			p++
			length++
		}
		if p < len(in) && in[p] == '.' {
			if length == 0 || pieces > 6 {
				return false
			}
			p -= length
			return validIPv4InIPv6(in[p:]) && (pieces+2 == 8 || compress >= 0)
		}
		if p < len(in) && in[p] == ':' {
			p++
			if p >= len(in) {
				return false
			}
		} else if p < len(in) {
			return false
		}
		pieces++
	}
	return compress >= 0 || pieces == 8
}

func validIPv4InIPv6(in []rune) bool {
	seen := 0
	for p := 0; p < len(in); {
		if seen > 0 {
			if in[p] != '.' || seen >= 4 {
				return false
			}
			p++
		}
		if p >= len(in) || in[p] < '0' || in[p] > '9' {
			return false
		}
		value := -1
		for p < len(in) && in[p] >= '0' && in[p] <= '9' {
			if value == 0 {
				return false
			}
			d := int(in[p] - '0')
			if value < 0 {
				value = d
			} else {
				value = value*10 + d
			}
			if value > 255 {
				return false
			}
			p++
		}
		seen++
	}
	return seen == 4
}
