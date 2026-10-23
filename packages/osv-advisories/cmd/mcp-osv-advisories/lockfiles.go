package main

// Lockfile parsers. Each returns the exact (name, version) pairs it can read and lists
// what it skipped, so a scan never silently drops a dependency. Parsing runs on unit-form
// text (see jsstr.go); results are converted back to JavaScript strings.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// lockfileFormats are the format names in the order of the TypeScript constant.
var lockfileFormats = []string{"package-lock", "requirements", "poetry", "go-sum"}

// lockPackage is one pinned dependency; package-lock.json can give any JSON value for
// the name and version.
type lockPackage struct {
	ecosystem     string
	name, version any
}

func (p lockPackage) value() *jsv.Obj {
	return jsv.O("ecosystem", p.ecosystem, "name", p.name, "version", p.version)
}

type skippedEntry struct{ entry, reason string }

type parsedLockfile struct {
	packages []lockPackage
	skipped  []skippedEntry
}

var (
	rePathSeparator  = jsRegexp(`[\\/]`, "")
	reLeadingBrace   = jsRegexp(`^\s*\{`, "")
	rePoetryHeader   = jsRegexp(`^\[\[package\]\]`, "")
	reGoSumLine      = jsRegexp(`^\S+ v[\w.+-]+(\/go\.mod)? h1:`, "")
	rePinnedLine     = jsRegexp(`^[A-Za-z0-9_.-]+(\[[^\]]*\])?\s*==\s*[\w.*+!-]+`, "")
	reLineBreak      = jsRegexp(`\r?\n`, "")
	reWhitespace     = jsRegexp(`\s+`, "")
	reContinuation   = jsRegexp(`\\\r?\n`, "g")
	reComment        = jsRegexp(`(^|\s)#.*$`, "")
	reRequirement    = jsRegexp(`^([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?\s*(===|==|~=|>=|<=|!=|>|<|@)\s*(\S+)(.*)$`, "")
	reNameSeparators = jsRegexp(`[._]+`, "g")
	rePoetryName     = jsRegexp(`^name\s*=\s*"([^"]+)"`, "")
	rePoetryVersion  = jsRegexp(`^version\s*=\s*"([^"]+)"`, "")
	reGoModSuffix    = jsRegexp(`\/go\.mod$`, "")
	reLeadingV       = jsRegexp(`^v`, "")
	reNonRegistry    = jsRegexp(`^(file|link|git|http|npm):`, "")
)

func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// lineStarts are the offsets where a multiline ^ matches: the start and every position
// after a line terminator.
func lineStarts(s string) []int {
	starts := []int{0}
	for i, r := range s {
		if isLineTerminator(r) {
			starts = append(starts, i+utf8.RuneLen(r))
		}
	}
	return starts
}

// matchAtLineStart is /re/m.exec(s) for a pattern that begins with ^ and whose only
// multiline-sensitive part is that anchor.
func matchAtLineStart(s string, re interface{ FindStringSubmatch(string) []string }) []string {
	for _, q := range lineStarts(s) {
		if m := re.FindStringSubmatch(s[q:]); m != nil {
			return m
		}
	}
	return nil
}

func detectFormat(filename optString, content string) string {
	base := ""
	if filename.set {
		parts := rePathSeparator.Split(filename.value, -1)
		base = jsLower(parts[len(parts)-1])
	}
	switch {
	case base == "package-lock.json" || base == "npm-shrinkwrap.json":
		return "package-lock"
	case base == "poetry.lock":
		return "poetry"
	case base == "go.sum":
		return "go-sum"
	case strings.HasSuffix(base, ".txt") && strings.Contains(base, "requirements"):
		return "requirements"
	}
	head := unitPrefix(content, 2000)
	switch {
	case reLeadingBrace.MatchString(head) && strings.Contains(head, `"lockfileVersion"`):
		return "package-lock"
	case matchAtLineStart(head, rePoetryHeader) != nil:
		return "poetry"
	case matchAtLineStart(head, reGoSumLine) != nil:
		return "go-sum"
	case matchAtLineStart(head, rePinnedLine) != nil:
		return "requirements"
	}
	return ""
}

func parseLockfile(content, format string) (parsedLockfile, error) {
	var p parsedLockfile
	var err error
	switch format {
	case "package-lock":
		p, err = parsePackageLock(content)
	case "requirements":
		p = parseRequirements(content)
	case "poetry":
		p = parsePoetryLock(content)
	case "go-sum":
		p = parseGoSum(content)
	}
	if err != nil {
		return parsedLockfile{}, err
	}
	p.packages = dedupe(p.packages)
	for i, pkg := range p.packages {
		p.packages[i] = lockPackage{pkg.ecosystem, deepFromUnits(pkg.name), deepFromUnits(pkg.version)}
	}
	for i, s := range p.skipped {
		p.skipped[i] = skippedEntry{fromUnits(s.entry), fromUnits(s.reason)}
	}
	return p, nil
}

func dedupe(packages []lockPackage) []lockPackage {
	seen := map[string]bool{}
	var out []lockPackage
	for _, p := range packages {
		key := p.ecosystem + "\x00" + toString(p.name) + "\x00" + toString(p.version)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

func pypiName(name string) string {
	return reNameSeparators.ReplaceAllLiteralString(jsLower(name), "-")
}

func parseRequirements(content string) parsedLockfile {
	var p parsedLockfile
	for _, rawLine := range reLineBreak.Split(reContinuation.ReplaceAllLiteralString(content, " "), -1) {
		line := jsTrim(reComment.ReplaceAllLiteralString(rawLine, ""))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") {
			p.skipped = append(p.skipped, skippedEntry{unitPrefix(line, 80), "pip option, not a requirement"})
			continue
		}
		spec := jsTrim(strings.Split(strings.Split(line, ";")[0], "--hash")[0])
		m := reRequirement.FindStringSubmatch(spec)
		if m == nil {
			p.skipped = append(p.skipped, skippedEntry{unitPrefix(spec, 80), "unpinned or unrecognised requirement"})
			continue
		}
		name, op, version, rest := m[1], m[3], m[4], m[5]
		if (op != "==" && op != "===") || strings.HasPrefix(jsTrim(rest), ",") {
			p.skipped = append(p.skipped, skippedEntry{unitPrefix(spec, 80), fmt.Sprintf("not pinned with == (found %s)", op)})
			continue
		}
		if strings.Contains(version, "*") {
			p.skipped = append(p.skipped, skippedEntry{unitPrefix(spec, 80), "wildcard version"})
			continue
		}
		p.packages = append(p.packages, lockPackage{"PyPI", pypiName(name), version})
	}
	return p
}

// isLineStart reports whether a multiline ^ matches at offset q of s.
func isLineStart(s string, q int) bool {
	if q == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:q])
	return isLineTerminator(r)
}

// packageBlockEnd matches \s*$ (multiline, greedy) from offset start of s.
func packageBlockEnd(s string, start int) (int, bool) {
	var ends []int
	k := start
	for {
		ends = append(ends, k)
		if k >= len(s) {
			break
		}
		r, n := utf8.DecodeRuneInString(s[k:])
		if !jsv.IsSpace(r) {
			break
		}
		k += n
	}
	for i := len(ends) - 1; i >= 0; i-- {
		e := ends[i]
		if e == len(s) {
			return e, true
		}
		if r, _ := utf8.DecodeRuneInString(s[e:]); isLineTerminator(r) {
			return e, true
		}
	}
	return 0, false
}

// splitPackageBlocks is content.split(/^\[\[package\]\]\s*$/m).
func splitPackageBlocks(s string) []string {
	const header = "[[package]]"
	var out []string
	p := 0
	for q := 0; q < len(s); {
		if isLineStart(s, q) && strings.HasPrefix(s[q:], header) {
			if e, ok := packageBlockEnd(s, q+len(header)); ok {
				out = append(out, s[p:q])
				p, q = e, e
				continue
			}
		}
		_, n := utf8.DecodeRuneInString(s[q:])
		q += n
	}
	return append(out, s[p:])
}

// beforeTable is block.split(/^\[/m)[0].
func beforeTable(block string) string {
	for _, q := range lineStarts(block) {
		if q < len(block) && block[q] == '[' {
			return block[:q]
		}
	}
	return block
}

func parsePoetryLock(content string) parsedLockfile {
	var p parsedLockfile
	for _, block := range splitPackageBlocks(content)[1:] {
		body := beforeTable(block)
		name := matchAtLineStart(body, rePoetryName)
		version := matchAtLineStart(body, rePoetryVersion)
		if name == nil || version == nil {
			first := strings.Split(jsTrim(body), "\n")[0]
			p.skipped = append(p.skipped, skippedEntry{unitPrefix(first, 80), "package block without name or version"})
			continue
		}
		p.packages = append(p.packages, lockPackage{"PyPI", pypiName(name[1]), version[1]})
	}
	return p
}

func parseGoSum(content string) parsedLockfile {
	var p parsedLockfile
	for _, rawLine := range reLineBreak.Split(content, -1) {
		line := jsTrim(rawLine)
		if line == "" {
			continue
		}
		parts := reWhitespace.Split(line, -1)
		if len(parts) < 3 {
			p.skipped = append(p.skipped, skippedEntry{unitPrefix(line, 80), "malformed go.sum line"})
			continue
		}
		version := reLeadingV.ReplaceAllLiteralString(reGoModSuffix.ReplaceAllLiteralString(parts[1], ""), "")
		p.packages = append(p.packages, lockPackage{"Go", parts[0], version})
	}
	return p
}
