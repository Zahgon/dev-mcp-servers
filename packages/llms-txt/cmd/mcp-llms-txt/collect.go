package main

// Collecting Markdown files from a local directory tree, in the order Node's readdir and
// localeCompare give them.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type skip struct{ file, reason string }

type collectOptions struct {
	maxFiles      int
	baseURL       optString
	keepExtension bool
	includeDirs   []string
}

type collected struct {
	entries []entry
	skipped []skip
	scanned int
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	return rel
}

func collectMarkdown(dir string, opts collectOptions) (collected, error) {
	root := resolvePath(dir)
	if st, err := os.Stat(fsPath(root)); err != nil || !st.IsDir() {
		return collected{}, errors.New(toUnits(root + " is not a directory"))
	}
	maxFiles := min(opts.maxFiles, maxFiles)
	var files []string
	var out collected
	var walk func(current string, depth int)
	walk = func(current string, depth int) {
		if depth > maxDepth || len(files) >= maxFiles {
			return
		}
		items, err := os.ReadDir(fsPath(current))
		if err != nil {
			file := relative(root, current)
			if file == "" {
				file = "."
			}
			out.skipped = append(out.skipped, skip{toUnits(file), "cannot read directory: " + toUnits(nodeFSMessage(err, "scandir", current))})
			return
		}
		type item struct {
			name, unitName string
			entry          os.DirEntry
		}
		list := make([]item, len(items))
		for k, e := range items {
			name := decodeUTF8([]byte(e.Name()))
			list[k] = item{name, toUnits(name), e}
		}
		sort.SliceStable(list, func(a, b int) bool { return localeCompare(list[a].unitName, list[b].unitName) < 0 })
		for _, it := range list {
			full := filepath.Join(current, it.name)
			if len(files) >= maxFiles {
				out.skipped = append(out.skipped, skip{toUnits(relative(root, full)), fmt.Sprintf("beyond the %d file limit", maxFiles)})
				continue
			}
			if strings.HasPrefix(it.name, ".") || skipDirs[it.name] {
				continue
			}
			switch {
			case it.entry.IsDir():
				if depth == 0 && len(opts.includeDirs) > 0 && !contains(opts.includeDirs, it.unitName) {
					continue
				}
				walk(full, depth+1)
			case it.entry.Type().IsRegular() && reMarkdownFile.MatchString(it.unitName):
				if depth == 0 && len(opts.includeDirs) > 0 && !contains(opts.includeDirs, ".") {
					continue
				}
				files = append(files, full)
			}
		}
	}
	walk(root, 0)
	for _, file := range files {
		out.scanned++
		rel := toUnits(relative(root, file))
		st, err := statFile(file)
		if err != nil {
			return collected{}, errors.New(toUnits(err.Error()))
		}
		if st.Size() > maxFileBytes {
			out.skipped = append(out.skipped, skip{rel, fmt.Sprintf("larger than %d bytes", maxFileBytes)})
			continue
		}
		text, err := readUTF8(file)
		if err != nil {
			return collected{}, errors.New(toUnits(err.Error()))
		}
		parts := strings.Split(rel, "/")
		m := extractMeta(toUnits(text), parts[len(parts)-1])
		section := "Docs"
		if len(parts) > 1 {
			section = humanise(parts[0])
		}
		out.entries = append(out.entries, entry{file: rel, url: toURL(rel, opts.baseURL, opts.keepExtension), title: m.title, description: m.description, section: section})
	}
	return out, nil
}
