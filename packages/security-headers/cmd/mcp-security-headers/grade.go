package main

// Pure grading of HTTP response security headers. No network access here.

import (
	"fmt"
	"math"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// gradedHeaders are the headers explain_header documents, in catalogue order.
var gradedHeaders = []string{
	"content-security-policy", "strict-transport-security", "x-frame-options", "x-content-type-options", "referrer-policy", "permissions-policy",
	"cross-origin-opener-policy", "cross-origin-resource-policy", "x-xss-protection", "server", "x-powered-by", "set-cookie",
}

type headerDoc struct{ purpose, recommended, reference string }

var headerDocs = map[string]headerDoc{
	"content-security-policy": {"Tells the browser which sources of scripts, styles, frames and other resources the page may load, which is the strongest mitigation for cross-site scripting and clickjacking.",
		"default-src 'self'; script-src 'self' 'nonce-<random>'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'",
		"https://developer.mozilla.org/docs/Web/HTTP/Headers/Content-Security-Policy"},
	"strict-transport-security": {"Instructs browsers to use HTTPS for every future request to the host, which blocks protocol-downgrade attacks and cookie hijacking over plain HTTP.",
		"max-age=31536000; includeSubDomains; preload", "https://developer.mozilla.org/docs/Web/HTTP/Headers/Strict-Transport-Security"},
	"x-frame-options": {"Stops other sites from framing the page, which prevents clickjacking. Superseded by the CSP frame-ancestors directive but still read by older browsers.",
		"DENY (or SAMEORIGIN if the site frames itself)", "https://developer.mozilla.org/docs/Web/HTTP/Headers/X-Frame-Options"},
	"x-content-type-options": {"Prevents MIME type sniffing, so a file served as text is never executed as a script.", "nosniff",
		"https://developer.mozilla.org/docs/Web/HTTP/Headers/X-Content-Type-Options"},
	"referrer-policy": {"Controls how much of the current URL is sent in the Referer header on navigation and subresource requests, which limits leakage of paths and query strings.",
		"strict-origin-when-cross-origin (or no-referrer for the most privacy)", "https://developer.mozilla.org/docs/Web/HTTP/Headers/Referrer-Policy"},
	"permissions-policy": {"Disables browser features such as camera, microphone, geolocation and payment for the page and for embedded frames.",
		"camera=(), microphone=(), geolocation=(), payment=(), usb=()", "https://developer.mozilla.org/docs/Web/HTTP/Headers/Permissions-Policy"},
	"cross-origin-opener-policy": {"Isolates the browsing context from cross-origin openers so another site cannot hold a reference to the window.", "same-origin",
		"https://developer.mozilla.org/docs/Web/HTTP/Headers/Cross-Origin-Opener-Policy"},
	"cross-origin-resource-policy": {"Blocks other origins from reading the resource in no-cors requests, which mitigates speculative side-channel leaks.", "same-origin (or same-site)",
		"https://developer.mozilla.org/docs/Web/HTTP/Headers/Cross-Origin-Resource-Policy"},
	"x-xss-protection": {"Legacy filter of old browsers. Modern browsers ignore it and the filter itself caused vulnerabilities; the header should be absent or 0.", "omit it, or send 0",
		"https://developer.mozilla.org/docs/Web/HTTP/Headers/X-XSS-Protection"},
	"server": {"Names the server software. Version strings help attackers pick exploits; the header carries no benefit for clients.", "omit it or send a product name without a version",
		"https://developer.mozilla.org/docs/Web/HTTP/Headers/Server"},
	"x-powered-by": {"Names the application framework. Pure information disclosure.", "omit it", "https://owasp.org/www-project-secure-headers/"},
	"set-cookie": {"Cookie attributes decide whether a cookie is sent over plain HTTP, readable from JavaScript, or attached to cross-site requests.",
		"Secure; HttpOnly; SameSite=Lax (or Strict) on every session cookie, and a __Host- prefix where possible", "https://developer.mozilla.org/docs/Web/HTTP/Headers/Set-Cookie"},
}

// headerSet is a response's headers: lower-cased names mapped to the joined value, in
// JavaScript property order, plus every Set-Cookie value separately.
type headerSet struct {
	Headers   *jsv.Obj
	SetCookie []string
	HTTPS     bool
}

// headerValue is a header value or JavaScript null (nil).
type headerValue = *string

func present(v headerValue) bool { return v != nil && *v != "" }

func valueOf(v headerValue) any {
	if v == nil {
		return nil
	}
	return *v
}

// check is one graded header; the JSON keys follow the TypeScript object spread order.
type check struct {
	header      string
	status      string
	value       any
	points      float64
	maxPoints   float64
	explanation string
	advice      string
}

func (c check) obj() *jsv.Obj {
	return jsv.O("header", c.header, "value", c.value, "max_points", c.maxPoints, "status", c.status, "points", c.points,
		"explanation", c.explanation, "recommendation", c.advice)
}

// parseCsp maps each directive name (lower-cased) to its sources; the first occurrence wins.
func parseCsp(value string) map[string][]string {
	d := map[string][]string{}
	for _, part := range strings.Split(value, ";") {
		var tokens []string
		for _, t := range splitSpace(jsv.Trim(part)) {
			if t != "" {
				tokens = append(tokens, t)
			}
		}
		if len(tokens) == 0 {
			continue
		}
		name := toLowerCase(tokens[0])
		if _, seen := d[name]; !seen {
			d[name] = tokens[1:]
		}
	}
	return d
}

func cspCheck(value, reportOnly headerValue) check {
	c := check{header: "content-security-policy", value: valueOf(value), maxPoints: 25}
	recommended := headerDocs["content-security-policy"].recommended
	if !present(value) {
		c.status, c.points = "fail", 0
		c.explanation = "No Content-Security-Policy header. The browser will load scripts from any origin and inline scripts injected by an attacker will run."
		if present(reportOnly) {
			c.explanation = "Only a Content-Security-Policy-Report-Only header is set. Report-only policies are never enforced."
		}
		c.advice = "Add: " + recommended
		return c
	}
	d := parseCsp(*value)
	var problems []string
	points := 25.0
	scriptSrc, hasScript := d["script-src"]
	defaultSrc, hasDefault := d["default-src"]
	if !hasScript {
		scriptSrc, hasScript = defaultSrc, hasDefault
	}
	if !hasDefault && !hasScript {
		problems = append(problems, "neither default-src nor script-src is set, so script loading is unrestricted")
		points -= 15
	}
	if hasScript {
		if contains(scriptSrc, "'unsafe-inline'") && !anyPrefix(scriptSrc, "'nonce-", "'sha256-", "'sha384-", "'sha512-") {
			problems = append(problems, "script-src allows 'unsafe-inline' without a nonce or hash, which permits injected inline scripts")
			points -= 10
		}
		if contains(scriptSrc, "'unsafe-eval'") {
			problems = append(problems, "script-src allows 'unsafe-eval'")
			points -= 5
		}
		if contains(scriptSrc, "*") || contains(scriptSrc, "http:") || contains(scriptSrc, "https:") {
			problems = append(problems, "script-src allows any host (* or a bare scheme)")
			points -= 10
		}
		if contains(scriptSrc, "data:") {
			problems = append(problems, "script-src allows data: URIs")
			points -= 5
		}
	}
	if _, ok := d["object-src"]; !ok && !contains(defaultSrc, "'none'") {
		problems = append(problems, "object-src is not set to 'none' (plugins such as Flash-style embeds can run scripts)")
		points -= 3
	}
	if _, ok := d["base-uri"]; !ok {
		problems = append(problems, "base-uri is not set, so an injected <base> tag can redirect relative script URLs")
		points -= 3
	}
	if _, ok := d["frame-ancestors"]; !ok {
		problems = append(problems, "frame-ancestors is not set, so clickjacking protection relies on X-Frame-Options alone")
		points -= 2
	}
	c.points = math.Max(0, points)
	switch {
	case c.points == 25:
		c.status = "pass"
	case c.points >= 12:
		c.status = "warn"
	default:
		c.status = "fail"
	}
	if len(problems) > 0 {
		c.explanation = "Policy present but: " + strings.Join(problems, "; ") + "."
		c.advice = "Tighten the policy. A strong baseline: " + recommended
	} else {
		c.explanation = "Policy present with restricted script sources, object-src, base-uri and frame-ancestors."
		c.advice = "Keep it, and add a nonce-based script-src if you still rely on host allowlists."
	}
	return c
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func anyPrefix(list []string, prefixes ...string) bool {
	for _, x := range list {
		for _, p := range prefixes {
			if strings.HasPrefix(x, p) {
				return true
			}
		}
	}
	return false
}

func hstsCheck(value headerValue, https bool) check {
	c := check{header: "strict-transport-security", value: valueOf(value), maxPoints: 20}
	recommended := headerDocs["strict-transport-security"].recommended
	if !https {
		c.status, c.explanation, c.advice = "info", "The URL was fetched over plain HTTP; browsers ignore HSTS on HTTP responses.", "Serve the site over HTTPS and send HSTS there."
		return c
	}
	if !present(value) {
		c.status, c.explanation, c.advice = "fail", "No Strict-Transport-Security header, so a first visit over http:// can be downgraded.", "Add: "+recommended
		return c
	}
	v := *value
	maxAge := math.NaN()
	if m := maxAgePattern.FindStringSubmatch(v); m != nil {
		maxAge = jsNumber(m[1])
	}
	subdomains := includeSubDomainsPattern.MatchString(v)
	preload := preloadPattern.MatchString(v)
	if math.IsNaN(maxAge) || math.IsInf(maxAge, 0) {
		c.status, c.explanation, c.advice = "fail", "HSTS header has no valid max-age, so browsers ignore it.", "Send: "+recommended
		return c
	}
	if maxAge == 0 {
		c.status, c.explanation, c.advice = "fail", "max-age=0 clears the HSTS policy.", "Set max-age to at least 31536000 (one year)."
		return c
	}
	points := 20.0
	var notes []string
	age := jsv.FormatNumber(maxAge)
	if maxAge < 15552000 {
		points -= 10
		notes = append(notes, "max-age "+age+" is under 180 days")
	} else if maxAge < 31536000 {
		points -= 4
		notes = append(notes, "max-age "+age+" is under one year")
	}
	if !subdomains {
		points -= 4
		notes = append(notes, "includeSubDomains is missing")
	}
	if !preload {
		notes = append(notes, "preload is missing (optional, needed for the browser preload list)")
	}
	ageNote := false
	for _, n := range notes {
		ageNote = ageNote || strings.Contains(n, "max-age")
	}
	c.points = points
	c.status = "warn"
	if points == 20 || (points >= 16 && !ageNote) {
		c.status = "pass"
	}
	if len(notes) > 0 {
		c.explanation = fmt.Sprintf("HSTS present with max-age %s. %s.", age, strings.Join(notes, "; "))
		c.advice = "Send: " + recommended
	} else {
		c.explanation = fmt.Sprintf("HSTS present with max-age %s, includeSubDomains and preload.", age)
		c.advice = "Keep it."
	}
	return c
}

func xfoCheck(value, cspValue headerValue) check {
	c := check{header: "x-frame-options", value: valueOf(value), maxPoints: 10}
	if !present(value) {
		if present(cspValue) {
			if fa, ok := parseCsp(*cspValue)["frame-ancestors"]; ok {
				c.status, c.points = "pass", 10
				c.explanation = "Absent, but CSP frame-ancestors (" + strings.Join(fa, " ") + ") covers modern browsers."
				c.advice = "Optionally add X-Frame-Options: DENY for older browsers."
				return c
			}
		}
		c.status = "fail"
		c.explanation = "No X-Frame-Options and no CSP frame-ancestors, so any site can frame this page (clickjacking)."
		c.advice = "Add X-Frame-Options: DENY and CSP frame-ancestors 'none'."
		return c
	}
	v := toUpperCase(jsv.Trim(*value))
	switch {
	case v == "DENY" || v == "SAMEORIGIN":
		c.status, c.points = "pass", 10
		c.explanation, c.advice = v+" blocks framing by other origins.", "Keep it, and set CSP frame-ancestors as well."
	case strings.HasPrefix(v, "ALLOW-FROM"):
		c.status, c.points = "warn", 3
		c.explanation = "ALLOW-FROM is obsolete and ignored by current browsers, which then apply no restriction."
		c.advice = "Replace with CSP frame-ancestors listing the allowed origin."
	default:
		c.status = "fail"
		c.explanation = "Unrecognised value " + jsv.Stringify(*value, 0) + "; browsers ignore invalid values."
		c.advice = "Use DENY or SAMEORIGIN."
	}
	return c
}

func xctoCheck(value headerValue) check {
	c := check{header: "x-content-type-options", value: valueOf(value), maxPoints: 10, status: "fail"}
	switch {
	case !present(value):
		c.explanation, c.advice = "Missing, so browsers may sniff content types and execute mislabelled files as scripts.", "Add X-Content-Type-Options: nosniff."
	case toLowerCase(jsv.Trim(*value)) == "nosniff":
		c.status, c.points, c.explanation, c.advice = "pass", 10, "nosniff is set.", "Keep it."
	default:
		c.explanation, c.advice = "Value "+jsv.Stringify(*value, 0)+" is not nosniff and has no effect.", "Send exactly nosniff."
	}
	return c
}
