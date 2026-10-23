// Command check-server-json validates every packages/<name>/server.json against
// scripts/server.schema.json (the MCP registry schema, draft-07) and checks the
// repository's own rules: the server name, the OCI package entry and its stdio
// transport, repository.subfolder, and the Go main package under cmd/mcp-<name>.
// Run it from the repository root: go run ./scripts/check-server-json
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/ajv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

func main() { os.Exit(run(os.Stdout, os.Stderr)) }

// nodeError words a file error the way Node's fs module does.
func nodeError(err error, path string) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("ENOENT: no such file or directory, open '%s'", path)
	case strings.Contains(err.Error(), "is a directory"):
		return "EISDIR: illegal operation on a directory, read"
	case strings.Contains(err.Error(), "not a directory"):
		return fmt.Sprintf("ENOTDIR: not a directory, open '%s'", path)
	}
	return err.Error()
}

func readJSON(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New(nodeError(err, path))
	}
	return jsv.Parse(jsv.ValidUTF8(raw))
}

// lessUTF16 is the default Array#sort order.
func lessUTF16(a, b string) bool {
	x, y := jsv.UTF16Units(a), jsv.UTF16Units(b)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// templateString is `${v}` for a property read that may be undefined.
func templateString(o *jsv.Obj, key string) string {
	v, ok := o.Get(key)
	return jsv.StringOrUndefined(v, ok)
}

func hasGoFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

func ociEntry(server *jsv.Obj) *jsv.Obj {
	list, _ := jsv.Prop(server, "packages").([]any)
	for _, p := range list {
		if o := jsv.AsObj(p); o != nil && jsv.Prop(o, "registryType") == "oci" {
			return o
		}
	}
	return nil
}

func problemsOf(validate *ajv.Validator, server *jsv.Obj, name, dir string) ([]string, error) {
	var problems []string
	valid, errs, err := validate.Validate(server)
	if err != nil {
		return nil, err
	}
	if !valid {
		for _, e := range errs {
			path, _ := jsv.Prop(e, "instancePath").(string)
			if path == "" {
				path = "/"
			}
			problems = append(problems, path+" "+jsv.ToString(jsv.Prop(e, "message")))
		}
	}
	if jsv.Prop(server, "name") != "io.github.basitalisandhu/"+name {
		problems = append(problems, "name should be io.github.basitalisandhu/"+name)
	}
	if oci := ociEntry(server); oci == nil {
		problems = append(problems, "no oci package entry")
	} else {
		want := "ghcr.io/basitalisandhu/mcp-" + name + ":" + templateString(server, "version")
		if jsv.Prop(oci, "identifier") != want {
			problems = append(problems, "oci identifier "+templateString(oci, "identifier")+" should be "+want)
		}
		if jsv.Prop(jsv.AsObj(jsv.Prop(oci, "transport")), "type") != "stdio" {
			problems = append(problems, "transport must be stdio")
		}
	}
	if jsv.Prop(jsv.AsObj(jsv.Prop(server, "repository")), "subfolder") != "packages/"+name {
		problems = append(problems, "repository.subfolder should be packages/"+name)
	}
	if !hasGoFile(filepath.Join(dir, "cmd", "mcp-"+name)) {
		problems = append(problems, "packages/"+name+"/cmd/mcp-"+name+" must hold the server's main package")
	}
	return problems, nil
}

func run(stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	schema, err := readJSON(filepath.Join(root, "scripts", "server.schema.json"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	validate, err := ajv.New(ajv.Draft07).Compile(schema)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	entries, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		fmt.Fprintln(stderr, nodeError(err, filepath.Join(root, "packages")))
		return 1
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	sort.SliceStable(names, func(i, j int) bool { return lessUTF16(names[i], names[j]) })
	failures := 0
	for _, name := range names {
		dir := filepath.Join(root, "packages", name)
		parsed, err := readJSON(filepath.Join(dir, "server.json"))
		if err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", name, err.Error())
			failures++
			continue
		}
		server := jsv.AsObj(parsed)
		if server == nil {
			fmt.Fprintf(stderr, "%s: server.json is not a JSON object\n", name)
			failures++
			continue
		}
		problems, err := problemsOf(validate, server, name, dir)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(problems) > 0 {
			failures++
			fmt.Fprintf(stderr, "%s:\n  %s\n", name, strings.Join(problems, "\n  "))
			continue
		}
		description, _ := jsv.Prop(server, "description").(string)
		fmt.Fprintf(stdout, "%s: ok (%d/100 chars)\n", name, jsv.UTF16Len(description))
	}
	if failures > 0 {
		fmt.Fprintf(stderr, "%d server.json file(s) failed\n", failures)
		return 1
	}
	return 0
}
