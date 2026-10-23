// Command mcp-osv-advisories is the osv-advisories MCP server on stdio: it looks up
// known vulnerabilities in the OSV.dev database and scans lockfiles.
package main

import (
	"fmt"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName       = "osv-advisories"
	serverVersion    = "0.1.0"
	maxLockfileBytes = 10 * 1024 * 1024
)

// ecosystems are the ecosystem names as OSV spells them.
var ecosystems = []string{
	"AlmaLinux", "Alpine", "Android", "Bioconductor", "Bitnami", "Chainguard", "ConanCenter", "CRAN", "crates.io", "Debian",
	"GHC", "GitHub Actions", "Go", "Hackage", "Hex", "Linux", "Mageia", "Maven", "npm", "NuGet", "openSUSE", "OSS-Fuzz",
	"Packagist", "Pub", "PyPI", "Red Hat", "Rocky Linux", "RubyGems", "SUSE", "SwiftURL", "Ubuntu", "Wolfi",
}

// normaliseEcosystem matches input case-insensitively against ecosystems.
func normaliseEcosystem(input string) (string, bool) {
	needle := jsLower(jsTrim(toUnits(input)))
	for _, e := range ecosystems {
		if jsLower(e) == needle {
			return e, true
		}
	}
	return "", false
}

func newServer() *mcp.Server { return newServerWith(clientOptions{}) }

// newServerWith builds the server around an OSV client with the given options; tests
// inject a transport here.
func newServerWith(options clientOptions) *mcp.Server {
	h := handlers{client: newOSVClient(options)}
	return &mcp.Server{
		Name:    serverName,
		Version: serverVersion,
		Instructions: "Looks up known vulnerabilities in the OSV.dev database (https://osv.dev) by package name, version and ecosystem. " +
			"query_package returns full advisories for one package; query_batch returns advisory ids for many; scan_lockfile parses a " +
			"package-lock.json, requirements.txt, poetry.lock or go.sum and batch-queries every pinned dependency; get_vulnerability fetches one advisory by id. " +
			"The server only contacts api.osv.dev. Results reflect the OSV database at query time and do not prove a package is safe.",
		Tools: h.tools(),
	}
}

func main() { mcp.Main(newServer()) }

type handlers struct{ client *osvClient }

func reply(v any) mcp.Result { return mcp.OK(toJSON(v)) }

func unknownEcosystem(ecosystem, suffix string) mcp.Result {
	return mcp.Fail(fmt.Sprintf("Unknown ecosystem %s%s. Known: %s.", jsv.Stringify(ecosystem, 0), suffix, strings.Join(ecosystems, ", ")))
}

func (h handlers) queryPackage(raw *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "query_package", Obj: raw}
	ecosystem, err1 := a.RequiredString("ecosystem", 1, 40)
	name, err2 := a.RequiredString("name", 1, 300)
	version, err3 := a.String("version", 1, 100)
	if err := firstError(err1, err2, err3); err != nil {
		return mcp.Fail(err.Error())
	}
	eco, known := normaliseEcosystem(ecosystem)
	if !known {
		return unknownEcosystem(ecosystem, "")
	}
	v := ""
	var versionValue any
	if version != nil {
		v, versionValue = *version, *version
	}
	vulns, err := h.client.queryPackage(eco, name, v)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	summaries := []any{}
	for _, vuln := range vulns {
		s, err := summarise(vuln)
		if err != nil {
			return mcp.Fail(err.Error())
		}
		summaries = append(summaries, s)
	}
	return reply(jsv.O("ecosystem", eco, "name", name, "version", versionValue, "count", float64(len(vulns)), "vulnerabilities", summaries))
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (h handlers) queryBatch(raw *jsv.Obj) mcp.Result {
	const tool = "query_batch"
	items, present, err := mcp.Args{Tool: tool, Obj: raw}.Array("packages", 1, maxBatchQueries)
	if err == nil && !present {
		err = mcp.Invalid(tool, "packages: Required")
	}
	if err != nil {
		return mcp.Fail(err.Error())
	}
	type query struct{ ecosystem, name, version string }
	var queries []query
	for k, item := range items {
		obj := jsv.AsObj(item)
		if obj == nil {
			return mcp.Fail(mcp.Invalid(tool, fmt.Sprintf("packages.%d: Expected object", k)).Error())
		}
		a := mcp.Args{Tool: tool, Obj: obj}
		ecosystem, err1 := a.RequiredString("ecosystem", 1, 40)
		name, err2 := a.RequiredString("name", 1, 300)
		version, err3 := a.String("version", 1, 100)
		if err := firstError(err1, err2, err3); err != nil {
			return mcp.Fail(err.Error())
		}
		q := query{ecosystem: ecosystem, name: name}
		if version != nil {
			q.version = *version
		}
		queries = append(queries, q)
	}
	batch := make([]batchQuery, len(queries))
	for i, q := range queries {
		eco, known := normaliseEcosystem(q.ecosystem)
		if !known {
			return unknownEcosystem(q.ecosystem, " for "+q.name)
		}
		queries[i].ecosystem = eco
		batch[i] = batchQuery{ecosystem: eco, name: q.name, version: q.version}
	}
	results, err := h.client.queryBatch(batch)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	rows := []any{}
	affected := 0
	for i, q := range queries {
		var version any
		if q.version != "" {
			version = q.version
		}
		if len(results[i]) > 0 {
			affected++
		}
		rows = append(rows, jsv.O("ecosystem", q.ecosystem, "name", q.name, "version", version, "vulnerability_ids", results[i]))
	}
	return reply(jsv.O("queried", float64(len(rows)), "affected", float64(affected), "results", rows))
}

func (h handlers) getVulnerability(raw *jsv.Obj) mcp.Result {
	const tool = "get_vulnerability"
	id, err := mcp.Args{Tool: tool, Obj: raw}.RequiredString("id", 3, 100)
	if err == nil && !reAdvisoryID.MatchString(id) {
		err = mcp.Invalid(tool, "id: advisory ids contain letters, digits, dots, colons, underscores and dashes")
	}
	if err != nil {
		return mcp.Fail(err.Error())
	}
	v, err := h.client.getVulnerability(id)
	if err == nil && !truthy(v, true) {
		return mcp.Fail(fmt.Sprintf("OSV has no advisory with id %s.", jsv.Stringify(id, 0)))
	}
	var summary *jsv.Obj
	if err == nil {
		summary, err = summarise(v)
	}
	if err != nil {
		if e, isJS := err.(*jsError); isJS && !e.osv {
			return mcp.Fail(e.String())
		}
		return mcp.Fail(err.Error())
	}
	return reply(jsv.O("summary", summary, "record", v))
}

var reAdvisoryID = jsRegexp(`^[A-Za-z0-9][A-Za-z0-9.:_-]*$`, "")
