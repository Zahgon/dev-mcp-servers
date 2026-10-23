package main

// Read-only git access. Every command is spawned without a shell, with a fixed argument list, a timeout,
// an output cap and GIT_OPTIONAL_LOCKS=0 so that nothing in the repository is modified.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	timeoutMS      = 20_000
	maxOutputBytes = 32 * 1024 * 1024
)

// gitError is an error whose message is shown to the caller as it is.
type gitError struct{ msg string }

func (e *gitError) Error() string { return e.msg }

func gitErrorf(format string, args ...any) error { return &gitError{fmt.Sprintf(format, args...)} }

func gitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat", "LC_ALL=C")
}

// cappedBuffer collects output and calls overflow once when more than MaxOutputBytes arrive.
type cappedBuffer struct {
	mu       sync.Mutex
	data     []byte
	overflow func()
	over     bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	if len(b.data)+len(p) > maxOutputBytes {
		b.over = true
		b.overflow()
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// spawnMessage is the message Node gives an error raised while spawning the child.
func spawnMessage(err error) string {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, syscall.ENOENT) {
		return "spawn git ENOENT"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errno, ok := pathErr.Err.(syscall.Errno); ok {
			return "spawn git " + errnoName(errno)
		}
	}
	return err.Error()
}

func errnoName(e syscall.Errno) string {
	switch e {
	case syscall.EACCES:
		return "EACCES"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	}
	return e.Error()
}

// firstLine is s.trim().split("\n")[0].
func firstLine(s string) string {
	line, _, _ := strings.Cut(jsv.Trim(s), "\n")
	return line
}

// runGit is execFile("git", ["--no-pager", ...args]) with a timeout and maxBuffer: a timeout or an
// output overflow kills the child, and Node reports both as a killed process.
func runGit(repo string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutMS*time.Millisecond)
	defer cancel()
	argv := append([]string{"--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	killed := false
	var once sync.Once
	kill := func() {
		once.Do(func() {
			killed = true
			cancel()
		})
	}
	stdout := &cappedBuffer{overflow: kill}
	stderr := &cappedBuffer{overflow: kill}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		killed = true
	}
	if err == nil && !killed {
		return decodeUTF8(stdout.data), nil
	}
	if killed {
		return "", gitErrorf("git %s exceeded %d ms", args[0], timeoutMS)
	}
	errText := decodeUTF8(stderr.data)
	if errText == "" {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			errText = "Command failed: git " + strings.Join(argv, " ")
		} else {
			errText = spawnMessage(err)
		}
	}
	return "", gitErrorf("git %s failed: %s", args[0], firstLine(errText))
}

// pipeGit runs two git commands with the first one's stdout piped into the second (used for
// cat-file --batch-check). Only the second command's exit status and stderr are checked.
func pipeGit(repo string, first, second []string) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := exec.CommandContext(ctx, "git", append([]string{"--no-pager"}, first...)...)
	b := exec.CommandContext(ctx, "git", append([]string{"--no-pager"}, second...)...)
	for _, c := range []*exec.Cmd{a, b} {
		c.Dir = repo
		c.Env = gitEnv()
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return "", &gitError{err.Error()}
	}
	a.Stdout = pw
	b.Stdin = pr
	var stderr strings.Builder
	b.Stderr = &stderr
	out, err := b.StdoutPipe()
	if err != nil {
		pr.Close()
		pw.Close()
		return "", &gitError{err.Error()}
	}
	if err := a.Start(); err != nil {
		pr.Close()
		pw.Close()
		return "", &gitError{spawnMessage(err)}
	}
	if err := b.Start(); err != nil {
		pr.Close()
		pw.Close()
		a.Wait()
		return "", &gitError{spawnMessage(err)}
	}
	pr.Close()
	pw.Close()

	type result struct {
		data     []byte
		overflow bool
	}
	read := make(chan result, 1)
	go func() {
		var data []byte
		buf := make([]byte, 64*1024)
		for {
			n, rerr := out.Read(buf)
			data = append(data, buf[:n]...)
			if len(data) > maxOutputBytes {
				read <- result{overflow: true}
				return
			}
			if rerr != nil {
				read <- result{data: data}
				return
			}
		}
	}()
	timer := time.NewTimer(timeoutMS * time.Millisecond)
	defer timer.Stop()
	var res result
	select {
	case res = <-read:
	case <-timer.C:
		cancel()
		a.Wait()
		b.Wait()
		return "", gitErrorf("git %s | git %s exceeded %d ms", first[0], second[0], timeoutMS)
	}
	if res.overflow {
		cancel()
		a.Wait()
		b.Wait()
		return "", &gitError{"output exceeds the size cap"}
	}
	berr := b.Wait()
	a.Wait()
	if berr != nil {
		return "", gitErrorf("git %s failed: %s", second[0], firstLine(stderr.String()))
	}
	return decodeUTF8(res.data), nil
}

// repoInfo describes the repository a tool reads.
type repoInfo struct {
	Path     string
	TopLevel string
	GitDir   string
	Bare     bool
}

// resolvePath is path.resolve(p): relative paths are taken against the process working directory.
func resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		cwd = "/"
	}
	return filepath.Join(cwd, p)
}

func resolveFrom(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

var notARepo = regexp.MustCompile(`(?i)not a git repository`)

func openRepo(input string) (repoInfo, error) {
	abs := resolvePath(input)
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return repoInfo{}, gitErrorf("%s is not a directory", abs)
	}
	out, err := runGit(abs, []string{"rev-parse", "--is-bare-repository", "--git-dir", "--show-toplevel"})
	if err != nil {
		if notARepo.MatchString(err.Error()) {
			return repoInfo{}, gitErrorf("%s is not inside a git repository", abs)
		}
		if bareOut, berr := runGit(abs, []string{"rev-parse", "--is-bare-repository", "--git-dir"}); berr == nil {
			bare := strings.Split(jsv.Trim(bareOut), "\n")
			if bare[0] == "true" && len(bare) > 1 {
				return repoInfo{Path: abs, TopLevel: abs, GitDir: resolveFrom(abs, bare[1]), Bare: true}, nil
			}
		}
		return repoInfo{}, err
	}
	lines := strings.Split(jsv.Trim(out), "\n")
	info := repoInfo{Path: abs, TopLevel: abs, Bare: lines[0] == "true"}
	if len(lines) > 1 {
		info.GitDir = resolveFrom(abs, lines[1])
	}
	if len(lines) > 2 {
		info.TopLevel = lines[2]
	}
	return info, nil
}

// cwd is the directory git commands run in.
func (r repoInfo) cwd() string {
	if r.Bare {
		return r.Path
	}
	return r.TopLevel
}

var revPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/^~-]{0,199}$`)

// safeRev returns rev, or fallback when rev is absent.
func safeRev(rev *string, fallback string) (string, error) {
	if rev == nil {
		return fallback, nil
	}
	r := *rev
	if !revPattern.MatchString(r) || strings.Contains(r, "..") || strings.Contains(r, "//") || strings.HasSuffix(r, ".lock") {
		return "", gitErrorf("%s is not an acceptable revision (branch, tag, commit or HEAD~n)", jsv.Stringify(r, 0))
	}
	return r, nil
}

var drivePrefix = regexp.MustCompile(`^[A-Za-z]:`)

func safePath(p string) (string, error) {
	norm := strings.ReplaceAll(p, `\`, "/")
	if jsv.UTF16Len(norm) > 1024 || strings.Contains(norm, "\x00") {
		return "", &gitError{"path is too long or contains NUL"}
	}
	if strings.HasPrefix(norm, "/") || drivePrefix.MatchString(norm) || strings.HasPrefix(norm, "-") {
		return "", gitErrorf("%s must be relative to the repository root and not start with -", jsv.Stringify(p, 0))
	}
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return "", gitErrorf("%s must not contain .. segments", jsv.Stringify(p, 0))
		}
	}
	return strings.TrimPrefix(norm, "./"), nil
}

// safeText checks a free-text filter; an absent value passes through as nil.
func safeText(value *string, label string, max int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	v := *value
	if jsv.UTF16Len(v) > max || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\x00\n\r") {
		return nil, gitErrorf("%s must be at most %d characters, not start with -, and contain no newlines", label, max)
	}
	return value, nil
}

var credentials = regexp.MustCompile(`//[^@/]+@`)

// sanitizeRemoteUrl removes the first user:password@ part of a URL.
func sanitizeRemoteUrl(url string) string {
	loc := credentials.FindStringIndex(url)
	if loc == nil {
		return url
	}
	return url[:loc[0]] + "//" + url[loc[1]:]
}
