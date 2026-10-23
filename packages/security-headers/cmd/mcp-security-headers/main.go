// Command mcp-security-headers is the security-headers MCP server on stdio: it fetches a URL's
// response headers and grades them. Network: one HEAD (or GET) request to the caller-supplied public
// URL plus at most a few redirect hops, each re-checked against private address ranges, with a 10 s
// timeout. Bodies are never read.
package main

import (
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "security-headers"
	serverVersion = "0.1.0"
)

const instructions = "Grades HTTP response security headers. check_url_headers fetches a public URL (HEAD, then GET if HEAD is refused), follows a limited number of redirects " +
	"and refuses private or local addresses; grade_headers scores a header set you already have (for example from curl -I); explain_header describes one header. " +
	"Scores: Content-Security-Policy 25, Strict-Transport-Security 20, X-Frame-Options 10, X-Content-Type-Options 10, Referrer-Policy 10, Set-Cookie 10, " +
	"Permissions-Policy 5, Cross-Origin-Opener-Policy 5, Cross-Origin-Resource-Policy 5; Server, X-Powered-By and X-XSS-Protection are reported without points. " +
	"A high grade means the headers are well configured, not that the site is secure."

func newServer() *mcp.Server { return newServerWith(fetchOptions{}) }

// newServerWith is createServer(options): tests inject a transport and a resolver.
func newServerWith(opts fetchOptions) *mcp.Server {
	return &mcp.Server{Name: serverName, Version: serverVersion, Instructions: instructions, Tools: tools(opts)}
}

func main() { mcp.Main(newServer()) }

func checkURLTool(opts fetchOptions) func(*jsv.Obj) mcp.Result {
	return func(args *jsv.Obj) mcp.Result {
		a := mcp.Args{Tool: "check_url_headers", Obj: args}
		url, err := a.RequiredString("url", 8, 2048)
		if err != nil {
			return mcp.Fail(err.Error())
		}
		method, err := a.Enum("method", []string{"HEAD", "GET"})
		if err != nil {
			return mcp.Fail(err.Error())
		}
		maxRedirects, err := a.Number("max_redirects", 0, redirectLimit, true)
		if err != nil {
			return mcp.Fail(err.Error())
		}
		m := "HEAD"
		if method != nil {
			m = *method
		}
		if maxRedirects != nil {
			n := int(*maxRedirects)
			opts.MaxRedirects = &n
		}
		fetched, err := fetchHeaders(url, m, opts)
		if err != nil {
			return mcp.Fail(err.Error())
		}
		headers, _ := fetched.Get("headers")
		cookies, _ := fetched.Get("set_cookie")
		final, _ := fetched.Get("final_url")
		var setCookie []string
		for _, c := range cookies.([]any) {
			setCookie = append(setCookie, c.(string))
		}
		report := gradeHeaders(headerSet{Headers: headers.(*jsv.Obj), SetCookie: setCookie, HTTPS: strings.HasPrefix(final.(string), "https:")})
		fetched.Set("report", report)
		return mcp.OK(fetched)
	}
}

// headerArgs applies z.record(z.string().min(1).max(200), z.union([z.string().max(16384),
// z.array(z.string().max(16384)).max(50)])); zod's record drops a __proto__ key.
func headerArgs(a mcp.Args) (*jsv.Obj, error) {
	raw, ok := a.Obj.Get("headers")
	if !ok {
		return nil, mcp.Invalid(a.Tool, "headers: Required")
	}
	in := jsv.AsObj(raw)
	if in == nil {
		return nil, mcp.Invalid(a.Tool, "headers: Expected object")
	}
	out := jsv.NewObj()
	for _, k := range in.Keys() {
		if n := jsv.UTF16Len(k); n < 1 || n > 200 {
			return nil, mcp.Invalid(a.Tool, "headers: Invalid key in record")
		}
		v := jsv.Prop(in, k)
		switch x := v.(type) {
		case string:
			if jsv.UTF16Len(x) > 16384 {
				return nil, mcp.Invalid(a.Tool, "headers: Invalid input")
			}
		case []any:
			if len(x) > 50 {
				return nil, mcp.Invalid(a.Tool, "headers: Invalid input")
			}
			for _, item := range x {
				s, isString := item.(string)
				if !isString || jsv.UTF16Len(s) > 16384 {
					return nil, mcp.Invalid(a.Tool, "headers: Invalid input")
				}
			}
		default:
			return nil, mcp.Invalid(a.Tool, "headers: Invalid input")
		}
		if k != "__proto__" {
			out.Set(k, v)
		}
	}
	return out, nil
}

func gradeTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "grade_headers", Obj: args}
	headers, err := headerArgs(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	https, err := a.Bool("https")
	if err != nil {
		return mcp.Fail(err.Error())
	}
	if headers.Len() > 200 {
		return mcp.Fail("At most 200 headers.")
	}
	return mcp.OK(gradeHeaders(toHeaderSet(headers, https == nil || *https)))
}

func explainTool(args *jsv.Obj) mcp.Result {
	raw, ok := args.Get("header")
	name, isString := raw.(string)
	doc, known := headerDocs[name]
	if !ok || !isString || !known {
		return mcp.Fail(mcp.Invalid("explain_header", "header: Invalid enum value").Error())
	}
	return mcp.OK(jsv.O("header", name, "purpose", doc.purpose, "recommended", doc.recommended, "reference", doc.reference))
}

func tools(opts fetchOptions) []mcp.Tool {
	annotations := func(openWorld bool) *jsv.Obj {
		return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", true, "openWorldHint", openWorld)
	}
	object := func(props *jsv.Obj, required ...string) *jsv.Obj {
		return jsv.O("type", "object", "properties", props, "required", jsv.Strings(required), "additionalProperties", false, "$schema", "http://json-schema.org/draft-07/schema#")
	}
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, schema *jsv.Obj, openWorld bool) mcp.Tool {
		return mcp.Tool{Name: name, Handler: handler, Spec: jsv.O("title", title, "description", description, "inputSchema", schema,
			"annotations", annotations(openWorld), "execution", jsv.O("taskSupport", "forbidden"))}
	}
	value := jsv.O("type", "string", "maxLength", float64(16384))
	return []mcp.Tool{
		tool("check_url_headers", checkURLTool(opts), "Fetch and grade a URL's security headers",
			"Send a HEAD request (GET when the server answers 405 or 501, or when method is GET) to a public http or https URL, follow up to max_redirects redirects "+
				"(default 5) and grade the final response's security headers. Returns the final URL, status, redirect chain, every response header, and per-header "+
				"pass/warn/fail/info checks with explanations, recommendations and an overall grade from A+ to F. Refuses URLs whose host is localhost, a .local or "+
				".internal name, or resolves to a private, loopback, link-local or cloud-metadata address. The response body is never downloaded.",
			object(jsv.O(
				"url", jsv.O("type", "string", "minLength", float64(8), "maxLength", float64(2048), "description", "Absolute http:// or https:// URL."),
				"method", jsv.O("type", "string", "enum", jsv.Strings([]string{"HEAD", "GET"}), "description", "Default HEAD."),
				"max_redirects", jsv.O("type", "integer", "minimum", float64(0), "maximum", float64(redirectLimit), "description", "Default 5."),
			), "url"), true),
		tool("grade_headers", gradeTool, "Grade a supplied header set",
			"Grade response headers you already have, without any network access. Pass headers as an object of name to value (names are case-insensitive; "+
				"Set-Cookie may be an array). Returns the same per-header checks and overall grade as check_url_headers. Set https to false when the response came over plain HTTP.",
			object(jsv.O(
				"headers", jsv.O("type", "object", "additionalProperties", jsv.O("anyOf", []any{
					jsv.O("type", "string", "maxLength", float64(16384)),
					jsv.O("type", "array", "items", value, "maxItems", float64(50)),
				}), "propertyNames", jsv.O("minLength", float64(1), "maxLength", float64(200)), "description", "Response headers, name to value."),
				"https", jsv.O("type", "boolean", "description", "Whether the response was served over HTTPS. Default true."),
			), "headers"), false),
		tool("explain_header", explainTool, "Explain a security header",
			"Describe what one response header does, the recommended value and a reference link. Headers: "+strings.Join(gradedHeaders, ", ")+".",
			object(jsv.O("header", jsv.O("type", "string", "enum", jsv.Strings(gradedHeaders))), "header"), false),
	}
}
