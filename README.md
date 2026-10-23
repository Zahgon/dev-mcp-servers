# dev-mcp-servers

Ten small MCP servers for everyday development and security checks. Each one is a separate static Go binary you add to Claude Code, Claude Desktop, Cursor or any other MCP client with one line, and each does one job well: look up vulnerabilities, grade security headers, decode a JWT, test a regex, explain a cron expression, validate JSON against a schema, lint an OpenAPI document or a Dockerfile, read statistics out of a git repository, or build an `llms.txt`.

All servers are Go programs in one module that implement the Model Context Protocol on stdio with no third-party protocol library, validate every input against its declared schema, bound their inputs and run time, send no telemetry, and ship with `go test` suites that cover every tool.

## Servers

| Server | Purpose | Install |
|---|---|---|
| [osv-advisories](packages/osv-advisories) | Query OSV.dev for known vulnerabilities by package and version; scan `package-lock.json`, `requirements.txt`, `poetry.lock` and `go.sum` | `claude mcp add osv-advisories -- /path/to/mcp-osv-advisories` |
| [security-headers](packages/security-headers) | Fetch a public URL's response headers and grade CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy and cookies | `claude mcp add security-headers -- /path/to/mcp-security-headers` |
| [jwt-tools](packages/jwt-tools) | Decode a JWT without verifying it, flag `alg: none`, expiry and missing claims, verify HS256/RS256 with a key, sign test tokens | `claude mcp add jwt-tools -- /path/to/mcp-jwt-tools` |
| [regex-lab](packages/regex-lab) | Test a regex against samples in a timeout-guarded worker, explain it, detect catastrophic backtracking | `claude mcp add regex-lab -- /path/to/mcp-regex-lab` |
| [cron-tools](packages/cron-tools) | Parse, explain and validate 5-field cron, list the next runs in a time zone | `claude mcp add cron-tools -- /path/to/mcp-cron-tools` |
| [json-schema-tools](packages/json-schema-tools) | Validate JSON with Ajv, infer a schema from samples, diff two schemas with a compatibility verdict | `claude mcp add json-schema-tools -- /path/to/mcp-json-schema-tools` |
| [openapi-lint](packages/openapi-lint) | Lint OpenAPI 3.x for missing security, responses, descriptions and versioning; list operations | `claude mcp add openapi-lint -- /path/to/mcp-openapi-lint` |
| [dockerfile-lint](packages/dockerfile-lint) | Lint Dockerfiles for root users, `latest` tags, secrets in `ENV`/`ARG`, missing `HEALTHCHECK`, apt without cleanup, `ADD` vs `COPY` | `claude mcp add dockerfile-lint -- /path/to/mcp-dockerfile-lint` |
| [git-insights](packages/git-insights) | Read-only git statistics: log, blame ownership, churn, authors, large files | `claude mcp add git-insights -- /path/to/mcp-git-insights` |
| [llms-txt](packages/llms-txt) | Generate `llms.txt` from local Markdown or a sitemap, check an existing one | `claude mcp add llms-txt -- /path/to/mcp-llms-txt` |

Every package README lists its tools with inputs and outputs, the exact `claude mcp add` command, a `.mcp.json` snippet, and what the server touches on disk and on the network.

## Quick start

Build the server you want from a checkout (Go 1.23 or newer, no cgo):

```bash
git clone https://github.com/basitalisandhu/dev-mcp-servers
cd dev-mcp-servers
go build -o bin/mcp-jwt-tools ./packages/jwt-tools/cmd/mcp-jwt-tools
claude mcp add jwt-tools -- "$PWD/bin/mcp-jwt-tools"
claude mcp list
```

Add `-s user` to install for every project rather than the current one. For clients that read `.mcp.json` (Claude Code, Claude Desktop, Cursor), add an entry per server:

```json
{
  "mcpServers": {
    "jwt-tools": { "command": "/path/to/bin/mcp-jwt-tools" },
    "osv-advisories": { "command": "/path/to/bin/mcp-osv-advisories" }
  }
}
```

Build every server at once with `for s in packages/*/; do s=$(basename "$s"); go build -o "bin/mcp-$s" "./packages/$s/cmd/mcp-$s"; done`.

## Install

Every release is published in two places: `release.yml` attaches a static binary per server and platform (`mcp-<server>-<version>-<os>-<arch>.tar.gz` for linux and darwin, amd64 and arm64, with `SHA256SUMS`) to the GitHub release, and `publish-github-packages.yml` pushes a container image per server to the GitHub Container Registry. Each archive holds one `mcp-<server>` binary that speaks MCP on stdio.

```bash
curl -fsSLO https://github.com/basitalisandhu/dev-mcp-servers/releases/download/v0.1.0/mcp-jwt-tools-0.1.0-linux-amd64.tar.gz
tar -xzf mcp-jwt-tools-0.1.0-linux-amd64.tar.gz
claude mcp add jwt-tools -- "$PWD/mcp-jwt-tools-0.1.0-linux-amd64/mcp-jwt-tools"
```

### Container images

Images are built for `linux/amd64` and `linux/arm64`, run as the non-root `mcp` user, and speak stdio, so `-i` is required and no port is published. Each image is tagged with the version and `latest`; pin the version.

With Claude Code:

```bash
claude mcp add jwt-tools -- docker run --rm -i ghcr.io/basitalisandhu/mcp-jwt-tools:0.1.0
```

With Cursor (`.cursor/mcp.json`) or any client that reads `.mcp.json`:

```json
{
  "mcpServers": {
    "jwt-tools": {
      "command": "docker",
      "args": ["run", "--rm", "-i", "ghcr.io/basitalisandhu/mcp-jwt-tools:0.1.0"]
    }
  }
}
```

A container only sees the files you mount. For servers that read local files (`dockerfile-lint`, `openapi-lint`, `osv-advisories` lockfile scans, `llms-txt`), mount the directory read-only and pass paths inside it, for example `docker run --rm -i -v "$PWD:/work:ro" ghcr.io/basitalisandhu/mcp-openapi-lint:0.1.0` and then `/work/openapi.yaml`. For `git-insights`, mount the repository at `/repo` (the image marks only `/repo` as a safe git directory): `docker run --rm -i -v "$PWD:/repo:ro" ghcr.io/basitalisandhu/mcp-git-insights:0.1.0`.

Every image is signed with cosign (keyless) and carries a build provenance attestation; an SPDX SBOM per image is attached to the GitHub release. To check an image before running it:

```bash
cosign verify ghcr.io/basitalisandhu/mcp-jwt-tools:0.1.0 \
  --certificate-identity-regexp '^https://github.com/basitalisandhu/dev-mcp-servers/\.github/workflows/publish-github-packages\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify oci://ghcr.io/basitalisandhu/mcp-jwt-tools:0.1.0 --owner basitalisandhu
```

To build an image locally from a checkout: `docker build --build-arg SERVER=jwt-tools -t mcp-jwt-tools .`

## Security posture

- **Stdio only.** No server opens a port. Each one is a child process of your MCP client and exits with it.
- **Validated, bounded inputs.** Every tool input is checked against its schema with length and range limits; files are read with size caps; regex runs, HTTP requests and git commands have timeouts; results are capped.
- **Network only where the job is network, and only to the documented host.** `osv-advisories` talks to `api.osv.dev` and refuses any other origin. `security-headers` fetches the URL you give it, refuses private, loopback, link-local and cloud-metadata addresses (including names that resolve to them), limits redirects and never reads a body. The other eight servers make no network requests at all.
- **Read-only.** No server writes files, runs a shell, or modifies a repository. `git-insights` spawns `git` without a shell from fixed argument lists with validated paths and revisions and `GIT_OPTIONAL_LOCKS=0`; the output of `llms-txt` is returned as text for you to save.
- **No telemetry.** Nothing phones home. There are no analytics, update checks or crash reporters.
- **Honest tool descriptions.** Tool descriptions say what the tool does and returns. They contain no instructions aimed at the model, and the outputs of `decode_jwt` and the lints say when something is unverified or heuristic.
- **Small dependency trees.** The only third-party module is `gopkg.in/yaml.v3` (YAML parsing for openapi-lint); everything else is the Go standard library. `go.sum` pins it.
- **Provenance.** Container images get a build provenance attestation and a keyless cosign signature from the workflow run that built them, plus an SPDX SBOM on the release. Release binaries are built with `-trimpath` by the tag's workflow run and listed with their SHA-256 in `SHA256SUMS`.

Before adding any MCP server, including these, read its source: it runs with your user's permissions and its tool results go into the model's context. See [SECURITY.md](SECURITY.md) for how to report a problem.

## Repository layout

```
go.mod, go.sum                  one Go module for every server
internal/jsv/                   JavaScript-compatible JSON values (key order, numbers, UTF-16 strings)
internal/mcp/                   MCP over stdio: JSON-RPC envelope, initialize, tools/list, tools/call
packages/<name>/
  cmd/mcp-<name>/              server: main package, tool registrations and logic
  cmd/mcp-<name>/*_test.go     go test suites that drive the server over stdio
  server.json                  MCP registry metadata (checked against the schema in CI)
  README.md                    tools, install, what it touches
scripts/check-server-json/      offline validation of every server.json (go run ./scripts/check-server-json)
.github/workflows/ci.yml        vet, build and test every server
.github/workflows/publish-github-packages.yml   GHCR images on a version tag
.github/workflows/release.yml   release binaries for linux and darwin on a version tag
Dockerfile                      one image per server: --build-arg SERVER=<name>
```

Build and test everything:

```bash
go vet ./...
go build ./...
go test ./...
go run ./scripts/check-server-json
```

Tests run offline: network-facing servers are tested against a fake HTTP transport, and git-insights builds a temporary repository.

## Adding a server

1. Copy the closest existing package to `packages/<name>` with its main package in `packages/<name>/cmd/mcp-<name>`; `server.json` names `io.github.basitalisandhu/<name>`, the version, the `ghcr.io/basitalisandhu/mcp-<name>:<version>` image and the `packages/<name>` subfolder.
2. Keep the pattern: `newServer()` returns the `mcp.Server` with its tools and `main()` serves it on stdio; every tool has an `inputSchema` with limits, `annotations`, and a description that states what it does, what it returns and what it refuses.
3. Write `*_test.go` suites that start the server over stdio and call every tool at least once, including an invalid input.
4. No network unless the tool's purpose is network, and then: one documented host, an allowlist check before the request, a timeout, a response size cap, and a fake transport in tests.
5. Add a row to the table above, a section in `CHANGELOG.md`, and run `go run ./scripts/check-server-json`. CI picks up new packages automatically.

## Releasing

Set every `server.json` `version` and server version constant (they move together), add the release to `CHANGELOG.md`, and push an annotated tag:

```bash
git tag -a v0.1.0 -m "0.1.0" && git push origin v0.1.0
```

`publish-github-packages.yml` vets, tests, checks that every `version` matches the tag, then:

- builds and pushes `ghcr.io/basitalisandhu/mcp-<server>:<version>` and `:latest` for every server, generates an SPDX SBOM, records a build provenance attestation and signs the image digest with cosign;
- creates the GitHub release for the tag with generated notes and the SBOMs attached.

`release.yml` runs on the same tags: it tests, checks the versions, cross-compiles every server for linux and darwin (amd64 and arm64) and attaches the archives and `SHA256SUMS` to the release. No secret is needed for either workflow. Pull requests that change the Dockerfile, the workflow, the Go code or a `server.json` run the container build as a dry run.

To list the servers in the [MCP registry](https://github.com/modelcontextprotocol/registry), each `server.json` is already in the registry's format with an `oci` package entry for its image; publish with the registry's `mcp-publisher` CLI from the package directory after the images are pushed.

## Related

More tools by the same author: https://github.com/basitalisandhu

- [agent-security-skills](https://github.com/basitalisandhu/agent-security-skills): Claude Code plugin and skill pack for securing LLM agents, including an MCP server over the AI agent incident dataset.

## Licence

MIT. See [LICENSE](LICENSE).
