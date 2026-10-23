# Contributing

Thank you for improving these servers. The repository values precision over volume: a tool that does one thing correctly, with a clear description and a test, beats three that almost work.

## Ground rules

- **No telemetry, no surprise network calls.** Network access is allowed only where the tool's purpose is network, and then only to the documented host, with an allowlist check before the request, a timeout and a response size cap. Tests must use a fake `fetch`.
- **Stdio only.** Do not add HTTP or SSE transports.
- **Bounded inputs.** Every tool input needs a schema with length or range limits, checked before the tool runs. Files are read with size caps. Anything that can loop or recurse has an iteration or depth cap.
- **Read-only.** Servers do not write files or run shells. If a tool needs a subprocess (as git-insights does), spawn it without a shell, from a fixed argument list, with validated arguments after `--`, a timeout and an output cap.
- **Minimal dependencies.** The Go standard library is the baseline (`gopkg.in/yaml.v3` is the one exception, for openapi-lint). Add a module only when implementing the thing yourself would be larger than the module and worse; say why in the pull request.
- **Tool descriptions are read by models.** Say what the tool does, what it returns, what it refuses and what its limits are. Do not put instructions to the model in a description, and do not overstate certainty: a heuristic is called a heuristic.
- **Tests come with code.** Every tool is called at least once over stdio in the package's `*_test.go` suite, including with an invalid input.
- **No model version strings** in code, descriptions or docs; the servers work with whatever model the client runs.
- **Plain language.** Write for a developer who has not used MCP before. Avoid em-dashes; use commas, colons or full stops.

## Workflow

```bash
go vet ./...
go build ./...
go test ./...
go run ./scripts/check-server-json
```

Work in one package at a time: `go test ./packages/<name>/...`. Go 1.23 or newer is supported; CI runs 1.23.

## Adding a tool or a server

See "Adding a server" in [README.md](README.md). For a new tool in an existing server: register it in the server's `cmd/mcp-<name>` package with a schema, annotations and a description; implement the logic; add tests; add a row to the package README's tool table and a line to `CHANGELOG.md`.

## Pull requests

- One topic per pull request.
- Describe what changed, why, and how you tested it.
- Changes to lint rules, graders or heuristics need a before/after example (a fixture that now produces or no longer produces a finding).
- By contributing you agree that your contribution is licensed under the MIT licence of this repository.

## Reporting security issues

See [SECURITY.md](SECURITY.md). Please do not file security problems as public issues.
