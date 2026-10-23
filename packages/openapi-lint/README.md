# openapi-lint MCP server

Loads an OpenAPI 3.0 or 3.1 document (JSON or YAML, from a local path or inline) and lints it for security and completeness problems; lists and fetches operations with their effective security. Only local `#/` references are resolved; nothing is fetched.

Part of [dev-mcp-servers](https://github.com/basitalisandhu/dev-mcp-servers). Stdio transport only; the server never opens a port.

## Tools

| Tool | Input | What it returns |
|---|---|---|
| `lint_openapi` | `path` or `document`, `ignore?` | Findings sorted by severity with rule id, JSON Pointer path and message: openapi version, http servers, missing, unused or undefined security schemes, operations without security (and explicitly public ones), operations without responses or success responses, undescribed responses and parameters, path parameters not declared or not required, missing or duplicate operationIds, unversioned paths, empty request bodies, deprecated operations, undeclared tags. |
| `list_operations` | `path` or `document`, `tag?`, `method?`, `path_prefix?` | Every operation with operationId, summary, tags, deprecated flag, effective security (`public` for `security: []`, `inherited-none` when nothing applies), parameters, request media types and response codes. |
| `get_operation` | `path` or `document`, `operation_id` or `method` + `operation_path` | One operation in full with path-level parameters merged and local references resolved. |
| `explain_rule` | `rule` | Severity, what the rule detects and how to fix it. |

## Install

Build the binary from the repository root:

```bash
go build -o mcp-openapi-lint ./packages/openapi-lint/cmd/mcp-openapi-lint
```

Claude Code:

```bash
claude mcp add openapi-lint -- /path/to/mcp-openapi-lint
```

Add `-s user` to make it available in every project. Any client that reads `.mcp.json` (Claude Code, Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "openapi-lint": {
      "command": "/path/to/mcp-openapi-lint"
    }
  }
}
```

A container image is published as `ghcr.io/basitalisandhu/mcp-openapi-lint`; pin a version tag such as `ghcr.io/basitalisandhu/mcp-openapi-lint:0.1.0` so that an update cannot change what runs in your editor without you noticing.

## What it touches

- Network: None.
- Local files: Reads the one file you name, up to 5 MB. YAML is parsed with an alias limit.
- Telemetry: none.

## Notes

- Swagger 2.0 documents are reported by the `openapi-version` rule rather than linted.
- Findings describe the document, not the running API.

## Build and test

```bash
go build -o mcp-openapi-lint ./packages/openapi-lint/cmd/mcp-openapi-lint   # at the repository root
go test ./packages/openapi-lint/...
```

The tests drive the server over stdio (set `MCP_SERVER_BIN` to test another executable); they do not reach the network.

## Licence

MIT. See [LICENSE](../../LICENSE).
