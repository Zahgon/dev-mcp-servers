// Command mcp-llms-txt is the llms-txt MCP server on stdio: it generates and checks
// llms.txt files from local Markdown and sitemaps.
package main

import (
	"errors"
	"fmt"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "llms-txt"
	serverVersion = "0.1.0"
	maxText       = 2 * 1024 * 1024
)

func newServer() *mcp.Server {
	return &mcp.Server{
		Name:    serverName,
		Version: serverVersion,
		Instructions: "Builds and checks llms.txt files (https://llmstxt.org): an H1 title, a blockquote summary, optional paragraphs, then H2 sections of '- [name](url): notes' links, " +
			"with an Optional section for secondary material. generate_llms_txt scans a local directory of Markdown; generate_from_sitemap reads a local sitemap.xml; " +
			"check_llms_txt validates an existing file. Tools return the text; they never write files or fetch URLs.",
		Tools: tools(),
	}
}

func main() { mcp.Main(newServer()) }

func reply(v any) mcp.Result { return mcp.OK(deepFromUnits(v)) }

func failure(err error) mcp.Result { return mcp.Fail(fromUnits(err.Error())) }

// readBounded reads a local text file of at most maxText bytes; errors are in unit form.
func readBounded(path string) (string, error) {
	abs := resolvePath(path)
	st, err := statFile(abs)
	if err != nil {
		return "", errors.New(toUnits(fmt.Sprintf("cannot read %s: %s", abs, err.Error())))
	}
	if !st.Mode().IsRegular() {
		return "", errors.New(toUnits(abs + " is not a file"))
	}
	if st.Size() > maxText {
		return "", errors.New(toUnits(fmt.Sprintf("%s is %d bytes; the limit is %d", abs, st.Size(), maxText)))
	}
	text, err := readUTF8(abs)
	if err != nil {
		return "", errors.New(toUnits(err.Error()))
	}
	return toUnits(text), nil
}

func generateTool(raw *jsv.Obj) mcp.Result {
	r := &args{a: mcp.Args{Tool: "generate_llms_txt", Obj: raw}}
	directory := r.required("directory", 1, 4096)
	title := r.required("title", 1, 200)
	description := r.str("description", 0, 500)
	details := r.str("details", 0, 4000)
	baseURL := r.str("base_url", 0, 500)
	style, err := r.a.Enum("url_style", []string{"strip-extension", "keep-extension"})
	if r.err == nil {
		r.err = err
	}
	maxFiles := r.integer("max_files", maxFiles, maxFiles)
	includeDirs := r.strings("include_dirs", 1, 200)
	optional := r.strings("optional_sections", 1, 200)
	if r.err != nil {
		return mcp.Fail(r.err.Error())
	}
	c, err := collectMarkdown(fromUnits(directory), collectOptions{
		maxFiles: maxFiles, baseURL: baseURL, keepExtension: style != nil && *style == "keep-extension", includeDirs: includeDirs,
	})
	if err != nil {
		return failure(err)
	}
	if len(c.entries) == 0 {
		return mcp.Fail(fmt.Sprintf("No Markdown files found under %s.", resolvePath(fromUnits(directory))))
	}
	content := render(c.entries, renderOptions{title: title, description: description, details: details, optionalSections: optional})
	skipped := []any{}
	for _, s := range c.skipped {
		skipped = append(skipped, jsv.O("file", s.file, "reason", s.reason))
	}
	detail := []any{}
	for _, e := range c.entries {
		detail = append(detail, e.value())
	}
	return reply(jsv.O("files_scanned", float64(c.scanned), "entries", float64(len(c.entries)), "skipped", skipped, "content", content, "entries_detail", detail))
}

func sitemapTool(raw *jsv.Obj) mcp.Result {
	r := &args{a: mcp.Args{Tool: "generate_from_sitemap", Obj: raw}}
	path := r.required("path", 1, 4096)
	title := r.required("title", 1, 200)
	description := r.str("description", 0, 500)
	details := r.str("details", 0, 4000)
	include := r.strings("include_prefix", 1, 500)
	exclude := r.strings("exclude_prefix", 1, 500)
	optional := r.strings("optional_sections", 1, 200)
	limit := r.integer("max_urls", 5000, 1000)
	if r.err != nil {
		return mcp.Fail(r.err.Error())
	}
	xml, err := readBounded(fromUnits(path))
	if err != nil {
		return failure(err)
	}
	urls, children := parseSitemap(xml)
	if len(urls) == 0 && len(children) == 0 {
		return mcp.Fail("No <url> or <sitemap> entries found; is this a sitemap.xml?")
	}
	entries := entriesFromSitemap(urls[:min(limit, len(urls))], include, exclude)
	if len(entries) == 0 {
		suffix := ""
		if len(children) > 0 {
			suffix = fmt.Sprintf("; %d child sitemap(s) were not read", len(children))
		}
		return mcp.Fail(fmt.Sprintf("The sitemap lists %d URL(s) but none passed the filters%s.", len(urls), suffix))
	}
	return reply(jsv.O(
		"urls_in_sitemap", float64(len(urls)),
		"truncated", len(urls) > limit,
		"entries", float64(len(entries)),
		"child_sitemaps", jsv.Strings(children),
		"content", render(entries, renderOptions{title: title, description: description, details: details, optionalSections: optional}),
	))
}

func checkTool(raw *jsv.Obj) mcp.Result {
	r := &args{a: mcp.Args{Tool: "check_llms_txt", Obj: raw}}
	path := r.str("path", 1, 4096)
	content := r.str("content", 1, maxText)
	if r.err != nil {
		return mcp.Fail(r.err.Error())
	}
	if path.set == content.set {
		return mcp.Fail("Supply exactly one of path or content.")
	}
	text := content.value
	if path.set {
		var err error
		if text, err = readBounded(fromUnits(path.value)); err != nil {
			return failure(err)
		}
	}
	return reply(check(text))
}

func tools() []mcp.Tool {
	str := func(min, max int, description string) *jsv.Obj {
		o := jsv.O("type", "string")
		if min > 0 {
			o.Set("minLength", float64(min))
		}
		o.Set("maxLength", float64(max))
		if description != "" {
			o.Set("description", description)
		}
		return o
	}
	list := func(min, max int, description string) *jsv.Obj {
		o := jsv.O("type", "array", "items", str(min, max, ""), "maxItems", float64(50))
		if description != "" {
			o.Set("description", description)
		}
		return o
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
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, schema *jsv.Obj) mcp.Tool {
		return mcp.Tool{Name: name, Handler: handler, Spec: jsv.O("title", title, "description", description, "inputSchema", schema,
			"annotations", jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false),
			"execution", jsv.O("taskSupport", "forbidden"))}
	}
	generateProps := jsv.O(
		"directory", str(1, 4096, ""),
		"title", str(1, 200, "H1 title, usually the project name."),
		"description", str(0, 500, "One-sentence summary for the blockquote."),
		"details", str(0, 4000, "Optional paragraph(s) placed after the summary."),
		"base_url", str(0, 500, "Prefix for links, for example https://docs.example.com. Default: root-relative paths."),
		"url_style", jsv.O("type", "string", "enum", jsv.Strings([]string{"strip-extension", "keep-extension"})),
		"max_files", jsv.O("type", "integer", "minimum", float64(1), "maximum", float64(maxFiles), "description", fmt.Sprintf("Default %d.", maxFiles)),
		"include_dirs", list(1, 200, `Only scan these top-level directories (use "." to keep root files).`),
		"optional_sections", list(1, 200, ""),
	)
	sitemapProps := jsv.O(
		"path", str(1, 4096, "Local path to sitemap.xml."),
		"title", str(1, 200, ""),
		"description", str(0, 500, ""),
		"details", str(0, 4000, ""),
		"include_prefix", list(1, 500, ""),
		"exclude_prefix", list(1, 500, ""),
		"optional_sections", list(1, 200, ""),
		"max_urls", jsv.O("type", "integer", "minimum", float64(1), "maximum", float64(5000), "description", "Default 1000."),
	)
	checkProps := jsv.O("path", str(1, 4096, ""), "content", str(1, maxText, ""))
	return []mcp.Tool{
		tool("generate_llms_txt", generateTool, "Generate llms.txt from Markdown",
			"Scan a local directory (recursively, skipping node_modules, .git, build output and hidden directories, up to max_files files) for .md, .mdx and .markdown files and return an llms.txt. "+
				"Each file's title comes from frontmatter, its first H1 or the file name; its notes from frontmatter description or the first paragraph (trimmed to 200 characters). "+
				"Files are grouped into sections by top-level folder (root files under Docs). URLs are base_url plus the relative path, with .md stripped and README/index mapped to the folder "+
				"unless url_style is keep-extension. Sections named in optional_sections are moved under Optional. Returns the text, the entries, and anything skipped.",
			object(generateProps, []any{"directory", "title"})),
		tool("generate_from_sitemap", sitemapTool, "Generate llms.txt from a local sitemap.xml",
			"Read a sitemap.xml from a local path (download it first; this tool does not fetch URLs) and return an llms.txt with one link per <url>, grouped into sections by the first path segment. "+
				"Titles are derived from the last path segment because page content is not fetched, so notes are empty; edit them afterwards. include_prefix and exclude_prefix filter by URL path. "+
				"Child sitemaps in a sitemap index are listed but not read.",
			object(sitemapProps, []any{"path", "title"})),
		tool("check_llms_txt", checkTool, "Check an llms.txt file",
			"Validate llms.txt text (from a local path or inline content) against the convention: exactly one H1 first, an optional blockquote summary, H2 sections containing only "+
				"'- [name](url): notes' items, no deeper headings, no duplicate URLs or sections, a modest size. Returns errors (convention violations), warnings, info (such as links without notes "+
				"or relative URLs) with line numbers, and statistics.",
			object(checkProps, nil)),
	}
}
