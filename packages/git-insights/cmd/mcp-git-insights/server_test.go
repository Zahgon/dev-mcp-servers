package main

// The upstream suite test/server.test.mjs, ported case by case. The upstream cases that call
// safeRev, safePath, safeText, sanitizeRemoteUrl and renamePaths directly drive the same inputs
// through the tools here, so the cases also run against an external server binary.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

type commitOpts struct{ date, name, email string }

// fixtureRepo builds the upstream fixture: five commits, a rename, a deleted binary, a tag, a
// remote with credentials and one untracked file. plain is a directory outside any repository.
func fixtureRepo(t *testing.T) (repo, plain string) {
	t.Helper()
	repo = t.TempDir()
	plain = t.TempDir()
	git := func(o commitOpts, args ...string) {
		t.Helper()
		if o.date == "" {
			o.date = "2026-01-01T10:00:00Z"
		}
		if o.name == "" {
			o.name, o.email = "Alice", "alice@example.com"
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+o.date, "GIT_COMMITTER_DATE="+o.date, "GIT_AUTHOR_NAME="+o.name,
			"GIT_AUTHOR_EMAIL="+o.email, "GIT_COMMITTER_NAME=CI", "GIT_COMMITTER_EMAIL=ci@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(text), 0o644); err != nil {
			t.Fatalf("os.WriteFile(filepath.Join(repo, rel), []byte(text), 0o644): %v", err)
		}
	}
	bob := commitOpts{name: "Bob", email: "bob@example.com"}
	git(commitOpts{}, "init", "-q", "-b", "main")
	if err := os.Mkdir(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatalf("os.Mkdir(filepath.Join(repo, \"src\"), 0o755): %v", err)
	}
	write("src/a.txt", "one\ntwo\nthree\n")
	write("README.md", "# Demo\n")
	git(commitOpts{}, "add", ".")
	git(commitOpts{}, "commit", "-q", "-m", "initial commit")
	write("src/a.txt", "one\ntwo\nthree\nfour\nfive\n")
	git(commitOpts{}, "add", ".")
	bob.date = "2026-02-01T10:00:00Z"
	git(bob, "commit", "-q", "-m", "add lines to a")
	write("big.bin", string(make([]byte, 50_000)))
	git(commitOpts{}, "add", ".")
	git(commitOpts{date: "2026-03-01T10:00:00Z"}, "commit", "-q", "-m", "add binary")
	if err := os.Rename(filepath.Join(repo, "src", "a.txt"), filepath.Join(repo, "src", "b.txt")); err != nil {
		t.Fatalf("os.Rename(filepath.Join(repo, \"src\", \"a.txt\"), filepath.Join(repo, \"src\", \"b.txt\")): %v", err)
	}
	git(commitOpts{}, "add", "-A")
	bob.date = "2026-04-01T10:00:00Z"
	git(bob, "commit", "-q", "-m", "rename a to b")
	git(commitOpts{}, "rm", "-q", "big.bin")
	git(commitOpts{date: "2026-05-01T10:00:00Z"}, "commit", "-q", "-m", "remove binary")
	git(commitOpts{}, "tag", "v1.0.0")
	git(commitOpts{}, "remote", "add", "origin", "https://user:token@github.com/example/demo.git")
	write("untracked.txt", "x")
	return repo, plain
}

func mustFail(t *testing.T, r toolReply, pattern string) {
	t.Helper()
	if !r.IsError {
		t.Fatalf("expected a tool error matching %q, got %s", pattern, r.Text)
	}
	if pattern != "" && !regexp.MustCompile(pattern).MatchString(r.Text) {
		t.Fatalf("error %q does not match %q", r.Text, pattern)
	}
}

type commit struct {
	Hash    string   `json:"hash"`
	Subject string   `json:"subject"`
	Files   []string `json:"files"`
}

type logResult struct {
	Count   int      `json:"count"`
	Commits []commit `json:"commits"`
}

func TestListsTheSixReadOnlyTools(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	info := decodeRaw[struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}](t, init)
	if info.ServerInfo.Name != serverName {
		t.Fatalf("serverInfo.name: %s", init.raw)
	}
	list := decodeRaw[struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				ReadOnlyHint bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}](t, c.request("tools/list", map[string]any{}))
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if !tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s is not read-only", tool.Name)
		}
	}
	sort.Strings(names)
	if want := []string{"authors", "blame_summary", "churn", "git_log", "large_files", "repo_summary"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestRepoSummaryStripsCredentialsAndCounts(t *testing.T) {
	repo, plain := fixtureRepo(t)
	c := connected(t)
	r := decodeResult[struct {
		Branch  string `json:"branch"`
		Commits int    `json:"commits"`
		Tags    int    `json:"tags"`
		Head    struct {
			Subject string `json:"subject"`
		} `json:"head"`
		Remotes []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"remotes"`
		WorkTree struct {
			Untracked int `json:"untracked"`
			Modified  int `json:"modified"`
		} `json:"work_tree"`
	}](t, c.call("repo_summary", map[string]any{"repo": filepath.Join(repo, "src")}))
	if r.Branch != "main" || r.Commits != 5 || r.Tags != 1 || r.Head.Subject != "remove binary" {
		t.Fatalf("branch %q commits %d tags %d head %q", r.Branch, r.Commits, r.Tags, r.Head.Subject)
	}
	if len(r.Remotes) != 1 || r.Remotes[0].Name != "origin" || r.Remotes[0].URL != "https://github.com/example/demo.git" {
		t.Fatalf("remotes %+v", r.Remotes)
	}
	if r.WorkTree.Untracked != 1 || r.WorkTree.Modified != 0 {
		t.Fatalf("work tree %+v", r.WorkTree)
	}
	mustFail(t, c.call("repo_summary", map[string]any{"repo": plain}), `not inside a git repository`)
	mustFail(t, c.call("repo_summary", map[string]any{"repo": filepath.Join(plain, "nope")}), "")
}

func TestGitLogFiltersByAuthorPathDateAndMessage(t *testing.T) {
	repo, _ := fixtureRepo(t)
	c := connected(t)
	all := decodeResult[logResult](t, c.call("git_log", map[string]any{"repo": repo, "with_files": true}))
	if all.Count != 5 || all.Commits[0].Subject != "remove binary" {
		t.Fatalf("all: %+v", all)
	}
	files := slices.Clone(all.Commits[4].Files)
	sort.Strings(files)
	if !slices.Equal(files, []string{"README.md", "src/a.txt"}) {
		t.Fatalf("files of the first commit: %v", files)
	}
	count := func(args map[string]any) logResult {
		t.Helper()
		args["repo"] = repo
		return decodeResult[logResult](t, c.call("git_log", args))
	}
	if n := count(map[string]any{"author": "bob@"}).Count; n != 2 {
		t.Fatalf("bob: %d", n)
	}
	since := count(map[string]any{"since": "2026-03-15", "until": "2026-04-15"})
	if len(since.Commits) != 1 || since.Commits[0].Subject != "rename a to b" {
		t.Fatalf("since: %+v", since.Commits)
	}
	if n := count(map[string]any{"grep": "binary", "limit": 1}).Count; n != 1 {
		t.Fatalf("grep: %d", n)
	}
	if n := count(map[string]any{"path": "README.md"}).Count; n != 1 {
		t.Fatalf("path: %d", n)
	}
	if n := count(map[string]any{"rev": "HEAD~3"}).Count; n != 2 {
		t.Fatalf("rev: %d", n)
	}
	for _, bad := range []map[string]any{{"rev": "--output=/tmp/x"}, {"path": "../etc/passwd"}, {"path": "/abs"}, {"author": "--all"}, {"rev": "a..b"}} {
		bad["repo"] = repo
		if r := c.call("git_log", bad); !r.IsError {
			t.Fatalf("%v accepted: %s", bad, r.Text)
		}
	}
}

func TestBlameSummaryAttributesLinesPerAuthor(t *testing.T) {
	repo, _ := fixtureRepo(t)
	c := connected(t)
	type blame struct {
		TotalLines int `json:"total_lines"`
		Commits    int `json:"commits"`
		Authors    []struct {
			Author       string  `json:"author"`
			Lines        int     `json:"lines"`
			Percent      float64 `json:"percent"`
			LatestCommit string  `json:"latest_commit"`
		} `json:"authors"`
	}
	r := decodeResult[blame](t, c.call("blame_summary", map[string]any{"repo": repo, "path": "src/b.txt"}))
	if r.TotalLines != 5 || r.Commits != 2 || len(r.Authors) != 2 {
		t.Fatalf("blame: %+v", r)
	}
	a, b := r.Authors[0], r.Authors[1]
	if a.Author != "Alice" || a.Lines != 3 || a.Percent != 60 || b.Author != "Bob" || b.Lines != 2 || b.Percent != 40 {
		t.Fatalf("authors: %+v", r.Authors)
	}
	if b.LatestCommit != "2026-02-01T10:00:00.000Z" {
		t.Fatalf("latest_commit %q", b.LatestCommit)
	}
	old := decodeResult[blame](t, c.call("blame_summary", map[string]any{"repo": repo, "path": "src/a.txt", "rev": "v1.0.0~2", "ignore_whitespace": true}))
	if old.TotalLines != 5 {
		t.Fatalf("old total %d", old.TotalLines)
	}
	mustFail(t, c.call("blame_summary", map[string]any{"repo": repo, "path": "nope.txt"}), "")
}

type churnFile struct {
	File    string `json:"file"`
	Commits int    `json:"commits"`
	Added   int    `json:"added"`
	Binary  bool   `json:"binary"`
}

type churnResult struct {
	CommitsExamined int         `json:"commits_examined"`
	Files           []churnFile `json:"files"`
}

func TestChurnFollowsRenamesAndCountsBinariesAuthorsAggregatesByEmail(t *testing.T) {
	repo, _ := fixtureRepo(t)
	c := connected(t)
	r := decodeResult[churnResult](t, c.call("churn", map[string]any{"repo": repo}))
	if r.CommitsExamined != 5 {
		t.Fatalf("commits_examined %d", r.CommitsExamined)
	}
	byFile := map[string]churnFile{}
	for _, f := range r.Files {
		byFile[f.File] = f
	}
	if f := byFile["src/b.txt"]; f.Commits != 3 || f.Added != 5 {
		t.Fatalf("rename is followed to the new name: %+v", f)
	}
	if f := byFile["big.bin"]; !f.Binary || f.Commits != 2 {
		t.Fatalf("big.bin: %+v", f)
	}
	limited := decodeResult[churnResult](t, c.call("churn", map[string]any{"repo": repo, "path": "src", "limit": 1, "since": "2026-01-15"}))
	if len(limited.Files) != 1 || limited.CommitsExamined != 2 {
		t.Fatalf("limited: %+v", limited)
	}
	a := decodeResult[struct {
		Commits int `json:"commits"`
		Authors []struct {
			Email       string  `json:"email"`
			Commits     int     `json:"commits"`
			Percent     float64 `json:"percent"`
			FirstCommit string  `json:"first_commit"`
			LastCommit  string  `json:"last_commit"`
		} `json:"authors"`
	}](t, c.call("authors", map[string]any{"repo": repo}))
	if a.Commits != 5 || len(a.Authors) != 2 {
		t.Fatalf("authors: %+v", a)
	}
	x, y := a.Authors[0], a.Authors[1]
	if x.Email != "alice@example.com" || x.Commits != 3 || x.Percent != 60 || y.Email != "bob@example.com" || y.Commits != 2 || y.Percent != 40 {
		t.Fatalf("authors: %+v", a.Authors)
	}
	if x.FirstCommit != "2026-01-01T10:00:00Z" || x.LastCommit != "2026-05-01T10:00:00Z" {
		t.Fatalf("alice dates %q %q", x.FirstCommit, x.LastCommit)
	}
}

func TestLargeFilesReadsTheTreeAndTheWholeHistory(t *testing.T) {
	repo, _ := fixtureRepo(t)
	c := connected(t)
	type files struct {
		Scope string `json:"scope"`
		Files []struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		} `json:"files"`
	}
	tree := decodeResult[files](t, c.call("large_files", map[string]any{"repo": repo, "limit": 2}))
	if len(tree.Files) != 2 || tree.Files[0].Path != "src/b.txt" {
		t.Fatalf("tree: %+v", tree.Files)
	}
	for _, f := range tree.Files {
		if f.Path == "big.bin" {
			t.Fatalf("large_files scope tree: big.bin listed, want it absent (deleted before HEAD): %+v", tree.Files)
		}
	}
	history := decodeResult[files](t, c.call("large_files", map[string]any{"repo": repo, "include_history": true, "limit": 1}))
	if history.Scope != "history" || history.Files[0].Path != "big.bin" || history.Files[0].Size != 50_000 {
		t.Fatalf("history: %+v", history)
	}
	atTag := decodeResult[files](t, c.call("large_files", map[string]any{"repo": repo, "rev": "v1.0.0~1"}))
	found := false
	for _, f := range atTag.Files {
		found = found || f.Path == "big.bin"
	}
	if !found {
		t.Fatalf("big.bin missing at v1.0.0~1: %+v", atTag.Files)
	}
}

func TestArgumentValidators(t *testing.T) {
	repo, _ := fixtureRepo(t)
	gitIn(t, repo, "branch", "feature/x-1")
	c := connected(t)
	logOf := func(args map[string]any) toolReply {
		args["repo"] = repo
		return c.call("git_log", args)
	}
	// safeRev: absent is HEAD, branch names and ancestry suffixes pass through.
	if r := decodeResult[struct {
		Rev string `json:"rev"`
	}](t, logOf(map[string]any{})); r.Rev != "HEAD" {
		t.Fatalf("default rev %q", r.Rev)
	}
	for _, rev := range []string{"feature/x-1", "HEAD~2^1"} {
		r := decodeResult[struct {
			Rev   string `json:"rev"`
			Count int    `json:"count"`
		}](t, logOf(map[string]any{"rev": rev}))
		if r.Rev != rev || r.Count == 0 {
			t.Fatalf("rev %q: %+v", rev, r)
		}
	}
	for _, rev := range []string{"-x", "main..dev", "refs/heads/x.lock"} {
		mustFail(t, logOf(map[string]any{"rev": rev}), `is not an acceptable revision`)
	}
	// safePath: a leading ./ is dropped; .. segments and leading dashes are refused.
	if r := decodeResult[logResult](t, logOf(map[string]any{"path": "./src/b.txt"})); r.Count != 1 {
		t.Fatalf("./src/b.txt: %+v", r)
	}
	mustFail(t, logOf(map[string]any{"path": "src/../../x"}), `must not contain \.\. segments`)
	mustFail(t, logOf(map[string]any{"path": "-rf"}), `must be relative to the repository root`)
	// safeText: free text may not start with - or hold a newline.
	mustFail(t, logOf(map[string]any{"author": "--help"}), `^author must be at most 200 characters`)
	mustFail(t, logOf(map[string]any{"grep": "a\nb"}), `^grep must be at most 200 characters`)
	// sanitizeRemoteUrl: credentials are removed, scp-style addresses are kept.
	gitIn(t, repo, "remote", "set-url", "origin", "https://user:secret@host/x.git")
	gitIn(t, repo, "remote", "add", "scp", "git@github.com:a/b.git")
	r := decodeResult[struct {
		Remotes []struct {
			URL string `json:"url"`
		} `json:"remotes"`
	}](t, c.call("repo_summary", map[string]any{"repo": repo}))
	var urls []string
	for _, rm := range r.Remotes {
		urls = append(urls, rm.URL)
	}
	if !slices.Equal(urls, []string{"https://host/x.git", "git@github.com:a/b.git"}) {
		t.Fatalf("remote urls %v", urls)
	}
}

func TestRenameParsing(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(filepath.Dir(p), 0o755): %v", err)
		}
		if err := os.WriteFile(p, []byte(strings.Repeat(rel+"\n", 20)), 0o644); err != nil {
			t.Fatalf("os.WriteFile(p, []byte(strings.Repeat(rel+\"\\n\", 20)), 0o644): %v", err)
		}
	}
	for _, f := range []string{"src/a.ts", "src/x.ts", "a.ts", "plain.ts"} {
		write(f)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "start")
	// git --numstat spells these renames src/{a.ts => b.ts}, src/{ => lib}/x.ts and a.ts => dir/b.ts.
	if err := os.MkdirAll(filepath.Join(repo, "src", "lib"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(filepath.Join(repo, \"src\", \"lib\"), 0o755): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(filepath.Join(repo, \"dir\"), 0o755): %v", err)
	}
	gitIn(t, repo, "mv", "src/a.ts", "src/b.ts")
	gitIn(t, repo, "mv", "src/x.ts", "src/lib/x.ts")
	gitIn(t, repo, "mv", "a.ts", "dir/b.ts")
	gitIn(t, repo, "commit", "-q", "-m", "renames")
	c := connected(t)
	r := decodeResult[churnResult](t, c.call("churn", map[string]any{"repo": repo}))
	got := map[string]int{}
	for _, f := range r.Files {
		got[f.File] = f.Commits
	}
	want := map[string]int{"src/b.ts": 2, "src/lib/x.ts": 2, "dir/b.ts": 2, "plain.ts": 1}
	if len(got) != len(want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("files %v, want %v", got, want)
		}
	}
}

// gitIn runs git in dir with a fixed identity and date.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T10:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T10:00:00Z", "GIT_AUTHOR_NAME=Alice",
		"GIT_AUTHOR_EMAIL=alice@example.com", "GIT_COMMITTER_NAME=CI", "GIT_COMMITTER_EMAIL=ci@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
