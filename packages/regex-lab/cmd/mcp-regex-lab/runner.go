package main

// Port of src/runner.ts. The original runs each pattern in a worker thread that is terminated at the
// timeout; here the matcher itself stops at the deadline, and results for samples finished before it
// are kept.

import (
	"math"
	"strings"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsregex"
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

type runOutcome struct {
	timedOut  bool
	elapsedMs float64
	err       string
	results   []any
}

func elapsedSince(start time.Time) float64 {
	return float64(time.Since(start).Milliseconds())
}

func runRegex(pattern, flags string, samples []string, timeout time.Duration, replacement *string) runOutcome {
	start := time.Now()
	deadline := start.Add(timeout)
	out := runOutcome{results: []any{}}
	gflags := flags
	if !strings.Contains(flags, "g") {
		gflags += "g"
	}
	re, err := jsregex.Compile(pattern, gflags)
	if err == nil && !strings.Contains(flags, "d") {
		re, err = jsregex.Compile(pattern, re.Flags+"d")
	}
	if err != nil {
		out.err = err.Error()
		out.elapsedMs = elapsedSince(start)
		return out
	}
	plain, err := jsregex.Compile(pattern, flags)
	for _, sample := range samples {
		in := jsregex.Units(sample)
		matches := []any{}
		truncated := false
		last := 0
		for {
			mt, next, aborted := re.Exec(in, last, deadline)
			if aborted {
				out.timedOut = true
				out.elapsedMs = elapsedSince(start)
				return out
			}
			if mt == nil {
				break
			}
			if len(matches) >= maxMatchesPerSample {
				truncated = true
				break
			}
			matches = append(matches, matchValue(re, in, mt))
			last = next
			if mt.Caps[1] == mt.Caps[0] {
				last++
			}
		}
		entry := jsv.O("sample", sample, "matched", len(matches) > 0, "match_count", float64(len(matches)), "truncated", truncated, "matches", matches)
		if replacement != nil {
			if err != nil {
				entry.Set("replaced", "replace failed: "+err.Error())
			} else {
				replaced, aborted := plain.Replace(sample, *replacement, deadline)
				if aborted {
					out.timedOut = true
					out.elapsedMs = elapsedSince(start)
					return out
				}
				entry.Set("replaced", replaced)
			}
		}
		out.results = append(out.results, entry)
	}
	out.elapsedMs = elapsedSince(start)
	return out
}

func matchValue(re *jsregex.Regexp, in []uint16, mt *jsregex.Match) *jsv.Obj {
	groups := []any{}
	group := func(index any, k int) {
		s, e := mt.Caps[2*k], mt.Caps[2*k+1]
		if s < 0 {
			groups = append(groups, jsv.O("index", index, "value", nil, "start", nil, "end", nil))
			return
		}
		groups = append(groups, jsv.O("index", index, "value", jsregex.String(in[s:e]), "start", float64(s), "end", float64(e)))
	}
	for k := 1; k <= re.Groups(); k++ {
		group(float64(k), k)
	}
	for _, n := range re.Names() {
		group(n.Name, n.Index)
	}
	return jsv.O("start", float64(mt.Caps[0]), "end", float64(mt.Caps[1]), "text", jsregex.String(in[mt.Caps[0]:mt.Caps[1]]), "groups", groups)
}

type probeTiming struct {
	length  int
	ms      float64
	matched bool
}

type probeOutcome struct {
	timedOut bool
	timings  []probeTiming
}

func probeRegex(pattern, flags string, inputs []string, timeout time.Duration) probeOutcome {
	deadline := time.Now().Add(timeout)
	var out probeOutcome
	re, err := jsregex.Compile(pattern, flags)
	if err != nil {
		return out
	}
	for _, input := range inputs {
		t0 := time.Now()
		matched, aborted := re.Test(input, deadline)
		if aborted {
			out.timedOut = true
			return out
		}
		ms := math.Round(float64(time.Since(t0).Nanoseconds())/1e6*100) / 100
		out.timings = append(out.timings, probeTiming{length: jsv.UTF16Len(input), ms: ms, matched: matched})
	}
	return out
}
