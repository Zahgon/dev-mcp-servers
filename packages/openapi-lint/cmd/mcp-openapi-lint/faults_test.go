package main

// Behaviour the upstream suite leaves unchecked: every rule, severities, pointers, the
// listing and get_operation shapes, the document loaders and the request contract.
// Each test pins exact results, so a server that drops or bends one of them fails.

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

const everyRuleDocument = `openapi: "2.0"
info:
  title: Every rule
servers:
  - url: http://api.example.com
components:
  securitySchemes:
    unused:
      type: http
      scheme: bearer
paths:
  /items/{id}:
    get: {}
    post:
      operationId: dup
      summary: Create
      security: []
      deprecated: true
      tags: [ghost]
      parameters:
        - name: id
          in: path
      requestBody:
        description: no content
      responses:
        "404": {}
    put:
      operationId: dup
      summary: Replace
      security:
        - missing: []
      parameters:
        - name: id
          in: path
          required: true
          description: The item.
          schema: {type: string}
      responses:
        "200":
          description: OK.
`

// everyRuleFindings is the rule, severity and pointer of each finding of everyRuleDocument.
var everyRuleFindings = []string{
	"openapi-version error /openapi",
	"path-parameter-undefined error /paths/~1items~1{id}/get",
	"operation-no-responses error /paths/~1items~1{id}/get/responses",
	"duplicate-operation-id error /paths/~1items~1{id}/post",
	"path-parameter-not-required error /paths/~1items~1{id}/post/parameters/0",
	"security-scheme-undefined error /paths/~1items~1{id}/put/security/0",
	"operation-no-operation-id warning /paths/~1items~1{id}/get",
	"operation-no-description warning /paths/~1items~1{id}/get",
	"operation-no-security warning /paths/~1items~1{id}/get",
	"parameter-no-schema warning /paths/~1items~1{id}/post/parameters/0",
	"request-body-no-content warning /paths/~1items~1{id}/post/requestBody",
	"operation-no-success-response warning /paths/~1items~1{id}/post/responses",
	"response-no-description warning /paths/~1items~1{id}/post/responses/404",
	"server-http warning /servers/0",
	"unused-security-scheme info /components/securitySchemes/unused",
	"info-description info /info",
	"path-unversioned info /paths/~1items~1{id}",
	"deprecated-operation info /paths/~1items~1{id}/post",
	"parameter-no-description info /paths/~1items~1{id}/post/parameters/0",
	"operation-public info /paths/~1items~1{id}/post/security",
	"tag-undefined info /paths/~1items~1{id}/post/tags",
}

var severityRank = map[string]int{"error": 0, "warning": 1, "info": 2}

// checkLint compares a lint result with the expected findings as a multiset, and checks
// the counts, the messages and the severity order.
func checkLint(t *testing.T, r lintResult, want []string) {
	t.Helper()
	var got []string
	counts := map[string]int{}
	for i, f := range r.Findings {
		got = append(got, f.Rule+" "+f.Severity+" "+f.Path)
		counts[f.Severity]++
		if f.Message == "" {
			t.Errorf("finding %d has no message", i)
		}
		if i > 0 && severityRank[r.Findings[i-1].Severity] > severityRank[f.Severity] {
			t.Errorf("finding %d (%s) comes after a less severe one", i, f.Severity)
		}
	}
	sortedWant := slices.Clone(want)
	sort.Strings(got)
	sort.Strings(sortedWant)
	if !slices.Equal(got, sortedWant) {
		t.Errorf("findings\n got: %q\nwant: %q", got, sortedWant)
	}
	if r.Error != counts["error"] || r.Warning != counts["warning"] || r.Info != counts["info"] {
		t.Errorf("counts error %d warning %d info %d, findings have %v", r.Error, r.Warning, r.Info, counts)
	}
}

func TestEveryRuleIsReportedWithItsSeverityAndPointer(t *testing.T) {
	c := connected(t)
	r := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": everyRuleDocument}))
	if r.Operations != 3 || r.OpenAPI != "2.0" || r.Title != "Every rule" {
		t.Errorf("summary fields: %+v", r)
	}
	checkLint(t, r, everyRuleFindings)
	empty := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": `{"openapi":"3.0.3","info":{"title":"t","description":"d"},"paths":{}}`}))
	checkLint(t, empty, []string{"no-security-schemes warning /components/securitySchemes"})
	for _, rule := range ruleNames {
		doc := decodeResult[struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
		}](t, c.call("explain_rule", map[string]any{"rule": rule}))
		if doc.Rule != rule || doc.Severity != rules[rule].Severity {
			t.Errorf("explain_rule %s: %+v", rule, doc)
		}
	}
}

func TestIgnoreSilencesOnlyTheNamedRules(t *testing.T) {
	c := connected(t)
	skip := []string{"server-http", "tag-undefined", "operation-no-security"}
	r := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": everyRuleDocument, "ignore": skip}))
	var want []string
	for _, f := range everyRuleFindings {
		if !slices.Contains(skip, strings.Fields(f)[0]) {
			want = append(want, f)
		}
	}
	checkLint(t, r, want)
}

func TestLocalServersAndGlobalSecurityAreNotReported(t *testing.T) {
	c := connected(t)
	doc := `openapi: 3.0.3
info: {title: s, description: d}
servers:
  - url: http://localhost:8080/v1
  - url: http://127.0.0.1/v1
  - url: http://[::1]/v1
  - url: http://public.example.com/v1
security:
  - key: []
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
tags:
  - name: t
paths:
  /a/b:
    get:
      operationId: ab
      summary: s
      tags: [t]
      responses:
        "200": {description: ok}
`
	r := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": doc}))
	checkLint(t, r, []string{"server-http warning /servers/3"})
}

const listingDocument = `openapi: 3.0.3
security:
  - key: []
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
  parameters:
    Trace: {name: trace, in: header}
paths:
  /v1/items/{id}:
    parameters:
      - name: id
        in: path
      - $ref: '#/components/parameters/Trace'
    get:
      operationId: getItem
      description: "` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `\nsecond line"
      tags: [items, null, 7]
      parameters:
        - name: verbose
          in: query
      responses:
        default: {description: d}
        2XX: {description: d}
        "200": {description: d}
    post:
      operationId: createItem
      summary: Create
      security: []
      requestBody:
        content:
          application/json: {}
          text/plain: {}
      responses:
        "201": {description: d}
  /items:
    get:
      summary: Old
      security: [{}]
      responses: {}
`

func TestListOperationsShapesEveryField(t *testing.T) {
	c := connected(t)
	sameJSON(t, c.call("list_operations", map[string]any{"document": listingDocument}), `{"source":"inline","count":3,"operations":[
{"method":"GET","path":"/v1/items/{id}","operation_id":"getItem","summary":"`+strings.Repeat("x", 120)+`","tags":["items","null","7"],"deprecated":false,"security":[["key"]],"parameters":[{"name":"id","in":"path","required":true},{"name":"trace","in":"header","required":false},{"name":"verbose","in":"query","required":false}],"request_body":null,"responses":["200","default","2XX"]},
{"method":"POST","path":"/v1/items/{id}","operation_id":"createItem","summary":"Create","tags":[],"deprecated":false,"security":"public","parameters":[{"name":"id","in":"path","required":true},{"name":"trace","in":"header","required":false}],"request_body":["application/json","text/plain"],"responses":["201"]},
{"method":"GET","path":"/items","operation_id":null,"summary":"Old","tags":[],"deprecated":false,"security":[[]],"parameters":[],"request_body":null,"responses":[]}]}`)
	sameJSON(t, c.call("list_operations", map[string]any{"document": listingDocument, "path_prefix": "/items"}), `{"source":"inline","count":1,"operations":[
{"method":"GET","path":"/items","operation_id":null,"summary":"Old","tags":[],"deprecated":false,"security":[[]],"parameters":[],"request_body":null,"responses":[]}]}`)
	sameJSON(t, c.call("list_operations", map[string]any{"document": `{"openapi":"3.0.3","paths":{"/z":{"get":{}}}}`}), `{"source":"inline","count":1,"operations":[
{"method":"GET","path":"/z","operation_id":null,"summary":null,"tags":[],"deprecated":false,"security":"inherited-none","parameters":[],"request_body":null,"responses":[]}]}`)
}

const getOperationDocument = `openapi: 3.0.3
components:
  parameters:
    A: {$ref: '#/components/parameters/B'}
    B: {$ref: '#/components/parameters/C'}
    C: {$ref: '#/components/parameters/D'}
    D: {$ref: '#/components/parameters/E'}
    E: {name: deep, in: query}
    a/b~c: {name: escaped, in: header}
  responses:
    NotFound: {$ref: '#/components/responses/Missing'}
    Missing: {description: Not found.}
paths:
  /things/{id}:
    parameters:
      - {name: id, in: path, required: true}
    get:
      operationId: getThing
      parameters:
        - $ref: '#/components/parameters/A'
        - $ref: '#/components/parameters/a~1b~0c'
      responses:
        "404": {$ref: '#/components/responses/NotFound'}
    put:
      - first
      - second
`

func TestGetOperationMergesResolvesAndKeepsShape(t *testing.T) {
	c := connected(t)
	sameJSON(t, c.call("get_operation", map[string]any{"document": getOperationDocument, "operation_id": "getThing"}),
		`{"method":"GET","path":"/things/{id}","operation":{"operationId":"getThing","parameters":[{"name":"id","in":"path","required":true},{"name":"deep","in":"query"},{"name":"escaped","in":"header"}],"responses":{"404":{"description":"Not found."}}},"effective_security":null}`)
	sameJSON(t, c.call("get_operation", map[string]any{"document": getOperationDocument, "method": "put", "operation_path": "/things/{id}"}),
		`{"method":"PUT","path":"/things/{id}","operation":{"0":"first","1":"second","parameters":[{"name":"id","in":"path","required":true}],"responses":{}},"effective_security":null}`)
}

func TestYAMLLoadsAsTheYAMLPackageReadsIt(t *testing.T) {
	c := connected(t)
	doc := `openapi: 3.0.3
paths:
  /a:
    get:
      operationId: op
      x-v:
        words: [yes, no, on, off, y, n]
        k: first
        base: &base {x: 1}
        merged:
          <<: *base
          y: 2
        k: last
`
	sameJSON(t, c.call("get_operation", map[string]any{"document": doc, "operation_id": "op"}),
		`{"method":"GET","path":"/a","operation":{"operationId":"op","x-v":{"words":["yes","no","on","off","y","n"],"k":"last","base":{"x":1},"merged":{"<<":{"x":1},"y":2}},"parameters":[],"responses":{}},"effective_security":null}`)
	if r := c.call("lint_openapi", map[string]any{"document": "openapi: 3.0.3\n---\nopenapi: 3.1.0\n"}); !r.IsError {
		t.Errorf("a stream of two documents loaded: %s", r.Text)
	}
	aliases := func(n int) string {
		return "openapi: 3.0.3\ns: &s v\nl:\n" + strings.Repeat("  - *s\n", n)
	}
	if r := c.call("lint_openapi", map[string]any{"document": aliases(99)}); r.IsError {
		t.Errorf("99 aliases rejected: %s", r.Text)
	}
	if r := c.call("lint_openapi", map[string]any{"document": aliases(100)}); !r.IsError {
		t.Errorf("%s: 100 aliases of one anchor accepted, want a tool error: %s", r.call, r.Text)
	}
	if r := c.call("lint_openapi", map[string]any{"document": "  {openapi: 3.0.3, paths: {}}"}); !r.IsError {
		t.Errorf("text starting with { that is not JSON was loaded: %s", r.Text)
	}
}

func TestJSONKeepsUnpairedSurrogates(t *testing.T) {
	c := connected(t)
	doc := `{"openapi":"3.0.3","paths":{"/a":{"get":{"operationId":"op","x-v":"a\ud83dz"}}}}`
	r := c.call("get_operation", map[string]any{"document": doc, "operation_id": "op"})
	if r.IsError || !strings.Contains(string(r.raw), `\\ud83d`) {
		t.Fatalf("the unpaired surrogate did not survive: %s", r.raw)
	}
}

func TestToolArgumentsAreValidatedAsTheSchemasSay(t *testing.T) {
	c := connected(t)
	doc := "openapi: 3.0.3\npaths: {}\n"
	if r := c.call("list_operations", map[string]any{"document": doc, "method": "GET"}); !r.IsError {
		t.Errorf("%s: upper-case method accepted, want a tool error: %s", r.call, r.Text)
	}
	if r := c.call("list_operations", map[string]any{"document": doc, "tag": strings.Repeat("é", 100)}); r.IsError {
		t.Errorf("a tag of 100 UTF-16 code units rejected: %s", r.Text)
	}
	if r := c.call("list_operations", map[string]any{"document": doc, "tag": strings.Repeat("😀", 51)}); !r.IsError {
		t.Errorf("%s: a tag of 102 UTF-16 code units accepted, want a tool error: %s", r.call, r.Text)
	}
	ignore := make([]string, 51)
	for i := range ignore {
		ignore[i] = "tag-undefined"
	}
	if r := c.call("lint_openapi", map[string]any{"document": doc, "ignore": ignore}); !r.IsError {
		t.Errorf("%s: 51 ignore entries accepted, want a tool error: %s", r.call, r.Text)
	}
	if r := c.call("lint_openapi", map[string]any{"document": doc, "ignore": ignore[:50]}); r.IsError {
		t.Errorf("50 ignore entries rejected: %s", r.Text)
	}
	unknown := c.request("tools/call", map[string]any{"name": "no_such_tool", "arguments": map[string]any{}})
	if r := c.toolResult(unknown); !r.IsError {
		t.Errorf("unknown tool: %s", r.Text)
	}
}

func TestRequestContract(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	silent := map[string]string{
		"not json":          "this is not json",
		"wrong version":     `{"jsonrpc":"1.0","id":2,"method":"ping"}`,
		"extra member":      `{"jsonrpc":"2.0","id":3,"method":"ping","extra":1}`,
		"fractional id":     `{"jsonrpc":"2.0","id":4.5,"method":"ping"}`,
		"null id":           `{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		"batch":             `[{"jsonrpc":"2.0","id":5,"method":"ping"}]`,
		"params not object": `{"jsonrpc":"2.0","id":6,"method":"ping","params":[1]}`,
		"notification":      `{"jsonrpc":"2.0","method":"notifications/whatever"}`,
		"response":          `{"jsonrpc":"2.0","id":7,"result":{}}`,
		"unsafe integer id": `{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`,
	}
	for name, line := range silent {
		replies := exchange(t, line, ping)
		if len(replies) != 1 || string(replies[0].ID) != "1" {
			t.Errorf("%s: want only the ping reply, got %d lines", name, len(replies))
		}
	}
	codes := map[string]struct {
		line string
		code int
	}{
		"unknown method":           {`{"jsonrpc":"2.0","id":8,"method":"nope"}`, -32601},
		"tools/call without name":  {`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{}}`, -32603},
		"arguments not an object":  {`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"explain_rule","arguments":[1]}}`, -32603},
		"initialize without param": {`{"jsonrpc":"2.0","id":8,"method":"initialize","params":{}}`, -32603},
	}
	for name, tc := range codes {
		replies := exchange(t, tc.line)
		if len(replies) != 1 || replies[0].Error == nil || replies[0].Error.Code != tc.code {
			t.Errorf("%s: want error %d, got %d replies %v", name, tc.code, len(replies), replies)
		}
	}
	for requested, want := range map[string]string{"1999-01-01": "2025-11-25", "2024-11-05": "2024-11-05"} {
		line := fmt.Sprintf(`{"jsonrpc":"2.0","id":9,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, requested)
		replies := exchange(t, line)
		var result struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(replies) != 1 || json.Unmarshal(replies[0].Result, &result) != nil || result.ProtocolVersion != want {
			t.Errorf("initialize %s: want protocolVersion %s, got %v", requested, want, replies)
		}
	}
	if replies := exchange(t, ping+"\r", `{"jsonrpc":"2.0","id":"s","method":"ping"}`); len(replies) != 2 {
		t.Errorf("CRLF line and string id: %d replies", len(replies))
	}
}

func TestYAMLExplicitTagsResolveAsTheYAMLPackageDoes(t *testing.T) {
	c := connected(t)
	doc := "openapi: 3.0.3\npaths:\n  /a:\n    get:\n      operationId: op\n      x-v:\n" +
		"        t: !!timestamp 2001-12-14t21:59:43.10-05:00\n        b: !!binary aGk=\n        f: !!float 3\n        g: !!float 2.5\n"
	sameJSON(t, c.call("get_operation", map[string]any{"document": doc, "operation_id": "op"}),
		`{"method":"GET","path":"/a","operation":{"operationId":"op","x-v":{"t":"2001-12-15T02:59:43.100Z","b":{"type":"Buffer","data":[104,105]},"f":"3","g":2.5},"parameters":[],"responses":{}},"effective_security":null}`)
}

func TestJSONIsReadExactlyAsJSONParseReadsIt(t *testing.T) {
	c := connected(t)
	for name, doc := range map[string]string{
		"byte order mark": "\ufeff{\"openapi\":\"3.0.3\",\"paths\":{}}",
		"trailing comma":  `{"openapi":"3.0.3","paths":{},}`,
		"leading zero":    `{"openapi":"3.0.3","paths":{},"x":01}`,
	} {
		if r := c.call("lint_openapi", map[string]any{"document": doc}); !r.IsError {
			t.Errorf("%s accepted: %s", name, r.Text)
		}
	}
	doc := `{"openapi":"3.0.3","paths":{"/a":{"get":{"operationId":"op","x-v":{"k":1,"__proto__":{"p":1},"k":2}}}}}`
	sameJSON(t, c.call("get_operation", map[string]any{"document": doc, "operation_id": "op"}),
		`{"method":"GET","path":"/a","operation":{"operationId":"op","x-v":{"k":2,"__proto__":{"p":1}},"parameters":[],"responses":{}},"effective_security":null}`)
}

func TestGetOperationSpreadsStringsAndFollowsPathItemReferences(t *testing.T) {
	c := connected(t)
	doc := `{"openapi":"3.0.3","components":{"pathItems":{"P":{"get":{"operationId":"viaRef"}}}},"paths":{` +
		`"/s":{"parameters":"a😀","get":{"operationId":"strings","responses":"ab"}},` +
		`"/r":{"$ref":"#/components/pathItems/P"}}}`
	sameJSON(t, c.call("get_operation", map[string]any{"document": doc, "operation_id": "strings"}),
		`{"method":"GET","path":"/s","operation":{"operationId":"strings","responses":{"0":"a","1":"b"},"parameters":["a","😀"]},"effective_security":null}`)
	sameJSON(t, c.call("get_operation", map[string]any{"document": doc, "method": "get", "operation_path": "/r"}),
		`{"method":"GET","path":"/r","operation":{"operationId":"viaRef","parameters":[],"responses":{}},"effective_security":null}`)
}

func TestLintCoercionsFollowJavaScript(t *testing.T) {
	c := connected(t)
	doc := `{"openapi":"3.0.3","info":{"title":"k","description":"\u0085"},"servers":[{"url":"https://x/{verſion}"}],` +
		`"components":{"securitySchemes":{"a/b":{"type":"http"}}},"security":[],` +
		`"paths":{"/t/{id}/{id}":{"get":{"operationId":"o","summary":"s","security":[],"responses":{"200":{"description":"ok"}}}}}}`
	r := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": doc}))
	checkLint(t, r, []string{
		"path-parameter-undefined error /paths/~1t~1{id}~1{id}/get",
		"path-parameter-undefined error /paths/~1t~1{id}~1{id}/get",
		"unused-security-scheme info /components/securitySchemes/a/b",
		"path-unversioned info /paths/~1t~1{id}~1{id}",
		"operation-public info /paths/~1t~1{id}~1{id}/get/security",
	})
	listing := c.call("list_operations", map[string]any{"document": `{"openapi":"3.0.3","paths":{"/a":{"get":{"description":"` + strings.Repeat("x", 119) + `😀tail"}}}}`})
	if listing.IsError || !strings.Contains(string(listing.raw), `"summary\": \"`+strings.Repeat("x", 119)+`\\ud83d\"`) {
		t.Errorf("summary is not cut at 120 UTF-16 code units: %s", listing.raw)
	}
}

func TestMoreArgumentLimits(t *testing.T) {
	c := connected(t)
	fixture := fixturePath(t)
	long := "/" + strings.Repeat("./", 2100) + strings.TrimPrefix(fixture, "/")
	if r := c.call("lint_openapi", map[string]any{"path": long}); !r.IsError {
		t.Errorf("a path of %d code units accepted", len(long))
	}
	if r := c.call("lint_openapi", map[string]any{"path": fixture, "ignore": []string{"no-such-rule"}}); !r.IsError {
		t.Errorf("%s: an unknown rule id in ignore accepted, want a tool error: %s", r.call, r.Text)
	}
	if replies := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":5}}`); len(replies) != 1 || replies[0].Error == nil || replies[0].Error.Code != -32603 {
		t.Errorf("tools/list with a numeric cursor: %v", replies)
	}
}
