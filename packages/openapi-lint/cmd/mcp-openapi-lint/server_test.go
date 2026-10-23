package main

// The upstream suite test/server.test.mjs, ported case by case. The upstream cases call
// lint() and parseDocument() directly; here the same documents go through the tools.

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

type finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type lintResult struct {
	Source     string    `json:"source"`
	OpenAPI    any       `json:"openapi"`
	Title      any       `json:"title"`
	Operations int       `json:"operations"`
	Error      int       `json:"error"`
	Warning    int       `json:"warning"`
	Info       int       `json:"info"`
	Findings   []finding `json:"findings"`
}

func ruleIDs(fs []finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

func TestListsTheFourTools(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != "openapi-lint" {
		t.Fatalf("serverInfo.name: %s", init.raw)
	}
	reply := c.request("tools/list", map[string]any{})
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &list); err != nil {
		t.Fatalf("tools/list: %s", reply.raw)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if want := []string{"explain_rule", "get_operation", "lint_openapi", "list_operations"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestLintOpenapiFindsThePlantedProblemsInTheYAMLFixture(t *testing.T) {
	c := connected(t)
	fixture := fixturePath(t)
	r := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"path": fixture}))
	if r.OpenAPI != "3.0.3" || r.Title != "Pet Store" || r.Operations != 5 {
		t.Fatalf("openapi %v, title %v, operations %d", r.OpenAPI, r.Title, r.Operations)
	}
	rules := ruleIDs(r.Findings)
	for _, want := range []string{"server-http", "unused-security-scheme", "operation-public", "security-scheme-undefined",
		"duplicate-operation-id", "operation-no-success-response", "deprecated-operation", "tag-undefined",
		"parameter-no-description", "operation-no-description", "operation-no-operation-id", "path-parameter-undefined",
		"operation-no-responses"} {
		if !slices.Contains(rules, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, absent := range []string{"path-unversioned", "no-security-schemes", "operation-no-security"} {
		if slices.Contains(rules, absent) {
			t.Errorf("unexpected %s", absent)
		}
	}
	if r.Findings[0].Severity != "error" {
		t.Errorf("first finding has severity %s", r.Findings[0].Severity)
	}
	i := slices.Index(rules, "duplicate-operation-id")
	if i < 0 || r.Findings[i].Path != "/paths/~1pets~1{petId}/delete" || !strings.Contains(r.Findings[i].Message, "GET /pets") {
		t.Fatalf("duplicate-operation-id finding: %+v", r.Findings[max(i, 0)])
	}
	skip := []string{"parameter-no-description", "tag-undefined"}
	ignored := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"path": fixture, "ignore": skip}))
	dropped := 0
	for _, f := range r.Findings {
		if slices.Contains(skip, f.Rule) {
			dropped++
		}
	}
	if slices.Contains(ruleIDs(ignored.Findings), "parameter-no-description") || len(ignored.Findings) != len(r.Findings)-dropped {
		t.Fatalf("ignore left %d findings, want %d", len(ignored.Findings), len(r.Findings)-dropped)
	}
}

func TestLintOpenapiHandlesInlineJSONObjectsSwagger2AndBadInput(t *testing.T) {
	c := connected(t)
	minimal := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": map[string]any{
		"openapi": "3.1.0", "info": map[string]any{"title": "x", "version": "1"},
		"paths": map[string]any{"/things": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}}},
	}}))
	rules := ruleIDs(minimal.Findings)
	for _, want := range []string{"info-description", "no-security-schemes", "operation-no-security", "operation-no-operation-id", "operation-no-description", "path-unversioned"} {
		if !slices.Contains(rules, want) {
			t.Errorf("missing %s", want)
		}
	}
	swagger := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": `{"swagger":"2.0","info":{},"paths":{}}`}))
	if swagger.Findings[0].Rule != "openapi-version" || !regexp.MustCompile(`Swagger 2.0`).MatchString(swagger.Findings[0].Message) {
		t.Fatalf("swagger finding: %+v", swagger.Findings[0])
	}
	fixture := fixturePath(t)
	for name, args := range map[string]map[string]any{
		"both":     {"path": fixture, "document": "{}"},
		"neither":  {},
		"missing":  {"path": "/nonexistent/openapi.yaml"},
		"bad yaml": {"document": "openapi: [unclosed"},
		"list":     {"document": "- a\n- b"},
		"bad json": {"document": "{ not json"},
	} {
		if r := c.call("lint_openapi", args); !r.IsError {
			t.Errorf("%s: expected isError, got %s", name, r.Text)
		}
	}
	if r := c.call("lint_openapi", map[string]any{"document": "{ not json"}); !strings.Contains(r.Text, "not valid JSON") {
		t.Errorf("bad json message: %s", r.Text)
	}
}

func TestListOperationsReportsEffectiveSecurityAndFilters(t *testing.T) {
	c := connected(t)
	text, err := os.ReadFile(fixturePath(t))
	if err != nil {
		t.Fatalf("os.ReadFile(fixturePath(t)): %v", err)
	}
	type operation struct {
		Method      string            `json:"method"`
		Path        string            `json:"path"`
		Deprecated  bool              `json:"deprecated"`
		Security    any               `json:"security"`
		Parameters  []json.RawMessage `json:"parameters"`
		RequestBody any               `json:"request_body"`
		Responses   []string          `json:"responses"`
	}
	type listing struct {
		Count      int         `json:"count"`
		Operations []operation `json:"operations"`
	}
	r := decodeResult[listing](t, c.call("list_operations", map[string]any{"document": string(text)}))
	if r.Count != 5 {
		t.Fatalf("count %d", r.Count)
	}
	byID := map[string]operation{}
	for _, o := range r.Operations {
		byID[o.Method+" "+o.Path] = o
	}
	asJSON := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	checks := []struct{ got, want string }{
		{asJSON(byID["GET /pets"].Security), `[["bearer"]]`},
		{asJSON(byID["POST /pets"].Security), `"public"`},
		{asJSON(byID["POST /pets"].RequestBody), `["application/json"]`},
		{asJSON(byID["GET /pets/{petId}"].Parameters), `[{"name":"petId","in":"path","required":true}]`},
		{asJSON(byID["GET /pets/{petId}"].Responses), `["200","404"]`},
		{asJSON(byID["DELETE /pets/{petId}"].Deprecated), `true`},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("got %s, want %s", check.got, check.want)
		}
	}
	if f := decodeResult[listing](t, c.call("list_operations", map[string]any{"document": string(text), "tag": "pets"})); f.Count != 1 {
		t.Errorf("tag filter count %d", f.Count)
	}
	if f := decodeResult[listing](t, c.call("list_operations", map[string]any{"document": string(text), "path_prefix": "/owners", "method": "get"})); f.Count != 1 {
		t.Errorf("prefix and method filter count %d", f.Count)
	}
	none := decodeResult[lintResult](t, c.call("lint_openapi", map[string]any{"document": map[string]any{
		"openapi": "3.0.0", "info": map[string]any{},
		"paths": map[string]any{"/a": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "x"}}}}},
	}}))
	if !slices.Contains(ruleIDs(none.Findings), "operation-no-security") {
		t.Errorf("lint_openapi of a document without security: findings %v, want operation-no-security among them", ruleIDs(none.Findings))
	}
}

func TestGetOperationResolvesReferencesAndExplainRuleDocumentsRules(t *testing.T) {
	c := connected(t)
	fixture := fixturePath(t)
	type got struct {
		Method    string `json:"method"`
		Operation struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name string `json:"name"`
			} `json:"parameters"`
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"operation"`
		EffectiveSecurity json.RawMessage `json:"effective_security"`
	}
	byID := decodeResult[got](t, c.call("get_operation", map[string]any{"path": fixture, "operation_id": "getPet"}))
	var security bytes.Buffer
	json.Compact(&security, byID.EffectiveSecurity)
	if byID.Method != "GET" || len(byID.Operation.Parameters) == 0 || byID.Operation.Parameters[0].Name != "petId" ||
		byID.Operation.Responses["404"].Description != "Not found." || security.String() != `[{"bearer":[]}]` {
		t.Fatalf("getPet: %+v", byID)
	}
	byPath := decodeResult[got](t, c.call("get_operation", map[string]any{"path": fixture, "method": "post", "operation_path": "/pets"}))
	if byPath.Operation.OperationID != "createPet" {
		t.Fatalf("POST /pets operationId %q", byPath.Operation.OperationID)
	}
	if r := c.call("get_operation", map[string]any{"path": fixture, "operation_id": "nope"}); !r.IsError {
		t.Errorf("unknown operationId: %s", r.Text)
	}
	if r := c.call("get_operation", map[string]any{"path": fixture, "method": "get"}); !r.IsError {
		t.Errorf("method without operation_path: %s", r.Text)
	}
	for _, rule := range ruleNames {
		doc := decodeResult[struct {
			Description string `json:"description"`
			Fix         string `json:"fix"`
		}](t, c.call("explain_rule", map[string]any{"rule": rule}))
		if len(doc.Description) <= 20 || len(doc.Fix) <= 10 {
			t.Errorf("%s: description %q fix %q", rule, doc.Description, doc.Fix)
		}
	}
	if r := c.call("explain_rule", map[string]any{"rule": "no-such-rule"}); !r.IsError {
		t.Errorf("unknown rule: %s", r.Text)
	}
}
