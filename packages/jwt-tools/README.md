# jwt-tools MCP server

Decodes JSON Web Tokens without verifying them (and says so in the output), flags risky algorithms and claims, verifies HS256/384/512 and RS256/384/512 signatures when you supply the key, and mints tokens for test fixtures. Go standard library crypto only; no network, no files.

Part of [dev-mcp-servers](https://github.com/basitalisandhu/dev-mcp-servers). Stdio transport only; the server never opens a port.

## Tools

| Tool | Input | What it returns |
|---|---|---|
| `decode_jwt` | `token`, `now?`, `max_ttl_hours?` | Header, payload, ISO timestamps and findings: `alg` none or missing, empty signature, `jku`/`x5u`/`jwk` in the header, expired, not yet valid, missing `exp`/`iat`/`aud`/`iss`/`sub`/`jti`, lifetime above the threshold (default 24 h), millisecond timestamps, claims named like secrets. Always reports `verified: false`. |
| `verify_jwt` | `token`, `key`, `algorithm`, `audience?`, `issuer?`, `now?`, `clock_tolerance_seconds?` | Signature check for exactly the given algorithm (a token whose header says anything else fails without a cryptographic check, which blocks algorithm confusion), then `exp` (required), `nbf`, `aud`, `iss`. Returns `valid`, `signature_valid`, `claims_valid` and reasons. |
| `sign_test_jwt` | `payload`, `key`, `algorithm`, `expires_in_seconds?`, `now?` | Signs a claims object for tests. HS* takes a shared secret, RS* a PEM private key. |

## Install

Build the binary from the repository root:

```bash
go build -o mcp-jwt-tools ./packages/jwt-tools/cmd/mcp-jwt-tools
```

Claude Code:

```bash
claude mcp add jwt-tools -- /path/to/mcp-jwt-tools
```

Add `-s user` to make it available in every project. Any client that reads `.mcp.json` (Claude Code, Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "jwt-tools": {
      "command": "/path/to/mcp-jwt-tools"
    }
  }
}
```

A container image is published as `ghcr.io/basitalisandhu/mcp-jwt-tools`; pin a version tag such as `ghcr.io/basitalisandhu/mcp-jwt-tools:0.1.0` so that an update cannot change what runs in your editor without you noticing.

## What it touches

- Network: None.
- Local files: None. Keys are used for the one call and not stored.
- Telemetry: none.

## Notes

- HMAC comparison is constant-time (`crypto/subtle`).
- RS* verification accepts a PEM public key, certificate or private key (the public part is derived).
- JWE (encrypted, five-part) tokens are rejected; ES* and PS* tokens decode but cannot be verified here.

## Build and test

```bash
go build -o mcp-jwt-tools ./packages/jwt-tools/cmd/mcp-jwt-tools   # at the repository root
go test ./packages/jwt-tools/...
```

The tests drive the server over stdio (set `MCP_SERVER_BIN` to test another executable); they do not reach the network.

## Licence

MIT. See [LICENSE](../../LICENSE).
