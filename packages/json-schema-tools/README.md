# json-schema-tools MCP server

Validates JSON against JSON Schema drafts 07, 2019-09 and 2020-12 with a Go validator that behaves as [Ajv](https://ajv.js.org/) 8 with ajv-formats does, infers a schema from sample documents, and diffs two schemas with a compatibility verdict per change.

Part of [dev-mcp-servers](https://github.com/basitalisandhu/dev-mcp-servers). Stdio transport only; the server never opens a port.

## Tools

| Tool | Input | What it returns |
|---|---|---|
| `validate_json` | `schema`, `data`, `draft?` | `valid`, draft used, error count and up to 200 errors with JSON Pointer path, schema path, keyword, message and params. Formats (email, uri, date-time, uuid and more) are checked. |
| `infer_schema` | `samples` (array of documents or a JSON string), `enum_threshold?`, `detect_formats?`, `title?` | A draft 2020-12 schema: types unioned across samples, `required` for keys present in every sample, nested objects, array items, string formats, optional enums. |
| `diff_schemas` | `before`, `after` | Every change with path, before and after values and an impact: breaking, compatible or review, from the point of view of documents valid under `before`. |

## Install

Build the binary from the repository root:

```bash
go build -o mcp-json-schema-tools ./packages/json-schema-tools/cmd/mcp-json-schema-tools
```

Claude Code:

```bash
claude mcp add json-schema-tools -- /path/to/mcp-json-schema-tools
```

Add `-s user` to make it available in every project. Any client that reads `.mcp.json` (Claude Code, Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "json-schema-tools": {
      "command": "/path/to/mcp-json-schema-tools"
    }
  }
}
```

A container image is published as `ghcr.io/basitalisandhu/mcp-json-schema-tools`; pin a version tag such as `ghcr.io/basitalisandhu/mcp-json-schema-tools:0.1.0` so that an update cannot change what runs in your editor without you noticing.

## What it touches

- Network: None. Remote `$ref` targets are not fetched.
- Local files: None.
- Telemetry: none.

## Notes

- Validation runs as Ajv's `strict: false` mode does, so real-world schemas with unknown keywords still validate.
- Draft-04 and draft-06 schemas are rejected; update `$schema` or remove it.
- Schemas and documents can be passed as JSON values or as JSON strings; both are capped at 2 MB.

## Build and test

```bash
go build -o mcp-json-schema-tools ./packages/json-schema-tools/cmd/mcp-json-schema-tools   # at the repository root
go test ./packages/json-schema-tools/...
```

The tests drive the server over stdio (set `MCP_SERVER_BIN` to test another executable); they do not reach the network.

## Licence

MIT. See [LICENSE](../../LICENSE).
