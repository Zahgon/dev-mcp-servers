# Security policy

These servers run as child processes of your MCP client, with your user's permissions, and their output goes into a model's context. We treat security problems in them seriously and want to hear about them.

## Supported versions

Only the latest release on `main` is supported. Pin the release version of the binary or image you run and update when a fix is announced in [CHANGELOG.md](CHANGELOG.md).

## Reporting a vulnerability

Please do not open a public issue for a security problem.

1. Use GitHub's private vulnerability reporting on this repository ("Security" tab, "Report a vulnerability").
2. If that is unavailable, open an issue titled "Security contact request" with no details, and the maintainer will reply with a private channel.

Include the package and version, what you found, how to reproduce it and what you think the impact is. You will get an acknowledgement within 5 working days and a fix or a mitigation plan within 30 days for confirmed issues.

## What counts

In scope:

- Any server making a network request other than the ones documented in its README (osv-advisories to `api.osv.dev`; security-headers to the URL you supply and its redirects).
- security-headers reaching a private, loopback, link-local or metadata address through a bypass of the address checks.
- Any server writing to disk, executing a command other than the allowlisted `git` subcommands in git-insights, or reading a file it was not pointed at.
- git-insights accepting an argument that changes what `git` does (an option smuggled through a path, revision or filter) or that writes to the repository.
- A tool that can be made to hang the server or exhaust memory despite the documented limits (for example a regex that escapes the worker timeout, a YAML document that defeats the alias limit).
- jwt-tools reporting a signature as valid when it is not, or accepting a different algorithm than the one requested.
- Dependency vulnerabilities that are reachable from a tool input.
- Prompt-injection-style instructions hidden anywhere in this repository or in tool descriptions.

Not vulnerabilities, but welcome as issues or pull requests: lint rules that miss a pattern, grading thresholds you disagree with, heuristics in `check_redos` that miss or over-report a pattern.

## What the servers do and do not do

- Stdio transport only; no server listens on a port.
- No telemetry, no update checks.
- Every tool input is validated against its schema and bounded; files, responses and outputs have size caps; network, regex and git calls have timeouts.
- Eight of the ten servers make no network requests. The two that do contact only the documented host and refuse everything else before connecting.
- Container images carry a build provenance attestation and release archives are listed in `SHA256SUMS`. Compare the attestation's commit with the tag in this repository before trusting an image.
