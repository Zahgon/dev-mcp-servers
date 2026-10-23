package main

// Rendering entries as llms.txt text.

import (
	"fmt"
	"sort"
	"strings"
)

type renderOptions struct {
	title                string
	description, details optString
	optionalSections     []string
}

func render(entries []entry, opts renderOptions) string {
	optional := map[string]bool{}
	for _, s := range opts.optionalSections {
		optional[jsLower(s)] = true
	}
	sections := map[string][]entry{}
	var names []string
	for _, e := range entries {
		if _, seen := sections[e.section]; !seen {
			names = append(names, e.section)
		}
		sections[e.section] = append(sections[e.section], e)
	}
	lines := []string{"# " + jsTrim(opts.title), ""}
	if opts.description.truthy() {
		lines = append(lines, "> "+reSpaces.ReplaceAllLiteralString(jsTrim(opts.description.value), " "), "")
	}
	if opts.details.truthy() {
		lines = append(lines, jsTrim(opts.details.value), "")
	}
	sort.SliceStable(names, func(i, j int) bool {
		a, b := names[i], names[j]
		switch {
		case a == "Docs":
			return true
		case b == "Docs":
			return false
		}
		return localeCompare(a, b) < 0
	})
	item := func(e entry) string {
		line := fmt.Sprintf("- [%s](%s)", e.title, e.url)
		if e.description != "" {
			line += ": " + e.description
		}
		return line
	}
	var optionalEntries []entry
	for _, name := range names {
		if optional[jsLower(name)] {
			optionalEntries = append(optionalEntries, sections[name]...)
			continue
		}
		lines = append(lines, "## "+name, "")
		for _, e := range sections[name] {
			lines = append(lines, item(e))
		}
		lines = append(lines, "")
	}
	if len(optionalEntries) > 0 {
		lines = append(lines, "## Optional", "")
		for _, e := range optionalEntries {
			lines = append(lines, item(e))
		}
		lines = append(lines, "")
	}
	return jsTrimEnd(reBlankLines.ReplaceAllLiteralString(strings.Join(lines, "\n"), "\n\n")) + "\n"
}
