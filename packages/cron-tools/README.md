# cron-tools MCP server

Parses, explains, validates and schedules 5-field cron expressions in the Vixie cron dialect (Linux crontab, GitHub Actions, most schedulers), including lists, ranges, steps, month and weekday names, 0 or 7 for Sunday and the `@hourly` to `@yearly` macros. Time zone arithmetic uses the Go standard library with the IANA time zone database embedded in the binary; there are no dependencies beyond the Go standard library.

Part of [dev-mcp-servers](https://github.com/basitalisandhu/dev-mcp-servers). Stdio transport only; the server never opens a port.

## Tools

| Tool | Input | What it returns |
|---|---|---|
| `parse_cron` | `expression` | Per field: raw text, wildcard flag and every accepted value; plus the normalised expression and macro expansion. |
| `explain_cron` | `expression` | One English sentence, for example `*/15 9-17 * * 1-5` is "At every 15th minute past every hour from 9 through 17 on Monday through Friday." |
| `validate_cron` | `expression` | `valid`, errors, and warnings: both day fields restricted (OR semantics), days that never occur in the chosen months, `7` for Sunday, `?` spelling, macro expansion. |
| `next_runs` | `expression`, `count?` (1 to 100), `timezone?` (IANA), `from?` (ISO 8601) | Upcoming run times in local time with offset and in UTC. Wall-clock times skipped by a forward DST transition are not run that day; a repeated hour runs twice. Stops after ten years. |

## Install

Build the binary from the repository root:

```bash
go build -o mcp-cron-tools ./packages/cron-tools/cmd/mcp-cron-tools
```

Claude Code:

```bash
claude mcp add cron-tools -- /path/to/mcp-cron-tools
```

Add `-s user` to make it available in every project. Any client that reads `.mcp.json` (Claude Code, Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "cron-tools": {
      "command": "/path/to/mcp-cron-tools"
    }
  }
}
```

A container image is published as `ghcr.io/basitalisandhu/mcp-cron-tools`; pin a version tag such as `ghcr.io/basitalisandhu/mcp-cron-tools:0.1.0` so that an update cannot change what runs in your editor without you noticing.

## What it touches

- Network: None.
- Local files: None.
- Telemetry: none.

## Notes

- Seconds fields (6-field Quartz) and `L`, `W`, `#` are rejected with an explanation.
- When both day-of-month and day-of-week are restricted, a day matches if either does, as in Vixie cron.

## Build and test

```bash
go build -o mcp-cron-tools ./packages/cron-tools/cmd/mcp-cron-tools   # at the repository root
go test ./packages/cron-tools/...
```

The tests drive the server over stdio (set `MCP_SERVER_BIN` to test another executable); they do not reach the network.

## Licence

MIT. See [LICENSE](../../LICENSE).
