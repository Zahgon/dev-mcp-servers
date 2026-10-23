package main

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// jsSpace is the character class of JavaScript's \s.
const jsSpace = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// The /i patterns of the TypeScript, spelled out: JavaScript's non-Unicode case folding matches
// only the ASCII letter pairs here, whereas Go's (?i) would also match K (Kelvin) and ſ.
var (
	maxAgePattern            = regexp.MustCompile(`[mM][aA][xX]-[aA][gG][eE]` + jsSpace + `*=` + jsSpace + `*"?([0-9]+)`)
	includeSubDomainsPattern = regexp.MustCompile(`[iI][nN][cC][lL][uU][dD][eE][sS][uU][bB][dD][oO][mM][aA][iI][nN][sS]`)
	preloadPattern           = regexp.MustCompile(`[pP][rR][eE][lL][oO][aA][dD]`)
	versionPattern           = regexp.MustCompile(`[0-9]+\.[0-9]+`)
	cookieSecure             = regexp.MustCompile(`;` + jsSpace + `*secure(;|$)`)
	cookieHTTPOnly           = regexp.MustCompile(`;` + jsSpace + `*httponly(;|$)`)
	cookieSameSite           = regexp.MustCompile(`;` + jsSpace + `*samesite=`)
	cookieSameSiteNone       = regexp.MustCompile(`;` + jsSpace + `*samesite=none`)
)

func referrerCheck(value headerValue) check {
	c := check{header: "referrer-policy", value: valueOf(value), maxPoints: 10}
	if !present(value) {
		c.status, c.points = "warn", 4
		c.explanation = "Missing. Modern browsers default to strict-origin-when-cross-origin, older ones send the full URL cross-origin."
		c.advice = "Add Referrer-Policy: strict-origin-when-cross-origin."
		return c
	}
	policies := strings.Split(toLowerCase(*value), ",")
	effective := jsv.Trim(policies[len(policies)-1])
	switch effective {
	case "no-referrer", "same-origin", "strict-origin", "strict-origin-when-cross-origin":
		c.status, c.points = "pass", 10
		c.explanation, c.advice = effective+" keeps paths and query strings from leaking cross-origin.", "Keep it."
	case "unsafe-url":
		c.status = "fail"
		c.explanation = "unsafe-url sends the full URL, including query strings, to every destination over any protocol."
		c.advice = "Use strict-origin-when-cross-origin."
	case "no-referrer-when-downgrade", "origin", "origin-when-cross-origin":
		c.status, c.points = "warn", 5
		c.explanation, c.advice = effective+" still leaks the origin or full URL in some cases.", "Use strict-origin-when-cross-origin or no-referrer."
	default:
		c.status, c.points = "warn", 2
		c.explanation = "Unrecognised policy " + jsv.Stringify(effective, 0) + "; browsers fall back to the default."
		c.advice = "Use strict-origin-when-cross-origin."
	}
	return c
}

func permissionsCheck(value headerValue) check {
	c := check{header: "permissions-policy", value: valueOf(value), maxPoints: 5}
	if !present(value) {
		c.status = "warn"
		c.explanation = "Missing. Embedded frames and the page itself may request camera, microphone, geolocation and other features."
		c.advice = "Add: " + headerDocs["permissions-policy"].recommended
		return c
	}
	c.status, c.points = "pass", 5
	c.explanation = fmt.Sprintf("Present with %d directive(s).", len(strings.Split(*value, ",")))
	c.advice = "Review that unused features are disabled with ()."
	return c
}

func coopCheck(value headerValue) check {
	c := check{header: "cross-origin-opener-policy", value: valueOf(value), maxPoints: 5, status: "info"}
	if !present(value) {
		c.explanation = "Missing. Cross-origin pages that open this one keep a window reference (needed only for some OAuth popups)."
		c.advice = "Add Cross-Origin-Opener-Policy: same-origin unless you rely on cross-origin window.opener."
		return c
	}
	v := toLowerCase(jsv.Trim(*value))
	switch v {
	case "same-origin", "same-origin-allow-popups", "noopener-allow-popups":
		c.status, c.points = "pass", 5
		c.explanation, c.advice = v+" isolates the browsing context group.", "Keep it."
	default:
		c.points = 1
		c.explanation, c.advice = v+" provides no isolation.", "Use same-origin."
	}
	return c
}

func corpCheck(value headerValue) check {
	c := check{header: "cross-origin-resource-policy", value: valueOf(value), maxPoints: 5, status: "info"}
	if !present(value) {
		c.explanation = "Missing. Other origins can embed this resource in no-cors requests."
		c.advice = "Add Cross-Origin-Resource-Policy: same-origin for private resources."
		return c
	}
	v := toLowerCase(jsv.Trim(*value))
	switch v {
	case "same-origin", "same-site":
		c.status, c.points = "pass", 5
		c.explanation, c.advice = v+" blocks cross-origin no-cors reads.", "Keep it."
	default:
		c.points = 1
		c.explanation, c.advice = v+" allows any origin to embed the resource (fine for public assets).", "Use same-origin for private resources."
	}
	return c
}

func xssCheck(value headerValue) check {
	c := check{header: "x-xss-protection", value: valueOf(value)}
	if !present(value) || jsv.Trim(*value) == "0" {
		c.status, c.advice = "pass", "Nothing to do."
		c.explanation = "Absent, which is correct for modern browsers."
		if present(value) {
			c.explanation = "Set to 0, which disables the legacy filter."
		}
		return c
	}
	c.status = "warn"
	c.explanation = "The legacy XSS auditor is enabled. It is removed from current browsers and introduced information leaks in old ones."
	c.advice = "Remove the header or send X-XSS-Protection: 0, and rely on CSP."
	return c
}

func disclosureCheck(header string, value headerValue) check {
	c := check{header: header, value: valueOf(value)}
	if !present(value) {
		c.status, c.explanation, c.advice = "pass", "Absent.", "Nothing to do."
		return c
	}
	if versionPattern.MatchString(*value) {
		c.status = "warn"
		c.explanation = "Discloses software and version (" + *value + "), which helps attackers select exploits."
	} else {
		c.status = "info"
		c.explanation = "Discloses software (" + *value + ") without a version."
	}
	c.advice = "Remove the header (for example app.disable('x-powered-by') in Express)."
	if header == "server" {
		c.advice = "Remove the version string or the header."
	}
	return c
}

func cookieCheck(cookies []string, https bool) check {
	c := check{header: "set-cookie", maxPoints: 10}
	if len(cookies) == 0 {
		c.status, c.points, c.explanation, c.advice = "info", 10, "No cookies set on this response.", "Nothing to do."
		return c
	}
	pairs := make([]string, len(cookies))
	var problems []string
	for i, ck := range cookies {
		pairs[i], _, _ = strings.Cut(ck, ";")
		name, _, _ := strings.Cut(ck, "=")
		name = jsv.Trim(name)
		attrs := toLowerCase(ck)
		var missing []string
		secure := cookieSecure.MatchString(attrs)
		if https && !secure {
			missing = append(missing, "Secure")
		}
		if !cookieHTTPOnly.MatchString(attrs) {
			missing = append(missing, "HttpOnly")
		}
		if !cookieSameSite.MatchString(attrs) {
			missing = append(missing, "SameSite")
		}
		if cookieSameSiteNone.MatchString(attrs) && !secure {
			missing = append(missing, "Secure (required with SameSite=None)")
		}
		if len(missing) > 0 {
			problems = append(problems, name+" lacks "+strings.Join(missing, ", "))
		}
	}
	c.value = strings.Join(pairs, ", ")
	c.points = math.Max(0, 10-float64(len(problems))*4)
	if len(problems) > 0 {
		c.status = "warn"
		c.explanation = fmt.Sprintf("%d cookie(s); %s.", len(cookies), strings.Join(problems, "; "))
		c.advice = "Add Secure, HttpOnly and SameSite=Lax (or Strict) to session cookies; use the __Host- prefix to bind them to the host."
	} else {
		c.status = "pass"
		c.explanation = fmt.Sprintf("%d cookie(s) with Secure, HttpOnly and SameSite.", len(cookies))
		c.advice = "Keep it."
	}
	return c
}

// gradeHeaders scores a header set and returns the report object.
func gradeHeaders(set headerSet) *jsv.Obj {
	get := func(name string) headerValue {
		if v, ok := set.Headers.Get(name); ok {
			s, _ := v.(string)
			return &s
		}
		return nil
	}
	checks := []check{
		cspCheck(get("content-security-policy"), get("content-security-policy-report-only")),
		hstsCheck(get("strict-transport-security"), set.HTTPS),
		xfoCheck(get("x-frame-options"), get("content-security-policy")),
		xctoCheck(get("x-content-type-options")),
		referrerCheck(get("referrer-policy")),
		permissionsCheck(get("permissions-policy")),
		coopCheck(get("cross-origin-opener-policy")),
		corpCheck(get("cross-origin-resource-policy")),
		xssCheck(get("x-xss-protection")),
		disclosureCheck("server", get("server")),
		disclosureCheck("x-powered-by", get("x-powered-by")),
		cookieCheck(set.SetCookie, set.HTTPS),
	}
	var score, max float64
	fails := 0
	list := make([]any, len(checks))
	for i, c := range checks {
		score += c.points
		max += c.maxPoints
		if c.status == "fail" {
			fails++
		}
		list[i] = c.obj()
	}
	pct := 0.0
	if max != 0 {
		pct = score / max * 100
	}
	grade := "F"
	switch {
	case pct >= 95 && fails == 0:
		grade = "A+"
	case pct >= 85 && fails == 0:
		grade = "A"
	case pct >= 70:
		grade = "B"
	case pct >= 55:
		grade = "C"
	case pct >= 40:
		grade = "D"
	}
	notes := []any{}
	if !set.HTTPS {
		notes = append(notes, "Fetched over plain HTTP: HSTS and Secure cookies cannot be assessed properly.")
	}
	if present(get("content-security-policy-report-only")) {
		notes = append(notes, "A report-only CSP is present; it collects reports but enforces nothing.")
	}
	return jsv.O("grade", grade, "score", score, "max_score", max, "checks", list, "notes", notes)
}

// toHeaderSet normalises a caller-supplied header object: names trimmed and lower-cased, values
// that are arrays joined with ", ", Set-Cookie values kept separately.
func toHeaderSet(headers *jsv.Obj, https bool) headerSet {
	out := jsv.NewObj()
	var setCookie []string
	for _, rawName := range headers.Keys() {
		name := toLowerCase(jsv.Trim(rawName))
		var values []string
		switch v := jsv.Prop(headers, rawName).(type) {
		case string:
			values = []string{v}
		case []any:
			for _, item := range v {
				values = append(values, item.(string))
			}
		}
		if name == "set-cookie" {
			setCookie = append(setCookie, values...)
			continue
		}
		if name == "__proto__" {
			// Assigning a string to __proto__ of a plain object changes nothing.
			continue
		}
		out.Set(name, strings.Join(values, ", "))
	}
	return headerSet{Headers: out, SetCookie: setCookie, HTTPS: https}
}
