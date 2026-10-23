# Good first issues

Small, well-specified pieces of work for a first contribution. Each one is self-contained, has a test to add, and needs no account, token or network access. Read [CONTRIBUTING.md](../CONTRIBUTING.md) first: bounded inputs, no new network calls, tests for every tool, plain language without em-dashes.

To claim one, open an issue with the title below (or comment on the existing one) and say you are working on it. Run `go vet ./... && go test ./...` at the repository root before opening the pull request.

## 1. osv-advisories: parse Cargo.lock and Gemfile.lock

**Labels:** good first issue, osv-advisories

**Context.** `packages/osv-advisories/cmd/mcp-osv-advisories/lockfiles.go` parses npm, pip, poetry and Go lockfiles. Rust and Ruby projects have equally simple lockfiles: `Cargo.lock` is TOML with `[[package]]` blocks carrying `name` and `version` (ecosystem `crates.io`), and `Gemfile.lock` lists `    name (1.2.3)` lines under `GEM` / `specs:` (ecosystem `RubyGems`).

**Acceptance criteria.**
- The lockfile format list gains `cargo` and `gemfile`; format detection recognises the file names and the content shapes.
- Lockfile parsing returns exact pins for both, skipping path and git sources in Cargo.lock (`source` missing or not starting with `registry+`) with a reason.
- Tests in `lockfiles_test.go` with a short fixture string for each format, and a `scan_lockfile` round-trip in `server_test.go` for one of them.
- The `scan_lockfile` description, the package README table and `CHANGELOG.md` list the new formats.

## 2. security-headers: parse Permissions-Policy and warn on wide-open features

**Labels:** good first issue, security-headers

**Context.** The Permissions-Policy check in `packages/security-headers/cmd/mcp-security-headers/checks.go` only checks that the header exists. A value such as `camera=*, geolocation=*` is worse than no header for the features it names. The header is a structured dictionary: `feature=(origin origin)`, `feature=*`, `feature=()`, `feature=self`.

**Acceptance criteria.**
- A small parser turns the header into a map from feature to allowlist and tolerates spaces and quoted origins.
- Features from a fixed list (`camera`, `microphone`, `geolocation`, `payment`, `usb`, `display-capture`, `midi`) set to `*` produce a `warn` with the feature names in the explanation and reduce the points.
- `grade_headers` tests cover `camera=*`, `camera=()` and a mixed value; the `pass` case keeps 5 points.
- The `permissions-policy` explanation of `explain_header` gains one sentence about `*`.

## 3. jwt-tools: verify ES256, ES384 and ES512

**Labels:** good first issue, jwt-tools

**Context.** `packages/jwt-tools/cmd/mcp-jwt-tools/jwt.go` verifies HS* and RS* tokens. ECDSA tokens are common (Apple, many OIDC providers) and need only the Go standard library: `crypto/ecdsa` verifies the signature after splitting it into `r` and `s`, because JWS encodes ECDSA signatures as raw `r || s`, not DER. The curve must match the algorithm (P-256 for ES256, P-384 for ES384, P-521 for ES512).

**Acceptance criteria.**
- The algorithm list gains `ES256`, `ES384`, `ES512`; signing and verification handle them with the raw `r || s` encoding and reject a key on the wrong curve with a clear reason.
- `sign_test_jwt` can mint ES* tokens from a PEM EC private key.
- Tests generate a key pair with `ecdsa.GenerateKey(elliptic.P256(), rand.Reader)`, round-trip sign and verify, and check that an ES256 token is refused when `algorithm` is `ES384`.
- The `verify_jwt` and `sign_test_jwt` descriptions, the README and `CHANGELOG.md` mention ES*.

## 4. cron-tools: previous_runs tool

**Labels:** good first issue, cron-tools

**Context.** `next_runs` in `packages/cron-tools/cmd/mcp-cron-tools/schedule.go` walks forward from a start time. Incident reviews often ask the opposite question: when did this job last run before 03:12 on Tuesday? The forward algorithm can be mirrored: step to the previous minute, and when a field does not match jump to the end of the previous hour, day or month.

**Acceptance criteria.**
- A `previousRuns` function next to `nextRuns` in `schedule.go` returns the `count` most recent runs strictly before `from`, newest first, with the same ten-year horizon.
- A `previous_runs` tool mirrors `next_runs` (same inputs and output shape).
- Tests cover a weekday schedule across a weekend, the Berlin DST gap and overlap used by the `next_runs` tests, and the never-runs case.
- README table and `CHANGELOG.md` updated.

## 5. dockerfile-lint: take .dockerignore into account

**Labels:** good first issue, dockerfile-lint

**Context.** The `copy-whole-context` rule in `packages/dockerfile-lint/cmd/mcp-dockerfile-lint/lint.go` fires on every `COPY . .`, which is noisy for projects that already have a good `.dockerignore`. The lint cannot see that file today.

**Acceptance criteria.**
- `lint_dockerfile` accepts an optional `dockerignore` input (text) and, when `path` is used and the input is absent, reads `.dockerignore` next to the Dockerfile if it exists (bounded to 64 KB).
- A small matcher (no dependency) understands the patterns that matter: exact names, `*` within a segment, `**`, leading `/`, and `!` negations.
- When `.git`, `.env*` (or `.env`) and `node_modules` are all excluded, `copy-whole-context` downgrades to a `pass`-level note or is suppressed; otherwise its message names the missing exclusions.
- Tests with and without a `.dockerignore`, including a negation that re-includes `.env`.

## 6. openapi-lint: flag API keys in the query string and operations without error responses

**Labels:** good first issue, openapi-lint

**Context.** `packages/openapi-lint/cmd/mcp-openapi-lint/lint.go` checks that security schemes exist and are used, but not what they are. An `apiKey` scheme with `in: query` puts the credential into server logs, proxies and browser history. Separately, many documents list only the success response; clients then cannot know what a 401 or 429 looks like.

**Acceptance criteria.**
- New rule `security-scheme-apikey-query` (warning) for `components.securitySchemes.*` with `type: apiKey` and `in: query`, and `security-scheme-http-basic` (info) for `type: http, scheme: basic`.
- New rule `operation-no-error-response` (info) for operations whose responses contain no `4xx`, `5xx` or `default` entry.
- Entries in the rule table with descriptions and fixes; the fixture `test/fixtures/petstore.yaml` gains a query API key and the tests assert all three rules, including that `ignore` silences them.
- README tool description and `CHANGELOG.md` updated.
