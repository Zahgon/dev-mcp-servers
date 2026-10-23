package main

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	sep = "\u001f"
	rec = "\u001e"
)

// isoZ keeps one canonical form of a UTC offset: git prints +00:00 or Z depending on its version.
func isoZ(d string) string {
	if strings.HasSuffix(d, "+00:00") {
		return strings.TrimSuffix(d, "+00:00") + "Z"
	}
	return d
}

// field is parts[i] as JavaScript destructuring gives it: ok is false for undefined.
func field(parts []string, i int) (string, bool) {
	if i < len(parts) {
		return parts[i], true
	}
	return "", false
}

// setDefined sets key unless the value is undefined, as JSON.stringify drops undefined members.
func setDefined(o *jsv.Obj, key, value string, ok bool) {
	if ok {
		o.Set(key, value)
	}
}

// dateField is `dateRaw ? isoZ(dateRaw) : dateRaw`.
func dateField(parts []string, i int) (string, bool) {
	d, ok := field(parts, i)
	if ok && d != "" {
		return isoZ(d), true
	}
	return d, ok
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func revOrHead(rev *string) string {
	if rev == nil {
		return "HEAD"
	}
	return *rev
}

func intOr(v *float64, fallback float64) float64 {
	if v == nil {
		return fallback
	}
	return *v
}

func isTrue(b *bool) bool { return b != nil && *b }

// rangeArgs are the since, until and path filters shared by several tools.
type rangeArgs struct {
	repo         string
	rev          *string
	since, until *string
	path         *string
}

func readRange(a mcp.Args, withDates bool) (rangeArgs, error) {
	var r rangeArgs
	var err error
	if r.repo, err = a.RequiredString("repo", 1, 4096); err != nil {
		return r, err
	}
	if r.rev, err = a.String("rev", 1, 200); err != nil {
		return r, err
	}
	if withDates {
		if r.since, err = a.String("since", 1, 64); err != nil {
			return r, err
		}
		if r.until, err = a.String("until", 1, 64); err != nil {
			return r, err
		}
		if r.path, err = a.String("path", 1, 1024); err != nil {
			return r, err
		}
	}
	return r, nil
}

// dateArgs appends --since and --until after checking them.
func dateArgs(args []string, r rangeArgs) ([]string, error) {
	if r.since != nil {
		v, err := safeText(r.since, "since", 64)
		if err != nil {
			return nil, err
		}
		args = append(args, "--since="+*v)
	}
	if r.until != nil {
		v, err := safeText(r.until, "until", 64)
		if err != nil {
			return nil, err
		}
		args = append(args, "--until="+*v)
	}
	return args, nil
}

// revAndPath appends rev, "--" and the optional path.
func revAndPath(args []string, r rangeArgs) ([]string, error) {
	rev, err := safeRev(r.rev, "HEAD")
	if err != nil {
		return nil, err
	}
	args = append(args, rev, "--")
	if r.path != nil {
		p, err := safePath(*r.path)
		if err != nil {
			return nil, err
		}
		args = append(args, p)
	}
	return args, nil
}

func repoSummary(a mcp.Args) (any, error) {
	repo, err := a.RequiredString("repo", 1, 4096)
	if err != nil {
		return nil, err
	}
	info, err := openRepo(repo)
	if err != nil {
		return nil, err
	}
	top := info.cwd()
	orEmpty := func(args ...string) string {
		out, err := runGit(top, args)
		if err != nil {
			return ""
		}
		return out
	}
	branch := jsv.Trim(orEmpty("rev-parse", "--abbrev-ref", "HEAD"))
	headParts := strings.Split(jsv.Trim(orEmpty("log", "-1", "--format=%H"+sep+"%an"+sep+"%aI"+sep+"%s")), sep)
	var head any
	if headParts[0] != "" {
		h := jsv.O("hash", headParts[0])
		author, ok := field(headParts, 1)
		setDefined(h, "author", author, ok)
		date, ok := dateField(headParts, 2)
		setDefined(h, "date", date, ok)
		subject, ok := field(headParts, 3)
		setDefined(h, "subject", subject, ok)
		head = h
	}
	remotes := []any{}
	for _, l := range nonEmptyLines(jsv.Trim(orEmpty("remote", "-v"))) {
		p := splitSpace(l)
		if len(p) > 2 && p[2] == "(fetch)" {
			remotes = append(remotes, jsv.O("name", p[0], "url", sanitizeRemoteUrl(p[1])))
		}
	}
	countOut, cerr := runGit(top, []string{"rev-list", "--count", "HEAD"})
	if cerr != nil {
		countOut = "0"
	}
	commits := jsNumber(countOut)
	tags := len(nonEmptyLines(jsv.Trim(orEmpty("tag", "--list"))))
	branches := len(nonEmptyLines(jsv.Trim(orEmpty("branch", "--list", "--format=%(refname:short)"))))
	if branch == "HEAD" {
		branch = "(detached)"
	}
	var workTree any
	if !info.Bare {
		modified, untracked := 0, 0
		for _, l := range nonEmptyLines(orEmpty("status", "--porcelain", "--untracked-files=normal")) {
			if strings.HasPrefix(l, "??") {
				untracked++
			} else {
				modified++
			}
		}
		workTree = jsv.O("modified", float64(modified), "untracked", float64(untracked))
	}
	return jsv.O("path", info.Path, "top_level", top, "bare", info.Bare, "branch", branch, "head", head, "remotes", remotes,
		"commits", commits, "tags", float64(tags), "branches", float64(branches), "work_tree", workTree), nil
}

func gitLog(a mcp.Args) (any, error) {
	r, err := readRange(a, false)
	if err != nil {
		return nil, err
	}
	limit, err := a.Number("limit", 1, 500, true)
	if err != nil {
		return nil, err
	}
	if r.path, err = a.String("path", 1, 1024); err != nil {
		return nil, err
	}
	author, err := a.String("author", 1, 200)
	if err != nil {
		return nil, err
	}
	if r.since, err = a.String("since", 1, 64); err != nil {
		return nil, err
	}
	if r.until, err = a.String("until", 1, 64); err != nil {
		return nil, err
	}
	grep, err := a.String("grep", 1, 200)
	if err != nil {
		return nil, err
	}
	noMerges, err := a.Bool("no_merges")
	if err != nil {
		return nil, err
	}
	withFiles, err := a.Bool("with_files")
	if err != nil {
		return nil, err
	}
	info, err := openRepo(r.repo)
	if err != nil {
		return nil, err
	}
	args := []string{"log", "--format=" + rec + "%H" + sep + "%h" + sep + "%an" + sep + "%ae" + sep + "%aI" + sep + "%s", "-n", jsv.FormatNumber(intOr(limit, 50)), "--date=iso-strict"}
	if isTrue(noMerges) {
		args = append(args, "--no-merges")
	}
	if author != nil {
		v, err := safeText(author, "author", 200)
		if err != nil {
			return nil, err
		}
		args = append(args, "--author="+*v)
	}
	if args, err = dateArgs(args, r); err != nil {
		return nil, err
	}
	if grep != nil {
		v, err := safeText(grep, "grep", 200)
		if err != nil {
			return nil, err
		}
		args = append(args, "--grep="+*v, "--fixed-strings")
	}
	if isTrue(withFiles) {
		args = append(args, "--name-only")
	}
	if args, err = revAndPath(args, r); err != nil {
		return nil, err
	}
	out, err := runGit(info.cwd(), args)
	if err != nil {
		return nil, err
	}
	commits := []any{}
	for _, raw := range strings.Split(out, rec) {
		chunk := jsv.Trim(raw)
		if chunk == "" {
			continue
		}
		lines := strings.Split(chunk, "\n")
		parts := strings.Split(lines[0], sep)
		entry := jsv.O("hash", parts[0])
		for i, key := range []string{"short", "author", "email"} {
			v, ok := field(parts, i+1)
			setDefined(entry, key, v, ok)
		}
		date, ok := dateField(parts, 4)
		setDefined(entry, "date", date, ok)
		subject, ok := field(parts, 5)
		setDefined(entry, "subject", subject, ok)
		if isTrue(withFiles) {
			files := []any{}
			for _, l := range lines[1:] {
				if t := jsv.Trim(l); t != "" {
					files = append(files, t)
				}
			}
			entry.Set("files", files)
		}
		commits = append(commits, entry)
	}
	return jsv.O("repo", info.TopLevel, "rev", revOrHead(r.rev), "count", float64(len(commits)), "commits", commits), nil
}

// errInvalidTime is the RangeError Date#toISOString throws for a date out of range.
var errInvalidTime = unexpected("Invalid time value")

// jsMax is Math.max of two numbers: NaN wins.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

var blameHeader = regexp.MustCompile(`^[0-9a-f]{40} [0-9]+ [0-9]+`)

type blameAuthor struct {
	name   string
	email  string
	lines  int
	latest float64
}

func blameSummary(a mcp.Args) (any, error) {
	repo, err := a.RequiredString("repo", 1, 4096)
	if err != nil {
		return nil, err
	}
	path, err := a.RequiredString("path", 1, 1024)
	if err != nil {
		return nil, err
	}
	rev, err := a.String("rev", 1, 200)
	if err != nil {
		return nil, err
	}
	ignoreWS, err := a.Bool("ignore_whitespace")
	if err != nil {
		return nil, err
	}
	info, err := openRepo(repo)
	if err != nil {
		return nil, err
	}
	args := []string{"blame", "--line-porcelain"}
	if isTrue(ignoreWS) {
		args = append(args, "-w")
	}
	safe, err := safeRev(rev, "HEAD")
	if err != nil {
		return nil, err
	}
	p, err := safePath(path)
	if err != nil {
		return nil, err
	}
	out, err := runGit(info.cwd(), append(args, safe, "--", p))
	if err != nil {
		return nil, err
	}
	var order []*blameAuthor
	byAuthor := map[string]*blameAuthor{}
	commits := map[string]bool{}
	total := 0
	var author, email *string
	var authorTime *float64
	for _, line := range strings.Split(out, "\n") {
		switch {
		case blameHeader.MatchString(line):
			commits[line[:40]] = true
			author, email, authorTime = nil, nil, nil
		case strings.HasPrefix(line, "author "):
			v := line[7:]
			author = &v
		case strings.HasPrefix(line, "author-mail "):
			v := strings.NewReplacer("<", "", ">", "").Replace(line[12:])
			email = &v
		case strings.HasPrefix(line, "author-time "):
			v := jsNumber(line[12:])
			authorTime = &v
		case strings.HasPrefix(line, "\t"):
			total++
			key := "unknown"
			if author != nil {
				key = *author
			}
			entry := byAuthor[key]
			if entry == nil {
				entry = &blameAuthor{name: key}
				if email != nil {
					entry.email = *email
				}
				byAuthor[key] = entry
				order = append(order, entry)
			}
			entry.lines++
			t := 0.0
			if authorTime != nil {
				t = *authorTime
			}
			entry.latest = jsMax(entry.latest, t)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].lines > order[j].lines })
	authors := []any{}
	for _, e := range order {
		var latest any
		if e.latest != 0 && !math.IsNaN(e.latest) {
			if iso, ok := isoString(e.latest * 1000); ok {
				latest = iso
			} else {
				return nil, errInvalidTime
			}
		}
		authors = append(authors, jsv.O("author", e.name, "email", e.email, "lines", float64(e.lines), "percent", percent(e.lines, total), "latest_commit", latest))
	}
	return jsv.O("repo", info.TopLevel, "path", path, "rev", revOrHead(rev), "total_lines", float64(total), "commits", float64(len(commits)), "authors", authors), nil
}
