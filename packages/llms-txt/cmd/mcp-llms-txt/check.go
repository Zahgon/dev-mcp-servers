package main

// check validates llms.txt text against the convention. The text is in unit form.

import (
	"fmt"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var (
	reTitleLine   = jsRegexp(`^#\s+\S`, "")
	reH1          = jsRegexp(`^#\s+(.+)$`, "")
	reH2          = jsRegexp(`^##\s+(.+)$`, "")
	reDeepHeading = jsRegexp(`^#{3,6}\s`, "")
	reBlockquote  = jsRegexp(`^>\s?`, "")
	reListItem    = jsRegexp(`^\s*[-*+]\s+(.*)$`, "")
	reLinkItem    = jsRegexp(`^\[([^\]]+)\]\(([^)\s]+)\)(?::\s*(.*))?$`, "")
	reAbsoluteURL = jsRegexp(`^https?:\/\/`, "i")
)

type note struct {
	line    int
	message string
}

func notes(list []note) []any {
	out := []any{}
	for _, n := range list {
		out = append(out, jsv.O("line", float64(n.line), "message", n.message))
	}
	return out
}

func check(text string) *jsv.Obj {
	var errs, warnings, info []note
	lines := reLineBreak.Split(text, -1)
	var title any
	hasSummary := false
	sections := []string{}
	urls := map[string]int{}
	links := 0
	var currentSection *string
	sectionItems := 0
	sawH1, inCode := false, false
	firstContent := -1
	for i, l := range lines {
		if jsTrim(l) != "" {
			firstContent = i
			break
		}
	}
	if firstContent == -1 {
		errs = append(errs, note{1, "the file is empty"})
	} else if !reTitleLine.MatchString(lines[firstContent]) {
		errs = append(errs, note{firstContent + 1, "the first line must be an H1 title (# Project name)"})
	}
	closeSection := func(lineNo int) {
		if currentSection != nil && sectionItems == 0 {
			warnings = append(warnings, note{lineNo, fmt.Sprintf("section \"%s\" has no link items", *currentSection)})
		}
	}
	for idx, raw := range lines {
		n := idx + 1
		line := jsTrimEnd(raw)
		if reFence.MatchString(jsTrim(line)) {
			inCode = !inCode
			if inCode {
				info = append(info, note{n, "code blocks are unusual in llms.txt; the file is meant to be a short index"})
			}
			continue
		}
		if inCode {
			continue
		}
		if h1 := reH1.FindStringSubmatch(line); h1 != nil {
			if sawH1 {
				errs = append(errs, note{n, "only one H1 is allowed; use ## for sections"})
			} else {
				sawH1 = true
				title = jsTrim(h1[1])
			}
			continue
		}
		if h2 := reH2.FindStringSubmatch(line); h2 != nil {
			closeSection(n)
			name := jsTrim(h2[1])
			currentSection = &name
			sectionItems = 0
			for _, s := range sections {
				if jsLower(s) == jsLower(name) {
					warnings = append(warnings, note{n, fmt.Sprintf("duplicate section \"%s\"", name)})
					break
				}
			}
			sections = append(sections, name)
			if jsLower(name) == "optional" {
				info = append(info, note{n, "the Optional section marks links that can be skipped when context is short"})
			}
			continue
		}
		if reDeepHeading.MatchString(line) {
			warnings = append(warnings, note{n, "H3 and deeper headings are not part of the convention; use H2 sections with link lists"})
			continue
		}
		if reBlockquote.MatchString(line) {
			if currentSection == nil && !hasSummary {
				hasSummary = true
			} else if currentSection != nil {
				warnings = append(warnings, note{n, "blockquotes belong in the summary before the first section"})
			}
			continue
		}
		if jsTrim(line) == "" {
			continue
		}
		if item := reListItem.FindStringSubmatch(line); item != nil {
			if currentSection == nil {
				warnings = append(warnings, note{n, "list items before the first ## section are not part of a file list"})
				continue
			}
			sectionItems++
			link := reLinkItem.FindStringSubmatchIndex(jsTrim(item[1]))
			itemText := jsTrim(item[1])
			if link == nil {
				errs = append(errs, note{n, "section items must be markdown links: - [name](url): optional notes"})
				continue
			}
			links++
			name, url := itemText[link[2]:link[3]], itemText[link[4]:link[5]]
			if first, seen := urls[url]; seen {
				warnings = append(warnings, note{n, fmt.Sprintf("duplicate URL %s (first on line %d)", url, first)})
			} else {
				urls[url] = n
			}
			if !reAbsoluteURL.MatchString(url) && !strings.HasPrefix(url, "/") {
				info = append(info, note{n, fmt.Sprintf("relative URL %s; absolute URLs resolve from anywhere", url)})
			}
			if link[6] < 0 || jsTrim(itemText[link[6]:link[7]]) == "" {
				info = append(info, note{n, fmt.Sprintf("link \"%s\" has no notes after the colon", name)})
			}
			continue
		}
		if currentSection != nil {
			warnings = append(warnings, note{n, "paragraph text inside a section; sections should contain only link items"})
		}
	}
	closeSection(len(lines))
	if sawH1 && !hasSummary {
		warnings = append(warnings, note{1, "no blockquote summary after the title (recommended: > one-sentence description)"})
	}
	bytes := len(fromUnits(text))
	if bytes > 100_000 {
		warnings = append(warnings, note{1, fmt.Sprintf("the file is %d bytes; keep llms.txt small and put full content in llms-full.txt", bytes)})
	}
	if sawH1 && len(sections) == 0 {
		info = append(info, note{1, "no ## sections; the file carries no links"})
	}
	return jsv.O(
		"valid", len(errs) == 0,
		"errors", notes(errs),
		"warnings", notes(warnings),
		"info", notes(info),
		"stats", jsv.O("title", title, "has_summary", hasSummary, "sections", jsv.Strings(sections), "links", float64(links), "bytes", float64(bytes)),
	)
}
