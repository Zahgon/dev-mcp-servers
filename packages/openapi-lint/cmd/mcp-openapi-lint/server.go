// Command mcp-openapi-lint is the openapi-lint MCP server on stdio.
package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "openapi-lint"
	serverVersion = "0.1.0"
)

func instructions() string {
	return "Reads OpenAPI 3.0 and 3.1 documents (JSON or YAML, from a local path or inline). lint_openapi reports security and completeness problems with JSON Pointer paths; " +
		"list_operations tabulates every operation with its effective security; get_operation returns one operation in full; explain_rule documents a rule. Rules: " + strings.Join(ruleNames, ", ") + ". " +
		"Only local #/ references are resolved. Findings describe the document, not the running API."
}

func sourceArgs(a mcp.Args) (source, error) {
	var src source
	p, err := a.String("path", 1, 4096)
	if err != nil {
		return src, err
	}
	src.path = p
	if raw, ok := a.Obj.Get("document"); ok {
		switch d := raw.(type) {
		case string:
			n := jsv.UTF16Len(d)
			if n < 1 || n > maxDocumentBytes {
				return src, mcp.Invalid(a.Tool, "document: Invalid input")
			}
		case *jsv.Obj:
		default:
			return src, mcp.Invalid(a.Tool, "document: Invalid input")
		}
		src.document = raw
		src.hasDocument = true
	}
	return src, nil
}

func newServer() *mcp.Server {
	return &mcp.Server{Name: serverName, Version: serverVersion, Instructions: instructions(), Tools: tools()}
}

func main() { mcp.Main(newServer()) }

func lintTool(args *jsv.Obj) mcp.Result {
	src, err := sourceArgs(mcp.Args{Tool: "lint_openapi", Obj: args})
	if err != nil {
		return mcp.Fail(err.Error())
	}
	ignore := map[string]bool{}
	if raw, present := args.Get("ignore"); present {
		arr, isArr := raw.([]any)
		if !isArr || len(arr) > 50 {
			return mcp.Fail(mcp.Invalid("lint_openapi", "ignore: Invalid input").Error())
		}
		for _, item := range arr {
			s, isString := item.(string)
			if _, known := rules[s]; !isString || !known {
				return mcp.Fail(mcp.Invalid("lint_openapi", "ignore: Invalid enum value").Error())
			}
			ignore[s] = true
		}
	}
	doc, origin, err := loadDocument(src)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	findings := lint(doc, ignore)
	counts := map[string]int{"error": 0, "warning": 0, "info": 0}
	list := []any{}
	for _, f := range findings {
		counts[f.Severity]++
		list = append(list, f.value())
	}
	out := jsv.NewObj()
	out.Set("source", origin)
	if v, present := doc.Get("openapi"); present && v != nil {
		out.Set("openapi", v)
	} else {
		out.Set("openapi", nil)
	}
	out.Set("title", nil)
	if info := jsv.AsObj(jsv.Prop(doc, "info")); info != nil {
		if t, present := info.Get("title"); present && t != nil {
			out.Set("title", t)
		}
	}
	out.Set("operations", float64(len(listOperations(doc))))
	out.Set("error", float64(counts["error"]))
	out.Set("warning", float64(counts["warning"]))
	out.Set("info", float64(counts["info"]))
	out.Set("findings", list)
	return mcp.OK(out)
}

func listTool(args *jsv.Obj) mcp.Result {
	const tool = "list_operations"
	src, err := sourceArgs(mcp.Args{Tool: tool, Obj: args})
	if err != nil {
		return mcp.Fail(err.Error())
	}
	tag, err := mcp.Args{Tool: tool, Obj: args}.String("tag", 0, 100)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	method, err := mcp.Args{Tool: tool, Obj: args}.Enum("method", methods)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	prefix, err := mcp.Args{Tool: tool, Obj: args}.String("path_prefix", 0, 500)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	doc, origin, err := loadDocument(src)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	ops := []any{}
	for _, o := range listOperations(doc) {
		if tag != nil && *tag != "" {
			found := false
			for _, t := range jsv.Prop(o, "tags").([]any) {
				if t == *tag {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		if method != nil && *method != "" && jsv.Prop(o, "method") != strings.ToUpper(*method) {
			continue
		}
		if prefix != nil && *prefix != "" && !strings.HasPrefix(jsv.Prop(o, "path").(string), *prefix) {
			continue
		}
		ops = append(ops, o)
	}
	out := jsv.NewObj()
	out.Set("source", origin)
	out.Set("count", float64(len(ops)))
	out.Set("operations", ops)
	return mcp.OK(out)
}

// spreadList mirrors [...(x ?? [])]: undefined and null give no items, arrays give
// their elements, strings give their code points, anything else is not iterable.
func spreadList(v any, present bool, what string) ([]any, error) {
	if !present || v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case []any:
		return x, nil
	case string:
		return jsv.CodePoints(x), nil
	}
	return nil, fmt.Errorf("(%s ?? []) is not iterable", what)
}

// entryPairs mirrors Object.entries(x ?? {}).
func entryPairs(v any, present bool) ([]string, []any) {
	if !present || v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case *jsv.Obj:
		keys := x.Keys()
		vals := make([]any, len(keys))
		for i, k := range keys {
			vals[i], _ = x.Get(k)
		}
		return keys, vals
	case []any:
		keys := make([]string, len(x))
		for i := range x {
			keys[i] = strconv.Itoa(i)
		}
		return keys, x
	case string:
		vals := jsv.CodeUnits(x)
		keys := make([]string, len(vals))
		for i := range vals {
			keys[i] = strconv.Itoa(i)
		}
		return keys, vals
	}
	return nil, nil
}

// spreadObject mirrors { ...op } for an operation that is an object or an array.
func spreadObject(op any) *jsv.Obj {
	out := jsv.NewObj()
	keys, vals := entryPairs(op, true)
	for i, k := range keys {
		out.Set(k, vals[i])
	}
	return out
}

func getTool(args *jsv.Obj) mcp.Result {
	const tool = "get_operation"
	src, err := sourceArgs(mcp.Args{Tool: tool, Obj: args})
	if err != nil {
		return mcp.Fail(err.Error())
	}
	operationID, err := mcp.Args{Tool: tool, Obj: args}.String("operation_id", 0, 200)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	method, err := mcp.Args{Tool: tool, Obj: args}.Enum("method", methods)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	operationPath, err := mcp.Args{Tool: tool, Obj: args}.String("operation_path", 0, 500)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	doc, _, err := loadDocument(src)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	byID := operationID != nil && *operationID != ""
	if !byID && !(method != nil && *method != "" && operationPath != nil && *operationPath != "") {
		return mcp.Fail("Supply operation_id, or both method and operation_path.")
	}
	pathsRaw, pathsOK := doc.Get("paths")
	keys, items := entryPairs(pathsRaw, pathsOK)
	for i, p := range keys {
		item := jsv.AsObj(deref(doc, items[i], 0))
		if item == nil {
			continue
		}
		for _, m := range methods {
			// typeof op === "object": arrays are operations too; a plain object view of an
			// array has none of the operation fields.
			raw := jsv.Prop(item, m)
			if !jsv.IsObject(raw) && !jsv.IsArray(raw) {
				continue
			}
			op := jsv.AsObj(raw)
			var matches bool
			if byID {
				matches = jsv.Prop(op, "operationId") == *operationID
			} else {
				matches = m == *method && p == *operationPath
			}
			if !matches {
				continue
			}
			itemParamsRaw, itemParamsOK := item.Get("parameters")
			itemParams, err := spreadList(itemParamsRaw, itemParamsOK, "item.parameters")
			if err != nil {
				return mcp.Fail(err.Error())
			}
			opParamsRaw, opParamsOK := op.Get("parameters")
			opParams, err := spreadList(opParamsRaw, opParamsOK, "op.parameters")
			if err != nil {
				return mcp.Fail(err.Error())
			}
			params := []any{}
			for _, x := range append(append([]any{}, itemParams...), opParams...) {
				params = append(params, deref(doc, x, 0))
			}
			responsesRaw, responsesOK := op.Get("responses")
			codes, values := entryPairs(responsesRaw, responsesOK)
			responses := jsv.NewObj()
			for j, code := range codes {
				responses.Set(code, deref(doc, values[j], 0))
			}
			operation := spreadObject(raw)
			operation.Set("parameters", params)
			if body, present := op.Get("requestBody"); present {
				operation.Set("requestBody", deref(doc, body, 0))
			}
			operation.Set("responses", responses)
			out := jsv.NewObj()
			out.Set("method", strings.ToUpper(m))
			out.Set("path", p)
			out.Set("operation", operation)
			if sec, present := op.Get("security"); present && sec != nil {
				out.Set("effective_security", sec)
			} else if sec, present := doc.Get("security"); present && sec != nil {
				out.Set("effective_security", sec)
			} else {
				out.Set("effective_security", nil)
			}
			return mcp.OK(out)
		}
	}
	if byID {
		return mcp.Fail(fmt.Sprintf("No operation with operationId %s.", jsv.Stringify(*operationID, 0)))
	}
	return mcp.Fail(fmt.Sprintf("No %s operation at %s.", strings.ToUpper(*method), *operationPath))
}

func explainTool(args *jsv.Obj) mcp.Result {
	raw, present := args.Get("rule")
	rule, isString := raw.(string)
	doc, known := rules[rule]
	if !present || !isString || !known {
		return mcp.Fail(mcp.Invalid("explain_rule", "rule: Invalid enum value").Error())
	}
	out := jsv.NewObj()
	out.Set("rule", rule)
	out.Set("severity", doc.Severity)
	out.Set("description", doc.Description)
	out.Set("fix", doc.Fix)
	return mcp.OK(out)
}

func sourceProperties(props *jsv.Obj) {
	props.Set("path", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(4096), "description", fmt.Sprintf("Path to a local OpenAPI JSON or YAML file (up to %d bytes).", maxDocumentBytes)))
	props.Set("document", jsv.O("anyOf", []any{
		jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(maxDocumentBytes)),
		jsv.O("type", "object", "additionalProperties", jsv.O()),
	}, "description", "The document itself, as JSON or YAML text or as an object. Alternative to path."))
}

func readOnly() *jsv.Obj {
	return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false)
}

func tools() []mcp.Tool {
	lintProps := jsv.NewObj()
	sourceProperties(lintProps)
	lintProps.Set("ignore", jsv.O("type", "array", "items", jsv.O("type", "string", "enum", jsv.Strings(ruleNames)), "maxItems", float64(50), "description", "Rule ids to skip."))
	listProps := jsv.NewObj()
	sourceProperties(listProps)
	listProps.Set("tag", jsv.O("type", "string", "maxLength", float64(100)))
	listProps.Set("method", jsv.O("type", "string", "enum", jsv.Strings(methods)))
	listProps.Set("path_prefix", jsv.O("type", "string", "maxLength", float64(500)))
	getProps := jsv.NewObj()
	sourceProperties(getProps)
	getProps.Set("operation_id", jsv.O("type", "string", "maxLength", float64(200)))
	getProps.Set("method", jsv.O("type", "string", "enum", jsv.Strings(methods)))
	getProps.Set("operation_path", jsv.O("type", "string", "maxLength", float64(500), "description", "Path template as written in the document, for example /users/{id}."))
	explainProps := jsv.O("rule", jsv.O("type", "string", "enum", jsv.Strings(ruleNames)))
	object := func(props *jsv.Obj, required []any) *jsv.Obj {
		o := jsv.O("type", "object", "properties", props)
		if required != nil {
			o.Set("required", required)
		}
		o.Set("additionalProperties", false)
		o.Set("$schema", "http://json-schema.org/draft-07/schema#")
		return o
	}
	ruleList := strings.Join(ruleNames, ", ")
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, spec ...any) mcp.Tool {
		return mcp.Tool{Name: name, Spec: jsv.O(spec...), Handler: handler}
	}
	return []mcp.Tool{
		tool("lint_openapi", lintTool, "title", "Lint an OpenAPI document",
			"description", "Check an OpenAPI 3.x document and return findings sorted by severity (error, warning, info), each with a rule id, a JSON Pointer path into the document and a message. "+
				"Rules cover: openapi version, http server URLs, missing or unused security schemes, operations with no security requirement (and explicitly public ones), undefined schemes, "+
				"operations without responses or without a success response, responses and parameters without descriptions, path parameters not declared or not required, missing operationId "+
				"and duplicates, unversioned paths, request bodies without content, deprecated operations and undeclared tags. Pass ignore to silence rule ids.",
			"inputSchema", object(lintProps, nil), "annotations", readOnly(), "execution", jsv.O("taskSupport", "forbidden")),
		tool("list_operations", listTool, "title", "List the operations of an OpenAPI document",
			"description", "Return every operation (method and path) with operationId, summary, tags, deprecated flag, effective security (the operation's own requirement, the global one, "+
				"'public' for security: [], or 'inherited-none' when nothing applies), parameters with location and required flag, request body media types and response codes. "+
				"Optional filters: tag, method, path_prefix.",
			"inputSchema", object(listProps, nil), "annotations", readOnly(), "execution", jsv.O("taskSupport", "forbidden")),
		tool("get_operation", getTool, "title", "Get one operation",
			"description", "Return the full definition of one operation, selected by operation_id or by method and path, with path-level parameters merged in and local $ref values resolved one level. "+
				"Useful for reading a single endpoint out of a large document.",
			"inputSchema", object(getProps, nil), "annotations", readOnly(), "execution", jsv.O("taskSupport", "forbidden")),
		tool("explain_rule", explainTool, "title", "Explain a lint rule",
			"description", "Describe one lint rule: its default severity, what it detects and how to fix it. Rules: "+ruleList+".",
			"inputSchema", object(explainProps, []any{"rule"}), "annotations", readOnly(), "execution", jsv.O("taskSupport", "forbidden")),
	}
}
