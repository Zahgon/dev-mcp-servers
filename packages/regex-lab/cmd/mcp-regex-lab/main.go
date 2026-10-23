// Command mcp-regex-lab is the regex-lab MCP server on stdio: test, explain and stress JavaScript
// (ECMAScript) regular expressions.
package main

import (
	"math"
	"strings"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsregex"
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName          = "regex-lab"
	serverVersion       = "0.1.0"
	maxPattern          = 2000
	maxSample           = 20000
	maxSamples          = 50
	defaultTimeoutMs    = 2000
	maxTimeoutMs        = 10000
	maxMatchesPerSample = 200
)

var noDeadline time.Time

var flagNotes = map[rune]string{'i': "case-insensitive", 'm': "^ and $ match at line breaks", 's': ". matches line terminators",
	'u': "Unicode mode", 'v': "Unicode sets mode", 'g': "global", 'y': "sticky", 'd': "match indices"}

func validateFlags(flags string) string {
	seen := map[rune]bool{}
	for _, f := range flags {
		if seen[f] {
			return "duplicate flag"
		}
		seen[f] = true
	}
	if seen['u'] && seen['v'] {
		return "u and v flags cannot be combined"
	}
	return ""
}

// flagsArg reads the optional flags argument: at most 8 characters from d g i m s u v y.
func flagsArg(a mcp.Args) (string, error) {
	f, err := a.String("flags", 0, 8)
	if err != nil || f == nil {
		return "", err
	}
	for _, c := range *f {
		if !strings.ContainsRune("dgimsuvy", c) {
			return "", mcp.Invalid(a.Tool, "flags: flags are a combination of d g i m s u v y")
		}
	}
	return *f, nil
}

func timeoutArg(a mcp.Args) (*float64, error) {
	return a.Number("timeout_ms", 50, maxTimeoutMs, true)
}

func clampTimeout(t *float64) time.Duration {
	ms := float64(defaultTimeoutMs)
	if t != nil {
		ms = *t
	}
	return time.Duration(math.Min(ms, maxTimeoutMs) * float64(time.Millisecond))
}

func testRegexTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "test_regex", Obj: args}
	pattern, err := a.RequiredString("pattern", 1, maxPattern)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	flags, err := flagsArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	rawSamples, present, err := a.Array("samples", 1, maxSamples)
	if err == nil && !present {
		err = mcp.Invalid(a.Tool, "samples: Required")
	}
	if err != nil {
		return mcp.Fail(err.Error())
	}
	samples := make([]string, len(rawSamples))
	for i, s := range rawSamples {
		str, ok := s.(string)
		if !ok || jsv.UTF16Len(str) > maxSample {
			return mcp.Fail(mcp.Invalid(a.Tool, "samples: Invalid input").Error())
		}
		samples[i] = str
	}
	replacement, err := a.String("replacement", 0, 2000)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	timeout, err := timeoutArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	if msg := validateFlags(flags); msg != "" {
		return mcp.Fail(msg)
	}
	out := runRegex(pattern, flags, samples, clampTimeout(timeout), replacement)
	if out.err != "" {
		return mcp.Fail("Invalid pattern: " + out.err)
	}
	res := jsv.O("pattern", pattern, "flags", flags, "timed_out", out.timedOut, "elapsed_ms", out.elapsedMs,
		"samples_completed", float64(len(out.results)), "samples_total", float64(len(samples)))
	if out.timedOut {
		ms := float64(defaultTimeoutMs)
		if timeout != nil {
			ms = *timeout
		}
		res.Set("note", "Execution exceeded "+jsv.FormatNumber(ms)+" ms and was terminated; run check_redos on this pattern.")
	}
	res.Set("results", out.results)
	return mcp.OK(res)
}

func compileChecked(pattern, flags string) error {
	_, err := jsregex.Compile(pattern, flags)
	return err
}

func explainRegexTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "explain_regex", Obj: args}
	pattern, err := a.RequiredString("pattern", 1, maxPattern)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	flags, err := flagsArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	if msg := validateFlags(flags); msg != "" {
		return mcp.Fail(msg)
	}
	if err := compileChecked(pattern, flags); err != nil {
		return mcp.Fail("Invalid pattern: " + err.Error())
	}
	ast, groups, err := parseRegex(pattern)
	if err != nil {
		return parseFailure(err)
	}
	meanings := []any{}
	for _, c := range flags {
		meanings = append(meanings, string(c)+": "+flagNotes[c])
	}
	return mcp.OK(jsv.O("pattern", pattern, "flags", flags, "flag_meanings", meanings, "capturing_groups", float64(groups),
		"explanation", explainNode(ast, 0, []any{})))
}

func parseFailure(err error) mcp.Result {
	if se, ok := err.(*regexSyntaxError); ok {
		return mcp.Fail("Could not parse pattern at position " + jsv.FormatNumber(float64(se.position)) + ": " + se.message)
	}
	return mcp.Fail(err.Error())
}

func checkRedosTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "check_redos", Obj: args}
	pattern, err := a.RequiredString("pattern", 1, maxPattern)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	flags, err := flagsArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	probe, err := a.Bool("probe")
	if err != nil {
		return mcp.Fail(err.Error())
	}
	timeout, err := timeoutArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	if msg := validateFlags(flags); msg != "" {
		return mcp.Fail(msg)
	}
	if err := compileChecked(pattern, flags); err != nil {
		return mcp.Fail("Invalid pattern: " + err.Error())
	}
	findings, err := analyse(pattern, flags)
	if err != nil {
		return parseFailure(err)
	}
	verdict := "no known backtracking hazard found"
	if len(findings) > 0 {
		verdict = "review"
	}
	for _, f := range findings {
		if f.severity == "high" {
			verdict = "likely vulnerable"
		}
	}
	list := []any{}
	for _, f := range findings {
		list = append(list, f.value())
	}
	if probe != nil && !*probe {
		return mcp.OK(jsv.O("pattern", pattern, "flags", flags, "verdict", verdict, "findings", list))
	}
	var chars []string
	seen := map[string]bool{}
	for _, f := range findings {
		if f.probeChar != nil && !seen[*f.probeChar] {
			seen[*f.probeChar] = true
			chars = append(chars, *f.probeChar)
		}
	}
	if len(chars) == 0 {
		chars = append(chars, "a")
	}
	if len(chars) > 3 {
		chars = chars[:3]
	}
	probes := []any{}
	confirmed := false
	for _, ch := range chars {
		var inputs []string
		for _, n := range []int{10, 14, 18, 22, 26, 30} {
			inputs = append(inputs, strings.Repeat(ch, n)+"\u0001")
		}
		out := probeRegex(pattern, flags, inputs, clampTimeout(timeout))
		growth := []any{}
		var gs []float64
		for i := 1; i < len(out.timings); i++ {
			prev := out.timings[i-1].ms
			g := 1.0
			if prev > 0.05 {
				g = jsRound(out.timings[i].ms/prev*10) / 10
			}
			gs = append(gs, g)
			growth = append(growth, g)
		}
		exponential := out.timedOut
		if !exponential && len(gs) >= 3 {
			exponential = true
			for _, g := range gs[len(gs)-3:] {
				if g < 1.8 {
					exponential = false
				}
			}
		}
		assessment := "timing stays flat: no blow-up observed with this input shape"
		if exponential {
			assessment = "timing grows exponentially or hit the timeout: confirmed catastrophic backtracking"
			confirmed = true
		}
		timings := []any{}
		for _, t := range out.timings {
			timings = append(timings, jsv.O("length", float64(t.length), "ms", t.ms, "matched", t.matched))
		}
		probes = append(probes, jsv.O("probe_char", ch, "timed_out", out.timedOut, "timings", timings, "growth_factors", growth, "assessment", assessment))
	}
	if confirmed {
		verdict = "confirmed vulnerable"
	}
	return mcp.OK(jsv.O("pattern", pattern, "flags", flags, "verdict", verdict, "findings", list, "probes", probes))
}

// jsRound is Math.round.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

func newServer() *mcp.Server {
	pure := jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false)
	str := func(pairs ...any) *jsv.Obj { return jsv.O(append([]any{"type", "string"}, pairs...)...) }
	patternSchema := str("minLength", float64(1), "maxLength", float64(maxPattern), "description", "JavaScript regular expression source, without the surrounding slashes.")
	flagsSchema := str("maxLength", float64(8), "pattern", "^[dgimsuvy]*$", "description", "RegExp flags such as i, m, s, u. g and d are added automatically when matching.")
	timeoutSchema := jsv.O("type", "integer", "minimum", float64(50), "maximum", float64(maxTimeoutMs))
	object := func(props *jsv.Obj, required ...string) *jsv.Obj {
		return jsv.O("type", "object", "properties", props, "required", jsv.Strings(required), "additionalProperties", false,
			"$schema", "http://json-schema.org/draft-07/schema#")
	}
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, schema *jsv.Obj) mcp.Tool {
		return mcp.Tool{Name: name, Handler: handler, Spec: jsv.O("title", title, "description", description, "inputSchema", schema,
			"annotations", pure, "execution", jsv.O("taskSupport", "forbidden"))}
	}
	return &mcp.Server{
		Name:    serverName,
		Version: serverVersion,
		Instructions: "Works with JavaScript (ECMAScript) regular expressions. test_regex runs a pattern against sample strings in a worker with a timeout and reports every match with group captures; " +
			"explain_regex describes the pattern construct by construct; check_redos looks for catastrophic backtracking shapes and can time the pattern against crafted inputs. " +
			"Syntax follows the V8 engine in Node.js; other engines (PCRE, RE2, Python re) differ in details such as lookbehind and possessive quantifiers.",
		Tools: []mcp.Tool{
			tool("test_regex", testRegexTool, "Test a regex against samples",
				"Run a pattern against up to 50 sample strings (each up to 20000 characters) and return every match with its start and end offsets, matched text, "+
					"numbered and named capture groups with their offsets, and optionally the result of String.prototype.replace with the given replacement. Execution runs in a "+
					"worker thread and is stopped after timeout_ms (default 2000, max 10000); results for samples finished before the timeout are still returned. "+
					"At most 200 matches per sample are listed.",
				object(jsv.O("pattern", patternSchema, "flags", flagsSchema,
					"samples", jsv.O("type", "array", "items", str("maxLength", float64(maxSample)), "minItems", float64(1), "maxItems", float64(maxSamples)),
					"replacement", str("maxLength", float64(2000), "description", "If given, each sample is also shown after replace(pattern, replacement); $1 and $<name> work as in JavaScript."),
					"timeout_ms", timeoutSchema), "pattern", "samples")),
			tool("explain_regex", explainRegexTool, "Explain a regex",
				"Parse a pattern and describe it construct by construct: literals, character classes, escapes, anchors, groups (capturing, named, non-capturing, lookaround), "+
					"quantifiers (greedy and lazy) and alternation, as indented lines plus the number of capturing groups. Reports a syntax error with its position when the pattern does not parse.",
				object(jsv.O("pattern", patternSchema, "flags", flagsSchema), "pattern")),
			tool("check_redos", checkRedosTool, "Check a regex for catastrophic backtracking",
				"Statically look for the constructs that cause exponential or polynomial backtracking: an unbounded repeat nested inside another (such as (a+)+ or (\\d+\\s?)*), "+
					"alternatives that can match the same text inside a repeated group (such as (a|ab)*), and consecutive repeats that accept the same characters (such as \\d+\\d+). "+
					"Each finding names the construct, a severity (high: likely exponential; medium: polynomial or needs review) and a character that exercises it. With probe (default true), "+
					"the pattern is then timed in a worker against crafted non-matching inputs of growing length (from the probe character) and the timings are returned; the worker is "+
					"terminated after timeout_ms (default 2000). No findings and flat timings is good evidence, not a proof, that the pattern is safe.",
				object(jsv.O("pattern", patternSchema, "flags", flagsSchema,
					"probe", jsv.O("type", "boolean", "description", "Time the pattern against crafted inputs. Default true."),
					"timeout_ms", timeoutSchema), "pattern")),
		},
	}
}

func main() { mcp.Main(newServer()) }
