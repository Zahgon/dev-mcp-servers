package main

// Dockerfile parsing: comments, the escape directive, line continuations, heredocs and
// build stages. All text here is in unit form (see jsstr.go).

import (
	"fmt"
	"strings"
)

// maxDockerfileBytes bounds both a file read from disk and inline content.
const maxDockerfileBytes = 1024 * 1024

type instruction struct {
	line, endLine int
	keyword, args string
	stage         int
}

type stage struct {
	index int
	name  *string
	base  string
	line  int
}

type parsed struct {
	instructions []instruction
	stages       []stage
	escape       string
}

var (
	reLineBreak    = jsRegexp(`\r?\n`, "")
	reEscape       = jsRegexp(`^#\s*escape\s*=\s*(\S)\s*$`, "i")
	reCommentStart = jsRegexp(`^\s*#`, "")
	reDirective    = jsRegexp(`^#\s*\w+\s*=`, "")
	reHeredoc      = jsRegexp(`<<-?\s*["']?([A-Za-z_][\w]*)["']?`, "")
	reInstruction  = jsRegexp(`^\s*([A-Za-z]+)\s*(.*)$`, "s")
	reFromFlag     = jsRegexp(`--\S+\s+`, "g")
	reWhitespace   = jsRegexp(`\s+`, "")
)

func parseDockerfile(text string) (parsed, error) {
	if unitLen(text) > maxDockerfileBytes {
		return parsed{}, fmt.Errorf("Dockerfile exceeds %d characters", maxDockerfileBytes)
	}
	lines := reLineBreak.Split(text, -1)
	escape := `\`
	var instructions []instruction
	var stages []stage
	stageIndex := -1
	i := 0
	// Parser directives appear before the first instruction or comment.
	for i < len(lines) {
		if m := reEscape.FindStringSubmatch(lines[i]); m != nil {
			escape = m[1]
			i++
			continue
		}
		if reCommentStart.MatchString(lines[i]) && reDirective.MatchString(lines[i]) {
			i++
			continue
		}
		break
	}
	for i < len(lines) {
		raw := lines[i]
		if t := jsTrim(raw); t == "" || strings.HasPrefix(t, "#") {
			i++
			continue
		}
		start := i
		logical := raw
		for strings.HasSuffix(jsTrimEnd(logical), escape) && i+1 < len(lines) {
			logical = jsTrimEnd(strings.TrimSuffix(jsTrimEnd(logical), escape))
			i++
			next := lines[i]
			if strings.HasPrefix(jsTrim(next), "#") {
				logical += escape
				continue
			}
			logical += " " + jsTrimStart(next)
		}
		endLine := i
		if m := reHeredoc.FindStringSubmatch(logical); m != nil {
			terminator := m[1]
			j := i + 1
			for j < len(lines) && jsTrim(lines[j]) != terminator {
				j++
			}
			if j < len(lines) {
				logical += "\n" + strings.Join(lines[i+1:j+1], "\n")
				endLine = j
				i = j
			}
		}
		i++
		m := reInstruction.FindStringSubmatch(logical)
		if m == nil {
			continue
		}
		keyword := strings.ToUpper(m[1])
		args := jsTrim(m[2])
		if keyword == "FROM" {
			stageIndex++
			parts := reWhitespace.Split(reFromFlag.ReplaceAllLiteralString(args, ""), -1)
			asIdx := -1
			for k, p := range parts {
				if jsUpper(p) == "AS" {
					asIdx = k
					break
				}
			}
			st := stage{index: stageIndex, base: parts[0], line: start + 1}
			if asIdx > 0 && asIdx+1 < len(parts) && parts[asIdx+1] != "" {
				name := parts[asIdx+1]
				st.name = &name
			}
			stages = append(stages, st)
		}
		instructions = append(instructions, instruction{line: start + 1, endLine: endLine + 1, keyword: keyword, args: args, stage: stageIndex})
	}
	return parsed{instructions: instructions, stages: stages, escape: escape}, nil
}

func splitArgs(args string) []string {
	var out []string
	for _, p := range reWhitespace.Split(jsTrim(reArgFlag.ReplaceAllLiteralString(args, "")), -1) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

type envPair struct {
	key      string
	value    string
	hasValue bool
}

func envPairs(args string) []envPair {
	trimmed := jsTrim(args)
	// Legacy ENV KEY value form.
	if words := reWhitespace.Split(trimmed, -1); len(words) >= 2 && !strings.Contains(trimmed, "=") {
		return []envPair{{key: words[0], value: strings.Join(words[1:], " "), hasValue: true}}
	}
	var pairs []envPair
	for _, m := range reEnvPair.FindAllStringSubmatchIndex(trimmed, -1) {
		p := envPair{key: trimmed[m[2]:m[3]]}
		if m[4] >= 0 {
			p.value, p.hasValue = trimmed[m[4]:m[5]], true
		}
		pairs = append(pairs, p)
	}
	return pairs
}
