package main

// The upstream suite test/server.test.mjs, ported case by case. The upstream case that calls
// parse, explain, analyse and runRegex directly runs in-process only when no MCP_SERVER_BIN is set;
// its inputs also go through the tools so they hold for any server.

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

type matchGroup struct {
	Index any     `json:"index"`
	Value *string `json:"value"`
	Start *int    `json:"start"`
	End   *int    `json:"end"`
}

type sampleResult struct {
	Sample     string `json:"sample"`
	Matched    bool   `json:"matched"`
	MatchCount int    `json:"match_count"`
	Truncated  bool   `json:"truncated"`
	Matches    []struct {
		Start  int          `json:"start"`
		End    int          `json:"end"`
		Text   string       `json:"text"`
		Groups []matchGroup `json:"groups"`
	} `json:"matches"`
	Replaced *string `json:"replaced"`
}

type testResult struct {
	TimedOut         bool           `json:"timed_out"`
	SamplesCompleted int            `json:"samples_completed"`
	Note             string         `json:"note"`
	Results          []sampleResult `json:"results"`
}

type explainResult struct {
	CapturingGroups int      `json:"capturing_groups"`
	FlagMeanings    []string `json:"flag_meanings"`
	Explanation     []string `json:"explanation"`
}

type redosFinding struct {
	Kind      string  `json:"kind"`
	Severity  string  `json:"severity"`
	ProbeChar *string `json:"probe_char"`
}

type redosResult struct {
	Verdict  string         `json:"verdict"`
	Findings []redosFinding `json:"findings"`
	Probes   []struct {
		TimedOut      bool      `json:"timed_out"`
		GrowthFactors []float64 `json:"growth_factors"`
	} `json:"probes"`
}

func TestListsTheThreeTools(t *testing.T) {
	c := connected(t)
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
	if !slices.Equal(names, []string{"check_redos", "explain_regex", "test_regex"}) {
		t.Fatalf("tools: %v", names)
	}
}

func TestTestRegexReportsMatchesGroupsReplacementsAndInvalidPatterns(t *testing.T) {
	c := connected(t)
	r := decodeResult[testResult](t, c.call("test_regex", map[string]any{"pattern": `(?<year>\d{4})-(\d{2})`, "flags": "i",
		"samples": []string{"2024-05 and 1999-12", "none"}, "replacement": "$<year>/$2"}))
	if r.TimedOut || len(r.Results) != 2 {
		t.Fatalf("result: %+v", r)
	}
	first := r.Results[0]
	if first.MatchCount != 2 || first.Matches[0].Text != "2024-05" {
		t.Errorf("first sample: %+v", first)
	}
	var groups [][3]any
	for _, g := range first.Matches[0].Groups {
		if g.Value == nil || g.Start == nil {
			t.Fatalf("group without value: %+v", g)
		}
		groups = append(groups, [3]any{g.Index, *g.Value, *g.Start})
	}
	want := [][3]any{{float64(1), "2024", 0}, {float64(2), "05", 5}, {"year", "2024", 0}}
	if !slices.Equal(groups, want) {
		t.Errorf("groups: %v, want %v", groups, want)
	}
	if first.Replaced == nil || *first.Replaced != "2024/05 and 1999-12" {
		t.Errorf("replaced: %v", first.Replaced)
	}
	if r.Results[1].Matched {
		t.Errorf("second sample matched: %+v", r.Results[1])
	}
	empty := decodeResult[testResult](t, c.call("test_regex", map[string]any{"pattern": "x*", "samples": []string{"ab"}}))
	if empty.Results[0].MatchCount != 3 {
		t.Errorf("x* on ab: %+v", empty.Results[0])
	}
	bad := c.call("test_regex", map[string]any{"pattern": "(", "samples": []string{"a"}})
	if !bad.IsError || !strings.Contains(bad.Text, "Invalid pattern") {
		t.Errorf("bad pattern: %+v", bad)
	}
	if badFlags := c.call("test_regex", map[string]any{"pattern": "a", "flags": "x", "samples": []string{"a"}}); !badFlags.IsError {
		t.Errorf("bad flags accepted: %s", badFlags.Text)
	}
}

func TestTestRegexTerminatesACatastrophicPatternAtTheTimeoutAndKeepsFinishedSamples(t *testing.T) {
	c := connected(t)
	r := decodeResult[testResult](t, c.call("test_regex", map[string]any{"pattern": "^(a+)+$",
		"samples": []string{"aaa", strings.Repeat("a", 40) + "!"}, "timeout_ms": 300}))
	if !r.TimedOut || r.SamplesCompleted != 1 || len(r.Results) != 1 || !r.Results[0].Matched {
		t.Errorf("result: %+v", r)
	}
	if !strings.Contains(r.Note, "terminated") {
		t.Errorf("note: %q", r.Note)
	}
}

func TestExplainRegexDescribesConstructsAndRejectsSyntaxErrors(t *testing.T) {
	c := connected(t)
	r := decodeResult[explainResult](t, c.call("explain_regex", map[string]any{"pattern": `^(?<user>[\w.]+)@(?:example|test)\.com$|\bfoo\b`, "flags": "i"}))
	if r.CapturingGroups != 1 {
		t.Errorf("capturing_groups: %d", r.CapturingGroups)
	}
	text := strings.Join(r.Explanation, "\n")
	for _, want := range []string{`named capturing group "user"`, "non-capturing group", "one or more times", "2 alternatives", "word boundary"} {
		if !strings.Contains(text, want) {
			t.Errorf("explanation lacks %q:\n%s", want, text)
		}
	}
	if !slices.Equal(r.FlagMeanings, []string{"i: case-insensitive"}) {
		t.Errorf("flag_meanings: %v", r.FlagMeanings)
	}
	if bad := c.call("explain_regex", map[string]any{"pattern": "a{3,1}"}); !bad.IsError {
		t.Errorf("a{3,1} accepted: %s", bad.Text)
	}
}

func TestCheckRedosFlagsNestedQuantifiersOverlappingAlternationAndAdjacentOverlapsAndProbesThem(t *testing.T) {
	c := connected(t)
	nested := decodeResult[redosResult](t, c.call("check_redos", map[string]any{"pattern": "^(a+)+$", "timeout_ms": 400}))
	if len(nested.Findings) == 0 || nested.Findings[0].Kind != "nested-quantifier" || nested.Findings[0].Severity != "high" ||
		nested.Findings[0].ProbeChar == nil || *nested.Findings[0].ProbeChar != "a" {
		t.Errorf("nested findings: %+v", nested.Findings)
	}
	if nested.Verdict != "confirmed vulnerable" {
		t.Errorf("nested verdict: %q", nested.Verdict)
	}
	if len(nested.Probes) == 0 || !(nested.Probes[0].TimedOut || slices.ContainsFunc(nested.Probes[0].GrowthFactors, func(g float64) bool { return g >= 1.8 })) {
		t.Errorf("nested probes: %+v", nested.Probes)
	}

	alt := decodeResult[redosResult](t, c.call("check_redos", map[string]any{"pattern": "^(a|ab)*c$", "probe": false}))
	if !slices.ContainsFunc(alt.Findings, func(f redosFinding) bool { return f.Kind == "overlapping-alternation" }) {
		t.Errorf("alternation findings: %+v", alt.Findings)
	}

	adjacent := decodeResult[redosResult](t, c.call("check_redos", map[string]any{"pattern": `\d+\d+x`, "probe": false}))
	if len(adjacent.Findings) == 0 || adjacent.Findings[0].Kind != "adjacent-overlap" || adjacent.Findings[0].Severity != "medium" || adjacent.Verdict != "review" {
		t.Errorf("adjacent: %+v", adjacent)
	}

	safe := decodeResult[redosResult](t, c.call("check_redos", map[string]any{"pattern": `^[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`, "flags": "i", "timeout_ms": 1500}))
	if len(safe.Findings) != 0 || safe.Verdict != "no known backtracking hazard found" || len(safe.Probes) == 0 || safe.Probes[0].TimedOut {
		t.Errorf("safe: %+v", safe)
	}
	if bad := c.call("check_redos", map[string]any{"pattern": "["}); !bad.IsError {
		t.Errorf("[ accepted: %s", bad.Text)
	}
}

func TestParserAndHeuristicsOnTheirOwn(t *testing.T) {
	c := connected(t)
	groups := decodeResult[explainResult](t, c.call("explain_regex", map[string]any{"pattern": "(a)(?<b>b)(?:c)"}))
	if groups.CapturingGroups != 2 {
		t.Errorf("capturing groups: %d", groups.CapturingGroups)
	}
	set := decodeResult[explainResult](t, c.call("explain_regex", map[string]any{"pattern": `[^a-z\d]`}))
	if !strings.Contains(strings.Join(set.Explanation, "\n"), "any character NOT in the set") {
		t.Errorf("negated set: %v", set.Explanation)
	}
	severity := func(pattern, kind string) string {
		r := decodeResult[redosResult](t, c.call("check_redos", map[string]any{"pattern": pattern, "probe": false}))
		for _, f := range r.Findings {
			if kind == "" || f.Kind == kind {
				return f.Severity
			}
		}
		return ""
	}
	if s := severity("(a+b)+", "nested-quantifier"); s != "medium" {
		t.Errorf("(a+b)+ severity: %q", s)
	}
	if s := severity(`(\d+\s?)*$`, ""); s != "high" {
		t.Errorf(`(\d+\s?)*$ severity: %q`, s)
	}
	if clean := decodeResult[redosResult](t, c.call("check_redos", map[string]any{"pattern": `^\s*(\w+)\s*=\s*(.*)$`, "probe": false})); len(clean.Findings) != 0 {
		t.Errorf("key=value findings: %+v", clean.Findings)
	}
	run := decodeResult[testResult](t, c.call("test_regex", map[string]any{"pattern": "a", "samples": []string{"a"}, "timeout_ms": 500}))
	if len(run.Results) != 1 || !run.Results[0].Matched {
		t.Errorf("a on a: %+v", run)
	}

	if os.Getenv("MCP_SERVER_BIN") != "" {
		return
	}
	if _, n, err := parseRegex("(a)(?<b>b)(?:c)"); err != nil || n != 2 {
		t.Errorf("parse groups: %d %v", n, err)
	}
	for pattern, want := range map[string]string{"a)": "unmatched", "*a": "nothing to repeat"} {
		if _, _, err := parseRegex(pattern); err == nil || !regexp.MustCompile(want).MatchString(err.Error()) {
			t.Errorf("parse %q: %v", pattern, err)
		}
	}
	ast, _, err := parseRegex(`[^a-z\d]`)
	if err != nil {
		t.Fatalf("parseRegex(`[^a-z\\d]`): %v", err)
	}
	var lines []string
	for _, l := range explainNode(ast, 0, nil) {
		lines = append(lines, l.(string))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "any character NOT in the set") {
		t.Errorf("explain: %v", lines)
	}
	if findings, err := analyse("(a+b)+", ""); err != nil || !slices.ContainsFunc(findings, func(f finding) bool { return f.kind == "nested-quantifier" && f.severity == "medium" }) {
		t.Errorf("analyse (a+b)+: %+v %v", findings, err)
	}
	if findings, err := analyse(`(\d+\s?)*$`, ""); err != nil || len(findings) == 0 || findings[0].severity != "high" {
		t.Errorf(`analyse (\d+\s?)*$: %+v %v`, findings, err)
	}
	if findings, err := analyse(`^\s*(\w+)\s*=\s*(.*)$`, ""); err != nil || len(findings) != 0 {
		t.Errorf("analyse key=value: %+v %v", findings, err)
	}
	if out := runRegex("a", "", []string{"a"}, 500*time.Millisecond, nil); out.err != "" || len(out.results) != 1 {
		t.Errorf("runRegex: %+v", out)
	}
}
