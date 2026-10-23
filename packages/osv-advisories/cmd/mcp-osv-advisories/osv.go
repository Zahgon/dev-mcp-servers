package main

// Minimal OSV.dev client. The only host it will ever contact is api.osv.dev, over HTTPS,
// with a timeout and a cap on the response size.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	osvOrigin               = "https://api.osv.dev"
	defaultTimeout          = 15 * time.Second
	defaultMaxResponseBytes = 8 * 1024 * 1024
	batchChunk              = 100
	maxBatchQueries         = 1000
	timeoutMessage          = "The operation was aborted due to timeout"
)

// clientOptions are the OsvClient options; a zero field takes the default. Transport is
// the fetch implementation: the default refuses redirects as fetch does with
// redirect: "error".
type clientOptions struct {
	Transport        http.RoundTripper
	Timeout          time.Duration
	MaxResponseBytes int64
}

type osvClient struct {
	transport http.RoundTripper
	timeout   time.Duration
	maxBytes  int64
}

func newOSVClient(o clientOptions) *osvClient {
	c := &osvClient{transport: o.Transport, timeout: o.Timeout, maxBytes: o.MaxResponseBytes}
	if c.transport == nil {
		c.transport = fetchTransport{base: http.DefaultTransport}
	}
	if c.timeout == 0 {
		c.timeout = defaultTimeout
	}
	if c.maxBytes == 0 {
		c.maxBytes = defaultMaxResponseBytes
	}
	return c
}

// requestPath is the pathname of new URL(path, OSV_ORIGIN) for the paths the client
// builds: dot segments are resolved.
func requestPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/.."):
		parent := path[:strings.LastIndex(path[:len(path)-3], "/")+1]
		return parent
	case strings.HasSuffix(path, "/."):
		return path[:len(path)-1]
	}
	return path
}

// request performs one call; a nil value with no error is a 404.
func (c *osvClient) request(path string, body any) (any, error) {
	pathname := requestPath(path)
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	method := http.MethodGet
	var payload io.Reader
	if body != nil {
		method = http.MethodPost
		payload = strings.NewReader(jsv.Stringify(body, 0))
	}
	req, err := http.NewRequestWithContext(ctx, method, osvOrigin+pathname, payload)
	if err != nil {
		return nil, osvError("request to %s failed: %s", pathname, err.Error())
	}
	req.Header.Set("accept", "application/json")
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	res, err := c.transport.RoundTrip(req)
	if err != nil {
		message := err.Error()
		if ctx.Err() != nil {
			message = timeoutMessage
		}
		return nil, osvError("request to %s failed: %s", pathname, message)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, osvError("OSV responded %d for %s", res.StatusCode, pathname)
	}
	text, err := c.readBounded(ctx, res)
	if err != nil {
		return nil, err
	}
	v, err := jsv.Parse(text)
	if err != nil {
		return nil, osvError("OSV returned a non-JSON body for %s", pathname)
	}
	return v, nil
}

func (c *osvClient) readBounded(ctx context.Context, res *http.Response) (string, error) {
	declared := jsNumber(res.Header.Get("Content-Length"))
	if declared > float64(c.maxBytes) {
		return "", osvError("response of %s bytes exceeds the %d byte limit", jsv.FormatNumber(declared), c.maxBytes)
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(res.Body, c.maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", &jsError{name: "TimeoutError", message: timeoutMessage}
		}
		return "", &jsError{name: "TypeError", message: "terminated"}
	}
	if n > c.maxBytes {
		return "", osvError("response exceeds the %d byte limit", c.maxBytes)
	}
	return decodeUTF8(buf.Bytes()), nil
}

// queryPackage is POST /v1/query, following next_page_token for up to five pages.
func (c *osvClient) queryPackage(ecosystem, name, version string) ([]any, error) {
	var vulns []any
	var pageToken any
	for page := 0; page < 5; page++ {
		payload := jsv.O("package", jsv.O("name", name, "ecosystem", ecosystem))
		if version != "" {
			payload.Set("version", version)
		}
		if truthy(pageToken, true) {
			payload.Set("page_token", pageToken)
		}
		data, err := c.request("/v1/query", payload)
		if err != nil {
			return nil, err
		}
		if !truthy(data, true) {
			break
		}
		list, listOK, _ := prop(data, "vulns")
		items, err := spread(list, listOK)
		if err != nil {
			return nil, err
		}
		vulns = append(vulns, items...)
		token, tokenOK, _ := prop(data, "next_page_token")
		if !truthy(token, tokenOK) {
			break
		}
		pageToken = token
	}
	return vulns, nil
}

// batchQuery is one package of a /v1/querybatch request; version may be any JSON value
// read from a lockfile, and only a truthy one is sent.
type batchQuery struct {
	ecosystem     string
	name, version any
}

// queryBatch is POST /v1/querybatch in chunks; it returns, per query, the advisory ids.
func (c *osvClient) queryBatch(queries []batchQuery) ([][]any, error) {
	if len(queries) > maxBatchQueries {
		return nil, osvError("at most %d packages per batch", maxBatchQueries)
	}
	var out [][]any
	for i := 0; i < len(queries); i += batchChunk {
		chunk := queries[i:min(i+batchChunk, len(queries))]
		list := []any{}
		for _, q := range chunk {
			item := jsv.O("package", jsv.O("name", q.name, "ecosystem", q.ecosystem))
			if truthy(q.version, true) {
				item.Set("version", q.version)
			}
			list = append(list, item)
		}
		data, err := c.request("/v1/querybatch", jsv.O("queries", list))
		if err != nil {
			return nil, err
		}
		results, resultsOK, err := optionalProp(data, data != nil, "results")
		if err != nil {
			return nil, err
		}
		if nullish(results, resultsOK) {
			results = []any{}
		}
		for j := range chunk {
			row, rowOK, err := prop(results, strconv.Itoa(j))
			if err != nil {
				return nil, err
			}
			vulns, vulnsOK, err := optionalProp(row, rowOK, "vulns")
			if err != nil {
				return nil, err
			}
			if nullish(vulns, vulnsOK) {
				vulns = []any{}
			}
			arr, isArr := vulns.([]any)
			if !isArr {
				return nil, typeError("((intermediate value) ?? []).map is not a function")
			}
			ids := []any{}
			for _, v := range arr {
				id, idOK, err := prop(v, "id")
				if err != nil {
					return nil, err
				}
				if !idOK {
					id = undefined
				}
				ids = append(ids, id)
			}
			out = append(out, ids)
		}
	}
	return out, nil
}

// getVulnerability is GET /v1/vulns/{id}; nil means OSV does not know the id.
func (c *osvClient) getVulnerability(id any) (any, error) {
	encoded, err := encodeURIComponent(toString(id))
	if err != nil {
		return nil, err
	}
	return c.request("/v1/vulns/"+encoded, nil)
}
