package main

// The upstream before() fixture tree and the shapes of the tool results the tests decode.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixtureDir builds the upstream before() directory tree.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"README.md":                   "# Demo Project\n\nA **small** project to [test](x) things.\nSecond line of the paragraph.\n\n## Install\n",
		"guide/01-getting-started.md": "---\ntitle: \"Getting started\"\ndescription: 'Install and run in two minutes.'\n---\n\n# Ignored heading\n\nBody.\n",
		"guide/faq.mdx":               "import X from 'y'\n\n```js\nconst a = 1\n```\n\nQuestions people ask.\n",
		"api/v2/index.md":             "# API v2\n",
		"node_modules/pkg/README.md":  "# Not included\n",
		".hidden/secret.md":           "# Not included\n",
		"notes.txt":                   "not markdown",
		"sitemap.xml":                 `<?xml version="1.0"?><urlset><url><loc>https://ex.com/</loc><lastmod>2026-01-01</lastmod></url><url><loc>https://ex.com/docs/getting-started</loc></url><url><loc>https://ex.com/docs/api/auth.html</loc></url><url><loc>https://ex.com/blog/hello-world</loc></url><url><loc>https://ex.com/legal/terms&amp;conditions</loc></url></urlset>`,
		"index.xml":                   "<sitemapindex><sitemap><loc>https://ex.com/sitemap-1.xml</loc></sitemap></sitemapindex>",
	}
	for rel, text := range files {
		writeFile(t, dir, rel, text)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, text string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(filepath.Dir(path), 0o755): %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("os.WriteFile(path, []byte(text), 0o644): %v", err)
	}
	return path
}

type entryResult struct {
	File        string `json:"file"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Section     string `json:"section"`
}

type generateResult struct {
	FilesScanned  int               `json:"files_scanned"`
	Entries       int               `json:"entries"`
	Skipped       []json.RawMessage `json:"skipped"`
	Content       string            `json:"content"`
	EntriesDetail []entryResult     `json:"entries_detail"`
}

type sitemapResult struct {
	URLsInSitemap int    `json:"urls_in_sitemap"`
	Entries       int    `json:"entries"`
	Content       string `json:"content"`
}

type lineMessage struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type checkResult struct {
	Valid    bool          `json:"valid"`
	Errors   []lineMessage `json:"errors"`
	Warnings []lineMessage `json:"warnings"`
	Info     []lineMessage `json:"info"`
	Stats    struct {
		Links    int      `json:"links"`
		Sections []string `json:"sections"`
	} `json:"stats"`
}
