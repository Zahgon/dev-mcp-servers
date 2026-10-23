package ajv

// A port of fast-uri 3.1 (the URI resolver Ajv 8 uses): parse, serialize and resolve with
// its normalisation of scheme, host, port, path, query and fragment. Host names are not
// converted with IDNA; a host that would need it is reported in the result file.

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

type uriComp struct {
	scheme, userinfo, host, port, path, query, fragment *string
	portNum                                             bool // port holds a parsed number
	reference                                           string
	err                                                 string
	nid, nss, uuid                                      *string
	resourceName                                        *string
	secure                                              *bool
}

func sp(s string) *string { return &s }

var (
	uriParseRe       = regexp.MustCompile(`^(?:([^#/:?]+):)?(?://((?:([^#/?@]*)@)?(\[[^#/?\]]+\]|[^#/:?]*)(?::(\d*))?))?([^#?]*)(?:\?([^#]*))?(?:#((?s:[^\x{2028}\x{2029}])*))?`)
	authorityPrefix  = regexp.MustCompile(`^(?:[^#/:?]+:)?//([^/?#]*)`)
	introducerRegion = regexp.MustCompile(`^(?:[^#/:?]+:)?([/\\\t\n\r]*)`)
	validScheme      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)
	ipv4Re           = regexp.MustCompile(`^(?:(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]\d|\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]\d|\d)$`)
	hextetRe         = regexp.MustCompile(`^[\dA-Fa-f]{1,4}$`)
	ipvFutureRe      = regexp.MustCompile(`^[vV][\dA-Fa-f]+\.[A-Za-z\d\-._~!$&'()*+,;=:]+$`)
	urnRe            = regexp.MustCompile(`^([\da-zA-Z][\da-zA-Z-]{0,31}):((?:[\w!$'()*+,\-./:;=@]|%[\da-fA-F]{2})+)$`)
	uuidRe           = regexp.MustCompile(`^[\da-fA-F]{8}-[\da-fA-F]{4}-[\da-fA-F]{4}-[\da-fA-F]{4}-[\da-fA-F]{12}$`)
)

const malformedScheme = "URI scheme is malformed."

func isHexPair(s string) bool {
	return len(s) == 2 && isHex(s[0]) && isHex(s[1])
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isUnreserved(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' || c == '.' || c == '_' || c == '~'
}

func inSet(c uint16, set string) bool {
	return c < 0x80 && (c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || strings.IndexByte(set, byte(c)) >= 0)
}

const (
	pathChars     = "-._~!$&'()*+,;=:@/"
	queryChars    = "-._~!$&'()*+,;=:@/?"
	userinfoChars = "-._~!$&'()*+,;=:"
)

func isEscapeSafe(c uint16) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '*' || c == '+' || c == '-' || c == '.' || c == '/' || c == '@' || c == '_'
}

const hexDigits = "0123456789ABCDEF"

func byteHex(b byte) string { return "%" + string(hexDigits[b>>4]) + string(hexDigits[b&15]) }

func percentEncodeRune(r rune) string {
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	var sb strings.Builder
	for _, b := range buf[:n] {
		sb.WriteString(byteHex(b))
	}
	return sb.String()
}

// unitString is the code unit at i of a UTF-16 slice as a one-unit string for comparisons.
func hexAt(u []uint16, i int) (string, bool) {
	if i+2 >= len(u) {
		return "", false
	}
	if u[i+1] >= 0x80 || u[i+2] >= 0x80 {
		return "", false
	}
	h := string([]byte{byte(u[i+1]), byte(u[i+2])})
	return h, isHexPair(h)
}

// encodeUnits implements the shared loop of fast-uri's encoders: valid escapes are kept
// (upper-cased, optionally decoded), allowed characters are kept, everything else is
// percent-encoded as UTF-8 (lone surrogates as U+FFFD).
func encodeUnits(input string, allowed func(uint16) bool, decode func(byte) bool, keepSafe bool) string {
	u := jsv.UTF16Units(input)
	var out strings.Builder
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c == '%' {
			if h, ok := hexAt(u, i); ok {
				up := strings.ToUpper(h)
				v, _ := strconv.ParseUint(up, 16, 8)
				if decode != nil && decode(byte(v)) {
					out.WriteByte(byte(v))
				} else {
					out.WriteString("%" + up)
				}
				i += 2
				continue
			}
		}
		if allowed(c) {
			out.WriteByte(byte(c))
			continue
		}
		switch {
		case c < 0x80:
			if keepSafe && isEscapeSafe(c) {
				out.WriteByte(byte(c))
			} else {
				out.WriteString(byteHex(byte(c)))
			}
		case c < 0xD800 || c > 0xDFFF:
			out.WriteString(percentEncodeRune(rune(c)))
		case c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF:
			out.WriteString(percentEncodeRune(0x10000 + (rune(c)-0xD800)<<10 + rune(u[i+1]-0xDC00)))
			i++
		default:
			out.WriteString(percentEncodeRune(0xFFFD))
		}
	}
	return out.String()
}

func normalizePathEncoding(s string) string {
	return encodeUnits(s, func(c uint16) bool { return inSet(c, pathChars) }, func(b byte) bool { return b != '.' && isUnreserved(b) }, true)
}

func normalizeQueryFragmentEncoding(s string) string {
	return encodeUnits(s, func(c uint16) bool { return inSet(c, queryChars) }, isUnreserved, true)
}

func encodeQuery(s string) string {
	return encodeUnits(s, func(c uint16) bool { return inSet(c, queryChars) }, nil, false)
}

func encodeUserinfo(s string) string {
	return encodeUnits(s, func(c uint16) bool { return inSet(c, userinfoChars) }, nil, false)
}

func serializePathEncoding(s string, pathNoScheme bool) string {
	first := pathNoScheme && (len(s) == 0 || s[0] != '/')
	return encodeUnits(s, func(c uint16) bool {
		if c == '/' {
			first = false
		}
		return inSet(c, pathChars) && (c != ':' || !first)
	}, nil, false)
}

// normalizePercentEncoding upper-cases escapes and, when asked, decodes unreserved ones.
func normalizePercentEncoding(s string, decodeUnreserved bool) string {
	if !strings.Contains(s, "%") {
		return s
	}
	u := jsv.UTF16Units(s)
	var out []uint16
	for i := 0; i < len(u); i++ {
		if u[i] == '%' {
			if h, ok := hexAt(u, i); ok {
				up := strings.ToUpper(h)
				v, _ := strconv.ParseUint(up, 16, 8)
				if decodeUnreserved && isUnreserved(byte(v)) {
					out = append(out, uint16(v))
				} else {
					out = append(out, '%', uint16(up[0]), uint16(up[1]))
				}
				i += 2
				continue
			}
		}
		out = append(out, u[i])
	}
	return jsv.FromUnits(out)
}

func hasMalformedPercent(p *string) bool {
	if p == nil {
		return false
	}
	u := jsv.UTF16Units(*p)
	for i := 0; i < len(u); i++ {
		if u[i] != '%' {
			continue
		}
		if _, ok := hexAt(u, i); !ok {
			return true
		}
		i += 2
	}
	return false
}

// safeSlice is s.slice(a, b) on ASCII text, clamped to the string.
func safeSlice(s string, a, b int) string {
	if b > len(s) {
		b = len(s)
	}
	if a > b {
		return ""
	}
	return s[a:b]
}

func isIPLiteral(h string) bool { return len(h) > 0 && h[0] == '[' && h[len(h)-1] == ']' }

type ipv6Result struct {
	host, escaped    string
	isIPv6, isFuture bool
	failed           bool
}

func compressZeroRun(h []string) string {
	bestStart, bestLen, runStart, runLen := -1, 0, -1, 0
	for i, x := range h {
		if x == "0" {
			if runStart == -1 {
				runStart = i
			}
			runLen++
			if runLen > bestLen {
				bestLen, bestStart = runLen, runStart
			}
		} else {
			runStart, runLen = -1, 0
		}
	}
	if bestLen < 2 {
		return strings.Join(h, ":")
	}
	return strings.Join(h[:bestStart], ":") + "::" + strings.Join(h[bestStart+bestLen:], ":")
}

func normalizeIPv6Address(in string) (string, bool) {
	comp := strings.Index(in, "::")
	if comp != -1 && strings.Contains(in[comp+1:], "::") {
		return "", false
	}
	var left, right []string
	if comp == -1 {
		left = strings.Split(in, ":")
	} else {
		left = strings.Split(in[:comp], ":")
		right = strings.Split(in[comp+2:], ":")
		if len(left) == 1 && left[0] == "" {
			left = nil
		}
		if len(right) == 1 && right[0] == "" {
			right = nil
		}
	}
	parts := append(append([]string{}, left...), right...)
	count := 0
	for i, p := range parts {
		if p == "" {
			return "", false
		}
		if strings.Contains(p, ".") {
			if i != len(parts)-1 || (comp != -1 && len(right) == 0) || !ipv4Re.MatchString(p) {
				return "", false
			}
			count += 2
			continue
		}
		if !hextetRe.MatchString(p) {
			return "", false
		}
		v, _ := strconv.ParseUint(p, 16, 32)
		parts[i] = strconv.FormatUint(v, 16)
		count++
	}
	if comp == -1 {
		if count != 8 {
			return "", false
		}
		return compressZeroRun(parts), true
	}
	if count >= 8 {
		return "", false
	}
	expanded := append([]string{}, parts[:len(left)]...)
	for i := count; i < 8; i++ {
		expanded = append(expanded, "0")
	}
	expanded = append(expanded, parts[len(left):]...)
	return compressZeroRun(expanded), true
}

func isZoneID(z string) bool {
	if z == "" {
		return false
	}
	for i := 0; i < len(z); i++ {
		c := z[i]
		if isUnreserved(c) {
			continue
		}
		if c == '%' && i+2 < len(z) && isHexPair(z[i+1:i+3]) {
			i += 2
			continue
		}
		return false
	}
	return true
}

func normalizeIPv6(host string) ipv6Result {
	bracketed := isIPLiteral(host)
	hasBracket := len(host) > 0 && (host[0] == '[' || host[len(host)-1] == ']')
	if hasBracket && !bracketed {
		return ipv6Result{host: host, failed: true}
	}
	in := host
	if bracketed {
		in = host[1 : len(host)-1]
	}
	if bracketed && ipvFutureRe.MatchString(in) {
		in = strings.ToLower(in)
		return ipv6Result{host: "[" + in + "]", escaped: in, isFuture: true}
	}
	if strings.Count(in, ":") < 2 {
		return ipv6Result{host: host, failed: bracketed}
	}
	zone := ""
	if sep := strings.IndexByte(in, '%'); sep != -1 {
		n := 1
		if strings.ToLower(safeSlice(in, sep, sep+3)) == "%25" {
			n = 3
		}
		zone = safeSlice(in, sep+n, len(in))
		if !isZoneID(zone) {
			return ipv6Result{host: host, failed: true}
		}
		in = in[:sep]
	}
	addr, ok := normalizeIPv6Address(in)
	if !ok {
		return ipv6Result{host: host, failed: true}
	}
	r := ipv6Result{host: addr, escaped: addr, isIPv6: true}
	if zone != "" {
		r.host += "%" + zone
		r.escaped += "%25" + zone
	}
	return r
}

func reescapeHostDelims(host string, isIP bool) string {
	var sb strings.Builder
	for i := 0; i < len(host); i++ {
		switch c := host[i]; c {
		case '@':
			sb.WriteString("%40")
		case '/':
			sb.WriteString("%2F")
		case '?':
			sb.WriteString("%3F")
		case '#':
			sb.WriteString("%23")
		case ':':
			if isIP {
				sb.WriteByte(c)
			} else {
				sb.WriteString("%3A")
			}
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// jsUnescape is the global unescape(): %XX and %uXXXX escapes become code units.
func jsUnescape(s string) string {
	u := jsv.UTF16Units(s)
	var out []uint16
	hexRun := func(from, n int) (uint16, bool) {
		if from+n > len(u) {
			return 0, false
		}
		var v uint16
		for _, c := range u[from : from+n] {
			if c >= 0x80 || !isHex(byte(c)) {
				return 0, false
			}
			d, _ := strconv.ParseUint(string(rune(c)), 16, 8)
			v = v<<4 | uint16(d)
		}
		return v, true
	}
	for i := 0; i < len(u); i++ {
		if u[i] == '%' {
			if i+1 < len(u) && u[i+1] == 'u' {
				if v, ok := hexRun(i+2, 4); ok {
					out = append(out, v)
					i += 5
					continue
				}
			}
			if v, ok := hexRun(i+1, 2); ok {
				out = append(out, v)
				i += 2
				continue
			}
		}
		out = append(out, u[i])
	}
	return jsv.FromUnits(out)
}

type schemeHandler struct {
	name          string
	domainHost    bool
	skipNormalize bool
}

func getSchemeHandler(scheme string) *schemeHandler {
	switch strings.ToLower(scheme) {
	case "http", "https", "ws", "wss":
		return &schemeHandler{name: strings.ToLower(scheme), domainHost: true}
	case "urn", "urn:uuid":
		return &schemeHandler{name: strings.ToLower(scheme), skipNormalize: true}
	}
	return nil
}

// nonSimpleDomain reports a character outside fast-uri's simple domain set.
func nonSimpleDomain(h string) bool {
	for _, r := range h {
		if r >= 'a' && r <= 'z' || strings.ContainsRune("!\"$&'()*+,-.;=_`{}~", r) {
			continue
		}
		return true
	}
	return false
}

type uriOpts struct {
	scheme     string
	skipEscape bool
}

type parseStatus struct {
	c                                                             *uriComp
	badAuthority, badPercent, badSpecific, badHost, badSchemeFlag bool
}

func (s parseStatus) malformed() bool {
	return s.badAuthority || s.badPercent || s.badSpecific || s.badHost || s.badSchemeFlag
}

func group(m []string, idx []int, i int) *string {
	if idx[2*i] < 0 {
		return nil
	}
	return sp(m[i])
}

func parseURI(uri string, opts uriOpts) parseStatus {
	c := &uriComp{host: sp(""), path: sp("")}
	st := parseStatus{c: c}
	setErr := func(e string) {
		if c.err == "" {
			c.err = e
		}
	}
	if m := authorityPrefix.FindStringSubmatch(uri); m != nil && strings.Contains(m[1], `\`) {
		setErr("URI authority must not contain a literal backslash.")
		st.badAuthority = true
	}
	if m := introducerRegion.FindStringSubmatch(uri); m != nil {
		region := m[1]
		norm := strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(region)
		if len(norm) >= 2 {
			if norm[:2] != "//" {
				setErr("URI authority must not contain a literal backslash.")
				st.badAuthority = true
			} else if len(region) != len(norm) {
				setErr("URI authority introducer must not contain whitespace.")
				st.badAuthority = true
			}
		}
	}
	idx := uriParseRe.FindStringSubmatchIndex(uri)
	m := make([]string, 9)
	for i := range m {
		if idx[2*i] >= 0 {
			m[i] = uri[idx[2*i]:idx[2*i+1]]
		}
	}
	c.scheme = group(m, idx, 1)
	c.userinfo = group(m, idx, 3)
	c.host = group(m, idx, 4)
	portStr := group(m, idx, 5)
	if p := group(m, idx, 6); p != nil {
		c.path = p
	}
	c.query = group(m, idx, 7)
	c.fragment = group(m, idx, 8)
	if c.scheme != nil {
		dec := jsUnescape(*c.scheme)
		if validScheme.MatchString(dec) {
			c.scheme = sp(strings.ToLower(dec))
		} else {
			setErr(malformedScheme)
			st.badSchemeFlag = true
		}
	}
	hostG := group(m, idx, 4)
	st.badPercent = hasMalformedPercent(group(m, idx, 3)) || (hostG != nil && !isIPLiteral(*hostG) && hasMalformedPercent(hostG)) ||
		hasMalformedPercent(group(m, idx, 6)) || hasMalformedPercent(group(m, idx, 7)) || hasMalformedPercent(group(m, idx, 8))
	if st.badPercent {
		setErr("URI contains malformed percent-encoding.")
	}
	portNumber := -1
	if portStr != nil && *portStr != "" {
		n, err := strconv.Atoi(strings.TrimLeft(*portStr, "0") + "0")
		if err == nil {
			portNumber = n / 10
		} else {
			portNumber = 1 << 30
		}
		c.port = sp(strconv.Itoa(portNumber))
		c.portNum = true
	} else {
		c.port = portStr
	}
	authority := idx[4] >= 0
	if authority && *c.path != "" && (*c.path)[0] != '/' {
		setErr(`URI path must start with "/" when authority is present.`)
		st.badAuthority = true
	} else if c.portNum && (portNumber < 0 || portNumber > 65535) {
		setErr("URI port is malformed.")
		st.badAuthority = true
	}
	isIP := false
	badLiteral := false
	if c.host != nil && *c.host != "" {
		if !ipv4Re.MatchString(*c.host) {
			h := *c.host
			bracketed := isIPLiteral(h)
			hasBracket := strings.ContainsAny(h, "[]")
			r := normalizeIPv6(h)
			isIP = r.isIPv6 || r.isFuture
			badLiteral = hasBracket && (!bracketed || r.failed)
			if isIP {
				c.host = sp(r.host)
			} else {
				c.host = sp(strings.ToLower(r.host))
			}
			if badLiteral {
				setErr("URI host is malformed.")
				st.badAuthority = true
			}
		} else {
			isIP = true
		}
	}
	switch {
	case c.scheme == nil && c.userinfo == nil && c.host == nil && c.port == nil && c.query == nil && *c.path == "":
		c.reference = "same-document"
	case c.scheme == nil:
		c.reference = "relative"
	case c.fragment == nil:
		c.reference = "absolute"
	default:
		c.reference = "uri"
	}
	name := opts.scheme
	if name == "" && c.scheme != nil {
		name = *c.scheme
	}
	handler := getSchemeHandler(name)
	if !badLiteral {
		st.badHost = canonicalizeHost(c, handler, isIP)
	}
	if strings.Contains(uri, "%") && c.host != nil && !badLiteral {
		h := *c.host
		if !isIP {
			h = normalizePercentEncoding(strings.ToLower(normalizePercentEncoding(h, true)), false)
		}
		c.host = sp(reescapeHostDelims(h, isIP))
	}
	if handler == nil || !handler.skipNormalize {
		if *c.path != "" {
			c.path = sp(normalizePathEncoding(*c.path))
		}
		if c.query != nil && *c.query != "" {
			c.query = sp(normalizeQueryFragmentEncoding(*c.query))
		}
		if c.fragment != nil && *c.fragment != "" {
			c.fragment = sp(normalizeQueryFragmentEncoding(*c.fragment))
		}
	}
	if handler != nil {
		switch handler.name {
		case "http", "https":
			if c.host == nil || *c.host == "" {
				setErr("HTTP URIs must have a host.")
			}
		case "ws", "wss":
			secure := handler.name == "wss"
			c.secure = &secure
			rn := "/"
			if *c.path != "" {
				rn = *c.path
			}
			if c.query != nil && *c.query != "" {
				rn += "?" + *c.query
			}
			c.resourceName = sp(rn)
			c.path, c.query = nil, nil
		case "urn", "urn:uuid":
			urnParse(c, opts)
			if c.nid == nil {
				st.badSpecific = true
			}
		}
	}
	return st
}

func urnParse(c *uriComp, opts uriOpts) {
	if c.path == nil || *c.path == "" {
		c.err = "URN can not be parsed"
		return
	}
	m := urnRe.FindStringSubmatch(*c.path)
	if m == nil || m[0] != *c.path {
		if c.err == "" {
			c.err = "URN can not be parsed."
		}
		return
	}
	c.nid = sp(strings.ToLower(m[1]))
	c.nss = sp(m[2])
	c.path = nil
	if *c.nid == "uuid" {
		c.uuid, c.nss = c.nss, nil
		if c.uuid == nil || !uuidRe.MatchString(*c.uuid) {
			if c.err == "" {
				c.err = "UUID is not valid."
			}
		}
	}
}

// canonicalizeHost stands in for the WHATWG host conversion fast-uri applies to domain
// hosts with characters outside its simple set. ASCII hosts made of letters, digits,
// dots and dashes keep their (already lower-cased) form; other hosts are reported as not
// convertible, which is what WHATWG does for the forbidden ASCII code points.
func canonicalizeHost(c *uriComp, h *schemeHandler, isIP bool) bool {
	if h == nil || !h.domainHost || c.host == nil || *c.host == "" || isIPLiteral(*c.host) || isIP || !nonSimpleDomain(*c.host) {
		return false
	}
	for _, r := range *c.host {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' || r == '%') {
			if c.err == "" {
				c.err = "Host's domain name can not be converted to ASCII: TypeError: Invalid URL"
			}
			return true
		}
	}
	return false
}

func recomposeAuthority(c *uriComp) (string, bool, error) {
	var parts []string
	if c.userinfo != nil {
		parts = append(parts, encodeUserinfo(*c.userinfo), "@")
	}
	if c.host != nil {
		host := *c.host
		if !ipv4Re.MatchString(host) {
			r := normalizeIPv6(host)
			if !r.isIPv6 && !r.isFuture {
				host = normalizePercentEncoding(host, true)
				r = normalizeIPv6(host)
			}
			if r.isIPv6 || r.isFuture {
				host = "[" + r.escaped + "]"
			} else {
				host = reescapeHostDelims(host, false)
			}
		}
		parts = append(parts, host)
	}
	if c.port != nil {
		for i := 0; i < len(*c.port); i++ {
			if (*c.port)[i] < '0' || (*c.port)[i] > '9' {
				return "", false, errors.New("URI port is malformed.")
			}
		}
		parts = append(parts, ":", *c.port)
	}
	if len(parts) == 0 {
		return "", false, nil
	}
	return strings.Join(parts, ""), true, nil
}

func serializeURI(in *uriComp, opts uriOpts) (string, error) {
	c := *in
	if c.scheme != nil {
		dec := jsUnescape(*c.scheme)
		if !validScheme.MatchString(dec) {
			return "", errors.New(malformedScheme)
		}
		c.scheme = sp(dec)
	}
	name := opts.scheme
	if name == "" && c.scheme != nil {
		name = *c.scheme
	}
	handler := getSchemeHandler(name)
	if handler != nil {
		switch handler.name {
		case "http", "https":
			def := "80"
			if c.scheme != nil && strings.ToLower(*c.scheme) == "https" {
				def = "443"
			}
			if c.port != nil && ((c.portNum && *c.port == def) || *c.port == "") {
				c.port = nil
			}
			if c.path == nil || *c.path == "" {
				c.path = sp("/")
			}
		case "ws", "wss":
			secure := handler.name == "wss"
			if c.secure != nil {
				secure = *c.secure
			}
			def := "80"
			if secure {
				def = "443"
			}
			if c.port != nil && ((c.portNum && *c.port == def) || *c.port == "") {
				c.port = nil
			}
			if c.secure != nil {
				if *c.secure {
					c.scheme = sp("wss")
				} else {
					c.scheme = sp("ws")
				}
				c.secure = nil
			}
			if c.resourceName != nil && *c.resourceName != "" {
				rn := *c.resourceName
				q := strings.IndexByte(rn, '?')
				p := rn
				if q != -1 {
					p = rn[:q]
					c.query = sp(rn[q+1:])
				} else {
					c.query = nil
				}
				if p != "" && p != "/" {
					c.path = sp(p)
				} else {
					c.path = nil
				}
				c.resourceName = nil
			}
			c.fragment = nil
		case "urn", "urn:uuid":
			if c.nid == nil {
				return "", errors.New("URN without nid cannot be serialized")
			}
			nid := strings.ToLower(*c.nid)
			nss := ""
			if nid == "uuid" {
				if c.uuid != nil {
					nss = strings.ToLower(*c.uuid)
				}
			} else if c.nss != nil {
				nss = *c.nss
			}
			c.path = sp(nid + ":" + nss)
			opts.skipEscape = true
		}
	}
	hasAuthority := c.userinfo != nil || c.host != nil || c.port != nil
	pathNoScheme := !opts.skipEscape && c.scheme == nil && !hasAuthority
	if c.path != nil {
		if !opts.skipEscape {
			c.path = sp(serializePathEncoding(*c.path, pathNoScheme))
		} else {
			c.path = sp(normalizePercentEncoding(*c.path, false))
		}
	}
	var out strings.Builder
	if c.scheme != nil && *c.scheme != "" {
		out.WriteString(*c.scheme + ":")
	}
	auth, hasAuth, err := recomposeAuthority(&c)
	if err != nil {
		return "", err
	}
	if hasAuth {
		out.WriteString("//" + auth)
		if c.path != nil && *c.path != "" && (*c.path)[0] != '/' {
			out.WriteByte('/')
		}
	}
	if c.path != nil {
		s := removeDotSegments(*c.path)
		if pathNoScheme {
			s = serializePathEncoding(s, true)
		}
		if !hasAuth && strings.HasPrefix(s, "//") {
			s = "/%2F" + s[2:]
		}
		out.WriteString(s)
	}
	if c.query != nil {
		out.WriteString("?" + encodeQuery(*c.query))
	}
	if c.fragment != nil {
		out.WriteString("#" + encodeQuery(*c.fragment))
	}
	return out.String(), nil
}

func removeDotSegments(path string) string {
	input := path
	var output []string
	for len(input) > 0 {
		switch n := len(input); {
		case n == 1:
			if input != "." {
				output = append(output, input)
			}
			return strings.Join(output, "")
		case n == 2:
			if input[0] == '.' {
				if input[1] == '.' {
					return strings.Join(output, "")
				} else if input[1] == '/' {
					input = input[2:]
					continue
				}
			} else if input[0] == '/' && (input[1] == '.' || input[1] == '/') {
				output = append(output, "/")
				return strings.Join(output, "")
			}
		case n == 3 && input == "/..":
			if len(output) != 0 {
				output = output[:len(output)-1]
			}
			output = append(output, "/")
			return strings.Join(output, "")
		}
		at := func(i int) byte {
			if i < len(input) {
				return input[i]
			}
			return 0
		}
		if at(0) == '.' {
			if at(1) == '.' {
				if at(2) == '/' {
					input = input[3:]
					continue
				}
			} else if at(1) == '/' {
				input = input[2:]
				continue
			}
		} else if at(0) == '/' && at(1) == '.' {
			if at(2) == '/' {
				input = input[2:]
				continue
			} else if at(2) == '.' && at(3) == '/' {
				input = input[3:]
				if len(output) != 0 {
					output = output[:len(output)-1]
				}
				continue
			}
		}
		next := strings.IndexByte(input[1:], '/')
		if next == -1 {
			output = append(output, input)
			break
		}
		output = append(output, input[:next+1])
		input = input[next+1:]
	}
	return strings.Join(output, "")
}

func resolveComponent(base, rel *uriComp) *uriComp {
	t := &uriComp{}
	dot := func(p *string) *string {
		if p == nil {
			return sp("")
		}
		return sp(removeDotSegments(*p))
	}
	if rel.scheme != nil && *rel.scheme != "" {
		t.scheme, t.userinfo, t.host, t.port, t.portNum = rel.scheme, rel.userinfo, rel.host, rel.port, rel.portNum
		t.path = dot(rel.path)
		t.query = rel.query
	} else {
		if rel.userinfo != nil || rel.host != nil || rel.port != nil {
			t.userinfo, t.host, t.port, t.portNum = rel.userinfo, rel.host, rel.port, rel.portNum
			t.path = dot(rel.path)
			t.query = rel.query
		} else {
			if rel.path == nil || *rel.path == "" {
				t.path = base.path
				if rel.query != nil {
					t.query = rel.query
				} else {
					t.query = base.query
				}
			} else {
				if (*rel.path)[0] == '/' {
					t.path = sp(removeDotSegments(*rel.path))
				} else {
					var p string
					switch {
					case (base.userinfo != nil || base.host != nil || base.port != nil) && (base.path == nil || *base.path == ""):
						p = "/" + *rel.path
					case base.path == nil || *base.path == "":
						p = *rel.path
					default:
						bp := *base.path
						p = bp[:strings.LastIndexByte(bp, '/')+1] + *rel.path
					}
					t.path = sp(removeDotSegments(p))
				}
				t.query = rel.query
			}
			t.userinfo, t.host, t.port, t.portNum = base.userinfo, base.host, base.port, base.portNum
		}
		t.scheme = base.scheme
	}
	t.fragment = rel.fragment
	return t
}

// uriResolve is fast-uri's resolve(base, relative).
func uriResolve(base, rel string) (string, error) {
	opts := uriOpts{scheme: "null"}
	b := parseURI(base, opts)
	r := parseURI(rel, opts)
	if b.malformed() || r.malformed() {
		msg := b.c.err
		if msg == "" {
			msg = r.c.err
		}
		if msg == "" {
			msg = "URI is malformed."
		}
		return "", errors.New(msg)
	}
	t := resolveComponent(b.c, r.c)
	scheme := ""
	if t.scheme != nil {
		scheme = *t.scheme
	}
	isIP := t.host != nil && *t.host != "" && (ipv4Re.MatchString(*t.host) || normalizeIPv6(*t.host).isIPv6)
	canonicalizeHost(t, getSchemeHandler(scheme), isIP)
	encodedASCII := t.host != nil && strings.Contains(*t.host, "%") && isASCII(*t.host)
	if t.err != "" && !encodedASCII {
		return "", errors.New(t.err)
	}
	return serializeURI(t, uriOpts{scheme: "null", skipEscape: true})
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// uriParse is fast-uri's parse(uri) with no options.
func uriParse(s string) *uriComp { return parseURI(s, uriOpts{}).c }

// uriSerialize is fast-uri's serialize(components) with no options.
func uriSerialize(c *uriComp) (string, error) { return serializeURI(c, uriOpts{}) }
