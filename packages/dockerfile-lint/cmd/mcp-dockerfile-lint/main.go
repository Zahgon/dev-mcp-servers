// Command mcp-dockerfile-lint is the dockerfile-lint MCP server on stdio: it parses and
// lints Dockerfiles without running Docker.
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "dockerfile-lint"
	serverVersion = "0.1.0"
)

func newServer() *mcp.Server {
	return &mcp.Server{
		Name:    serverName,
		Version: serverVersion,
		Instructions: "Lints Dockerfiles without running Docker. lint_dockerfile returns findings with rule ids, line numbers and fixes; parse_dockerfile returns the instructions and build stages; " +
			"explain_rule documents one rule. Rules: " + strings.Join(ruleNames, ", ") + ". Findings are static observations about the file; they do not scan the image or its packages.",
		Tools: tools(),
	}
}

func main() { mcp.Main(newServer()) }

// source is the Dockerfile text in unit form and where it came from.
type source struct {
	text, origin string
}

func sourceArgs(a mcp.Args) (path, content *string, err error) {
	if path, err = a.String("path", 1, 4096); err != nil {
		return nil, nil, err
	}
	if content, err = a.String("content", 1, maxDockerfileBytes); err != nil {
		return nil, nil, err
	}
	return path, content, nil
}

// load reads the Dockerfile; its errors are worded in unit form like the text.
func load(path, content *string) (source, error) {
	if (path != nil) == (content != nil) {
		return source{}, errors.New("supply exactly one of path or content")
	}
	if content != nil {
		return source{text: toUnits(*content), origin: "inline"}, nil
	}
	abs := resolvePath(*path)
	fail := func(format string, args ...any) (source, error) {
		return source{}, errors.New(toUnits(fmt.Sprintf(format, args...)))
	}
	st, err := statFile(abs)
	if err != nil {
		return fail("cannot read %s: %s", abs, err.Error())
	}
	if !st.Mode().IsRegular() {
		return fail("%s is not a file", abs)
	}
	if st.Size() > maxDockerfileBytes {
		return fail("%s is %d bytes; the limit is %d", abs, st.Size(), maxDockerfileBytes)
	}
	text, err := readUTF8(abs)
	if err != nil {
		return fail("%s", err.Error())
	}
	return source{text: toUnits(text), origin: toUnits(abs)}, nil
}

// reply converts a unit-form result back to JavaScript strings.
func reply(v any) mcp.Result { return mcp.OK(deepFromUnits(v)) }

func failure(err error) mcp.Result { return mcp.Fail(fromUnits(err.Error())) }

func stageValue(s stage) *jsv.Obj {
	var name any
	if s.name != nil {
		name = *s.name
	}
	return jsv.O("index", float64(s.index), "name", name, "base", s.base, "line", float64(s.line))
}

func lintTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "lint_dockerfile", Obj: args}
	path, content, err := sourceArgs(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	ignore := map[string]bool{}
	items, _, err := a.Array("ignore", 0, 50)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	for _, item := range items {
		name, isString := item.(string)
		if _, known := rules[name]; !isString || !known {
			return mcp.Fail(mcp.Invalid(a.Tool, "ignore: Invalid enum value").Error())
		}
		ignore[name] = true
	}
	src, err := load(path, content)
	if err != nil {
		return failure(err)
	}
	p, err := parseDockerfile(src.text)
	if err != nil {
		return failure(err)
	}
	findings := lint(p, ignore)
	counts := map[string]int{}
	list := []any{}
	for _, f := range findings {
		counts[f.severity]++
		list = append(list, jsv.O("rule", f.rule, "severity", f.severity, "line", float64(f.line), "message", f.message, "fix", rules[f.rule].fix))
	}
	stages := []any{}
	for _, s := range p.stages {
		stages = append(stages, stageValue(s))
	}
	return reply(jsv.O(
		"source", src.origin,
		"instructions", float64(len(p.instructions)),
		"stages", stages,
		"error", float64(counts["error"]),
		"warning", float64(counts["warning"]),
		"info", float64(counts["info"]),
		"findings", list,
	))
}

func parseTool(args *jsv.Obj) mcp.Result {
	path, content, err := sourceArgs(mcp.Args{Tool: "parse_dockerfile", Obj: args})
	if err != nil {
		return mcp.Fail(err.Error())
	}
	src, err := load(path, content)
	if err != nil {
		return failure(err)
	}
	p, err := parseDockerfile(src.text)
	if err != nil {
		return failure(err)
	}
	stages := []any{}
	for _, s := range p.stages {
		stages = append(stages, stageValue(s))
	}
	instructions := []any{}
	for _, x := range p.instructions {
		instructions = append(instructions, jsv.O("line", float64(x.line), "end_line", float64(x.endLine), "instruction", x.keyword, "args", x.args, "stage", float64(x.stage)))
	}
	return reply(jsv.O("source", src.origin, "escape", p.escape, "stages", stages, "instructions", instructions))
}

func explainTool(args *jsv.Obj) mcp.Result {
	raw, present := args.Get("rule")
	name, isString := raw.(string)
	doc, known := rules[name]
	if !present || !isString || !known {
		return mcp.Fail(mcp.Invalid("explain_rule", "rule: Invalid enum value").Error())
	}
	return mcp.OK(jsv.O("rule", name, "severity", doc.severity, "description", doc.description, "fix", doc.fix))
}

func tools() []mcp.Tool {
	sourceProps := func() *jsv.Obj {
		return jsv.O(
			"path", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(4096), "description", fmt.Sprintf("Path to a local Dockerfile (up to %d bytes).", maxDockerfileBytes)),
			"content", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(maxDockerfileBytes), "description", "Dockerfile text. Alternative to path."),
		)
	}
	object := func(props *jsv.Obj, required []any) *jsv.Obj {
		o := jsv.O("type", "object", "properties", props)
		if required != nil {
			o.Set("required", required)
		}
		o.Set("additionalProperties", false)
		o.Set("$schema", "http://json-schema.org/draft-07/schema#")
		return o
	}
	annotations := func() *jsv.Obj {
		return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false)
	}
	lintProps := sourceProps()
	lintProps.Set("ignore", jsv.O("type", "array", "items", jsv.O("type", "string", "enum", jsv.Strings(ruleNames)), "maxItems", float64(50), "description", "Rule ids to skip."))
	ruleList := strings.Join(ruleNames, ", ")
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, schema *jsv.Obj) mcp.Tool {
		return mcp.Tool{Name: name, Handler: handler, Spec: jsv.O("title", title, "description", description, "inputSchema", schema,
			"annotations", annotations(), "execution", jsv.O("taskSupport", "forbidden"))}
	}
	return []mcp.Tool{
		tool("lint_dockerfile", lintTool, "Lint a Dockerfile",
			"Parse a Dockerfile (comments, escape directive, line continuations, heredocs, multi-stage builds) and return findings sorted by severity with line numbers and fixes. "+
				"Checks: final stage running as root, base images without a tag or with :latest, images without a digest, credential-like names in ENV or ARG, credential literals in RUN, "+
				"missing HEALTHCHECK, apt-get install without list cleanup or --no-install-recommends, apt-get upgrade, apk add without --no-cache, pip without --no-cache-dir, ADD for plain files "+
				"or remote URLs, curl piped to a shell, sudo, relative WORKDIR, repeated CMD or ENTRYPOINT, shell-form CMD, EXPOSE 22, MAINTAINER, copying the whole build context, "+
				"and COPY after USER without --chown. Pass ignore to silence rule ids.",
			object(lintProps, nil)),
		tool("parse_dockerfile", parseTool, "Parse a Dockerfile",
			"Return the Dockerfile's instructions in order (instruction keyword, arguments with continuations joined, start and end line, stage index) and its build stages "+
				"(index, AS name, base image, line), plus the escape character in effect.",
			object(sourceProps(), nil)),
		tool("explain_rule", explainTool, "Explain a lint rule",
			"Describe one lint rule: its default severity, what it detects and how to fix it. Rules: "+ruleList+".",
			object(jsv.O("rule", jsv.O("type", "string", "enum", jsv.Strings(ruleNames))), []any{"rule"})),
	}
}
