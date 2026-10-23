package main

// The part of the WHATWG URL parser behind `new URL(text).pathname` with no base URL:
// whether the text parses, and the pathname it gets. Hosts are validated (IPv4, IPv6,
// ports, forbidden code points, punycode labels) but not serialised, because the server
// only reads the pathname.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

var specialSchemes = map[string]bool{"ftp": true, "file": true, "http": true, "https": true, "ws": true, "wss": true}

// urlPathname returns the pathname of text parsed as an absolute URL, or false when
// `new URL(text)` throws. text is in unit form.
func urlPathname(text string) (string, bool) {
	input := []rune(toScalarValues(strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.TrimFunc(text, func(r rune) bool { return r <= 0x20 }))))
	if len(input) == 0 || !isASCIIAlpha(input[0]) {
		return "", false
	}
	colon := -1
	for i, r := range input {
		if r == ':' {
			colon = i
			break
		}
		if !isASCIIAlpha(r) && !(r >= '0' && r <= '9') && r != '+' && r != '-' && r != '.' {
			return "", false
		}
	}
	if colon < 0 {
		return "", false
	}
	scheme := strings.ToLower(string(input[:colon]))
	rest := input[colon+1:]
	special := specialSchemes[scheme]
	isSlash := func(r rune) bool { return r == '/' || (special && r == '\\') }
	switch {
	case scheme == "file":
		return fileURLPath(rest)
	case special:
		for len(rest) > 0 && isSlash(rest[0]) {
			rest = rest[1:]
		}
		path, ok := authority(rest, true)
		if !ok {
			return "", false
		}
		if len(path) > 0 && isSlash(path[0]) {
			path = path[1:]
		}
		return parsePath(path, true, false), true
	case len(rest) >= 2 && rest[0] == '/' && rest[1] == '/':
		path, ok := authority(rest[2:], false)
		if !ok {
			return "", false
		}
		if len(path) == 0 || path[0] != '/' {
			return "", true
		}
		return parsePath(path[1:], false, false), true
	case len(rest) >= 1 && rest[0] == '/':
		return parsePath(rest[1:], false, false), true
	}
	var b strings.Builder
	for _, r := range rest {
		if r == '?' || r == '#' {
			break
		}
		percentEncode(&b, r, false)
	}
	return b.String(), true
}

// toScalarValues turns unit form into code points, unpaired surrogates becoming U+FFFD
// as the conversion to a USVString does.
func toScalarValues(s string) string {
	var b strings.Builder
	units := []rune(s)
	for i := 0; i < len(units); i++ {
		r := units[i]
		if r >= 0xD800+surrogateRune && r <= 0xDFFF+surrogateRune {
			r -= surrogateRune
			if r <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00+surrogateRune && units[i+1] <= 0xDFFF+surrogateRune {
				r = 0x10000 + (r-0xD800)<<10 + (units[i+1] - surrogateRune - 0xDC00)
				i++
			} else {
				r = utf8.RuneError
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isASCIIAlpha(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

func fileURLPath(rest []rune) (string, bool) {
	isSlash := func(r rune) bool { return r == '/' || r == '\\' }
	if len(rest) >= 2 && isSlash(rest[0]) && isSlash(rest[1]) {
		rest = rest[2:]
		end := len(rest)
		for i, r := range rest {
			if isSlash(r) || r == '?' || r == '#' {
				end = i
				break
			}
		}
		host := string(rest[:end])
		if isDriveLetter(host) {
			return parsePath(rest, true, true), true
		}
		if host != "" && host != "localhost" && !validHost(host, true) {
			return "", false
		}
		rest = rest[end:]
		if len(rest) > 0 && isSlash(rest[0]) {
			rest = rest[1:]
		}
		return parsePath(rest, true, true), true
	}
	if len(rest) > 0 && isSlash(rest[0]) {
		rest = rest[1:]
	}
	return parsePath(rest, true, true), true
}

func isDriveLetter(s string) bool {
	r := []rune(s)
	return len(r) == 2 && isASCIIAlpha(r[0]) && (r[1] == ':' || r[1] == '|')
}

// parsePath runs the path state over in (the input after the path's leading slash) and
// returns the pathname.
func parsePath(in []rune, special, file bool) string {
	var segments []string
	var buf strings.Builder
	flush := func(atSlash bool) {
		seg := buf.String()
		buf.Reset()
		lower := strings.ToLower(seg)
		switch {
		case lower == ".." || lower == ".%2e" || lower == "%2e." || lower == "%2e%2e":
			if len(segments) > 0 && !(file && len(segments) == 1 && isDriveLetter(segments[0]) && segments[0][1] == ':') {
				segments = segments[:len(segments)-1]
			}
			if !atSlash {
				segments = append(segments, "")
			}
		case lower == "." || lower == "%2e":
			if !atSlash {
				segments = append(segments, "")
			}
		default:
			if file && len(segments) == 0 && isDriveLetter(seg) {
				seg = seg[:1] + ":"
			}
			segments = append(segments, seg)
		}
	}
	for _, r := range in {
		if r == '?' || r == '#' {
			break
		}
		if r == '/' || (special && r == '\\') {
			flush(true)
			continue
		}
		percentEncode(&buf, r, true)
	}
	flush(false)
	return "/" + strings.Join(segments, "/")
}

// percentEncode writes r with the path percent-encode set (path) or the C0 control
// percent-encode set.
func percentEncode(b *strings.Builder, r rune, path bool) {
	if r > 0x20 && r < 0x7f && !(path && strings.ContainsRune("\"#<>?`{}", r)) {
		b.WriteRune(r)
		return
	}
	if r == ' ' && !path {
		b.WriteRune(r)
		return
	}
	var buf [4]byte
	for _, c := range buf[:utf8.EncodeRune(buf[:], r)] {
		fmt.Fprintf(b, "%%%02X", c)
	}
}
