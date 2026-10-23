# security-headers MCP server

Fetches a public URL's response headers and grades Content-Security-Policy, Strict-Transport-Security, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy, the Cross-Origin-* headers, Set-Cookie attributes and information-disclosure headers, with an explanation and a recommendation per header. The response body is never downloaded.

Part of [dev-mcp-servers](https://github.com/basitalisandhu/dev-mcp-servers). Stdio transport only; the server never opens a port.

## Tools

| Tool | Input | What it returns |
|---|---|---|
| `check_url_headers` | `url`, `method?` (HEAD or GET), `max_redirects?` (0 to 10, default 5) | HEAD request (GET on 405/501) to a public http(s) URL, redirects followed with the same checks on every hop, then a graded report. Refuses localhost, `.local`, `.internal`, private, loopback, link-local, CGNAT and cloud-metadata addresses, including names that resolve to them. |
| `grade_headers` | `headers` (object), `https?` | The same grading for headers you already have, for example from `curl -I`. No network. |
| `explain_header` | `header` | Purpose, recommended value and reference for one header. |

## Install

Build the server from the repository root:

```bash
go build -o mcp-security-headers ./packages/security-headers/cmd/mcp-security-headers
```

Claude Code:

```bash
claude mcp add security-headers -- /path/to/mcp-security-headers
```

Add `-s user` to make it available in every project. Any client that reads `.mcp.json` (Claude Code, Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "security-headers": {
      "command": "/path/to/mcp-security-headers"
    }
  }
}
```

A container image is published as `ghcr.io/basitalisandhu/mcp-security-headers`. Pin a version tag (for example `:0.1.0`) so that an update cannot change what runs in your editor without you noticing.

## What it touches

- Network: One request to the URL you give plus at most `max_redirects` hops, each validated. 10 s timeout per request. The host is resolved and every address checked before connecting; this does not defeat DNS rebinding between the check and the connection, so do not point the tool at hosts you do not trust to answer honestly. No telemetry.
- Local files: None.
- Telemetry: none.

## Notes

- Scores: CSP 25, HSTS 20, X-Frame-Options 10, X-Content-Type-Options 10, Referrer-Policy 10, Set-Cookie 10, Permissions-Policy 5, COOP 5, CORP 5. Grades: A+ (95% and no fail), A (85% and no fail), B (70%), C (55%), D (40%), otherwise F.
- A report-only CSP earns nothing: it is not enforced.
- A good grade means the headers are configured well, not that the application is secure.

## Build and test

```bash
go build -o mcp-security-headers ./packages/security-headers/cmd/mcp-security-headers   # at the repository root
go test ./packages/security-headers/...
```

The tests drive the server over stdio, and the cases that need a fetch double and a resolver run in-process with an injected transport; they do not reach the network. Set `MCP_SERVER_BIN` to test another server executable (the in-process cases are then skipped).

## Licence

MIT. See [LICENSE](../../LICENSE).
