package main

import (
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

func schemaInput() *jsv.Obj {
	return jsv.O("anyOf", []any{
		jsv.O("type", "object", "additionalProperties", jsv.NewObj()),
		jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(maxJSON)),
		jsv.O("type", "boolean"),
	}, "description", "A JSON Schema object, or the same as a JSON string.")
}

func inputSchema(props *jsv.Obj, required ...string) *jsv.Obj {
	return jsv.O("type", "object", "properties", props, "required", jsv.Strings(required), "additionalProperties", false,
		"$schema", "http://json-schema.org/draft-07/schema#")
}

func pure() *jsv.Obj {
	return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false)
}

func tools() []mcp.Tool {
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, input *jsv.Obj) mcp.Tool {
		return mcp.Tool{Name: name, Handler: handler, Spec: jsv.O("title", title, "description", description, "inputSchema", input,
			"annotations", pure(), "execution", jsv.O("taskSupport", "forbidden"))}
	}
	validateProps := jsv.O(
		"schema", schemaInput(),
		"data", jsv.O("description", `The document to validate: any JSON value, or a JSON string (strings are parsed; to validate a literal string, pass it JSON-encoded, for example "\"text\"").`),
		"draft", jsv.O("type", "string", "enum", jsv.Strings(drafts), "description", "Force a draft. Default auto."),
	)
	inferProps := jsv.O(
		"samples", jsv.O("anyOf", []any{
			jsv.O("type", "array", "items", jsv.NewObj(), "minItems", float64(1), "maxItems", float64(500)),
			jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(maxJSON)),
		}, "description", "Array of sample documents, or a JSON string of one document or an array of documents."),
		"enum_threshold", jsv.O("type", "integer", "minimum", float64(0), "maximum", float64(50), "description", "Default 0 (no enums)."),
		"detect_formats", jsv.O("type", "boolean", "description", "Default true."),
		"title", jsv.O("type", "string", "maxLength", float64(200), "description", "Optional title for the root schema."),
	)
	diffProps := jsv.O("before", schemaInput(), "after", jsv.O("$ref", "#/properties/before"))
	return []mcp.Tool{
		tool("validate_json", validateTool, "Validate JSON against a schema",
			"Validate a JSON document against a JSON Schema. The draft is read from $schema (draft-07 when absent) unless draft is given. Formats such as email, uri, date-time and uuid "+
				"are checked. Returns valid, the draft used, the total error count and up to 200 errors with instance path (JSON Pointer), schema path, keyword, message and params. "+
				"Unknown keywords are ignored (Ajv strict mode is off). Draft-04 and draft-06 schemas are rejected.",
			inputSchema(validateProps, "schema")),
		tool("infer_schema", inferTool, "Infer a schema from sample JSON",
			"Build a JSON Schema (draft 2020-12) that describes the given samples: a single document or an array of documents (each sample is one document; wrap one array document as [[...]]). "+
				"Properties present in every sample become required; types seen across samples are unioned; nested objects and array items are inferred recursively; strings that all look like "+
				"a date-time, date, time, email, uuid, uri or ipv4 get a format. With enum_threshold above 0, string properties with few distinct values across at least three samples become enums. "+
				"The result is a starting point to edit, not a complete contract.",
			inputSchema(inferProps, "samples")),
		tool("diff_schemas", diffTool, "Diff two JSON Schemas",
			"Compare two JSON Schemas structurally and list every change with its path, the before and after values, and an impact: breaking (a document valid under the old schema can "+
				"now be rejected: required added, property removed with additionalProperties false, type narrowed, enum value removed, tighter min/max), compatible (every old document stays valid), "+
				"or review (composition keywords, changed patterns or formats). Walks properties, required, additionalProperties, items, enum, numeric and length bounds, pattern, format, $ref and annotations. "+
				"oneOf/anyOf/allOf and conditionals are compared as a whole.",
			inputSchema(diffProps, "before", "after")),
	}
}
