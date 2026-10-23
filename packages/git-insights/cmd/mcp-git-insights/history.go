package main

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

// anyChar is JavaScript's `.`: every code point except the line terminators.
const anyChar = `[^\n\r\x{2028}\x{2029}]`

var (
	bracedRename = regexp.MustCompile(`^(` + anyChar + `*)\{(` + anyChar + `*) => (` + anyChar + `*)\}(` + anyChar + `*)$`)
	plainRename  = regexp.MustCompile(`^(` + anyChar + `+) => (` + anyChar + `+)$`)
	slashes      = regexp.MustCompile(`//+`)
	numstatLine  = regexp.MustCompile(`^([0-9]+|-)\t([0-9]+|-)\t(` + anyChar + `+)$`)
	lsTreeLine   = regexp.MustCompile(`^([0-9]+) ([A-Za-z0-9_]+) ([0-9a-f]+)[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+([0-9]+|-)\t(` + anyChar + `+)$`)
)

// renamePaths splits a --numstat rename such as "src/{a.ts => b.ts}" or "a.ts => dir/b.ts" into
// its old and new paths; ok is false when raw is not a rename.
func renamePaths(raw string) (oldPath, newPath string, ok bool) {
	if m := bracedRename.FindStringSubmatch(raw); m != nil {
		return slashes.ReplaceAllString(m[1]+m[2]+m[4], "/"), slashes.ReplaceAllString(m[1]+m[3]+m[4], "/"), true
	}
	if m := plainRename.FindStringSubmatch(raw); m != nil {
		return m[1], m[2], true
	}
	return "", "", false
}

type churnEntry struct {
	file           string
	commits        int
	added, deleted float64
	binary         bool
}

func churn(a mcp.Args) (any, error) {
	r, err := readRange(a, true)
	if err != nil {
		return nil, err
	}
	limit, err := a.Number("limit", 1, 200, true)
	if err != nil {
		return nil, err
	}
	maxCommits, err := a.Number("max_commits", 1, 20_000, true)
	if err != nil {
		return nil, err
	}
	info, err := openRepo(r.repo)
	if err != nil {
		return nil, err
	}
	args := []string{"log", "--numstat", "-M", "--format=" + rec + "%H", "-n", jsv.FormatNumber(intOr(maxCommits, 5000))}
	if args, err = dateArgs(args, r); err != nil {
		return nil, err
	}
	if args, err = revAndPath(args, r); err != nil {
		return nil, err
	}
	out, err := runGit(info.cwd(), args)
	if err != nil {
		return nil, err
	}
	var order []*churnEntry
	files := map[string]*churnEntry{}
	// Commits arrive newest first, so a rename maps its old name onto the name the file has today.
	aliases := map[string]string{}
	current := func(name string) string {
		n := name
		for hop := 0; hop < 64; hop++ {
			next, ok := aliases[n]
			if !ok {
				break
			}
			n = next
		}
		return n
	}
	commitCount := 0
	for _, chunk := range strings.Split(out, rec) {
		lines := nonEmptyLines(jsv.Trim(chunk))
		if len(lines) == 0 {
			continue
		}
		commitCount++
		for _, l := range lines[1:] {
			m := numstatLine.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			var name string
			if oldPath, newPath, ok := renamePaths(m[3]); ok {
				name = current(newPath)
				if oldPath != name {
					aliases[oldPath] = name
				}
			} else {
				name = current(m[3])
			}
			e := files[name]
			if e == nil {
				e = &churnEntry{file: name}
				files[name] = e
				order = append(order, e)
			}
			e.commits++
			if m[1] == "-" {
				e.binary = true
			} else {
				e.added += jsNumber(m[1])
				e.deleted += jsNumber(m[2])
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].commits != order[j].commits {
			return order[i].commits > order[j].commits
		}
		return order[i].added+order[i].deleted > order[j].added+order[j].deleted
	})
	ranked := []any{}
	for i, e := range order {
		if float64(i) >= intOr(limit, 25) {
			break
		}
		ranked = append(ranked, jsv.O("file", e.file, "commits", float64(e.commits), "added", e.added, "deleted", e.deleted, "binary", e.binary, "churn", e.added+e.deleted))
	}
	return jsv.O("repo", info.TopLevel, "rev", revOrHead(r.rev), "commits_examined", float64(commitCount), "files_total", float64(len(files)), "files", ranked), nil
}

type authorEntry struct {
	email, name string
	commits     int
	first, last string
}

func authors(a mcp.Args) (any, error) {
	r, err := readRange(a, true)
	if err != nil {
		return nil, err
	}
	includeMerges, err := a.Bool("include_merges")
	if err != nil {
		return nil, err
	}
	limit, err := a.Number("limit", 1, 500, true)
	if err != nil {
		return nil, err
	}
	info, err := openRepo(r.repo)
	if err != nil {
		return nil, err
	}
	args := []string{"log", "--format=%an" + sep + "%ae" + sep + "%aI"}
	if !isTrue(includeMerges) {
		args = append(args, "--no-merges")
	}
	if args, err = dateArgs(args, r); err != nil {
		return nil, err
	}
	if args, err = revAndPath(args, r); err != nil {
		return nil, err
	}
	out, err := runGit(info.cwd(), args)
	if err != nil {
		return nil, err
	}
	var order []*authorEntry
	byEmail := map[string]*authorEntry{}
	total := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, sep)
		date, _ := dateField(parts, 2)
		total++
		email, ok := field(parts, 1)
		if !ok {
			return nil, unexpected("Cannot read properties of undefined (reading 'toLowerCase')")
		}
		key := toLowerCase(email)
		e := byEmail[key]
		if e == nil {
			e = &authorEntry{email: key, name: parts[0], commits: 1, first: date, last: date}
			byEmail[key] = e
			order = append(order, e)
			continue
		}
		e.commits++
		if date < e.first {
			e.first = date
		}
		if date > e.last {
			e.last = date
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].commits > order[j].commits })
	list := []any{}
	for i, e := range order {
		if float64(i) >= intOr(limit, 50) {
			break
		}
		list = append(list, jsv.O("email", e.email, "name", e.name, "commits", float64(e.commits), "percent", percent(e.commits, total), "first_commit", e.first, "last_commit", e.last))
	}
	return jsv.O("repo", info.TopLevel, "rev", revOrHead(r.rev), "commits", float64(total), "authors", list), nil
}

type blob struct {
	hash string
	size float64
	path any
}

func sortBlobs(blobs []blob, n float64) []any {
	sort.SliceStable(blobs, func(i, j int) bool { return blobs[i].size > blobs[j].size })
	out := []any{}
	for i, b := range blobs {
		if float64(i) >= n {
			break
		}
		out = append(out, jsv.O("hash", b.hash, "size", b.size, "path", b.path))
	}
	return out
}

func largeFiles(a mcp.Args) (any, error) {
	r, err := readRange(a, false)
	if err != nil {
		return nil, err
	}
	limit, err := a.Number("limit", 1, 100, true)
	if err != nil {
		return nil, err
	}
	includeHistory, err := a.Bool("include_history")
	if err != nil {
		return nil, err
	}
	info, err := openRepo(r.repo)
	if err != nil {
		return nil, err
	}
	n := intOr(limit, 20)
	if isTrue(includeHistory) {
		out, err := pipeGit(info.cwd(), []string{"rev-list", "--objects", "--all"}, []string{"cat-file", "--batch-check=%(objecttype) %(objectname) %(objectsize) %(rest)"})
		if err != nil {
			return nil, err
		}
		var blobs []blob
		for _, l := range strings.Split(out, "\n") {
			if !strings.HasPrefix(l, "blob ") {
				continue
			}
			parts := strings.Split(l, " ")
			b := blob{hash: parts[1], size: math.NaN()}
			if len(parts) > 2 {
				b.size = jsNumber(parts[2])
			}
			if rest := strings.Join(parts[min(3, len(parts)):], " "); rest != "" {
				b.path = rest
			}
			blobs = append(blobs, b)
		}
		return jsv.O("repo", info.TopLevel, "scope", "history", "files", sortBlobs(blobs, n)), nil
	}
	rev, err := safeRev(r.rev, "HEAD")
	if err != nil {
		return nil, err
	}
	out, err := runGit(info.cwd(), []string{"ls-tree", "-r", "-l", rev})
	if err != nil {
		return nil, err
	}
	var blobs []blob
	for _, l := range nonEmptyLines(out) {
		m := lsTreeLine.FindStringSubmatch(l)
		if m == nil || m[2] != "blob" {
			continue
		}
		size := 0.0
		if m[4] != "-" {
			size = jsNumber(m[4])
		}
		blobs = append(blobs, blob{hash: m[3], size: size, path: m[5]})
	}
	return jsv.O("repo", info.TopLevel, "scope", "tree at "+revOrHead(r.rev), "files", sortBlobs(blobs, n)), nil
}
