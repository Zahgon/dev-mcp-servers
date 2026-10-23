// Command mcp-json-schema-tools is the json-schema-tools MCP server on stdio.
package main

import (
	"errors"
	"strconv"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "json-schema-tools"
	serverVersion = "0.1.0"
	maxJSON       = 2 * 1024 * 1024
)

const instructions = "Works with JSON Schema drafts 07, 2019-09 and 2020-12 using Ajv. validate_json checks a document against a schema and lists every violation with its JSON Pointer path; " +
	"infer_schema builds a 2020-12 schema from one or more sample documents (types, required keys, nested objects, array items, common string formats); " +
	"diff_schemas compares two schemas and classifies each change as breaking, compatible or review from the point of view of documents valid under the old schema. " +
	"Schemas and documents may be passed as JSON values or JSON strings. Remote $ref targets are not fetched."

func newServer() *mcp.Server {
	return &mcp.Server{Name: serverName, Version: serverVersion, Instructions: instructions, Tools: tools()}
}

func main() { mcp.Main(newServer()) }

var errTooLong = strconv.Itoa(maxJSON)

// coerce accepts a JSON value or a JSON string and enforces the size limit.
func coerce(value any, present bool, label string) (any, error) {
	if s, ok := value.(string); ok {
		if jsv.UTF16Len(s) > maxJSON {
			return nil, errors.New(label + " exceeds " + errTooLong + " characters")
		}
		v, err := jsv.Parse(s)
		if err != nil {
			return nil, errors.New(label + " is not valid JSON: " + err.Error())
		}
		return v, nil
	}
	if !present {
		return nil, errors.New("Cannot read properties of undefined (reading 'length')")
	}
	if jsv.UTF16Len(jsv.Stringify(value, 0)) > maxJSON {
		return nil, errors.New(label + " exceeds " + errTooLong + " characters")
	}
	return value, nil
}

func asObject(value any, label string) (*jsv.Obj, error) {
	v, err := coerce(value, true, label)
	if err != nil {
		return nil, err
	}
	if b, ok := v.(bool); ok {
		if b {
			return jsv.NewObj(), nil
		}
		return jsv.O("not", jsv.NewObj()), nil
	}
	o, ok := v.(*jsv.Obj)
	if !ok {
		return nil, errors.New(label + " must be a JSON object")
	}
	return o, nil
}

// schemaArg is the zod union record | string(1..MAX) | boolean; a record is copied
// without its "__proto__" key, as zod builds the parsed object.
func schemaArg(tool string, args *jsv.Obj, key string) (any, error) {
	v, ok := args.Get(key)
	if !ok {
		return nil, mcp.Invalid(tool, key+": Required")
	}
	switch x := v.(type) {
	case *jsv.Obj:
		out := jsv.NewObj()
		for _, k := range x.Keys() {
			if k != "__proto__" {
				out.Set(k, jsv.Prop(x, k))
			}
		}
		return out, nil
	case string:
		if n := jsv.UTF16Len(x); n >= 1 && n <= maxJSON {
			return x, nil
		}
	case bool:
		return x, nil
	}
	return nil, mcp.Invalid(tool, key+": Invalid input")
}

func fail(err error) mcp.Result { return mcp.Fail(err.Error()) }

func validateTool(args *jsv.Obj) mcp.Result {
	const tool = "validate_json"
	schema, err := schemaArg(tool, args, "schema")
	if err != nil {
		return fail(err)
	}
	draft, err := mcp.Args{Tool: tool, Obj: args}.Enum("draft", drafts)
	if err != nil {
		return fail(err)
	}
	d := "auto"
	if draft != nil {
		d = *draft
	}
	s, err := asObject(schema, "schema")
	if err != nil {
		return fail(err)
	}
	rawData, present := args.Get("data")
	data, err := coerce(rawData, present, "data")
	if err != nil {
		return fail(err)
	}
	out, err := validate(s, data, d)
	if err != nil {
		return fail(err)
	}
	return mcp.OK(out)
}

func inferTool(args *jsv.Obj) mcp.Result {
	const tool = "infer_schema"
	a := mcp.Args{Tool: tool, Obj: args}
	raw, ok := args.Get("samples")
	if !ok {
		return fail(mcp.Invalid(tool, "samples: Required"))
	}
	switch x := raw.(type) {
	case []any:
		if len(x) < 1 || len(x) > 500 {
			return fail(mcp.Invalid(tool, "samples: Invalid input"))
		}
		raw = append([]any{}, x...)
	case string:
		if n := jsv.UTF16Len(x); n < 1 || n > maxJSON {
			return fail(mcp.Invalid(tool, "samples: Invalid input"))
		}
	default:
		return fail(mcp.Invalid(tool, "samples: Invalid input"))
	}
	threshold, err := a.Number("enum_threshold", 0, 50, true)
	if err != nil {
		return fail(err)
	}
	detect, err := a.Bool("detect_formats")
	if err != nil {
		return fail(err)
	}
	title, err := a.String("title", 0, 200)
	if err != nil {
		return fail(err)
	}
	var list []any
	if s, isStr := raw.(string); isStr {
		parsed, err := coerce(s, true, "samples")
		if err != nil {
			return fail(err)
		}
		if arr, isArr := parsed.([]any); isArr {
			list = arr
		} else {
			list = []any{parsed}
		}
	} else {
		if _, err := coerce(raw, true, "samples"); err != nil {
			return fail(err)
		}
		list = raw.([]any)
	}
	opts := inferOptions{detectFormats: detect == nil || *detect}
	if threshold != nil {
		opts.enumThreshold = *threshold
	}
	inferred, err := inferSchema(list, opts, 0)
	if err != nil {
		return fail(err)
	}
	schema := jsv.O("$schema", "https://json-schema.org/draft/2020-12/schema")
	if title != nil && *title != "" {
		schema.Set("title", *title)
	}
	for _, k := range inferred.Keys() {
		schema.Set(k, jsv.Prop(inferred, k))
	}
	return mcp.OK(jsv.O("samples", float64(len(list)), "schema", schema))
}

func diffTool(args *jsv.Obj) mcp.Result {
	const tool = "diff_schemas"
	before, err := schemaArg(tool, args, "before")
	if err != nil {
		return fail(err)
	}
	after, err := schemaArg(tool, args, "after")
	if err != nil {
		return fail(err)
	}
	b, err := asObject(before, "before")
	if err != nil {
		return fail(err)
	}
	a, err := asObject(after, "after")
	if err != nil {
		return fail(err)
	}
	df := &differ{}
	if err := df.diff(b, a, "", 0); err != nil {
		return fail(err)
	}
	counts := map[string]int{}
	details := make([]any, len(df.out))
	for i, c := range df.out {
		counts[jsv.Prop(c, "impact").(string)]++
		details[i] = c
	}
	verdict := "identical"
	switch {
	case counts["breaking"] > 0:
		verdict = "breaking"
	case counts["review"] > 0:
		verdict = "review"
	case len(df.out) > 0:
		verdict = "compatible"
	}
	return mcp.OK(jsv.O("changes", float64(len(df.out)), "breaking", float64(counts["breaking"]), "compatible", float64(counts["compatible"]),
		"review", float64(counts["review"]), "verdict", verdict, "details", details))
}
