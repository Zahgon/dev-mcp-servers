package main

// The upstream suite test/server.test.mjs, ported case by case. The upstream assertions that call
// validate, inferSchema and diffSchemas directly send the same inputs through the tools, so the
// whole suite also holds for a server started from MCP_SERVER_BIN.

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func userSchema() map[string]any {
	return map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"id":    map[string]any{"type": "integer", "minimum": 1},
			"email": map[string]any{"type": "string", "format": "email"},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 3},
			"role":  map[string]any{"enum": []any{"admin", "user"}},
		},
		"required":             []any{"id", "email"},
		"additionalProperties": false,
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(v): %v", err)
	}
	return string(b)
}

type validation struct {
	Valid  bool   `json:"valid"`
	Draft  string `json:"draft"`
	Errors []struct {
		Path    string `json:"path"`
		Keyword string `json:"keyword"`
	} `json:"errors"`
}

type change struct {
	Path   string `json:"path"`
	Change string `json:"change"`
	Impact string `json:"impact"`
	After  any    `json:"after"`
}

type schemaDiff struct {
	Verdict  string   `json:"verdict"`
	Breaking int      `json:"breaking"`
	Details  []change `json:"details"`
}

func TestListsTheThreeTools(t *testing.T) {
	c := connected(t)
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
	if !slices.Equal(names, []string{"diff_schemas", "infer_schema", "validate_json"}) {
		t.Fatalf("tools: %v", names)
	}
}

func TestValidateJsonReportsEveryViolationWithPathsAcrossDrafts(t *testing.T) {
	c := connected(t)
	ok := decodeResult[validation](t, c.call("validate_json", map[string]any{"schema": userSchema(),
		"data": map[string]any{"id": 1, "email": "a@b.co", "tags": []any{"x"}, "role": "admin"}}))
	if !ok.Valid || ok.Draft != "draft-07" {
		t.Errorf("valid user: %+v", ok)
	}
	bad := decodeResult[validation](t, c.call("validate_json", map[string]any{"schema": mustJSON(t, userSchema()),
		"data": mustJSON(t, map[string]any{"id": 0, "email": "nope", "tags": []any{1, 2, 3, 4}, "role": "root", "extra": true})}))
	if bad.Valid {
		t.Fatalf("validate_json of an invalid user: valid = true, want false: %+v", bad)
	}
	var keywords []string
	for _, e := range bad.Errors {
		keywords = append(keywords, e.Keyword)
	}
	sort.Strings(keywords)
	want := []string{"additionalProperties", "enum", "format", "maxItems", "minimum", "type", "type", "type", "type"}
	if !slices.Equal(keywords, want) {
		t.Errorf("keywords: %v", keywords)
	}
	has := func(path, keyword string) bool {
		for _, e := range bad.Errors {
			if e.Path == path && e.Keyword == keyword {
				return true
			}
		}
		return false
	}
	if !has("/id", "minimum") || !has("/tags/0", "type") {
		t.Errorf("error paths: %+v", bad.Errors)
	}
	s2020 := decodeResult[validation](t, c.call("validate_json", map[string]any{"schema": map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "array", "prefixItems": []any{map[string]any{"type": "string"}}, "items": false},
		"data": []any{"a", "b"}}))
	if s2020.Draft != "2020-12" || s2020.Valid {
		t.Errorf("2020-12: %+v", s2020)
	}
	s2019 := decodeResult[validation](t, c.call("validate_json", map[string]any{"schema": map[string]any{"type": "object",
		"dependentRequired": map[string]any{"a": []any{"b"}}}, "data": map[string]any{"a": 1}, "draft": "2019-09"}))
	if s2019.Valid || len(s2019.Errors) == 0 || s2019.Errors[0].Keyword != "dependentRequired" {
		t.Errorf("2019-09: %+v", s2019)
	}
	literal := decodeResult[validation](t, c.call("validate_json", map[string]any{"schema": map[string]any{"type": "string"}, "data": `"text"`}))
	if !literal.Valid {
		t.Errorf("string literal: %+v", literal)
	}
	if notJSON := c.call("validate_json", map[string]any{"schema": "{", "data": 1}); !notJSON.IsError {
		t.Errorf("schema { accepted: %s", notJSON.Text)
	}
	if draft4 := c.call("validate_json", map[string]any{"schema": map[string]any{"$schema": "http://json-schema.org/draft-04/schema#", "type": "string"}, "data": "x"}); !draft4.IsError {
		t.Errorf("draft-04 accepted: %s", draft4.Text)
	}
	bogus := c.call("validate_json", map[string]any{"schema": map[string]any{"type": "banana"}, "data": 1})
	if !bogus.IsError || !strings.Contains(bogus.Text, "does not compile") {
		t.Errorf("type banana: %+v", bogus)
	}
}

func TestInferSchemaUnionsTypesFindsRequiredKeysNestsAndDetectsFormats(t *testing.T) {
	c := connected(t)
	samples := []any{
		map[string]any{"id": 1, "name": "a", "created": "2024-01-01T00:00:00Z", "tags": []any{"x"}, "meta": map[string]any{"k": 1}, "status": "open"},
		map[string]any{"id": 2, "name": nil, "created": "2024-02-01T10:00:00+01:00", "tags": []any{}, "meta": map[string]any{"k": 2.5, "extra": true}, "status": "closed"},
		map[string]any{"id": 3.5, "name": "c", "created": "2024-03-01T00:00:00Z", "tags": []any{"y", "z"}, "meta": map[string]any{"k": 3}, "status": "open"},
	}
	type inferred struct {
		Samples int            `json:"samples"`
		Schema  map[string]any `json:"schema"`
	}
	r := decodeResult[inferred](t, c.call("infer_schema", map[string]any{"samples": samples, "enum_threshold": 3, "title": "Ticket"}))
	s := r.Schema
	prop := func(path ...string) any {
		var v any = s
		for _, k := range path {
			m, _ := v.(map[string]any)
			v = m[k]
		}
		return v
	}
	checks := []struct {
		got, want any
		what      string
	}{
		{float64(r.Samples), float64(3), "samples"},
		{s["title"], "Ticket", "title"},
		{s["$schema"], "https://json-schema.org/draft/2020-12/schema", "$schema"},
		{s["type"], "object", "type"},
		{s["required"], []any{"created", "id", "meta", "name", "status", "tags"}, "required"},
		{prop("properties", "id", "type"), "number", "id type"},
		{prop("properties", "name", "type"), []any{"string", "null"}, "name type"},
		{prop("properties", "created", "format"), "date-time", "created format"},
		{prop("properties", "tags"), map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "tags"},
		{prop("properties", "meta", "required"), []any{"k"}, "meta required"},
		{prop("properties", "meta", "properties", "extra", "type"), "boolean", "meta.extra type"},
		{prop("properties", "status", "enum"), []any{"closed", "open"}, "status enum"},
	}
	for _, ch := range checks {
		if !reflect.DeepEqual(ch.got, ch.want) {
			t.Errorf("%s: %#v, want %#v", ch.what, ch.got, ch.want)
		}
	}
	if v := decodeResult[validation](t, c.call("validate_json", map[string]any{"schema": s, "data": samples[1]})); !v.Valid {
		t.Errorf("inferred schema rejects its own sample: %+v", v)
	}

	single := decodeResult[inferred](t, c.call("infer_schema", map[string]any{"samples": mustJSON(t, map[string]any{
		"email": "x@y.z", "id": "123e4567-e89b-12d3-a456-426614174000", "url": "https://example.com/a"})}))
	for key, format := range map[string]string{"email": "email", "id": "uuid", "url": "uri"} {
		got, _ := single.Schema["properties"].(map[string]any)[key].(map[string]any)
		if got["format"] != format {
			t.Errorf("%s format: %v", key, got["format"])
		}
	}
	nested := decodeResult[inferred](t, c.call("infer_schema", map[string]any{"samples": "[[]]"}))
	if nested.Schema["type"] != "array" || !reflect.DeepEqual(nested.Schema["items"], map[string]any{}) {
		t.Errorf("[[]]: %#v", nested.Schema)
	}
	if bad := c.call("infer_schema", map[string]any{"samples": "not json"}); !bad.IsError {
		t.Errorf("not json accepted: %s", bad.Text)
	}
}

func TestDiffSchemasClassifiesChanges(t *testing.T) {
	c := connected(t)
	after := userSchema()
	props := map[string]any{}
	for k, v := range after["properties"].(map[string]any) {
		props[k] = v
	}
	props["id"] = map[string]any{"type": "number", "minimum": 2}
	props["name"] = map[string]any{"type": "string"}
	props["tags"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 5}
	props["role"] = map[string]any{"enum": []any{"admin"}}
	props["phone"] = map[string]any{"type": "string"}
	delete(props, "email")
	after["properties"] = props
	after["required"] = []any{"id", "email", "name", "tags"}

	r := decodeResult[schemaDiff](t, c.call("diff_schemas", map[string]any{"before": userSchema(), "after": after}))
	if r.Verdict != "breaking" {
		t.Errorf("verdict: %q", r.Verdict)
	}
	by := map[string]string{}
	for _, d := range r.Details {
		by[d.Path+" "+d.Change] = d.Impact
	}
	for key, impact := range map[string]string{
		"/properties/email property removed": "breaking",
		"/properties/name property added":    "breaking",
		"/properties/phone property added":   "compatible",
		"/properties/id type":                "compatible",
		"/properties/id/minimum minimum":     "breaking",
		"/properties/tags/maxItems maxItems": "compatible",
		"/properties/role/enum enum":         "breaking",
		"/required required added":           "breaking",
	} {
		if by[key] != impact {
			t.Errorf("%s: %q, want %q", key, by[key], impact)
		}
	}
	added := slices.IndexFunc(r.Details, func(d change) bool { return d.Change == "required added" })
	if added < 0 || r.Details[added].After != "tags" {
		t.Errorf("required added: %+v", r.Details)
	}
	if r.Breaking != 5 {
		t.Errorf("breaking: %d", r.Breaking)
	}

	same := decodeResult[schemaDiff](t, c.call("diff_schemas", map[string]any{"before": userSchema(), "after": mustJSON(t, userSchema())}))
	if same.Verdict != "identical" {
		t.Errorf("identical: %q", same.Verdict)
	}
	loosened := decodeResult[schemaDiff](t, c.call("diff_schemas", map[string]any{
		"before": map[string]any{"type": "object", "additionalProperties": false, "required": []any{"a"}, "properties": map[string]any{"a": map[string]any{"type": "string", "pattern": "^x"}}},
		"after":  map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "description": "d"}}))
	for _, d := range loosened.Details {
		if d.Impact != "compatible" {
			t.Errorf("loosening not compatible: %+v", loosened.Details)
			break
		}
	}
	composed := decodeResult[schemaDiff](t, c.call("diff_schemas", map[string]any{
		"before": map[string]any{"oneOf": []any{map[string]any{"type": "string"}}},
		"after":  map[string]any{"oneOf": []any{map[string]any{"type": "number"}}}}))
	if len(composed.Details) == 0 || composed.Details[0].Impact != "review" {
		t.Errorf("composed: %+v", composed.Details)
	}
	if bad := c.call("diff_schemas", map[string]any{"before": "[]", "after": map[string]any{}}); !bad.IsError {
		t.Errorf("array schema accepted: %s", bad.Text)
	}
}
