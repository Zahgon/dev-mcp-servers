# Changelog

All notable changes to this project are documented here. The format follows Keep a Changelog, and every package in the repository shares one version number.

## [Unreleased]

### Changed

- Rewrote all ten servers in Go as one module (`go.mod` at the root, each server's main package in `packages/<name>/cmd/mcp-<name>`), with the same tools, inputs and results. The `node:test` suites became `go test` suites, `scripts/check-server-json.mjs` became `go run ./scripts/check-server-json`, the Dockerfile builds static binaries, and CI runs `go vet`, `go build` and `go test`.
- Releases ship static binaries for linux and darwin (amd64, arm64) on the GitHub release and container images on GHCR; the npm packages, `package.json` files, the lockfile and the TypeScript configuration are gone, and each `server.json` lists its OCI image instead of an npm package.
- Removed the umbrella branding; this project stands alone and links its sibling repositories directly.

## [0.1.0] - 2026-10-04

First release. Every server is published to two registries on GitHub Packages, using only the workflow's `GITHUB_TOKEN`:

- npm (`https://npm.pkg.github.com`): `@basitalisandhu/mcp-osv-advisories`, `@basitalisandhu/mcp-security-headers`, `@basitalisandhu/mcp-jwt-tools`, `@basitalisandhu/mcp-regex-lab`, `@basitalisandhu/mcp-cron-tools`, `@basitalisandhu/mcp-json-schema-tools`, `@basitalisandhu/mcp-openapi-lint`, `@basitalisandhu/mcp-dockerfile-lint`, `@basitalisandhu/mcp-git-insights`, `@basitalisandhu/mcp-llms-txt`.
- GitHub Container Registry (`ghcr.io`): `ghcr.io/basitalisandhu/mcp-osv-advisories`, `mcp-security-headers`, `mcp-jwt-tools`, `mcp-regex-lab`, `mcp-cron-tools`, `mcp-json-schema-tools`, `mcp-openapi-lint`, `mcp-dockerfile-lint`, `mcp-git-insights`, `mcp-llms-txt`, tagged `0.1.0` and `latest`, for linux/amd64 and linux/arm64, each with an SPDX SBOM, a build provenance attestation and a keyless cosign signature.

### Added

- `publish-github-packages.yml`: on a `v*` tag, builds and tests, publishes the ten npm packages to GitHub Packages (skipping versions that already exist), builds, pushes, attests and signs the ten images, and creates the GitHub release with the SBOMs attached. Pull requests that touch packaging run it as a dry run.
- A root `Dockerfile` that builds any one server (`--build-arg SERVER=<name>`) on a digest-pinned `node:22-alpine`, with only that server's runtime dependencies, running as the non-root `node` user on stdio. The git-insights image adds `git` and trusts only `/repo`.
- CI builds the jwt-tools and git-insights images and starts each one on stdio.
- A test per server that starts it through a symlinked bin.

- `osv-advisories`: `query_package`, `query_batch`, `scan_lockfile` (package-lock.json v1 to v3, requirements.txt `==` pins, poetry.lock, go.sum) and `get_vulnerability`, all against `api.osv.dev` only, with a 15 s timeout and an 8 MB response cap.
- `security-headers`: `check_url_headers` (HEAD with GET fallback, limited redirects, private and metadata addresses refused, body never read), `grade_headers` and `explain_header`, grading CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy, COOP, CORP, X-XSS-Protection, Server, X-Powered-By and Set-Cookie.
- `jwt-tools`: `decode_jwt` (never verifies; findings for `alg: none`, expiry, missing claims, long lifetimes, key URLs in the header, sensitive claims), `verify_jwt` (HS256/384/512 and RS256/384/512 for exactly the requested algorithm) and `sign_test_jwt`.
- `regex-lab`: `test_regex` in a terminated-on-timeout worker thread, `explain_regex`, and `check_redos` with static findings plus timed probes.
- `cron-tools`: `parse_cron`, `explain_cron`, `validate_cron` and `next_runs` with IANA time zones and DST handling, implemented with `Intl` and no extra dependency.
- `json-schema-tools`: `validate_json` (drafts 07, 2019-09, 2020-12 via Ajv with formats), `infer_schema` and `diff_schemas` with breaking/compatible/review verdicts.
- `openapi-lint`: `lint_openapi` with 22 rules, `list_operations`, `get_operation` and `explain_rule`; JSON or YAML from a path or inline, local references only.
- `dockerfile-lint`: `lint_dockerfile` with 25 rules, `parse_dockerfile` (escape directive, continuations, heredocs, stages) and `explain_rule`.
- `git-insights`: `repo_summary`, `git_log`, `blame_summary`, `churn` (renames followed), `authors` and `large_files` (tree or full history), read-only with validated arguments.
- `llms-txt`: `generate_llms_txt` from a Markdown directory, `generate_from_sitemap` from a local sitemap, and `check_llms_txt`.
- Root workspace with shared `tsconfig.base.json`, CI on Node 20 and 22, a release workflow that publishes every workspace to npmjs.com with provenance (off until the repository variable `NPMJS_PUBLISH` is `true`), an offline `server.json` schema check, and six good first issues in `docs/good-first-issues.md`.

### Changed

- Renamed the umbrella project from Hisar to Masoon; links, names and identifiers updated.
- Each package's `publishConfig` now targets `https://npm.pkg.github.com`; `release.yml` passes `--registry https://registry.npmjs.org` to keep publishing to npmjs.com.

### Fixed

- Every server exited without serving when started through its installed bin (`npx`, `npm i -g`), because the bin is a symlink and the entry-point check compared the unresolved path. The check now compares real paths.
