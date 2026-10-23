package main

// scan_lockfile and the tool catalogue.

import (
	"fmt"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

// optString is a JavaScript string that may be undefined.
type optString struct {
	value string
	set   bool
}

func optional(s *string) optString {
	if s == nil {
		return optString{}
	}
	return optString{*s, true}
}

// readLockfile reads the lockfile at path; the failure is the tool's error text.
func readLockfile(path string) (text, abs string, failure *mcp.Result) {
	abs = resolvePath(path)
	fail := func(msg string) (string, string, *mcp.Result) {
		r := mcp.Fail(msg)
		return "", abs, &r
	}
	st, err := statFile(abs)
	if err != nil {
		return fail(fmt.Sprintf("Cannot read %s: %s", abs, err.Error()))
	}
	if !st.Mode().IsRegular() {
		return fail(abs + " is not a file.")
	}
	if st.Size() > maxLockfileBytes {
		return fail(fmt.Sprintf("%s is %d bytes; the limit is %d.", abs, st.Size(), maxLockfileBytes))
	}
	text, err = readUTF8(abs)
	if err != nil {
		return fail(fmt.Sprintf("Cannot read %s: %s", abs, err.Error()))
	}
	return text, abs, nil
}

func (h handlers) scanLockfile(raw *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "scan_lockfile", Obj: raw}
	path, err1 := a.String("path", 1, 4096)
	content, err2 := a.String("content", 1, maxLockfileBytes)
	filename, err3 := a.String("filename", 0, 255)
	format, err4 := a.Enum("format", lockfileFormats)
	details, err5 := a.Bool("include_details")
	if err := firstError(err1, err2, err3, err4, err5); err != nil {
		return mcp.Fail(err.Error())
	}
	if (path != nil) == (content != nil) {
		return mcp.Fail("Supply exactly one of path or content.")
	}
	name := optional(filename)
	var text string
	if content != nil {
		text = *content
	} else {
		var abs string
		var failure *mcp.Result
		if text, abs, failure = readLockfile(*path); failure != nil {
			return *failure
		}
		if !name.set {
			name = optString{abs, true}
		}
	}
	text = toUnits(text)
	fmtName := ""
	if format != nil {
		fmtName = *format
	} else if fmtName = detectFormat(optString{toUnits(name.value), name.set}, text); fmtName == "" {
		return mcp.Fail("Could not detect the lockfile format. Pass format as one of " + strings.Join(lockfileFormats, ", ") + ".")
	}
	parsed, err := parseLockfile(text, fmtName)
	if err != nil {
		return mcp.Fail(fromUnits(err.Error()))
	}
	skipped := []any{}
	for _, s := range parsed.skipped {
		skipped = append(skipped, jsv.O("entry", s.entry, "reason", s.reason))
	}
	if len(parsed.packages) == 0 {
		return reply(jsv.O("format", fmtName, "packages", float64(0), "skipped", skipped, "affected", []any{}, "note", "No exactly pinned packages found."))
	}
	if len(parsed.packages) > maxBatchQueries {
		return mcp.Fail(fmt.Sprintf("%d packages found; the limit is %d per scan.", len(parsed.packages), maxBatchQueries))
	}
	out := jsv.O("format", fmtName)
	if err := h.scanResults(out, parsed, skipped, details != nil && *details); err != nil {
		return mcp.Fail(err.Error())
	}
	return reply(out)
}

// scanResults queries OSV for the parsed packages and adds the counts, the affected
// packages, the skipped entries and, with details, the first 20 advisories to out.
func (h handlers) scanResults(out *jsv.Obj, parsed parsedLockfile, skipped []any, withDetails bool) error {
	batch := make([]batchQuery, len(parsed.packages))
	for i, p := range parsed.packages {
		batch[i] = batchQuery{ecosystem: p.ecosystem, name: p.name, version: p.version}
	}
	results, err := h.client.queryBatch(batch)
	if err != nil {
		return err
	}
	affected := []any{}
	ids := &valueSet{}
	for i, p := range parsed.packages {
		if len(results[i]) == 0 {
			continue
		}
		row := p.value()
		row.Set("vulnerability_ids", results[i])
		affected = append(affected, row)
		for _, id := range results[i] {
			ids.add(id)
		}
	}
	out.Set("packages", float64(len(parsed.packages)))
	out.Set("affected_packages", float64(len(affected)))
	out.Set("distinct_advisories", float64(len(ids.items)))
	out.Set("affected", affected)
	out.Set("skipped", skipped)
	if withDetails {
		list := []any{}
		for _, id := range ids.items[:min(20, len(ids.items))] {
			v, err := h.client.getVulnerability(id)
			if err != nil {
				return err
			}
			if truthy(v, true) {
				s, err := summarise(v)
				if err != nil {
					return err
				}
				list = append(list, s)
			}
		}
		out.Set("details", list)
		out.Set("details_truncated", len(ids.items) > 20)
	}
	return nil
}

func (h handlers) tools() []mcp.Tool {
	ecosystemDoc := "OSV ecosystem name, case-insensitive. One of: " + strings.Join(ecosystems, ", ") + "."
	packageProps := func() *jsv.Obj {
		return jsv.O(
			"ecosystem", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(40), "description", ecosystemDoc),
			"name", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(300), "description", "Package name as the ecosystem spells it (npm scope included, Go module path, PyPI project name)."),
			"version", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(100), "description", "Exact installed version. For Go, with or without the leading v."),
		)
	}
	object := func(props *jsv.Obj, required []any, top bool) *jsv.Obj {
		o := jsv.O("type", "object", "properties", props)
		if required != nil {
			o.Set("required", required)
		}
		o.Set("additionalProperties", false)
		if top {
			o.Set("$schema", "http://json-schema.org/draft-07/schema#")
		}
		return o
	}
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, schema *jsv.Obj) mcp.Tool {
		return mcp.Tool{Name: name, Handler: handler, Spec: jsv.O("title", title, "description", description, "inputSchema", schema,
			"annotations", jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", true),
			"execution", jsv.O("taskSupport", "forbidden"))}
	}
	batchProps := jsv.O("packages", jsv.O("type", "array", "items", object(packageProps(), []any{"ecosystem", "name"}, false),
		"minItems", float64(1), "maxItems", float64(maxBatchQueries)))
	scanProps := jsv.O(
		"path", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(4096), "description", "Path to the lockfile on this machine."),
		"content", jsv.O("type", "string", "minLength", float64(1), "maxLength", float64(maxLockfileBytes), "description", "Lockfile text, as an alternative to path."),
		"filename", jsv.O("type", "string", "maxLength", float64(255), "description", "Original file name, used for format detection when content is supplied."),
		"format", jsv.O("type", "string", "enum", jsv.Strings(lockfileFormats)),
		"include_details", jsv.O("type", "boolean", "description", "Also fetch the first 20 advisories in full. Default false."),
	)
	idProps := jsv.O("id", jsv.O("type", "string", "minLength", float64(3), "maxLength", float64(100), "pattern", "^[A-Za-z0-9][A-Za-z0-9.:_-]*$"))
	return []mcp.Tool{
		tool("query_package", h.queryPackage, "Query OSV for one package",
			"Return the OSV advisories affecting one package at one version (or every known advisory for the package when version is omitted). "+
				"Each advisory carries its id, aliases such as CVE ids, summary, severity, affected ranges, fixed versions and reference URLs. "+
				"Calls POST https://api.osv.dev/v1/query.",
			object(packageProps(), []any{"ecosystem", "name"}, true)),
		tool("query_batch", h.queryBatch, "Query OSV for many packages",
			fmt.Sprintf("Return, for each package in the list (up to %d), the ids of OSV advisories that affect it. Ids only; call get_vulnerability for details. ", maxBatchQueries)+
				"Calls POST https://api.osv.dev/v1/querybatch in chunks of 100.",
			object(batchProps, []any{"packages"}, true)),
		tool("scan_lockfile", h.scanLockfile, "Scan a lockfile against OSV",
			"Parse a lockfile and batch-query OSV for every exactly pinned dependency in it. Supported formats: package-lock.json (lockfileVersion 1 to 3), "+
				"requirements.txt (only == pins are queried; other specifiers are listed as skipped), poetry.lock and go.sum (basic: every module line, leading v removed). "+
				fmt.Sprintf("Supply either path (a local file up to %d bytes) or content (the file text). Format is detected from the file name and content unless given. ", maxLockfileBytes)+
				"Returns the packages with advisories, the ids per package, the skipped entries and counts. With include_details, fetches up to 20 advisories in full.",
			object(scanProps, nil, true)),
		tool("get_vulnerability", h.getVulnerability, "Get one OSV advisory",
			"Fetch one advisory by OSV id (for example GHSA-xxxx-xxxx-xxxx, PYSEC-2023-1, GO-2024-1234, or a CVE id that OSV aliases). "+
				"Returns the summary plus the raw OSV record. Calls GET https://api.osv.dev/v1/vulns/{id}.",
			object(idProps, []any{"id"}, true)),
	}
}
