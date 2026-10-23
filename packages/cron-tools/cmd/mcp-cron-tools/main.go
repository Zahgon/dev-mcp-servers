// Command mcp-cron-tools is the cron-tools MCP server on stdio: it parses, explains,
// validates and schedules 5-field cron expressions.
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "cron-tools"
	serverVersion = "0.1.0"
)

const instructions = "Handles 5-field cron expressions in the Vixie cron dialect used by Linux crontab, GitHub Actions and most schedulers: lists (1,15), ranges (1-5), steps (*/10, 1-30/5), " +
	"month and weekday names, 0 or 7 for Sunday, and @hourly/@daily/@weekly/@monthly/@yearly. Seconds fields and Quartz L/W/# are rejected with an explanation. " +
	"parse_cron shows the accepted values per field, explain_cron gives an English sentence, validate_cron lists errors and warnings, next_runs computes upcoming run times in an IANA time zone."

func newServer() *mcp.Server {
	return &mcp.Server{Name: serverName, Version: serverVersion, Instructions: instructions, Tools: tools()}
}

func main() { mcp.Main(newServer()) }

func expressionArg(tool string, args *jsv.Obj) (string, error) {
	return mcp.Args{Tool: tool, Obj: args}.RequiredString("expression", 1, 200)
}

func ints(values []int) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = float64(v)
	}
	return out
}

func parseTool(args *jsv.Obj) mcp.Result {
	expression, err := expressionArg("parse_cron", args)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	p, err := parseCron(expression)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	fields := jsv.NewObj()
	for i, f := range p.fields {
		fields.Set(fieldName(i).String(), jsv.O("raw", f.raw, "wildcard", f.wildcard, "values", ints(f.values)))
	}
	var macroValue any
	if p.macro != "" {
		macroValue = p.macro
	}
	return mcp.OK(jsv.O("expression", p.expression, "normalised", p.normalised, "macro", macroValue, "fields", fields))
}

func explainTool(args *jsv.Obj) mcp.Result {
	expression, err := expressionArg("explain_cron", args)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	v := validateCron(expression)
	if !v.valid {
		return mcp.Fail(strings.Join(v.errors, "; "))
	}
	explanation, err := explainCron(expression)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	return mcp.OK(jsv.O("expression", expression, "normalised", v.parsed.normalised, "explanation", explanation, "warnings", jsv.Strings(v.warnings)))
}

func validateTool(args *jsv.Obj) mcp.Result {
	expression, err := expressionArg("validate_cron", args)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	v := validateCron(expression)
	var normalised any
	if v.parsed != nil {
		normalised = v.parsed.normalised
	}
	return mcp.OK(jsv.O("expression", expression, "valid", v.valid, "errors", jsv.Strings(v.errors), "warnings", jsv.Strings(v.warnings), "normalised", normalised))
}

func nowMs() float64 { return float64(time.Now().UnixMilli()) }

func nextRunsTool(args *jsv.Obj) mcp.Result {
	const tool = "next_runs"
	a := mcp.Args{Tool: tool, Obj: args}
	expression, err := expressionArg(tool, args)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	count, err := a.Number("count", 1, maxRuns, true)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	timezone, err := a.String("timezone", 1, 64)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	from, err := a.String("from", 0, 40)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	tz := "UTC"
	if timezone != nil {
		tz = *timezone
	}
	loc, ok := lookupZone(tz)
	if !ok {
		return mcp.Fail(quote(tz) + " is not a known IANA time zone.")
	}
	fromMs := nowMs()
	if from != nil {
		if fromMs = parseDate(*from); !validTime(fromMs) {
			return mcp.Fail(quote(*from) + " is not an ISO 8601 date-time.")
		}
	}
	v := validateCron(expression)
	if !v.valid {
		return mcp.Fail(strings.Join(v.errors, "; "))
	}
	n := 5
	if count != nil {
		n = int(*count)
	}
	runs, err := nextRuns(v.parsed, fromMs, loc, n)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	explanation, err := explainCron(expression)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	list := make([]any, len(runs))
	for i, ms := range runs {
		local, err := formatLocal(ms, loc)
		if err != nil {
			return mcp.Fail(err.Error())
		}
		p, err := partsAt(ms, loc)
		if err != nil {
			return mcp.Fail(err.Error())
		}
		list[i] = jsv.O("local", local, "utc", isoString(ms), "weekday", dayName[p.weekday])
	}
	out := jsv.O("expression", expression, "timezone", tz, "from", isoString(fromMs), "explanation", explanation,
		"warnings", jsv.Strings(v.warnings), "found", float64(len(runs)))
	if len(runs) < n {
		if len(runs) == 0 {
			out.Set("note", "No run within ten years of 'from'.")
		} else {
			out.Set("note", fmt.Sprintf("Only %d run(s) within ten years of 'from'.", len(runs)))
		}
	}
	out.Set("runs", list)
	return mcp.OK(out)
}

func pure() *jsv.Obj {
	return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false)
}

func inputSchema(props *jsv.Obj) *jsv.Obj {
	return jsv.O("type", "object", "properties", props, "required", []any{"expression"}, "additionalProperties", false,
		"$schema", "http://json-schema.org/draft-07/schema#")
}

func expressionSchema() *jsv.Obj {
	return jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(200),
		"description", "Five space-separated fields (minute hour day-of-month month day-of-week) or a macro such as @daily.")
}

func tools() []mcp.Tool {
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, props *jsv.Obj) mcp.Tool {
		spec := jsv.O("title", title, "description", description, "inputSchema", inputSchema(props), "annotations", pure(),
			"execution", jsv.O("taskSupport", "forbidden"))
		return mcp.Tool{Name: name, Spec: spec, Handler: handler}
	}
	expressionOnly := func() *jsv.Obj { return jsv.O("expression", expressionSchema()) }
	return []mcp.Tool{
		tool("parse_cron", parseTool, "Parse a cron expression",
			"Parse a 5-field cron expression (or @ macro) and return, for each field, the raw text, whether it is a wildcard, and every value it accepts, plus the normalised expression. "+
				"Returns an error with the offending field when the expression is invalid.", expressionOnly()),
		tool("explain_cron", explainTool, "Explain a cron expression in English",
			"Translate a 5-field cron expression into one English sentence, for example '*/15 9-17 * * 1-5' becomes 'At every 15th minute past every hour from 9 through 17 on Monday through Friday.' "+
				"When both day fields are restricted the sentence says 'or', matching Vixie cron semantics. Also returns the validation warnings.", expressionOnly()),
		tool("validate_cron", validateTool, "Validate a cron expression",
			"Check a 5-field cron expression and return valid (boolean), errors (syntax or range problems) and warnings for expressions that parse but behave unexpectedly: "+
				"day-of-month and day-of-week both restricted (OR semantics), days that do not exist in the selected months, 7 used for Sunday, ? spelling, macro expansion.", expressionOnly()),
		tool("next_runs", nextRunsTool, "List the next runs of a cron expression",
			fmt.Sprintf("Compute the next count (1 to %d, default 5) run times of a 5-field cron expression in an IANA time zone (default UTC), starting strictly after 'from' ", maxRuns)+
				"(ISO 8601 date-time, default now). Each run is returned in the zone's local time with its UTC offset and in UTC. Daylight-saving transitions follow the zone's rules: "+
				"a wall-clock time skipped by a forward transition is not run that day, and a repeated hour runs twice. Reports when no run exists within ten years.",
			jsv.O("expression", expressionSchema(),
				"count", jsv.O("type", "integer", "minimum", float64(1), "maximum", float64(maxRuns)),
				"timezone", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(64), "description", "IANA zone such as Europe/Berlin or America/New_York. Default UTC."),
				"from", jsv.O("type", "string", "maxLength", float64(40), "description", "ISO 8601 date-time to start after, for example 2026-01-31T09:00:00Z. Default: now."))),
	}
}
