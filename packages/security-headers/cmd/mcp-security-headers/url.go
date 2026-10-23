package main

// A WHATWG URL parser, as Node's URL class implements it: enough of the standard to parse,
// resolve against a base and serialize every URL the fetcher handles, with the same failures.

import (
	"errors"
	"strconv"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var errInvalidURL = errors.New("Invalid URL")

var specialPorts = map[string]string{"ftp": "21", "file": "", "http": "80", "https": "443", "ws": "80", "wss": "443"}

// webURL is a parsed URL record.
type webURL struct {
	scheme   string
	username string
	password string
	host     *string
	port     string
	path     []string
	opaque   *string
	query    *string
	fragment *string
}

func (u *webURL) special() bool { _, ok := specialPorts[u.scheme]; return ok }

func (u *webURL) protocol() string { return u.scheme + ":" }

func (u *webURL) hostname() string {
	if u.host == nil {
		return ""
	}
	return *u.host
}

// pathString is the URL path serializer.
func (u *webURL) pathString() string {
	if u.opaque != nil {
		return *u.opaque
	}
	var b strings.Builder
	for _, seg := range u.path {
		b.WriteString("/" + seg)
	}
	return b.String()
}

// href is the URL serializer.
func (u *webURL) href() string {
	var b strings.Builder
	b.WriteString(u.scheme + ":")
	if u.host != nil {
		b.WriteString("//")
		if u.username != "" || u.password != "" {
			b.WriteString(u.username)
			if u.password != "" {
				b.WriteString(":" + u.password)
			}
			b.WriteString("@")
		}
		b.WriteString(*u.host)
		if u.port != "" {
			b.WriteString(":" + u.port)
		}
	} else if u.opaque == nil && len(u.path) > 1 && u.path[0] == "" {
		b.WriteString("/.")
	}
	b.WriteString(u.pathString())
	if u.query != nil {
		b.WriteString("?" + *u.query)
	}
	if u.fragment != nil {
		b.WriteString("#" + *u.fragment)
	}
	return b.String()
}

// origin is the ASCII serialization of the URL's origin for http(s), ws(s) and ftp.
func (u *webURL) origin() string {
	switch u.scheme {
	case "http", "https", "ws", "wss", "ftp":
		o := u.scheme + "://" + u.hostname()
		if u.port != "" {
			o += ":" + u.port
		}
		return o
	}
	return "null"
}

type encodeSet func(r rune) bool

func c0Set(r rune) bool       { return r < 0x20 || r > 0x7E }
func fragmentSet(r rune) bool { return c0Set(r) || strings.ContainsRune(" \"<>`", r) }
func querySet(r rune) bool    { return c0Set(r) || strings.ContainsRune(" \"#<>", r) }
func specialQuerySet(r rune) bool {
	return querySet(r) || r == '\''
}
func pathSet(r rune) bool     { return querySet(r) || strings.ContainsRune("?`{}", r) }
func userinfoSet(r rune) bool { return pathSet(r) || strings.ContainsRune("/:;=@[\\]^|", r) }

func percentEncode(b *strings.Builder, r rune, set encodeSet) {
	if !set(r) {
		b.WriteRune(r)
		return
	}
	var buf [4]byte
	n := encodeRune(buf[:], r)
	for _, c := range buf[:n] {
		b.WriteString("%" + strings.ToUpper(strconv.FormatUint(uint64(c)|0x100, 16)[1:]))
	}
}

func encodeRune(p []byte, r rune) int {
	s := string(r)
	return copy(p, s)
}

func isASCIIAlpha(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }
func isASCIIHex(r rune) bool   { return isASCIIDigit(r) || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' }
func isASCIIAlnum(r rune) bool { return isASCIIAlpha(r) || isASCIIDigit(r) }
func lowerASCII(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

// usvRunes turns a JavaScript string into the code points the URL parser sees: lone surrogates
// become U+FFFD, leading and trailing C0 controls and spaces are removed, tab and newlines go.
func usvRunes(s string) []rune {
	var rs []rune
	for i := 0; i < len(s); {
		r, n := jsv.DecodeWTF8(s, i)
		i += n
		if r >= 0xD800 && r <= 0xDFFF {
			r = 0xFFFD
		}
		rs = append(rs, r)
	}
	start, end := 0, len(rs)
	for start < end && rs[start] <= 0x20 {
		start++
	}
	for end > start && rs[end-1] <= 0x20 {
		end--
	}
	out := make([]rune, 0, end-start)
	for _, r := range rs[start:end] {
		if r != '\t' && r != '\n' && r != '\r' {
			out = append(out, r)
		}
	}
	return out
}

func isWindowsDriveLetter(rs []rune, normalizedOnly bool) bool {
	if len(rs) != 2 || !isASCIIAlpha(rs[0]) {
		return false
	}
	return rs[1] == ':' || (!normalizedOnly && rs[1] == '|')
}

func startsWithDriveLetter(rs []rune) bool {
	if len(rs) < 2 || !isWindowsDriveLetter(rs[:2], false) {
		return false
	}
	return len(rs) == 2 || strings.ContainsRune("/\\?#", rs[2])
}

func isSingleDot(s string) bool {
	return s == "." || strings.EqualFold(s, "%2e")
}

func isDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

func (u *webURL) shortenPath() {
	if u.scheme == "file" && len(u.path) == 1 && isWindowsDriveLetter([]rune(u.path[0]), true) {
		return
	}
	if len(u.path) > 0 {
		u.path = u.path[:len(u.path)-1]
	}
}

func strPtr(s string) *string { return &s }

// parseURL is new URL(input, base) without the base when base is nil.
func parseURL(input string, base *webURL) (*webURL, error) {
	p := &urlParser{in: usvRunes(input), base: base, u: &webURL{}}
	if err := p.run(); err != nil {
		return nil, err
	}
	return p.u, nil
}
