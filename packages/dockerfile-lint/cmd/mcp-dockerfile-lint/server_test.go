package main

// The upstream suite test/server.test.mjs, ported case by case. Upstream cases that call
// lint() and parseDockerfile() directly send the same text through the tools here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"testing"
)

func findRule(fs []reportedFinding, rule string) *reportedFinding {
	for i := range fs {
		if fs[i].Rule == rule {
			return &fs[i]
		}
	}
	return nil
}

func ruleSet(fs []reportedFinding) map[string]bool {
	out := map[string]bool{}
	for _, f := range fs {
		out[f.Rule] = true
	}
	return out
}

func writeFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("os.WriteFile(path, []byte(text), 0o644): %v", err)
	}
	return path
}

func TestListsTheThreeTools(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != "dockerfile-lint" {
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
	if want := []string{"explain_rule", "lint_dockerfile", "parse_dockerfile"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestLintDockerfileFindsEveryPlantedProblemAndStaysQuietOnAGoodFile(t *testing.T) {
	c := connected(t)
	r := decodeResult[lintResult](t, c.call("lint_dockerfile", map[string]any{"content": badDockerfile}))
	rules := ruleSet(r.Findings)
	for _, expected := range []string{"latest-tag", "maintainer-deprecated", "secret-in-env", "secret-in-arg", "apt-no-cleanup", "apt-no-recommends", "curl-pipe-shell", "sudo", "apk-no-cache", "pip-no-cache", "add-for-copy", "add-remote", "workdir-relative", "copy-whole-context", "expose-ssh", "shell-form-cmd", "multiple-cmd", "root-user", "no-healthcheck"} {
		if !rules[expected] {
			t.Errorf("lint_dockerfile of badDockerfile: missing finding %s", expected)
		}
	}
	if rules["secret-literal"] {
		t.Errorf("lint_dockerfile of badDockerfile: secret-literal reported, want no such finding")
	}
	if f := findRule(r.Findings, "curl-pipe-shell"); f == nil || f.Line != 6 {
		t.Errorf("continuation lines report the first line: %+v", f)
	}
	if f := findRule(r.Findings, "add-for-copy"); f == nil || f.Line != 10 {
		t.Errorf("add-for-copy line: %+v", f)
	}
	for _, f := range r.Findings {
		if f.Rule == "add-for-copy" && f.Line == 12 {
			t.Errorf("lint_dockerfile of badDockerfile: add-for-copy on line 12, want none (a tar archive is a legitimate ADD): %+v", f)
		}
		if len(f.Fix) <= 10 {
			t.Errorf("finding %s has no fix", f.Rule)
		}
	}
	if len(r.Findings) == 0 || r.Findings[0].Severity != "error" {
		t.Fatalf("first finding is not an error: %+v", r.Findings)
	}

	dir := t.TempDir()
	file := writeFile(t, dir, "Dockerfile", goodDockerfile)
	good := decodeResult[lintResult](t, c.call("lint_dockerfile", map[string]any{"path": file}))
	if good.Source != file {
		t.Errorf("source %q, want %q", good.Source, file)
	}
	if len(good.Stages) != 2 || good.Stages[0].Name == nil || *good.Stages[0].Name != "build" {
		t.Errorf("stages %+v", good.Stages)
	}
	if good.Findings == nil || len(good.Findings) != 0 {
		t.Errorf("good file findings: %+v", good.Findings)
	}

	ignored := decodeResult[lintResult](t, c.call("lint_dockerfile", map[string]any{"content": badDockerfile, "ignore": []string{"latest-tag", "no-healthcheck"}}))
	for _, f := range ignored.Findings {
		if f.Rule == "latest-tag" || f.Rule == "no-healthcheck" {
			t.Errorf("ignored rule reported: %s", f.Rule)
		}
	}
	if both := c.call("lint_dockerfile", map[string]any{"content": badDockerfile, "path": file}); !both.IsError {
		t.Errorf("path and content together: %s", both.Text)
	}
	if missing := c.call("lint_dockerfile", map[string]any{"path": filepath.Join(dir, "nope")}); !missing.IsError {
		t.Errorf("missing file: %s", missing.Text)
	}
}

func TestRulesHandleDigestsScratchBuildArgBasesRootUSERChownAndSecretLiterals(t *testing.T) {
	c := connected(t)
	lintText := func(text string) []reportedFinding {
		t.Helper()
		args := map[string]any{"content": text}
		if text == "" {
			args = map[string]any{"path": writeFile(t, t.TempDir(), "Dockerfile", "")}
		}
		return decodeResult[lintResult](t, c.call("lint_dockerfile", args)).Findings
	}
	expect := func(text, rule string, want bool, what string) {
		t.Helper()
		if got := ruleSet(lintText(text))[rule]; got != want {
			t.Errorf("%s: lint_dockerfile %q reports %s = %v, want %v", what, text, rule, got, want)
		}
	}
	expect("FROM alpine:3.20\nUSER nobody\nHEALTHCHECK NONE\n", "unpinned-digest", true, "tag without digest")
	expect("FROM alpine:latest\n", "latest-tag", true, ":latest")
	expect("FROM ghcr.io/org/image\n", "latest-tag", true, "registry image without tag")
	expect("FROM ghcr.io/org/image:1.2\n", "latest-tag", false, "registry image with tag")
	if fs := lintText("FROM scratch\nCOPY --chown=1000:1000 app /app\nUSER 1000\nENTRYPOINT [\"/app\"]\n"); len(fs) != 0 {
		t.Errorf("scratch image findings: %+v", fs)
	}
	expect("ARG BASE=alpine:3.20\nFROM $BASE\nUSER nobody\nHEALTHCHECK NONE\n", "latest-tag", false, "declared build-arg base")
	expect("FROM ${UNDECLARED}\n", "latest-tag", true, "undeclared build-arg base")
	if f := findRule(lintText("FROM alpine:3.20@sha256:aa\nUSER nobody\nUSER root\nHEALTHCHECK NONE\n"), "root-user"); f == nil || f.Line != 3 {
		t.Errorf("root-user line: %+v", f)
	}
	expect("FROM alpine:3.20@sha256:aa\nUSER nobody\nCOPY app /app\nHEALTHCHECK NONE\n", "copy-chown-missing", true, "COPY after USER without --chown")
	expect("FROM alpine:3.20@sha256:aa\nRUN echo AKIAIOSFODNN7EXAMPLE > /k\nUSER nobody\nHEALTHCHECK NONE\n", "secret-literal", true, "AWS key literal")
	expect("FROM alpine:3.20@sha256:aa\nENV GITHUB_TOKEN=$TOKEN\nUSER nobody\nHEALTHCHECK NONE\n", "secret-in-env", false, "variable references are not literals")
	expect("FROM alpine:3.20@sha256:aa\nENV SECRET_KEY value\nUSER nobody\nHEALTHCHECK NONE\n", "secret-in-env", true, "legacy ENV form")
	expect("", "empty-dockerfile", true, "empty Dockerfile")
	expect("RUN echo hi\n", "from-missing", true, "no FROM")
	expect("FROM debian:12@sha256:aa\nRUN --mount=type=cache,target=/var/lib/apt apt-get install -y --no-install-recommends git\nUSER nobody\nHEALTHCHECK NONE\n", "apt-no-cleanup", false, "apt cache mount")
}

func TestParseDockerfileHandlesDirectivesContinuationsCommentsInsideContinuationsAndHeredocs(t *testing.T) {
	c := connected(t)
	text := "# escape=`\nFROM alpine:3.20 AS base\nRUN apk add --no-cache `\n    # a comment in the middle\n    git\nRUN <<EOF\necho one\necho two\nEOF\nCOPY a b\n"
	r := decodeResult[parseResult](t, c.call("parse_dockerfile", map[string]any{"content": text}))
	if r.Escape != "`" {
		t.Errorf("escape %q", r.Escape)
	}
	if len(r.Stages) != 1 || r.Stages[0].Index != 0 || r.Stages[0].Name == nil || *r.Stages[0].Name != "base" || r.Stages[0].Base != "alpine:3.20" || r.Stages[0].Line != 2 {
		t.Errorf("stages %+v", r.Stages)
	}
	var keywords []string
	for _, ins := range r.Instructions {
		keywords = append(keywords, ins.Instruction)
	}
	if want := []string{"FROM", "RUN", "RUN", "COPY"}; !slices.Equal(keywords, want) {
		t.Fatalf("instructions %v, want %v", keywords, want)
	}
	if r.Instructions[1].Args != "apk add --no-cache git" {
		t.Errorf("continued args %q", r.Instructions[1].Args)
	}
	if r.Instructions[1].Line != 3 || r.Instructions[1].EndLine != 5 {
		t.Errorf("continuation lines %d-%d", r.Instructions[1].Line, r.Instructions[1].EndLine)
	}
	if r.Instructions[2].Line != 6 || r.Instructions[2].EndLine != 9 {
		t.Errorf("heredoc lines %d-%d", r.Instructions[2].Line, r.Instructions[2].EndLine)
	}
	if !regexp.MustCompile(`echo two`).MatchString(r.Instructions[2].Args) {
		t.Errorf("heredoc args %q", r.Instructions[2].Args)
	}
	if r.Instructions[3].Line != 10 {
		t.Errorf("COPY line %d", r.Instructions[3].Line)
	}
	platform := decodeResult[parseResult](t, c.call("parse_dockerfile", map[string]any{"content": "FROM --platform=linux/amd64 node:22-slim AS deps\n"}))
	if len(platform.Stages) != 1 || platform.Stages[0].Base != "node:22-slim" || platform.Stages[0].Name == nil || *platform.Stages[0].Name != "deps" {
		t.Errorf("platform stage %+v", platform.Stages)
	}
	if bad := c.call("parse_dockerfile", map[string]any{}); !bad.IsError {
		t.Errorf("no source: %s", bad.Text)
	}
}

func TestExplainRuleDocumentsEveryRule(t *testing.T) {
	c := connected(t)
	for _, rule := range ruleNames {
		r := decodeResult[struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
		}](t, c.call("explain_rule", map[string]any{"rule": rule}))
		if r.Rule != rule || !slices.Contains([]string{"error", "warning", "info"}, r.Severity) {
			t.Errorf("explain_rule %s: %+v", rule, r)
		}
	}
	if bad := c.call("explain_rule", map[string]any{"rule": "nope"}); !bad.IsError {
		t.Errorf("unknown rule: %s", bad.Text)
	}
}
