package main

// llms.txt generation from local Markdown or a local sitemap.xml, and checking against the
// convention at https://llmstxt.org. Strings are in unit form (see jsstr.go); file-system
// paths are Go strings and are converted when they enter a result.

import (
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	maxFiles     = 1000
	maxFileBytes = 1024 * 1024
	maxDepth     = 12
)

var skipDirs = map[string]bool{"node_modules": true, ".git": true, "dist": true, "build": true, ".next": true, ".nuxt": true,
	"vendor": true, "coverage": true, "target": true, "out": true, ".cache": true, "__pycache__": true}

var (
	reMarkdownFile  = jsRegexp(`\.(md|mdx|markdown)$`, "i")
	reNumberPrefix  = jsRegexp(`^\d+[-_.]\s*`, "")
	reDashes        = jsRegexp(`[-_]+`, "g")
	reWordStart     = jsRegexp(`\b\w`, "g")
	reImage         = jsRegexp(`!\[[^\]]*\]\([^)]*\)`, "g")
	reLink          = jsRegexp(`\[([^\]]+)\]\([^)]*\)`, "g")
	reEmphasis      = jsRegexp("[*_`~]+", "g")
	reTag           = jsRegexp(`<[^>]+>`, "g")
	reSpaces        = jsRegexp(`\s+`, "g")
	reFrontMatter   = jsRegexp(`^---\r?\n([\s\S]*?)\r?\n---\r?\n?`, "")
	reLineBreak     = jsRegexp(`\r?\n`, "")
	reMetaField     = jsRegexp(`^(title|description)\s*:\s*(.+)$`, "i")
	reFence         = jsRegexp("^(```|~~~)", "")
	reH1Title       = jsRegexp(`^#\s+(.+?)\s*#*\s*$`, "")
	reStructural    = jsRegexp(`^(#{1,6}\s|[-*+]\s|\d+\.\s|>|\||<|\s*$|---|===|import |export )`, "")
	reTrailingWord  = jsRegexp(`\s+\S*$`, "")
	reIndexFile     = jsRegexp(`(^|\/)(README|index)\.(md|mdx|markdown)$`, "i")
	reTrailingSlash = jsRegexp(`\/+$`, "")
	reLeadingSlash  = jsRegexp(`^\/+`, "")
	reHTMLExtension = jsRegexp(`\.html?$`, "i")
	reBlankLines    = jsRegexp(`\n{3,}`, "g")
	reURLBlock      = jsRegexp(`<url>([\s\S]*?)<\/url>`, "gi")
	reSitemapBlock  = jsRegexp(`<sitemap>([\s\S]*?)<\/sitemap>`, "gi")
	reLoc           = jsRegexp(`<loc>([\s\S]*?)<\/loc>`, "i")
)

func humanise(name string) string {
	s := reMarkdownFile.ReplaceAllLiteralString(name, "")
	s = reNumberPrefix.ReplaceAllLiteralString(s, "")
	s = reDashes.ReplaceAllLiteralString(s, " ")
	s = reWordStart.ReplaceAllStringFunc(s, strings.ToUpper)
	return jsTrim(s)
}

func stripMarkdown(s string) string {
	s = reImage.ReplaceAllLiteralString(s, "")
	s = reLink.ReplaceAllString(s, "${1}")
	s = reEmphasis.ReplaceAllLiteralString(s, "")
	s = reTag.ReplaceAllLiteralString(s, "")
	s = reSpaces.ReplaceAllLiteralString(s, " ")
	return jsTrim(s)
}

// unquote is value.replace(/^(['"])(.*)\1$/, "$2").
func unquote(value string) string {
	r := []rune(value)
	if len(r) >= 2 && (r[0] == '"' || r[0] == '\'') && r[len(r)-1] == r[0] {
		inner := string(r[1 : len(r)-1])
		if !strings.ContainsAny(inner, "\n\r\u2028\u2029") {
			return inner
		}
	}
	return value
}

// optString is a JavaScript string that may be undefined.
type optString struct {
	value string
	set   bool
}

func (o optString) truthy() bool { return o.set && o.value != "" }

type meta struct{ title, description string }

func extractMeta(text, fallbackTitle string) meta {
	body := text
	var fmTitle, fmDescription optString
	if fm := reFrontMatter.FindStringSubmatch(text); fm != nil {
		body = text[len(fm[0]):]
		for _, line := range reLineBreak.Split(fm[1], -1) {
			m := reMetaField.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			value := unquote(jsTrim(m[2]))
			if jsLower(m[1]) == "title" {
				fmTitle = optString{value, true}
			} else {
				fmDescription = optString{value, true}
			}
		}
	}
	title, description := fmTitle, fmDescription
	inCode := false
	var paragraph []string
	for _, raw := range reLineBreak.Split(body, -1) {
		line := jsTrimEnd(raw)
		if reFence.MatchString(jsTrim(line)) {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if !title.truthy() {
			if h1 := reH1Title.FindStringSubmatch(line); h1 != nil {
				title = optString{stripMarkdown(h1[1]), true}
				continue
			}
		}
		if description.truthy() {
			break
		}
		if reStructural.MatchString(line) {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, jsTrim(line))
		if unitLen(strings.Join(paragraph, " ")) > 400 {
			break
		}
	}
	if !description.truthy() && len(paragraph) > 0 {
		description = optString{stripMarkdown(strings.Join(paragraph, " ")), true}
	}
	if description.truthy() && unitLen(description.value) > 200 {
		description.value = reTrailingWord.ReplaceAllLiteralString(unitPrefix(description.value, 197), "") + "..."
	}
	out := meta{title: title.value, description: description.value}
	if !title.set {
		out.title = humanise(fallbackTitle)
	}
	return out
}

func toURL(relPath string, baseURL optString, keepExtension bool) string {
	p := relPath
	if !keepExtension {
		p = reIndexFile.ReplaceAllString(p, "${1}")
		p = reMarkdownFile.ReplaceAllLiteralString(p, "/")
		if p == "" {
			p = "/"
		}
	}
	if !baseURL.truthy() {
		if strings.HasPrefix(p, "/") {
			return p
		}
		return "/" + p
	}
	base := reTrailingSlash.ReplaceAllLiteralString(baseURL.value, "")
	tail := ""
	if strings.HasSuffix(p, "/") {
		tail = "/"
	}
	return reTrailingSlash.ReplaceAllLiteralString(base+"/"+reLeadingSlash.ReplaceAllLiteralString(p, ""), tail)
}

type entry struct {
	file, url, title, description, section string
}

func (e entry) value() *jsv.Obj {
	return jsv.O("file", e.file, "url", e.url, "title", e.title, "description", e.description, "section", e.section)
}
