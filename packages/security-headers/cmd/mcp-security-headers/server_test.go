package main

// The upstream suite test/server.test.mjs, ported case by case. The cases that inject a fetch
// double and a resolver into createServer run in-process against newServerWith (an injected
// http.RoundTripper and resolver) and are skipped when MCP_SERVER_BIN names an external server.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

var goodHeaders = map[string]string{
	"content-security-policy":      "default-src 'self'; script-src 'self' 'nonce-abc'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'",
	"strict-transport-security":    "max-age=63072000; includeSubDomains; preload",
	"x-frame-options":              "DENY",
	"x-content-type-options":       "nosniff",
	"referrer-policy":              "strict-origin-when-cross-origin",
	"permissions-policy":           "camera=(), microphone=()",
	"cross-origin-opener-policy":   "same-origin",
	"cross-origin-resource-policy": "same-origin",
}

func fakeResolve(_ context.Context, host string) ([]string, error) {
	switch host {
	case "public.example":
		return []string{"93.184.216.34"}, nil
	case "evil.example":
		return []string{"93.184.216.34", "10.0.0.5"}, nil
	case "v6.example":
		return []string{"2606:2800:220:1:248:1893:25c8:1946"}, nil
	}
	return nil, errors.New("ENOTFOUND")
}

// countingBody records whether the server read a response body.
type countingBody struct {
	reads *int
	r     io.Reader
}

func (b countingBody) Read(p []byte) (int, error) {
	*b.reads++
	return b.r.Read(p)
}

func (countingBody) Close() error { return nil }

type logEntry struct{ url, method string }

// fakeFetch answers like the upstream fakeFetch double.
type fakeFetch struct {
	mu    sync.Mutex
	log   []logEntry
	reads int
}

func (f *fakeFetch) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, logEntry{req.URL.String(), req.Method})
	res := func(status int, headers [][2]string, body string) *http.Response {
		h := http.Header{}
		for _, kv := range headers {
			h.Add(kv[0], kv[1])
		}
		return &http.Response{StatusCode: status, Header: h, Body: countingBody{&f.reads, strings.NewReader(body)}, Request: req}
	}
	switch p := req.URL.Path; {
	case p == "/redirect":
		return res(302, [][2]string{{"location", "/final"}}, ""), nil
	case p == "/to-private":
		return res(301, [][2]string{{"location", "http://127.0.0.1/admin"}}, ""), nil
	case p == "/loop":
		return res(302, [][2]string{{"location", "/loop"}}, ""), nil
	case p == "/nohead" && req.Method == "HEAD":
		return res(405, nil, ""), nil
	case p == "/nohead":
		return res(200, [][2]string{{"x-content-type-options", "nosniff"}}, "body"), nil
	case p == "/cookies":
		return res(200, [][2]string{{"set-cookie", "a=1; Path=/"}, {"set-cookie", "b=2; Secure; HttpOnly; SameSite=Lax"}}, ""), nil
	case p == "/final":
		var h [][2]string
		for k, v := range goodHeaders {
			h = append(h, [2]string{k, v})
		}
		return res(200, h, "ignored body"), nil
	}
	return res(200, [][2]string{{"server", "nginx/1.18.0"}, {"x-powered-by", "Express"}, {"x-xss-protection", "1; mode=block"}}, ""), nil
}

func inProcessOnly(t *testing.T) {
	t.Helper()
	if os.Getenv("MCP_SERVER_BIN") != "" {
		t.Skip("injects a fetch double and a resolver into the server in-process")
	}
}

// callInProcess runs one tool of a server built with the doubles.
func callInProcess(t *testing.T, s *mcp.Server, name string, args any) toolReply {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("json.Marshal(args): %v", err)
	}
	v, err := jsv.Parse(string(data))
	if err != nil {
		t.Fatalf("jsv.Parse(string(data)): %v", err)
	}
	for _, tool := range s.Tools {
		if tool.Name == name {
			r := tool.Handler(jsv.AsObj(v))
			return toolReply{IsError: r.IsError, Text: r.Text}
		}
	}
	t.Fatalf("no tool %s", name)
	return toolReply{}
}

type checkResult struct {
	Header      string `json:"header"`
	Status      string `json:"status"`
	Explanation string `json:"explanation"`
}

type report struct {
	Grade  string        `json:"grade"`
	Checks []checkResult `json:"checks"`
	Notes  []string      `json:"notes"`
}

func (r report) byName() map[string]checkResult {
	m := map[string]checkResult{}
	for _, c := range r.Checks {
		m[c.Header] = c
	}
	return m
}

type fetched struct {
	Status    int               `json:"status"`
	FinalURL  string            `json:"final_url"`
	Method    string            `json:"method"`
	Redirects []json.RawMessage `json:"redirects"`
	Headers   map[string]string `json:"headers"`
	SetCookie []string          `json:"set_cookie"`
	Report    report            `json:"report"`
}

func TestListsThreeToolsAndNamesTheServer(t *testing.T) {
	c := startServer(t)
	init := decodeRaw[struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}](t, c.initialize("2025-06-18"))
	if init.ServerInfo.Name != serverName {
		t.Fatalf("serverInfo.name %q", init.ServerInfo.Name)
	}
	list := decodeRaw[struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				OpenWorldHint bool `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}](t, c.request("tools/list", map[string]any{}))
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Name == "grade_headers" && tool.Annotations.OpenWorldHint {
			t.Fatal("tools/list: grade_headers has openWorldHint true, want false")
		}
	}
	sort.Strings(names)
	if want := []string{"check_url_headers", "explain_header", "grade_headers"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestCheckUrlHeadersFollowsARedirectGradesTheFinalResponseAndNeverReadsTheBody(t *testing.T) {
	inProcessOnly(t)
	fake := &fakeFetch{}
	s := newServerWith(fetchOptions{Transport: fake, Resolve: fakeResolve})
	r := decodeResult[fetched](t, callInProcess(t, s, "check_url_headers", map[string]any{"url": "https://public.example/redirect"}))
	if r.Status != 200 || r.FinalURL != "https://public.example/final" || len(r.Redirects) != 1 || r.Report.Grade != "A+" {
		t.Fatalf("redirect: %+v", r)
	}
	for _, c := range r.Report.Checks {
		if c.Status == "fail" {
			t.Fatalf("failing check %+v", c)
		}
	}
	// redirect: "manual": each hop is its own HEAD request.
	if want := []logEntry{{"https://public.example/redirect", "HEAD"}, {"https://public.example/final", "HEAD"}}; !slices.Equal(fake.log, want) {
		t.Fatalf("requests %v, want %v", fake.log, want)
	}
	if fake.reads != 0 {
		t.Fatalf("the body was read %d times", fake.reads)
	}

	bare := decodeResult[fetched](t, callInProcess(t, s, "check_url_headers", map[string]any{"url": "https://public.example/bare"}))
	by := bare.Report.byName()
	if bare.Report.Grade != "F" || by["content-security-policy"].Status != "fail" || by["server"].Status != "warn" ||
		by["x-powered-by"].Status != "info" || by["x-xss-protection"].Status != "warn" {
		t.Fatalf("bare: %+v", bare.Report)
	}

	fallback := decodeResult[fetched](t, callInProcess(t, s, "check_url_headers", map[string]any{"url": "https://public.example/nohead"}))
	if fallback.Method != "GET" || fallback.Headers["x-content-type-options"] != "nosniff" {
		t.Fatalf("fallback: %+v", fallback)
	}

	cookies := decodeResult[fetched](t, callInProcess(t, s, "check_url_headers", map[string]any{"url": "https://public.example/cookies"}))
	cookie := cookies.Report.byName()["set-cookie"]
	if len(cookies.SetCookie) != 2 || cookie.Status != "warn" || !regexp.MustCompile(`a lacks Secure, HttpOnly, SameSite`).MatchString(cookie.Explanation) {
		t.Fatalf("cookies: %+v", cookies)
	}
}

func TestCheckUrlHeadersRefusesPrivateTargetsRedirectToPrivateLoopsAndBadSchemes(t *testing.T) {
	inProcessOnly(t)
	s := newServerWith(fetchOptions{Transport: &fakeFetch{}, Resolve: fakeResolve})
	for _, url := range []string{"http://localhost:3000/", "http://127.0.0.1/", "http://169.254.169.254/latest/meta-data", "http://[::1]/", "http://10.1.2.3/",
		"http://evil.example/", "ftp://public.example/", "http://user:pw@public.example/", "http://unknown.example/"} {
		if r := callInProcess(t, s, "check_url_headers", map[string]any{"url": url}); !r.IsError {
			t.Fatalf("%s was fetched: %s", url, r.Text)
		}
	}
	toPrivate := callInProcess(t, s, "check_url_headers", map[string]any{"url": "https://public.example/to-private"})
	if !toPrivate.IsError || !strings.Contains(toPrivate.Text, "127.0.0.1") {
		t.Fatalf("to-private: %+v", toPrivate)
	}
	loop := callInProcess(t, s, "check_url_headers", map[string]any{"url": "https://public.example/loop", "max_redirects": 2})
	if !loop.IsError || !strings.Contains(loop.Text, "more than 2 redirects") {
		t.Fatalf("loop: %+v", loop)
	}
}

func TestGradeHeadersScoresASuppliedSetAndExplainHeaderDocumentsEachHeader(t *testing.T) {
	c := connected(t)
	weak := decodeResult[report](t, c.call("grade_headers", map[string]any{"headers": map[string]any{
		"Content-Security-Policy":   "default-src *; script-src * 'unsafe-inline' 'unsafe-eval'",
		"Strict-Transport-Security": "max-age=300",
		"X-Frame-Options":           "ALLOW-FROM https://x",
		"Referrer-Policy":           "unsafe-url",
		"Set-Cookie":                []string{"s=1; SameSite=None"},
	}}))
	by := weak.byName()
	if by["content-security-policy"].Status != "fail" || !strings.Contains(by["content-security-policy"].Explanation, "unsafe-inline") {
		t.Fatalf("csp: %+v", by["content-security-policy"])
	}
	if by["strict-transport-security"].Status != "warn" || by["x-frame-options"].Status != "warn" || by["referrer-policy"].Status != "fail" {
		t.Fatalf("checks: %+v", weak.Checks)
	}
	if !strings.Contains(by["set-cookie"].Explanation, "Secure (required with SameSite=None)") {
		t.Fatalf("set-cookie: %+v", by["set-cookie"])
	}
	if weak.Grade != "D" && weak.Grade != "F" {
		t.Fatalf("grade %q", weak.Grade)
	}

	http := decodeResult[report](t, c.call("grade_headers", map[string]any{"headers": goodHeaders, "https": false}))
	plain := false
	for _, n := range http.Notes {
		plain = plain || strings.Contains(n, "plain HTTP")
	}
	if http.byName()["strict-transport-security"].Status != "info" || !plain {
		t.Fatalf("http: %+v", http)
	}

	frames := decodeResult[report](t, c.call("grade_headers", map[string]any{"headers": map[string]any{"content-security-policy": "default-src 'self'; frame-ancestors 'self'"}}))
	if frames.byName()["x-frame-options"].Status != "pass" {
		t.Fatalf("frame-ancestors: %+v", frames.byName()["x-frame-options"])
	}

	doc := decodeResult[struct {
		Recommended string `json:"recommended"`
		Reference   string `json:"reference"`
	}](t, c.call("explain_header", map[string]any{"header": "strict-transport-security"}))
	if !strings.Contains(doc.Recommended, "max-age=31536000") || !strings.HasPrefix(doc.Reference, "https://") {
		t.Fatalf("doc: %+v", doc)
	}
	if bad := c.call("explain_header", map[string]any{"header": "x-made-up"}); !bad.IsError {
		t.Fatalf("x-made-up explained: %s", bad.Text)
	}
}

func TestAddressClassificationAndURLValidation(t *testing.T) {
	inProcessOnly(t)
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.5.5", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "::", "fe80::1", "fd00::1", "::ffff:10.0.0.1", "ff02::1"} {
		if !isPrivateAddress(ip) {
			t.Fatalf("%s is private", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "93.184.216.34", "172.32.0.1", "2606:4700::1111"} {
		if isPrivateAddress(ip) {
			t.Fatalf("%s is public", ip)
		}
	}
	ctx := context.Background()
	for raw, want := range map[string]string{"not a url": "not an absolute URL", "http://app.internal/": "local hostname", "http://evil.example/": "10.0.0.5"} {
		if _, err := assertPublicURL(ctx, raw, fakeResolve); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", raw, err, want)
		}
	}
	ok, err := assertPublicURL(ctx, "https://v6.example/x", fakeResolve)
	if err != nil || ok.hostname() != "v6.example" {
		t.Fatalf("v6.example: %v %v", ok, err)
	}
	failing := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("timeout") })
	if _, err := fetchHeaders("https://public.example/", "HEAD", fetchOptions{Transport: failing, Resolve: fakeResolve}); err == nil || !strings.Contains(err.Error(), "failed: timeout") {
		t.Fatalf("failing fetch: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
