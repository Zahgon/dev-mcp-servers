package main

// What the client needs from Node's fetch and runtime: the default transport's failure
// and redirect behaviour, Number() of a header and encodeURIComponent.

import (
	"compress/flate"
	"compress/gzip"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// fetchTransport gives a RoundTripper the observable behaviour of Node's fetch: any
// network failure or redirect is "fetch failed", and compressed bodies are decoded while
// the declared content-length is kept.
type fetchTransport struct{ base http.RoundTripper }

func (t fetchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	res, err := t.base.RoundTrip(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, errors.New(timeoutMessage)
		}
		return nil, errors.New("fetch failed")
	}
	switch res.StatusCode {
	case 301, 302, 303, 307, 308:
		res.Body.Close()
		return nil, errors.New("fetch failed")
	}
	switch strings.ToLower(res.Header.Get("Content-Encoding")) {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			res.Body.Close()
			return nil, errors.New("fetch failed")
		}
		res.Body = struct {
			io.Reader
			io.Closer
		}{zr, res.Body}
	case "deflate":
		res.Body = struct {
			io.Reader
			io.Closer
		}{flate.NewReader(res.Body), res.Body}
	}
	return res, nil
}

// jsNumber is Number(s) for a header value; an absent header reads as "0".
func jsNumber(s string) float64 {
	t := jsTrim(s)
	switch {
	case t == "":
		return 0
	case t == "Infinity" || t == "+Infinity":
		return math.Inf(1)
	case t == "-Infinity":
		return math.Inf(-1)
	case len(t) > 2 && t[0] == '0' && strings.ContainsRune("xXoObB", rune(t[1])):
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[t[1]]
		if v, err := strconv.ParseUint(t[2:], base, 64); err == nil {
			return float64(v)
		}
		return math.NaN()
	case strings.ContainsAny(t, "xXnNpP_"):
		return math.NaN()
	}
	v, err := strconv.ParseFloat(t, 64)
	var numErr *strconv.NumError
	if err != nil && !(errors.As(err, &numErr) && numErr.Err == strconv.ErrRange) {
		return math.NaN()
	}
	return v
}

// encodeURIComponent percent-encodes s (WTF-8) as JavaScript does; an unpaired
// surrogate throws URIError.
func encodeURIComponent(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := jsv.DecodeWTF8(s, i)
		if r >= 0xD800 && r <= 0xDFFF {
			return "", &jsError{name: "URIError", message: "URI malformed"}
		}
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.!~*'()", r)) {
			b.WriteRune(r)
		} else {
			for _, c := range []byte(s[i : i+n]) {
				b.WriteString("%" + strings.ToUpper(strconv.FormatUint(uint64(c)|0x100, 16)[1:]))
			}
		}
		i += n
	}
	return b.String(), nil
}
