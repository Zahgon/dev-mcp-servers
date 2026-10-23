package main

// The upstream suite test/lockfiles.test.mjs. Upstream calls detectFormat() and
// parseLockfile() directly. Each case sends the same text through scan_lockfile, which
// shows the detected format and the skipped entries; a lockfile with pinned packages is
// then queried against api.osv.dev, so its result is either the real answer (with
// network) or the request failure (offline), and both are accepted. Against the
// in-process server the parser's exact output is checked as well.

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type scanResult struct {
	Format   string         `json:"format"`
	Packages int            `json:"packages"`
	Skipped  []skippedValue `json:"skipped"`
}

type skippedValue struct {
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

const networkFailure = "request to /v1/querybatch failed: "

// scan calls scan_lockfile; offline is true when the call failed because api.osv.dev
// could not be reached.
func scan(t *testing.T, c *mcpClient, args map[string]any) (r scanResult, offline bool) {
	t.Helper()
	reply := c.call("scan_lockfile", args)
	if reply.IsError {
		if !strings.HasPrefix(reply.Text, networkFailure) {
			t.Fatalf("scan_lockfile %v: %s", args, reply.Text)
		}
		return r, true
	}
	if err := json.Unmarshal([]byte(reply.Text), &r); err != nil {
		t.Fatalf("scan_lockfile result: %s", reply.Text)
	}
	return r, false
}

func scanError(t *testing.T, c *mcpClient, args map[string]any) string {
	t.Helper()
	reply := c.call("scan_lockfile", args)
	if !reply.IsError {
		t.Fatalf("scan_lockfile %v succeeded: %s", args, reply.Text)
	}
	return reply.Text
}

func inProcess() bool { return os.Getenv("MCP_SERVER_BIN") == "" }

func TestDetectsFormatsByNameAndByContent(t *testing.T) {
	c := connected(t)
	byName := []struct{ filename, content, want string }{
		{"package-lock.json", `{"packages":{}}`, "package-lock"},
		{"/x/poetry.lock", "x", "poetry"},
		{"go.sum", "x", "go-sum"},
		{"dev-requirements.txt", "x", "requirements"},
	}
	for _, tc := range byName {
		if r, _ := scan(t, c, map[string]any{"content": tc.content, "filename": tc.filename}); r.Format != tc.want {
			t.Errorf("%s: format %q, want %q", tc.filename, r.Format, tc.want)
		}
	}
	if msg := scanError(t, c, map[string]any{"content": `{"name":"a","lockfileVersion":2}`}); msg != "package-lock.json has neither a packages nor a dependencies map" {
		t.Errorf("lockfileVersion content: %s", msg)
	}
	if r, _ := scan(t, c, map[string]any{"content": "[[package]]\nname = \"a\""}); r.Format != "poetry" {
		t.Errorf("[[package]] content: format %q", r.Format)
	}
	for content, want := range map[string]string{"golang.org/x/text v0.3.0 h1:abc=\n": "go-sum", "requests==2.0\n": "requirements"} {
		if r, offline := scan(t, c, map[string]any{"content": content}); !offline && r.Format != want {
			t.Errorf("%q: format %q, want %q", content, r.Format, want)
		}
	}
	if msg := scanError(t, c, map[string]any{"content": "hello"}); !strings.HasPrefix(msg, "Could not detect the lockfile format.") {
		t.Errorf("undetectable content: %s", msg)
	}
	if !inProcess() {
		return
	}
	for _, tc := range []struct {
		filename optString
		content  string
		want     string
	}{
		{optString{"package-lock.json", true}, "", "package-lock"},
		{optString{"/x/poetry.lock", true}, "", "poetry"},
		{optString{"go.sum", true}, "", "go-sum"},
		{optString{"dev-requirements.txt", true}, "", "requirements"},
		{optString{}, `{"name":"a","lockfileVersion":2}`, "package-lock"},
		{optString{}, "[[package]]\nname = \"a\"", "poetry"},
		{optString{}, "golang.org/x/text v0.3.0 h1:abc=\n", "go-sum"},
		{optString{}, "requests==2.0\n", "requirements"},
		{optString{}, "hello", ""},
	} {
		if got := detectFormat(tc.filename, tc.content); got != tc.want {
			t.Errorf("detectFormat(%v, %q) = %q, want %q", tc.filename, tc.content, got, tc.want)
		}
	}
}

func TestPoetryLockAndGoSumParseToExactPins(t *testing.T) {
	c := connected(t)
	poetryText := "[[package]]\nname = \"Requests\"\nversion = \"2.31.0\"\n\n[package.extras]\nsocks = []\n\n[[package]]\nname = \"idna\"\nversion = \"3.4\"\n"
	if r, offline := scan(t, c, map[string]any{"content": poetryText, "format": "poetry"}); !offline && (r.Format != "poetry" || r.Packages != 2) {
		t.Errorf("poetry scan %+v", r)
	}
	gosumText := "golang.org/x/text v0.3.0 h1:a=\ngolang.org/x/text v0.3.0/go.mod h1:b=\nbad line\n"
	if r, offline := scan(t, c, map[string]any{"content": gosumText, "format": "go-sum"}); !offline && (r.Packages != 1 || len(r.Skipped) != 1) {
		t.Errorf("go.sum scan %+v", r)
	}
	if r, _ := scan(t, c, map[string]any{"content": "bad line\n", "format": "go-sum"}); r.Packages != 0 || len(r.Skipped) != 1 || r.Skipped[0].Reason != "malformed go.sum line" {
		t.Errorf("malformed go.sum line %+v", r)
	}
	if !inProcess() {
		return
	}
	poetry, err := parseLockfile(toUnits(poetryText), "poetry")
	if err != nil {
		t.Fatalf("parseLockfile(toUnits(poetryText), \"poetry\"): %v", err)
	}
	if want := []lockPackage{{"PyPI", "requests", "2.31.0"}, {"PyPI", "idna", "3.4"}}; !reflect.DeepEqual(poetry.packages, want) {
		t.Errorf("poetry packages %+v", poetry.packages)
	}
	gosum, err := parseLockfile(toUnits(gosumText), "go-sum")
	if err != nil {
		t.Fatalf("parseLockfile(toUnits(gosumText), \"go-sum\"): %v", err)
	}
	if want := []lockPackage{{"Go", "golang.org/x/text", "0.3.0"}}; !reflect.DeepEqual(gosum.packages, want) || len(gosum.skipped) != 1 {
		t.Errorf("go.sum packages %+v skipped %+v", gosum.packages, gosum.skipped)
	}
}

func TestPackageLockV1DependenciesTreeIsWalked(t *testing.T) {
	c := connected(t)
	v1 := `{"lockfileVersion":1,"dependencies":{"a":{"version":"1.0.0","dependencies":{"b":{"version":"2.0.0"}}},"c":{"version":"file:../c"}}}`
	if r, offline := scan(t, c, map[string]any{"content": v1, "format": "package-lock"}); !offline && (r.Packages != 2 || len(r.Skipped) == 0 || r.Skipped[0].Entry != "c") {
		t.Errorf("v1 scan %+v", r)
	}
	local := `{"lockfileVersion":1,"dependencies":{"c":{"version":"file:../c"}}}`
	if r, _ := scan(t, c, map[string]any{"content": local, "format": "package-lock"}); r.Packages != 0 || len(r.Skipped) == 0 || r.Skipped[0].Entry != "c" {
		t.Errorf("non-registry v1 dependency %+v", r)
	}
	if msg := scanError(t, c, map[string]any{"content": "{", "format": "package-lock"}); !strings.Contains(msg, "not valid JSON") {
		t.Errorf("invalid JSON: %s", msg)
	}
	if !inProcess() {
		return
	}
	parsed, err := parseLockfile(toUnits(v1), "package-lock")
	if err != nil {
		t.Fatalf("parseLockfile(toUnits(v1), \"package-lock\"): %v", err)
	}
	var names []any
	for _, p := range parsed.packages {
		names = append(names, p.name)
	}
	if !reflect.DeepEqual(names, []any{"a", "b"}) || parsed.skipped[0].entry != "c" {
		t.Errorf("v1 packages %v skipped %+v", names, parsed.skipped)
	}
	if _, err := parseLockfile("{", "package-lock"); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("invalid JSON: %v", err)
	}
}
