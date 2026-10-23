package main

// In-process test support: upstream cases that inject a fetch double into createServer()
// run the server in this process with an injected http.RoundTripper. They cannot drive a
// separate executable, so they are skipped when MCP_SERVER_BIN is set.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

// requireInProcess skips a test that needs an injected transport when the server under
// test is an external executable.
func requireInProcess(t *testing.T) {
	t.Helper()
	if os.Getenv("MCP_SERVER_BIN") != "" {
		t.Skip("injects a fetch double into the server; runs only against the in-process server")
	}
}

// connectedWith serves newServerWith(options) on in-memory pipes and runs the handshake.
func connectedWith(t *testing.T, options clientOptions) *mcpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	server := newServerWith(options)
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Serve(inR, outW)
		outW.Close()
	}()
	c := &mcpClient{t: t, stdin: inW, lines: make(chan []byte, 64), nextID: 100}
	go func() {
		defer close(c.lines)
		reader := bufio.NewReaderSize(outR, 1<<20)
		for {
			line, err := reader.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				c.lines <- line
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		inW.Close()
		<-done
	})
	if reply := c.initialize("2025-06-18"); reply.Error != nil {
		t.Fatalf("initialize: got the error reply %s, want a result", reply.raw)
	}
	return c
}

const ghsaJSON = `{"id":"GHSA-test-0001","summary":"Prototype pollution in example","aliases":["CVE-2020-0001"],"published":"2020-01-01T00:00:00Z","modified":"2021-01-01T00:00:00Z",` +
	`"severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}],` +
	`"affected":[{"package":{"ecosystem":"npm","name":"example"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.2.3"}]}]}],` +
	`"references":[{"type":"ADVISORY","url":"https://example.com/advisory"}],"database_specific":{"severity":"HIGH"}}`

type recordedCall struct {
	URL    string
	Method string
	Body   map[string]any
}

// fakeOSV is the upstream fetch double: it only answers api.osv.dev and records every request.
type fakeOSV struct {
	t     *testing.T
	mu    sync.Mutex
	calls []recordedCall
}

func respond(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}

func (f *fakeOSV) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		if len(data) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				f.t.Errorf("request body is not JSON: %s", data)
			}
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{URL: req.URL.String(), Method: req.Method, Body: body})
	f.mu.Unlock()
	if origin := req.URL.Scheme + "://" + req.URL.Host; origin != "https://api.osv.dev" {
		f.t.Errorf("request to %s", origin)
	}
	packageName := func(q map[string]any) string {
		pkg, _ := q["package"].(map[string]any)
		name, _ := pkg["name"].(string)
		return name
	}
	switch path := req.URL.Path; {
	case path == "/v1/query":
		if packageName(body) == "example" {
			return respond(200, `{"vulns":[`+ghsaJSON+`]}`), nil
		}
		return respond(200, `{}`), nil
	case path == "/v1/querybatch":
		queries, _ := body["queries"].([]any)
		var results []string
		for _, q := range queries {
			qm, _ := q.(map[string]any)
			if n := packageName(qm); n == "example" || n == "lodash" {
				results = append(results, `{"vulns":[{"id":"GHSA-test-0001","modified":"x"}]}`)
			} else {
				results = append(results, `{}`)
			}
		}
		return respond(200, `{"results":[`+strings.Join(results, ",")+`]}`), nil
	case path == "/v1/vulns/GHSA-test-0001":
		return respond(200, ghsaJSON), nil
	case strings.HasPrefix(path, "/v1/vulns/"):
		return respond(404, "not found"), nil
	}
	return respond(500, "boom"), nil
}

func (f *fakeOSV) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall{}, f.calls...)
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
