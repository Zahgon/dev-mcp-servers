package main

// Reading <url> and <sitemap> entries from a sitemap and turning URLs into entries.

import "strings"

// parseSitemap returns the <url> locations and the child sitemaps of a sitemap index.
func parseSitemap(xml string) (urls, children []string) {
	decode := func(s string) string {
		for _, r := range [][2]string{{"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", `"`}, {"&#39;", "'"}} {
			s = strings.ReplaceAll(s, r[0], r[1])
		}
		return jsTrim(s)
	}
	for _, m := range reURLBlock.FindAllStringSubmatch(xml, -1) {
		if loc := reLoc.FindStringSubmatch(m[1]); loc != nil && loc[1] != "" {
			urls = append(urls, decode(loc[1]))
		}
	}
	for _, m := range reSitemapBlock.FindAllStringSubmatch(xml, -1) {
		if loc := reLoc.FindStringSubmatch(m[1]); loc != nil && loc[1] != "" {
			children = append(children, decode(loc[1]))
		}
	}
	return urls, children
}

func entriesFromSitemap(urls, includePrefix, excludePrefix []string) []entry {
	var out []entry
	hasPrefix := func(path string, prefixes []string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		return false
	}
	for _, u := range urls {
		path, ok := urlPathname(u)
		if !ok {
			continue
		}
		if len(includePrefix) > 0 && !hasPrefix(path, includePrefix) {
			continue
		}
		if hasPrefix(path, excludePrefix) {
			continue
		}
		var segments []string
		for _, s := range strings.Split(path, "/") {
			if s != "" {
				segments = append(segments, s)
			}
		}
		title, section := "Home", "Pages"
		if len(segments) > 0 {
			title = humanise(reHTMLExtension.ReplaceAllLiteralString(segments[len(segments)-1], ""))
		}
		if len(segments) > 1 {
			section = humanise(segments[0])
		}
		out = append(out, entry{file: u, url: u, title: title, section: section})
	}
	return out
}
