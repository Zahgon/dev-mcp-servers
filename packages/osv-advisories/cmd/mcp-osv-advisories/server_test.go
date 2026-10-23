package main

// The upstream suite test/server.test.mjs, ported case by case. Every upstream case but
// the first injects a fetch double; those run in process (see inprocess_test.go).

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

type summaryResult struct {
	ID            string   `json:"id"`
	Aliases       []string `json:"aliases"`
	Severity      []string `json:"severity"`
	FixedVersions []string `json:"fixed_versions"`
}

func TestExposesTheFourDocumentedToolsAllReadOnly(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != "osv-advisories" {
		t.Fatalf("serverInfo.name: %s", init.raw)
	}
	reply := c.request("tools/list", map[string]any{})
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				ReadOnlyHint *bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &list); err != nil {
		t.Fatalf("tools/list: %s", reply.raw)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not read-only", tool.Name)
		}
	}
	sort.Strings(names)
	if want := []string{"get_vulnerability", "query_batch", "query_package", "scan_lockfile"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestQueryPackageNormalisesTheEcosystemAndSummarisesAdvisories(t *testing.T) {
	requireInProcess(t)
	fake := &fakeOSV{t: t}
	c := connectedWith(t, clientOptions{Transport: fake})
	r := decodeResult[struct {
		Ecosystem       string          `json:"ecosystem"`
		Count           int             `json:"count"`
		Vulnerabilities []summaryResult `json:"vulnerabilities"`
	}](t, c.call("query_package", map[string]any{"ecosystem": "NPM", "name": "example", "version": "1.0.0"}))
	if r.Ecosystem != "npm" || r.Count != 1 || len(r.Vulnerabilities) != 1 {
		t.Fatalf("result %+v", r)
	}
	v := r.Vulnerabilities[0]
	if v.ID != "GHSA-test-0001" || !slices.Equal(v.FixedVersions, []string{"1.2.3"}) || !slices.Equal(v.Aliases, []string{"CVE-2020-0001"}) {
		t.Errorf("summary %+v", v)
	}
	if !slices.ContainsFunc(v.Severity, func(s string) bool { return strings.HasPrefix(s, "CVSS_V3") }) {
		t.Errorf("severity %v", v.Severity)
	}
	calls := fake.recorded()
	if len(calls) == 0 || calls[0].Method != "POST" {
		t.Fatalf("calls %+v", calls)
	}
	want := map[string]any{"package": map[string]any{"name": "example", "ecosystem": "npm"}, "version": "1.0.0"}
	if !reflect.DeepEqual(calls[0].Body, want) {
		t.Errorf("request body %v, want %v", calls[0].Body, want)
	}
	// redirect: "error": the default transport refuses a redirect instead of following it.
	redirecting := fetchTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := newOSVClient(clientOptions{Transport: redirecting}).queryPackage("npm", "example", ""); err == nil || err.Error() != "request to /v1/query failed: fetch failed" {
		t.Errorf("redirect: %v", err)
	}

	none := decodeResult[struct {
		Count int `json:"count"`
	}](t, c.call("query_package", map[string]any{"ecosystem": "pypi", "name": "clean"}))
	if none.Count != 0 {
		t.Errorf("clean package count %d", none.Count)
	}
	bad := c.call("query_package", map[string]any{"ecosystem": "maven-central", "name": "x"})
	if !bad.IsError || !regexp.MustCompile(`Unknown ecosystem`).MatchString(bad.Text) {
		t.Errorf("unknown ecosystem: %+v", bad)
	}
}

func TestQueryBatchReturnsIdsPerPackageInOrder(t *testing.T) {
	requireInProcess(t)
	fake := &fakeOSV{t: t}
	c := connectedWith(t, clientOptions{Transport: fake})
	r := decodeResult[struct {
		Queried  int `json:"queried"`
		Affected int `json:"affected"`
		Results  []struct {
			VulnerabilityIDs []string `json:"vulnerability_ids"`
		} `json:"results"`
	}](t, c.call("query_batch", map[string]any{"packages": []any{
		map[string]any{"ecosystem": "npm", "name": "clean", "version": "1.0.0"},
		map[string]any{"ecosystem": "npm", "name": "example", "version": "1.0.0"},
	}}))
	if r.Queried != 2 || r.Affected != 1 || len(r.Results) != 2 {
		t.Fatalf("result %+v", r)
	}
	if r.Results[0].VulnerabilityIDs == nil || len(r.Results[0].VulnerabilityIDs) != 0 || !slices.Equal(r.Results[1].VulnerabilityIDs, []string{"GHSA-test-0001"}) {
		t.Errorf("ids %+v", r.Results)
	}
	if calls := fake.recorded(); len(calls) == 0 || calls[0].URL != "https://api.osv.dev/v1/querybatch" {
		t.Errorf("calls %+v", calls)
	}
	if empty := c.call("query_batch", map[string]any{"packages": []any{}}); !empty.IsError {
		t.Errorf("empty batch: %s", empty.Text)
	}
}

func TestScanLockfileParsesAPackageLockJsonFromDiskAndARequirementsTxtFromContent(t *testing.T) {
	requireInProcess(t)
	c := connectedWith(t, clientOptions{Transport: &fakeOSV{t: t}})
	dir := t.TempDir()
	lock := filepath.Join(dir, "package-lock.json")
	doc := `{"name":"app","lockfileVersion":3,"packages":{"":{"name":"app"},"node_modules/lodash":{"version":"4.17.20"},"node_modules/a/node_modules/b":{"version":"2.0.0"},"packages/ws":{"link":true}}}`
	if err := os.WriteFile(lock, []byte(doc), 0o644); err != nil {
		t.Fatalf("os.WriteFile(lock, []byte(doc), 0o644): %v", err)
	}
	type scan struct {
		Format           string `json:"format"`
		Packages         int    `json:"packages"`
		AffectedPackages int    `json:"affected_packages"`
		Affected         []struct {
			Name string `json:"name"`
		} `json:"affected"`
		Details []summaryResult     `json:"details"`
		Skipped []map[string]string `json:"skipped"`
	}
	r := decodeResult[scan](t, c.call("scan_lockfile", map[string]any{"path": lock, "include_details": true}))
	if r.Format != "package-lock" || r.Packages != 2 || r.AffectedPackages != 1 || len(r.Affected) != 1 || r.Affected[0].Name != "lodash" {
		t.Errorf("package-lock scan %+v", r)
	}
	if len(r.Details) == 0 || r.Details[0].ID != "GHSA-test-0001" {
		t.Errorf("details %+v", r.Details)
	}
	if want := []map[string]string{{"entry": "packages/ws", "reason": "workspace link"}}; !reflect.DeepEqual(r.Skipped, want) {
		t.Errorf("skipped %v", r.Skipped)
	}

	req := decodeResult[scan](t, c.call("scan_lockfile", map[string]any{"content": "# deps\nrequests==2.31.0\nflask>=2.0\nexample[extra]==1.0 ; python_version<'3'\n-r other.txt\n", "filename": "requirements.txt"}))
	if req.Format != "requirements" || req.Packages != 2 || len(req.Skipped) != 2 || req.AffectedPackages != 1 {
		t.Errorf("requirements scan %+v", req)
	}
	if both := c.call("scan_lockfile", map[string]any{"content": "x", "path": lock}); !both.IsError {
		t.Errorf("path and content: %s", both.Text)
	}
	if missing := c.call("scan_lockfile", map[string]any{"path": filepath.Join(dir, "nope.lock")}); !missing.IsError {
		t.Errorf("missing file: %s", missing.Text)
	}
}

func TestGetVulnerabilityReturnsTheRecordOrAClearError(t *testing.T) {
	requireInProcess(t)
	c := connectedWith(t, clientOptions{Transport: &fakeOSV{t: t}})
	r := decodeResult[struct {
		Summary summaryResult `json:"summary"`
		Record  struct {
			Summary string `json:"summary"`
		} `json:"record"`
	}](t, c.call("get_vulnerability", map[string]any{"id": "GHSA-test-0001"}))
	if r.Summary.ID != "GHSA-test-0001" || r.Record.Summary != "Prototype pollution in example" {
		t.Errorf("record %+v", r)
	}
	if missing := c.call("get_vulnerability", map[string]any{"id": "GHSA-nope"}); !missing.IsError {
		t.Errorf("unknown id: %s", missing.Text)
	}
	if invalid := c.call("get_vulnerability", map[string]any{"id": "../x"}); !invalid.IsError {
		t.Errorf("invalid id: %s", invalid.Text)
	}
}

func TestClientRefusesOtherOriginsOversizedBodiesAndSurfacesHTTPErrors(t *testing.T) {
	requireInProcess(t)
	oversized := newOSVClient(clientOptions{MaxResponseBytes: 50, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return respond(200, strings.Repeat("x", 100)), nil
	})})
	if _, err := oversized.getVulnerability("GHSA-a"); err == nil || !regexp.MustCompile(`exceeds the 50 byte limit`).MatchString(err.Error()) {
		t.Errorf("oversized body: %v", err)
	}
	down := newOSVClient(clientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return respond(503, "down"), nil
	})})
	if _, err := down.queryPackage("npm", "x", ""); err == nil || !regexp.MustCompile(`responded 503`).MatchString(err.Error()) {
		t.Errorf("HTTP 503: %v", err)
	}
	failing := newOSVClient(clientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("timeout")
	})})
	if _, err := failing.queryBatch([]batchQuery{{ecosystem: "npm", name: "x"}}); err == nil || !regexp.MustCompile(`failed: timeout`).MatchString(err.Error()) {
		t.Errorf("transport failure: %v", err)
	}
}
