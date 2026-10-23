package main

// The upstream suite test/server.test.mjs, ported case by case. Upstream cases that call
// check(), extractMeta(), parseSitemap() and toUrl() directly send the same input through
// the tools here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

func mustMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("%q does not match %s", text, pattern)
	}
}

func TestListsTheThreeTools(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != "llms-txt" {
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
	if want := []string{"check_llms_txt", "generate_from_sitemap", "generate_llms_txt"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestGenerateLlmsTxtScansMarkdownExtractsTitlesAndNotesGroupsAndLinks(t *testing.T) {
	dir := fixtureDir(t)
	c := connected(t)
	r := decodeResult[generateResult](t, c.call("generate_llms_txt", map[string]any{"directory": dir, "title": "Demo", "description": "Docs for Demo.", "base_url": "https://docs.example.com/", "optional_sections": []string{"Api"}}))
	if r.Entries != 4 {
		t.Errorf("entries %d, want 4", r.Entries)
	}
	if r.Skipped == nil || len(r.Skipped) != 0 {
		t.Errorf("skipped %s", r.Skipped)
	}
	content := r.Content
	if !strings.HasPrefix(content, "# Demo\n\n> Docs for Demo.\n\n## Docs\n") {
		t.Errorf("content starts %q", content)
	}
	mustMatch(t, content, `- \[Demo Project\]\(https://docs\.example\.com/\): A small project to test things\. Second line of the paragraph\.`)
	mustMatch(t, content, `## Guide\n\n- \[Getting started\]\(https://docs\.example\.com/guide/01-getting-started/\): Install and run in two minutes\.\n- \[Faq\]\(https://docs\.example\.com/guide/faq/\): Questions people ask\.`)
	mustMatch(t, content, `## Optional\n\n- \[API v2\]\(https://docs\.example\.com/api/v2/\)\n$`)
	if strings.Contains(content, "Not included") {
		t.Errorf("skipped directories were scanned: %q", content)
	}
	if v := decodeResult[checkResult](t, c.call("check_llms_txt", map[string]any{"content": content})); !v.Valid {
		t.Errorf("generated file does not pass the check: %+v", v)
	}

	kept := decodeResult[generateResult](t, c.call("generate_llms_txt", map[string]any{"directory": dir, "title": "Demo", "url_style": "keep-extension", "include_dirs": []string{"guide"}}))
	if kept.Entries != 2 {
		t.Errorf("include_dirs entries %d, want 2", kept.Entries)
	}
	mustMatch(t, kept.Content, `\(/guide/faq\.mdx\)`)
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(filepath.Join(dir, \"empty\"), 0o755): %v", err)
	}
	if empty := c.call("generate_llms_txt", map[string]any{"directory": filepath.Join(dir, "empty"), "title": "x"}); !empty.IsError {
		t.Errorf("empty directory: %s", empty.Text)
	}
	if missing := c.call("generate_llms_txt", map[string]any{"directory": filepath.Join(dir, "nope"), "title": "x"}); !missing.IsError {
		t.Errorf("missing directory: %s", missing.Text)
	}
}

func TestGenerateFromSitemapGroupsURLsAndHonoursFiltersAndSitemapIndexes(t *testing.T) {
	dir := fixtureDir(t)
	c := connected(t)
	r := decodeResult[sitemapResult](t, c.call("generate_from_sitemap", map[string]any{"path": filepath.Join(dir, "sitemap.xml"), "title": "Ex", "description": "Site.", "exclude_prefix": []string{"/legal"}}))
	if r.URLsInSitemap != 5 || r.Entries != 4 {
		t.Errorf("urls_in_sitemap %d entries %d", r.URLsInSitemap, r.Entries)
	}
	mustMatch(t, r.Content, `## Blog\n\n- \[Hello World\]\(https://ex\.com/blog/hello-world\)`)
	mustMatch(t, r.Content, `## Docs\n\n- \[Getting Started\]\(https://ex\.com/docs/getting-started\)\n- \[Auth\]\(https://ex\.com/docs/api/auth\.html\)`)
	mustMatch(t, r.Content, `## Pages\n\n- \[Home\]\(https://ex\.com/\)`)
	only := decodeResult[sitemapResult](t, c.call("generate_from_sitemap", map[string]any{"path": filepath.Join(dir, "sitemap.xml"), "title": "Ex", "include_prefix": []string{"/docs/"}}))
	if only.Entries != 2 {
		t.Errorf("include_prefix entries %d, want 2", only.Entries)
	}
	idx := c.call("generate_from_sitemap", map[string]any{"path": filepath.Join(dir, "index.xml"), "title": "Ex"})
	if !idx.IsError {
		t.Errorf("sitemap index: %s", idx.Text)
	}
	mustMatch(t, idx.Text, `1 child sitemap`)
	if notXML := c.call("generate_from_sitemap", map[string]any{"path": filepath.Join(dir, "notes.txt"), "title": "Ex"}); !notXML.IsError {
		t.Errorf("not a sitemap: %s", notXML.Text)
	}
	loc := writeFile(t, dir, "loc.xml", "<urlset><url><loc> https://a/&amp;b </loc></url></urlset>")
	decoded := decodeResult[sitemapResult](t, c.call("generate_from_sitemap", map[string]any{"path": loc, "title": "Ex"}))
	if decoded.URLsInSitemap != 1 || !strings.Contains(decoded.Content, "(https://a/&b)\n") {
		t.Errorf("decoded and trimmed loc: %+v", decoded)
	}
}

func TestCheckLlmsTxtReportsConventionViolations(t *testing.T) {
	dir := fixtureDir(t)
	c := connected(t)
	good := decodeResult[checkResult](t, c.call("check_llms_txt", map[string]any{"content": "# Title\n\n> Summary.\n\nSome details.\n\n## Docs\n\n- [A](https://x/a): notes\n- [B](https://x/b)\n\n## Optional\n\n- [C](/c): more\n"}))
	if !good.Valid || good.Errors == nil || len(good.Errors) != 0 || good.Warnings == nil || len(good.Warnings) != 0 {
		t.Errorf("good file: %+v", good)
	}
	if good.Stats.Links != 3 || !slices.Equal(good.Stats.Sections, []string{"Docs", "Optional"}) {
		t.Errorf("stats %+v", good.Stats)
	}
	if !slices.ContainsFunc(good.Info, func(i lineMessage) bool { return strings.Contains(i.Message, "no notes") }) {
		t.Errorf("info %+v", good.Info)
	}

	bad := decodeResult[checkResult](t, c.call("check_llms_txt", map[string]any{"content": "Intro text\n# One\n# Two\n## Empty\n## Docs\n### Deep\n- plain bullet\n- [A](https://x/a): n\n- [A again](https://x/a)\nStray paragraph\n> late quote\n"}))
	if bad.Valid {
		t.Errorf("check_llms_txt of the malformed file: valid = true, want false: %+v", bad)
	}
	var msgs []string
	for _, m := range append(append([]lineMessage{}, bad.Errors...), bad.Warnings...) {
		msgs = append(msgs, m.Message)
	}
	joined := strings.Join(msgs, "\n")
	for _, pattern := range []string{`first line must be an H1`, `only one H1`, `"Empty" has no link items`, `H3 and deeper`, `must be markdown links`, `duplicate URL`, `paragraph text inside a section`, `blockquotes belong`, `no blockquote summary`} {
		mustMatch(t, joined, pattern)
	}

	file := writeFile(t, dir, "llms.txt", "# F\n\n> s\n\n## S\n\n- [x](https://x): y\n")
	if fromFile := decodeResult[checkResult](t, c.call("check_llms_txt", map[string]any{"path": file})); !fromFile.Valid {
		t.Errorf("file check: %+v", fromFile)
	}
	if neither := c.call("check_llms_txt", map[string]any{}); !neither.IsError {
		t.Errorf("no source: %s", neither.Text)
	}
	empty := writeFile(t, dir, "empty.txt", "")
	if r := decodeResult[checkResult](t, c.call("check_llms_txt", map[string]any{"path": empty})); r.Valid {
		t.Errorf("check_llms_txt of %s: valid = true, want false for an empty file", empty)
	}
}

// metaOf runs extractMeta on text through generate_llms_txt with one file named name.
func metaOf(t *testing.T, c *mcpClient, name, text string) entryResult {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, name, text)
	r := decodeResult[generateResult](t, c.call("generate_llms_txt", map[string]any{"directory": dir, "title": "T"}))
	if len(r.EntriesDetail) != 1 {
		t.Fatalf("entries %+v", r.EntriesDetail)
	}
	return r.EntriesDetail[0]
}

// urlOf runs toUrl on rel through generate_llms_txt.
func urlOf(t *testing.T, c *mcpClient, rel string, args map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, rel, "# x\n")
	args["directory"], args["title"] = dir, "T"
	r := decodeResult[generateResult](t, c.call("generate_llms_txt", args))
	if len(r.EntriesDetail) != 1 {
		t.Fatalf("entries %+v", r.EntriesDetail)
	}
	return r.EntriesDetail[0].URL
}

func TestMetaExtractionAndURLMapping(t *testing.T) {
	c := connected(t)
	if m := metaOf(t, c, "f.md", "---\ntitle: T\n---\n# H\n\nPara\n"); m.Title != "T" || m.Description != "Para" {
		t.Errorf("front matter title: %+v", m)
	}
	if m := metaOf(t, c, "f.md", "# H #\n\n- list\n\nPara one.\n"); m.Title != "H" || m.Description != "Para one." {
		t.Errorf("closed H1: %+v", m)
	}
	if m := metaOf(t, c, "my-page.md", "no heading\n"); m.Title != "My Page" {
		t.Errorf("file name title: %+v", m)
	}
	if m := metaOf(t, c, "f.md", "# T\n\n"+strings.Repeat("word ", 100)); len([]rune(m.Description)) > 200 {
		t.Errorf("description length %d", len([]rune(m.Description)))
	}
	if u := urlOf(t, c, "README.md", map[string]any{"base_url": "https://d.ex", "url_style": "strip-extension"}); u != "https://d.ex/" {
		t.Errorf("README url %q", u)
	}
	if u := urlOf(t, c, "a/index.md", map[string]any{"url_style": "strip-extension"}); u != "/a/" {
		t.Errorf("index url %q", u)
	}
	if u := urlOf(t, c, "a/b.md", map[string]any{"base_url": "https://d.ex/base/", "url_style": "keep-extension"}); u != "https://d.ex/base/a/b.md" {
		t.Errorf("kept extension url %q", u)
	}
}
