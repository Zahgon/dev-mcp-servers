package main

// Fetches response headers for a public http(s) URL. Refuses private, loopback, link-local and
// metadata addresses (both literal IPs and names that resolve to them), follows at most a few
// redirects with the same checks on every hop, times out, and never reads the response body.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	defaultTimeout      = 10 * time.Second
	defaultMaxRedirects = 5
	redirectLimit       = 10
	userAgent           = "mcp-security-headers/0.1 (+https://github.com/basitalisandhu/dev-mcp-servers)"
)

// resolver returns the addresses of a host name.
type resolver func(ctx context.Context, host string) ([]string, error)

// fetchOptions are the seams the TypeScript createServer options expose.
type fetchOptions struct {
	Transport    http.RoundTripper
	Resolve      resolver
	Timeout      time.Duration
	MaxRedirects *int
}

// unsafeURLError is a URL the fetcher refuses.
type unsafeURLError struct{ msg string }

func (e *unsafeURLError) Error() string { return e.msg }

func unsafeURL(format string, args ...any) error {
	return &unsafeURLError{fmt.Sprintf(format, args...)}
}

var v4Blocked = []struct {
	base string
	bits uint
}{
	{"0.0.0.0", 8}, {"10.0.0.0", 8}, {"100.64.0.0", 10}, {"127.0.0.0", 8}, {"169.254.0.0", 16}, {"172.16.0.0", 12},
	{"192.0.0.0", 24}, {"192.168.0.0", 16}, {"198.18.0.0", 15}, {"224.0.0.0", 4}, {"240.0.0.0", 4},
}

// ipv4ToInt is ip.split(".").reduce((n, o) => (n << 8) + Number(o), 0) >>> 0 for dotted quads.
func ipv4ToInt(ip string) uint32 {
	var n uint32
	for _, o := range strings.Split(ip, ".") {
		v, _ := strconv.ParseUint(o, 10, 32)
		n = n<<8 + uint32(v)
	}
	return n
}

func isPrivateIPv4(ip string) bool {
	n := ipv4ToInt(ip)
	for _, b := range v4Blocked {
		if n>>(32-b.bits) == ipv4ToInt(b.base)>>(32-b.bits) {
			return true
		}
	}
	return false
}

var (
	bracketEnds = regexp.MustCompile(`^\[|\]$`)
	mappedV4    = regexp.MustCompile(`^::ffff:([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)$`)
	linkLocal   = regexp.MustCompile(`^fe[89ab]`)
	uniqueLocal = regexp.MustCompile(`^f[cd]`)
)

func isPrivateIPv6(ip string) bool {
	lower := bracketEnds.ReplaceAllString(toLowerCase(ip), "")
	if lower == "::" || lower == "::1" {
		return true
	}
	if m := mappedV4.FindStringSubmatch(lower); m != nil {
		return isPrivateIPv4(m[1])
	}
	first, _, _ := strings.Cut(lower, ":")
	return linkLocal.MatchString(first) || uniqueLocal.MatchString(first) || first == "" || first == "0" || strings.HasPrefix(first, "ff")
}

// ipKind is net.isIP: 4, 6 or 0.
func ipKind(s string) int {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return 0
	}
	if addr.Is4() {
		return 4
	}
	return 6
}

func isPrivateAddress(ip string) bool {
	switch ipKind(ip) {
	case 4:
		return isPrivateIPv4(ip)
	case 6:
		return isPrivateIPv6(ip)
	}
	return true
}

func isBlockedHostname(hostname string) bool {
	h := strings.TrimSuffix(toLowerCase(hostname), ".")
	return h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal")
}

// defaultResolve is dns.lookup(host, { all: true }); failures carry getaddrinfo's wording.
func defaultResolve(ctx context.Context, host string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		code := "EAI_AGAIN"
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			code = "ENOTFOUND"
		}
		return nil, fmt.Errorf("getaddrinfo %s %s", code, host)
	}
	return addrs, nil
}

func assertPublicURL(ctx context.Context, raw string, resolve resolver) (*webURL, error) {
	u, err := parseURL(raw, nil)
	if err != nil {
		return nil, unsafeURL("%s is not an absolute URL", jsv.Stringify(raw, 0))
	}
	if u.scheme != "http" && u.scheme != "https" {
		return nil, unsafeURL("only http and https URLs are supported, not %s", u.protocol())
	}
	if u.username != "" || u.password != "" {
		return nil, unsafeURL("URLs with embedded credentials are refused")
	}
	host := bracketEnds.ReplaceAllString(u.hostname(), "")
	if isBlockedHostname(host) {
		return nil, unsafeURL("%s is a local hostname; only public hosts are fetched", host)
	}
	if ipKind(host) != 0 {
		if isPrivateAddress(host) {
			return nil, unsafeURL("%s is a private, loopback, link-local or reserved address", host)
		}
		return u, nil
	}
	addresses, err := resolve(ctx, host)
	if err != nil {
		return nil, unsafeURL("%s does not resolve: %s", host, err.Error())
	}
	if len(addresses) == 0 {
		return nil, unsafeURL("%s does not resolve to any address", host)
	}
	for _, a := range addresses {
		if isPrivateAddress(a) {
			return nil, unsafeURL("%s resolves to %s, a private or reserved address; refusing to fetch", host, a)
		}
	}
	return u, nil
}

// fetchResult is the TypeScript FetchedHeaders object.
type fetchResult struct {
	requested, final, method string
	status                   int
	redirects                []any
	headers                  *jsv.Obj
	setCookie                []string
}

func (f fetchResult) obj() *jsv.Obj {
	return jsv.O("requested_url", f.requested, "final_url", f.final, "method", f.method, "status", float64(f.status),
		"redirects", f.redirects, "headers", f.headers, "set_cookie", jsv.Strings(f.setCookie))
}

func newTransport() http.RoundTripper {
	return &http.Transport{
		// Node's fetch ignores proxy variables, speaks HTTP/1.1 and leaves Content-Encoding alone.
		Proxy:              nil,
		DisableCompression: true,
		TLSNextProto:       map[string]func(string, *tls.Conn) http.RoundTripper{},
		DialContext:        (&net.Dialer{Timeout: defaultTimeout}).DialContext,
	}
}

// request sends one request without following redirects and closes the body unread. Failures read
// as Node's fetch words them; an injected transport's own error text passes through, as the error of
// an injected fetchImpl does in the TypeScript.
func request(client *http.Client, u *webURL, method string, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.href(), nil)
	if err != nil {
		return nil, errors.New("fetch failed")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "*")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	res, err := client.Do(req)
	if err != nil {
		var injected *injectedError
		switch {
		case errors.As(err, &injected):
			return nil, injected.err
		case ctx.Err() == context.DeadlineExceeded:
			return nil, errors.New("The operation was aborted due to timeout")
		}
		return nil, errors.New("fetch failed")
	}
	res.Body.Close()
	return res, nil
}

// injectedError marks an error returned by a transport the caller supplied.
type injectedError struct{ err error }

func (e *injectedError) Error() string { return e.err.Error() }

type injectedTransport struct{ rt http.RoundTripper }

func (t injectedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, &injectedError{err}
	}
	return res, nil
}

func fetchHeaders(raw, method string, opts fetchOptions) (*jsv.Obj, error) {
	var transport http.RoundTripper = injectedTransport{opts.Transport}
	if opts.Transport == nil {
		transport = newTransport()
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resolve := opts.Resolve
	if resolve == nil {
		resolve = defaultResolve
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	maxRedirects := defaultMaxRedirects
	if opts.MaxRedirects != nil {
		maxRedirects = *opts.MaxRedirects
	}
	maxRedirects = min(maxRedirects, redirectLimit)
	redirects := []any{}
	current, err := assertPublicURL(context.Background(), raw, resolve)
	if err != nil {
		return nil, err
	}
	currentMethod := method
	for hop := 0; ; hop++ {
		res, err := request(client, current, currentMethod, timeout)
		if err != nil {
			return nil, fmt.Errorf("request to %s failed: %s", current.origin(), err.Error())
		}
		if currentMethod == "HEAD" && (res.StatusCode == 405 || res.StatusCode == 501) {
			currentMethod = "GET"
			continue
		}
		location := headerGet(res.Header, "Location")
		if res.StatusCode >= 300 && res.StatusCode < 400 && location != "" {
			if hop >= maxRedirects {
				return nil, fmt.Errorf("more than %d redirects (last: %s -> %s)", maxRedirects, current.href(), location)
			}
			target, err := parseURL(location, current)
			if err != nil {
				return nil, err
			}
			next, err := assertPublicURL(context.Background(), target.href(), resolve)
			if err != nil {
				return nil, err
			}
			redirects = append(redirects, jsv.O("from", current.href(), "to", next.href(), "status", float64(res.StatusCode)))
			current = next
			continue
		}
		return fetchResult{requested: raw, final: current.href(), method: currentMethod, status: res.StatusCode, redirects: redirects,
			headers: headerObject(res.Header), setCookie: headerValues(res.Header, "Set-Cookie")}.obj(), nil
	}
}

// headerGet is Headers#get: every value of the name joined with ", ", or "" when absent.
func headerGet(h http.Header, name string) string {
	return strings.Join(headerValues(h, name), ", ")
}

// headerValues are a header's values as fetch exposes them: each byte is one code unit (an
// isomorphic decode), so bytes above 0x7F read as Latin-1 characters.
func headerValues(h http.Header, name string) []string {
	values := h.Values(name)
	out := make([]string, len(values))
	for i, v := range values {
		var b strings.Builder
		for j := 0; j < len(v); j++ {
			b.WriteRune(rune(v[j]))
		}
		out[i] = b.String()
	}
	return out
}

// headerObject is Headers#forEach into an object: names lower-cased and sorted, values combined,
// Set-Cookie left out.
func headerObject(h http.Header) *jsv.Obj {
	names := make([]string, 0, len(h))
	for name := range h {
		if lower := strings.ToLower(name); lower != "set-cookie" {
			names = append(names, lower)
		}
	}
	sort.Strings(names)
	out := jsv.NewObj()
	for _, name := range names {
		out.Set(name, headerGet(h, name))
	}
	return out
}
