# dockerfile-lint MCP server

Parses Dockerfiles (comments, `escape` directive, line continuations, heredocs, multi-stage builds) and lints them for the mistakes that matter in production images. Static analysis only; Docker is never invoked.

Part of [dev-mcp-servers](https://github.com/basitalisandhu/dev-mcp-servers). Stdio transport only; the server never opens a port.

## Tools

| Tool | Input | What it returns |
|---|---|---|
| `lint_dockerfile` | `path` or `content`, `ignore?` | Findings with rule id, severity, line and fix: final stage running as root, `latest` or missing tags, no digest, credential-like `ENV`/`ARG` names and literals, missing `HEALTHCHECK`, `apt-get` without cleanup or `--no-install-recommends`, `apt-get upgrade`, `apk add` without `--no-cache`, `pip` without `--no-cache-dir`, `ADD` for plain files or URLs, `curl | sh`, `sudo`, relative `WORKDIR`, repeated `CMD`/`ENTRYPOINT`, shell-form `CMD`, `EXPOSE 22`, `MAINTAINER`, `COPY . .`, `COPY` after `USER` without `--chown`. |
| `parse_dockerfile` | `path` or `content` | Instructions with line ranges and stage index, stages with `AS` names and base images, escape character. |
| `explain_rule` | `rule` | Severity, description and fix for one rule. |

## Install

Build the server from the repository root (Go 1.23 or later):

```bash
go build -o mcp-dockerfile-lint ./packages/dockerfile-lint/cmd/mcp-dockerfile-lint
```

Claude Code:

```bash
claude mcp add dockerfile-lint -- /path/to/mcp-dockerfile-lint
```

Add `-s user` to make it available in every project. Any client that reads `.mcp.json` (Claude Code, Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "dockerfile-lint": {
      "command": "/path/to/mcp-dockerfile-lint"
    }
  }
}
```

A container image is published as `ghcr.io/basitalisandhu/mcp-dockerfile-lint`; it runs the same binary on stdio (`docker run -i --rm ghcr.io/basitalisandhu/mcp-dockerfile-lint:0.1.0`).

## What it touches

- Network: None.
- Local files: Reads the one file you name, up to 1 MB.
- Telemetry: none.

## Notes

- `FROM $ARG` bases are checked against the declared `ARG`s; `scratch` and stage references are exempt from tag checks.
- Rules are heuristics on the text; a clean result does not scan the image's packages.

## Build and test

```bash
go build -o mcp-dockerfile-lint ./packages/dockerfile-lint/cmd/mcp-dockerfile-lint   # at the repository root
go test ./packages/dockerfile-lint/...
```

The tests start the server on stdio and talk to it over JSON-RPC; set `MCP_SERVER_BIN` to test another build of the server. They do not reach the network.

## Licence

MIT. See [LICENSE](../../LICENSE).
