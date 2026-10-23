// Command mcp-git-insights is the git-insights MCP server on stdio: read-only statistics for a
// local git repository. It runs an allowlisted set of git commands (rev-parse, log, blame, ls-tree,
// rev-list, cat-file, status, remote, tag) with validated arguments and never writes.
package main

import (
	"errors"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "git-insights"
	serverVersion = "0.1.0"
)

const instructions = "Read-only statistics for a local git repository: repo_summary (branch, head, remotes, counts), git_log (commits with filters), blame_summary (who owns the lines of a file), " +
	"churn (files changed most often, with lines added and removed), authors (commits per author with first and last dates) and large_files (biggest blobs in a tree or in history). " +
	"Every tool takes a repo path. Nothing is written, fetched or checked out; remote URLs are shown with credentials removed."

func newServer() *mcp.Server {
	return &mcp.Server{Name: serverName, Version: serverVersion, Instructions: instructions, Tools: tools()}
}

func main() { mcp.Main(newServer()) }

// guarded turns a handler's error into a tool error: a gitError shows its message, anything else
// is an unexpected error. Argument validation failures keep the SDK's wording.
func guarded(fn func(a mcp.Args) (any, error)) func(*jsv.Obj) mcp.Result {
	return func(args *jsv.Obj) mcp.Result {
		v, err := fn(mcp.Args{Obj: args})
		if err == nil {
			return mcp.OK(v)
		}
		var argErr mcp.ArgError
		var gitErr *gitError
		switch {
		case errors.As(err, &argErr), errors.As(err, &gitErr):
			return mcp.Fail(err.Error())
		}
		return mcp.Fail("unexpected error: " + err.Error())
	}
}

// unexpected is an error a JavaScript handler would hit as a runtime exception.
func unexpected(msg string) error { return errors.New(msg) }

func str(desc string, min, max int) *jsv.Obj {
	o := jsv.O("type", "string", "minLength", float64(min), "maxLength", float64(max))
	if desc != "" {
		o.Set("description", desc)
	}
	return o
}

func integer(min, max int, desc string) *jsv.Obj {
	o := jsv.O("type", "integer", "minimum", float64(min), "maximum", float64(max))
	if desc != "" {
		o.Set("description", desc)
	}
	return o
}

func boolean(desc string) *jsv.Obj {
	o := jsv.O("type", "boolean")
	if desc != "" {
		o.Set("description", desc)
	}
	return o
}

const (
	repoDesc  = "Path to the repository (any directory inside the work tree)."
	revDesc   = "Branch, tag, commit or HEAD~n. Default HEAD."
	sinceDesc = "Only commits after this date, in any form git accepts, for example 2026-01-01 or '3 months ago'."
	untilDesc = "Only commits before this date."
	pathDesc  = "Limit to this file or directory, relative to the repository root."
)

func tools() []mcp.Tool {
	readOnly := func() *jsv.Obj {
		return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", false)
	}
	tool := func(name, title, description string, props *jsv.Obj, required []string, handler func(mcp.Args) (any, error)) mcp.Tool {
		schema := jsv.O("type", "object", "properties", props, "required", jsv.Strings(required), "additionalProperties", false, "$schema", "http://json-schema.org/draft-07/schema#")
		spec := jsv.O("title", title, "description", description, "inputSchema", schema, "annotations", readOnly(), "execution", jsv.O("taskSupport", "forbidden"))
		return mcp.Tool{Name: name, Spec: spec, Handler: guarded(func(a mcp.Args) (any, error) {
			a.Tool = name
			return handler(a)
		})}
	}
	repo := func() *jsv.Obj { return str(repoDesc, 1, 4096) }
	rev := func() *jsv.Obj { return str(revDesc, 1, 200) }
	since := func() *jsv.Obj { return str(sinceDesc, 1, 64) }
	until := func() *jsv.Obj { return str(untilDesc, 1, 64) }
	path := func() *jsv.Obj { return str(pathDesc, 1, 1024) }
	return []mcp.Tool{
		tool("repo_summary", "Summarise a repository",
			"Return the repository's top-level path, current branch, HEAD commit (hash, author, date, subject), remotes with credential-free URLs, number of commits reachable from HEAD, "+
				"number of tags and local branches, and the count of modified or untracked files in the work tree.",
			jsv.O("repo", repo()), []string{"repo"}, repoSummary),
		tool("git_log", "List commits",
			"List commits reachable from rev (default HEAD), newest first, with hash, short hash, author name and email, ISO date, subject and (optionally) the files touched. "+
				"Filters: path, author (substring of name or email), since, until, grep (substring of the message), no_merges. limit is 1 to 500, default 50.",
			jsv.O("repo", repo(), "rev", rev(), "limit", integer(1, 500, ""), "path", path(), "author", str("", 1, 200), "since", since(), "until", until(),
				"grep", str("", 1, 200), "no_merges", boolean(""), "with_files", boolean("Include the list of files changed by each commit. Default false.")),
			[]string{"repo"}, gitLog),
		tool("blame_summary", "Summarise blame for a file",
			"Attribute every line of one file at rev (default HEAD) to its last author and return, per author, the number of lines, the percentage, and the most recent commit date; plus the total "+
				"line count and the number of distinct commits involved. With ignore_whitespace, whitespace-only changes are attributed to the earlier author.",
			jsv.O("repo", repo(), "path", str("File path relative to the repository root.", 1, 1024), "rev", rev(), "ignore_whitespace", boolean("")),
			[]string{"repo", "path"}, blameSummary),
		tool("churn", "Files changed most often",
			"Rank files by how many commits touched them, with lines added and removed, over the commits reachable from rev (default HEAD) and optionally limited by since, until and path. "+
				"Renames are followed and reported under the newest name. Binary files count commits but not lines. limit is 1 to 200, default 25. Also returns the number of commits examined.",
			jsv.O("repo", repo(), "rev", rev(), "since", since(), "until", until(), "path", path(), "limit", integer(1, 200, ""), "max_commits", integer(1, 20_000, "Cap on commits examined. Default 5000.")),
			[]string{"repo"}, churn),
		tool("authors", "Commits per author",
			"Count commits per author (grouped by email) over the commits reachable from rev (default HEAD), optionally limited by since, until and path, with each author's first and last commit dates. "+
				"Merge commits are excluded unless include_merges is true.",
			jsv.O("repo", repo(), "rev", rev(), "since", since(), "until", until(), "path", path(), "include_merges", boolean(""), "limit", integer(1, 500, "Default 50.")),
			[]string{"repo"}, authors),
		tool("large_files", "Largest files",
			"List the largest blobs in the tree at rev (default HEAD) with their paths and sizes in bytes. With include_history, instead scan every object reachable from any ref "+
				"(git rev-list --objects --all piped to cat-file --batch-check) and report the largest blobs ever committed, including ones no longer in the tree, which is what makes a clone big. "+
				"limit is 1 to 100, default 20.",
			jsv.O("repo", repo(), "rev", rev(), "limit", integer(1, 100, ""), "include_history", boolean("")),
			[]string{"repo"}, largeFiles),
	}
}
