package main

// The upstream suite test/server.test.mjs, ported case by case. The upstream cases that
// call parseCron, explainCron, validateCron and nextRuns directly send the same inputs
// through the tools here, so they also hold for a server started from MCP_SERVER_BIN.

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"
	"time"
)

type cronField struct {
	Raw      string `json:"raw"`
	Wildcard bool   `json:"wildcard"`
	Values   []int  `json:"values"`
}

type parseResult struct {
	Normalised string               `json:"normalised"`
	Macro      *string              `json:"macro"`
	Fields     map[string]cronField `json:"fields"`
}

type validateResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

type explainResult struct {
	Explanation string   `json:"explanation"`
	Warnings    []string `json:"warnings"`
}

type run struct {
	Local   string `json:"local"`
	UTC     string `json:"utc"`
	Weekday string `json:"weekday"`
}

type runsResult struct {
	Found int    `json:"found"`
	Note  string `json:"note"`
	Runs  []run  `json:"runs"`
}

func anyMatch(items []string, pattern string) bool {
	re := regexp.MustCompile(pattern)
	for _, s := range items {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func (c *mcpClient) parse(expression string) parseResult {
	c.t.Helper()
	return decodeResult[parseResult](c.t, c.call("parse_cron", map[string]any{"expression": expression}))
}

func (c *mcpClient) validate(expression string) validateResult {
	c.t.Helper()
	return decodeResult[validateResult](c.t, c.call("validate_cron", map[string]any{"expression": expression}))
}

func (c *mcpClient) runs(args map[string]any) runsResult {
	c.t.Helper()
	return decodeResult[runsResult](c.t, c.call("next_runs", args))
}

func utcTimes(r runsResult) []string {
	out := make([]string, len(r.Runs))
	for i, x := range r.Runs {
		out[i] = x.UTC
	}
	return out
}

func TestListsTheFourTools(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != serverName {
		t.Fatalf("serverInfo.name: %s", init.raw)
	}
	reply := c.request("tools/list", map[string]any{})
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &list); err != nil {
		t.Fatalf("tools/list: %s", reply.raw)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if want := []string{"explain_cron", "next_runs", "parse_cron", "validate_cron"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestParseCronExpandsListsRangesStepsNamesAndMacros(t *testing.T) {
	c := connected(t)
	r := c.parse("*/15 9-17 1,15 jan-mar mon-fri")
	want := map[string][]int{
		"minute":       {0, 15, 30, 45},
		"hour":         {9, 10, 11, 12, 13, 14, 15, 16, 17},
		"day_of_month": {1, 15},
		"month":        {1, 2, 3},
		"day_of_week":  {1, 2, 3, 4, 5},
	}
	for name, values := range want {
		if got := r.Fields[name].Values; !slices.Equal(got, values) {
			t.Errorf("%s values %v, want %v", name, got, values)
		}
	}
	if r.Fields["minute"].Wildcard {
		t.Errorf("minute field %+v is reported as a wildcard, want an explicit list", r.Fields["minute"])
	}
	macro := c.parse("@weekly")
	if macro.Normalised != "0 0 * * 0" || macro.Macro == nil || *macro.Macro != "@weekly" {
		t.Errorf("@weekly: normalised %q macro %v", macro.Normalised, macro.Macro)
	}
	if got := c.parse("0 0 * * 7").Fields["day_of_week"].Values; !slices.Equal(got, []int{0}) {
		t.Errorf("7 for Sunday: %v", got)
	}
	if got := c.parse("5/20 * * * *").Fields["minute"].Values; !slices.Equal(got, []int{5, 25, 45}) {
		t.Errorf("5/20: %v", got)
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "a * * * *", "0 0 L * *", "@reboot", "1-5-7 * * * *", "0 0 * * 1#2", "* * * * * *"} {
		if r := c.call("parse_cron", map[string]any{"expression": bad}); !r.IsError {
			t.Errorf("%q parsed: %s", bad, r.Text)
		}
	}
}

func TestExplainCronProducesReadableSentences(t *testing.T) {
	cases := [][2]string{
		{"* * * * *", "Every minute."},
		{"*/5 * * * *", "Every 5 minutes."},
		{"30 9 * * *", "At 09:30."},
		{"0 9,17 * * *", "At 09:00 and 17:00."},
		{"0,30 9 * * *", "At 09:00 and 09:30."},
		{"*/15 9-17 * * 1-5", "At every 15th minute past every hour from 9 through 17 on Monday through Friday."},
		{"0 0 1 * *", "At 00:00 on day-of-month 1."},
		{"0 0 1,15 * 1", "At 00:00 on day-of-months 1 and 15 or on Monday."},
		{"0 12 * jan,jul *", "At 12:00 in January and July."},
		{"5 * * * *", "At minute 5 past every hour."},
		{"* 9 * * *", "Every minute of hour 9."},
		{"0 */6 * * *", "At minute 0 past every 6th hour."},
		{"@daily", "At 00:00."},
	}
	c := connected(t)
	for _, tc := range cases {
		r := decodeResult[explainResult](t, c.call("explain_cron", map[string]any{"expression": tc[0]}))
		if r.Explanation != tc[1] {
			t.Errorf("%q: %q, want %q", tc[0], r.Explanation, tc[1])
		}
	}
	r := decodeResult[explainResult](t, c.call("explain_cron", map[string]any{"expression": "0 0 1,15 * 1"}))
	if r.Explanation != "At 00:00 on day-of-months 1 and 15 or on Monday." || !anyMatch(r.Warnings, "EITHER") {
		t.Errorf("0 0 1,15 * 1: %+v", r)
	}
	if bad := c.call("explain_cron", map[string]any{"expression": "nope"}); !bad.IsError {
		t.Errorf("nope explained: %s", bad.Text)
	}
}

func TestValidateCronReportsErrorsAndWarnings(t *testing.T) {
	c := connected(t)
	if ok := c.validate("0 9 * * 1-5"); !ok.Valid || len(ok.Warnings) != 0 {
		t.Errorf("0 9 * * 1-5: %+v", ok)
	}
	if never := c.validate("0 0 31 2,4 *"); !never.Valid || !anyMatch(never.Warnings, "never runs") {
		t.Errorf("0 0 31 2,4 *: %+v", never)
	}
	if short := c.validate("0 0 31 * *"); !anyMatch(short.Warnings, "shorter months") {
		t.Errorf("0 0 31 * *: %+v", short)
	}
	bad := c.validate("0 0 * * 1,2,9")
	if bad.Valid || len(bad.Errors) == 0 || !regexp.MustCompile("outside 0-6").MatchString(bad.Errors[0]) {
		t.Errorf("0 0 * * 1,2,9: %+v", bad)
	}
	if seven := c.validate("0 0 * * 7"); !anyMatch(seven.Warnings, "7 was used") {
		t.Errorf("0 0 * * 7: %+v", seven)
	}
	if q := c.validate("0 0 ? * MON"); !q.Valid || !anyMatch(q.Warnings, "Quartz") {
		t.Errorf("0 0 ? * MON: %+v", q)
	}
}

func TestNextRunsHonoursTimeZonesDSTAndTheHorizon(t *testing.T) {
	c := connected(t)
	r := c.runs(map[string]any{"expression": "30 9 * * 1-5", "timezone": "Europe/Berlin", "from": "2026-03-27T12:00:00Z", "count": 3})
	var local, weekdays []string
	for _, x := range r.Runs {
		local = append(local, x.Local)
		weekdays = append(weekdays, x.Weekday)
	}
	if want := []string{"2026-03-30T09:30:00+02:00", "2026-03-31T09:30:00+02:00", "2026-04-01T09:30:00+02:00"}; !slices.Equal(local, want) {
		t.Errorf("local %v, want %v", local, want)
	}
	if want := []string{"2026-03-30T07:30:00.000Z", "2026-03-31T07:30:00.000Z", "2026-04-01T07:30:00.000Z"}; !slices.Equal(utcTimes(r), want) {
		t.Errorf("utc %v, want %v", utcTimes(r), want)
	}
	if want := []string{"Monday", "Tuesday", "Wednesday"}; !slices.Equal(weekdays, want) {
		t.Errorf("weekdays %v, want %v", weekdays, want)
	}

	// 02:30 does not exist on 2026-03-29 in Berlin (clocks jump from 02:00 to 03:00), so the run is skipped that day.
	gap := c.runs(map[string]any{"expression": "30 2 * * *", "timezone": "Europe/Berlin", "from": "2026-03-28T12:00:00Z", "count": 2})
	if want := []string{"2026-03-30T00:30:00.000Z", "2026-03-31T00:30:00.000Z"}; !slices.Equal(utcTimes(gap), want) {
		t.Errorf("gap %v, want %v", utcTimes(gap), want)
	}
	// 02:30 happens twice on 2026-10-25 in Berlin.
	twice := c.runs(map[string]any{"expression": "30 2 * * *", "timezone": "Europe/Berlin", "from": "2026-10-24T12:00:00Z", "count": 2})
	if want := []string{"2026-10-25T00:30:00.000Z", "2026-10-25T01:30:00.000Z"}; !slices.Equal(utcTimes(twice), want) {
		t.Errorf("repeated hour %v, want %v", utcTimes(twice), want)
	}
	// Hourly across the US fall-back transition keeps one run per real hour.
	hourly := c.runs(map[string]any{"expression": "0 * * * *", "timezone": "America/New_York", "from": "2026-11-01T04:30:00Z", "count": 4})
	if want := []string{"2026-11-01T05:00:00.000Z", "2026-11-01T06:00:00.000Z", "2026-11-01T07:00:00.000Z", "2026-11-01T08:00:00.000Z"}; !slices.Equal(utcTimes(hourly), want) {
		t.Errorf("hourly %v, want %v", utcTimes(hourly), want)
	}

	leap := c.runs(map[string]any{"expression": "0 0 29 2 *", "from": "2026-01-01T00:00:00Z", "count": 2})
	if want := []string{"2028-02-29T00:00:00.000Z", "2032-02-29T00:00:00.000Z"}; !slices.Equal(utcTimes(leap), want) {
		t.Errorf("29 February %v, want %v", utcTimes(leap), want)
	}
	never := c.runs(map[string]any{"expression": "0 0 31 2 *", "from": "2026-01-01T00:00:00Z"})
	if never.Found != 0 || !regexp.MustCompile("No run within ten years").MatchString(never.Note) {
		t.Errorf("31 February: %+v", never)
	}
	kolkata := c.runs(map[string]any{"expression": "0 0 * * *", "timezone": "Asia/Kolkata", "from": "2026-06-01T00:00:00Z", "count": 1})
	if len(kolkata.Runs) != 1 || kolkata.Runs[0].Local != "2026-06-02T00:00:00+05:30" || kolkata.Runs[0].UTC != "2026-06-01T18:30:00.000Z" {
		t.Errorf("Kolkata: %+v", kolkata)
	}
	if os.Getenv("MCP_SERVER_BIN") == "" {
		loc, _ := lookupZone("Asia/Kolkata")
		got, err := wallToUtc(dateUTC(2026, 5, 2, 0, 0), loc)
		if err != nil || got != parseDate("2026-06-01T18:30:00Z") {
			t.Errorf("wallToUtc Kolkata midnight: %v %v", got, err)
		}
	}

	if badTz := c.call("next_runs", map[string]any{"expression": "* * * * *", "timezone": "Mars/Olympus"}); !badTz.IsError {
		t.Errorf("Mars/Olympus accepted: %s", badTz.Text)
	}
	if badFrom := c.call("next_runs", map[string]any{"expression": "* * * * *", "from": "yesterday"}); !badFrom.IsError {
		t.Errorf("from yesterday accepted: %s", badFrom.Text)
	}
	defaults := c.runs(map[string]any{"expression": "* * * * *"})
	if len(defaults.Runs) != 5 {
		t.Fatalf("default count gave %d runs", len(defaults.Runs))
	}
	first, err := time.Parse(time.RFC3339Nano, defaults.Runs[0].UTC)
	if err != nil || !first.After(time.Now().Add(-time.Minute)) {
		t.Errorf("first default run %s is not after now minus a minute", defaults.Runs[0].UTC)
	}
}
