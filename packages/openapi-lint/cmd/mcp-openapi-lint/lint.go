package main

// OpenAPI 3.x loading, operation listing and linting. Only local (#/...) references are resolved.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const maxDocumentBytes = 5 * 1024 * 1024

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type ruleDoc struct {
	Severity    string
	Description string
	Fix         string
}

var ruleNames = []string{
	"openapi-version", "info-description", "server-http", "no-security-schemes",
	"operation-no-security", "operation-public", "security-scheme-undefined", "unused-security-scheme",
	"operation-no-responses", "operation-no-success-response", "response-no-description",
	"operation-no-description", "operation-no-operation-id", "duplicate-operation-id", "path-unversioned",
	"path-parameter-undefined", "path-parameter-not-required", "parameter-no-description",
	"parameter-no-schema", "request-body-no-content", "deprecated-operation", "tag-undefined",
}

var rules = map[string]ruleDoc{
	"openapi-version":               {"error", "The document must declare an OpenAPI 3.x version in the top-level openapi field.", "Add openapi: 3.0.3 (or 3.1.0). Swagger 2.0 documents use a different structure and must be converted."},
	"info-description":              {"info", "info has no description, so consumers see only a title.", "Add info.description with the API's purpose, audience and authentication model."},
	"server-http":                   {"warning", "A server URL uses plain http://, so credentials and data travel unencrypted.", "Use https:// server URLs. Keep http only for an explicitly local development server."},
	"no-security-schemes":           {"warning", "components.securitySchemes is empty, so no operation can declare how it is authenticated.", "Define at least one scheme (http bearer, apiKey, oauth2 or openIdConnect) under components.securitySchemes and reference it in security."},
	"operation-no-security":         {"warning", "The operation has no security requirement and no global security applies, so it is documented as anonymous.", "Add a security requirement to the operation, or a top-level security array for the whole API. Use security: [] only for intentionally public operations."},
	"operation-public":              {"info", "The operation sets security: [] and is therefore explicitly public.", "Confirm the endpoint is meant to be callable without credentials."},
	"security-scheme-undefined":     {"error", "A security requirement names a scheme that components.securitySchemes does not define.", "Define the scheme or correct the name in the security requirement."},
	"unused-security-scheme":        {"info", "A security scheme is defined but never referenced.", "Reference it from security or remove it."},
	"operation-no-responses":        {"error", "The operation has no responses object or it is empty; the specification requires at least one response.", "Document at least the success response and the common error responses."},
	"operation-no-success-response": {"warning", "No 2xx, 3xx or default response is documented.", "Add the success response (for example 200 or 204) with its content."},
	"response-no-description":       {"warning", "A response has no description; the specification requires one.", "Add a short description to every response."},
	"operation-no-description":      {"warning", "The operation has neither a summary nor a description.", "Add a summary (one line) and, where useful, a description."},
	"operation-no-operation-id":     {"warning", "The operation has no operationId, which code generators and tooling use to name functions.", "Add a unique, camelCase operationId such as listUsers."},
	"duplicate-operation-id":        {"error", "Two operations share an operationId; identifiers must be unique across the document.", "Rename one of them."},
	"path-unversioned":              {"info", "Neither the path nor any server URL carries a version segment such as /v1, so breaking changes cannot be introduced side by side.", "Version the API in the server URL (https://api.example.com/v1) or in the path (/v1/users)."},
	"path-parameter-undefined":      {"error", "The path template contains a {parameter} that no path-level or operation-level parameter with in: path declares.", "Declare the parameter with in: path and required: true."},
	"path-parameter-not-required":   {"error", "A parameter with in: path is not marked required: true, which the specification demands.", "Set required: true on every path parameter."},
	"parameter-no-description":      {"info", "A parameter has no description.", "Describe the parameter's meaning, format and default."},
	"parameter-no-schema":           {"warning", "A parameter has neither schema nor content, so its type is unknown.", "Add a schema (type, format, enum) to the parameter."},
	"request-body-no-content":       {"warning", "A requestBody declares no content media types.", "Add content with at least one media type and schema."},
	"deprecated-operation":          {"info", "The operation is marked deprecated.", "Document the replacement in the description and plan removal."},
	"tag-undefined":                 {"info", "An operation uses a tag that is not declared in the top-level tags list.", "Declare the tag with a description under tags, which groups operations in generated documentation."},
}

type lintFinding struct {
	Rule     string
	Severity string
	Path     string
	Message  string
}

func (f lintFinding) value() *jsv.Obj {
	o := jsv.NewObj()
	o.Set("rule", f.Rule)
	o.Set("severity", f.Severity)
	o.Set("path", f.Path)
	o.Set("message", f.Message)
	return o
}

// ---- loading ------------------------------------------------------------------

func parseDocument(text string) (*jsv.Obj, error) {
	if jsv.UTF16Len(text) > maxDocumentBytes {
		return nil, fmt.Errorf("document exceeds %d characters", maxDocumentBytes)
	}
	var doc any
	var err error
	if strings.HasPrefix(jsv.TrimStart(text), "{") {
		doc, err = jsv.Parse(text)
		if err != nil {
			return nil, fmt.Errorf("not valid JSON: %s", err.Error())
		}
	} else {
		doc, err = parseYAML(text)
		if err != nil {
			return nil, fmt.Errorf("not valid YAML: %s", err.Error())
		}
	}
	obj, ok := doc.(*jsv.Obj)
	if !ok {
		return nil, errors.New("the document is not an object")
	}
	return obj, nil
}

type source struct {
	path        *string
	document    any
	hasDocument bool
}

func loadDocument(in source) (*jsv.Obj, string, error) {
	given := 0
	if in.path != nil {
		given++
	}
	if in.hasDocument {
		given++
	}
	if given != 1 {
		return nil, "", errors.New("supply exactly one of path or document")
	}
	if in.path != nil {
		abs, err := filepath.Abs(*in.path)
		if err != nil {
			return nil, "", fmt.Errorf("cannot read %s: %s", *in.path, err.Error())
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, "", fmt.Errorf("cannot read %s: %s", abs, err.Error())
		}
		if !st.Mode().IsRegular() {
			return nil, "", fmt.Errorf("%s is not a file", abs)
		}
		if st.Size() > maxDocumentBytes {
			return nil, "", fmt.Errorf("%s is %d bytes; the limit is %d", abs, st.Size(), maxDocumentBytes)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, "", fmt.Errorf("cannot read %s: %s", abs, err.Error())
		}
		doc, err := parseDocument(jsv.ValidUTF8(data))
		if err != nil {
			return nil, "", err
		}
		return doc, abs, nil
	}
	if text, ok := in.document.(string); ok {
		doc, err := parseDocument(text)
		if err != nil {
			return nil, "", err
		}
		return doc, "inline", nil
	}
	obj, ok := in.document.(*jsv.Obj)
	if !ok {
		return nil, "", errors.New("document must be an object or a string")
	}
	if jsv.UTF16Len(jsv.Stringify(obj, 0)) > maxDocumentBytes {
		return nil, "", fmt.Errorf("document exceeds %d characters", maxDocumentBytes)
	}
	return obj, "inline", nil
}

// ---- references -----------------------------------------------------------------

func deref(doc *jsv.Obj, value any, depth int) any {
	o := jsv.AsObj(value)
	if o == nil || depth > 16 {
		return value
	}
	rawRef, ok := o.Get("$ref")
	ref, isString := rawRef.(string)
	if !ok || !isString || !strings.HasPrefix(ref, "#/") {
		return value
	}
	var node any = doc
	for _, part := range strings.Split(ref[2:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		holder := jsv.AsObj(node)
		if holder == nil || !holder.Has(key) {
			return value
		}
		node, _ = holder.Get(key)
	}
	return deref(doc, node, depth+1)
}

// ---- listing --------------------------------------------------------------------

func securityNames(req any, ok bool) ([]any, bool) {
	arr, isArr := req.([]any)
	if !ok || !isArr {
		return nil, false
	}
	out := make([]any, 0, len(arr))
	for _, r := range arr {
		names := []any{}
		if o := jsv.AsObj(r); o != nil {
			for _, k := range o.Keys() {
				names = append(names, k)
			}
		}
		out = append(out, names)
	}
	return out, true
}

func parameterList(doc *jsv.Obj, pathParams []any, op *jsv.Obj) []*jsv.Obj {
	all := append([]any{}, pathParams...)
	if raw, ok := op.Get("parameters"); ok {
		if arr, isArr := raw.([]any); isArr {
			all = append(all, arr...)
		}
	}
	var out []*jsv.Obj
	for _, p := range all {
		if o := jsv.AsObj(deref(doc, p, 0)); o != nil {
			out = append(out, o)
		}
	}
	return out
}

func entries(v any) []string {
	if o := jsv.AsObj(v); o != nil {
		return o.Keys()
	}
	return nil
}

func listOperations(doc *jsv.Obj) []*jsv.Obj {
	var out []*jsv.Obj
	paths := jsv.AsObj(jsv.Prop(doc, "paths"))
	globalRaw, globalOK := doc.Get("security")
	globalSecurity, hasGlobal := securityNames(globalRaw, globalOK)
	for _, path := range entries(paths) {
		rawItem, _ := paths.Get(path)
		item := jsv.AsObj(deref(doc, rawItem, 0))
		if item == nil {
			continue
		}
		pathParams, _ := jsv.Prop(item, "parameters").([]any)
		for _, method := range methods {
			op := jsv.AsObj(jsv.Prop(item, method))
			if op == nil {
				continue
			}
			params := parameterList(doc, pathParams, op)
			opSecRaw, opSecOK := op.Get("security")
			opSecurity, hasOp := securityNames(opSecRaw, opSecOK)
			body := jsv.AsObj(deref(doc, jsv.Prop(op, "requestBody"), 0))
			responses := []any{}
			if r := jsv.AsObj(jsv.Prop(op, "responses")); r != nil {
				for _, k := range r.Keys() {
					responses = append(responses, k)
				}
			}
			var security any
			if hasOp {
				if len(opSecurity) == 0 {
					security = "public"
				} else {
					security = opSecurity
				}
			} else if hasGlobal && len(globalSecurity) > 0 {
				security = globalSecurity
			} else {
				security = "inherited-none"
			}
			entry := jsv.NewObj()
			entry.Set("method", strings.ToUpper(method))
			entry.Set("path", path)
			if id, ok := jsv.Prop(op, "operationId").(string); ok {
				entry.Set("operation_id", id)
			} else {
				entry.Set("operation_id", nil)
			}
			if s, ok := jsv.Prop(op, "summary").(string); ok {
				entry.Set("summary", s)
			} else if d, ok := jsv.Prop(op, "description").(string); ok {
				first, _, _ := strings.Cut(d, "\n")
				entry.Set("summary", jsv.UTF16Slice(first, 120))
			} else {
				entry.Set("summary", nil)
			}
			tags := []any{}
			if arr, ok := jsv.Prop(op, "tags").([]any); ok {
				for _, t := range arr {
					tags = append(tags, jsv.ToString(t))
				}
			}
			entry.Set("tags", tags)
			entry.Set("deprecated", jsv.Prop(op, "deprecated") == true)
			entry.Set("security", security)
			plist := []any{}
			for _, p := range params {
				q := jsv.NewObj()
				name, nameOK := p.Get("name")
				if !nameOK || name == nil {
					q.Set("name", "")
				} else {
					q.Set("name", jsv.ToString(name))
				}
				in, inOK := p.Get("in")
				if !inOK || in == nil {
					q.Set("in", "")
				} else {
					q.Set("in", jsv.ToString(in))
				}
				q.Set("required", jsv.Prop(p, "required") == true || in == "path")
				plist = append(plist, q)
			}
			entry.Set("parameters", plist)
			if body != nil && jsv.IsObject(jsv.Prop(body, "content")) {
				media := []any{}
				for _, k := range jsv.AsObj(jsv.Prop(body, "content")).Keys() {
					media = append(media, k)
				}
				entry.Set("request_body", media)
			} else {
				entry.Set("request_body", nil)
			}
			entry.Set("responses", responses)
			out = append(out, entry)
		}
	}
	return out
}

// ---- linting --------------------------------------------------------------------

var (
	reVersion3 = regexp.MustCompile(`^3\.[0-9]+`)
	// JavaScript's /i without the u flag folds ASCII letters only, so the classes are spelled out.
	reHTTP         = regexp.MustCompile(`^[hH][tT][tT][pP]://`)
	reLocalHTTP    = regexp.MustCompile(`^[hH][tT][tT][pP]://([lL][oO][cC][aA][lL][hH][oO][sS][tT]|127\.0\.0\.1|\[::1\])`)
	reVersionSeg   = regexp.MustCompile(`/[vV][0-9]+(\.[0-9]+)*(/|$)`)
	reVersionVar   = regexp.MustCompile(`\{[vV][eE][rR][sS][iI][oO][nN]\}`)
	reTemplate     = regexp.MustCompile(`\{([^}]+)\}`)
	reSuccessCode  = regexp.MustCompile(`^[23]`)
	severityOrders = map[string]int{"error": 0, "warning": 1, "info": 2}
)

func isBlankString(v any) bool {
	s, ok := v.(string)
	return !ok || jsv.Trim(s) == ""
}

func lint(doc *jsv.Obj, ignore map[string]bool) []lintFinding {
	var findings []lintFinding
	add := func(rule, path, message string) {
		if ignore[rule] {
			return
		}
		findings = append(findings, lintFinding{rule, rules[rule].Severity, path, message})
	}

	version, _ := jsv.Prop(doc, "openapi").(string)
	if version == "" || !reVersion3.MatchString(version) {
		var message string
		swagger, swaggerOK := doc.Get("swagger")
		switch {
		case version != "":
			message = fmt.Sprintf("openapi is %s, not 3.x", jsv.Stringify(version, 0))
		case jsv.Truthy(swagger, swaggerOK):
			message = fmt.Sprintf("this is a Swagger %s document", jsv.ToString(swagger))
		default:
			message = "openapi field is missing"
		}
		add("openapi-version", "/openapi", message)
	}
	info := jsv.AsObj(jsv.Prop(doc, "info"))
	if info == nil {
		info = jsv.NewObj()
	}
	if isBlankString(jsv.Prop(info, "description")) {
		add("info-description", "/info", "info.description is missing")
	}

	servers, _ := jsv.Prop(doc, "servers").([]any)
	versionedServer := false
	for i, raw := range servers {
		s := jsv.AsObj(raw)
		if s == nil {
			continue
		}
		url, ok := jsv.Prop(s, "url").(string)
		if !ok {
			continue
		}
		if reHTTP.MatchString(url) && !reLocalHTTP.MatchString(url) {
			add("server-http", fmt.Sprintf("/servers/%d", i), url+" is not HTTPS")
		}
		if reVersionSeg.MatchString(url) || reVersionVar.MatchString(url) {
			versionedServer = true
		}
	}

	components := jsv.AsObj(jsv.Prop(doc, "components"))
	var schemes []string
	if components != nil {
		if ss := jsv.AsObj(jsv.Prop(components, "securitySchemes")); ss != nil {
			schemes = ss.Keys()
		}
	}
	schemeSet := map[string]bool{}
	for _, s := range schemes {
		schemeSet[s] = true
	}
	if len(schemes) == 0 {
		add("no-security-schemes", "/components/securitySchemes", "no security schemes are defined")
	}
	used := map[string]bool{}
	checkSecurity := func(req any, path string) {
		arr, ok := req.([]any)
		if !ok {
			return
		}
		for i, r := range arr {
			o := jsv.AsObj(r)
			if o == nil {
				continue
			}
			for _, name := range o.Keys() {
				used[name] = true
				if !schemeSet[name] {
					add("security-scheme-undefined", fmt.Sprintf("%s/%d", path, i), fmt.Sprintf("security scheme %s is not defined in components.securitySchemes", jsv.Stringify(name, 0)))
				}
			}
		}
	}
	checkSecurity(jsv.Prop(doc, "security"), "/security")
	globalArr, _ := jsv.Prop(doc, "security").([]any)
	globalSecurity := len(globalArr) > 0

	declaredTags := map[string]bool{}
	if tags, ok := jsv.Prop(doc, "tags").([]any); ok {
		for _, t := range tags {
			if o := jsv.AsObj(t); o != nil {
				name, nameOK := o.Get("name")
				declaredTags[jsv.StringOrUndefined(name, nameOK)] = true
			} else {
				declaredTags[jsv.ToString(t)] = true
			}
		}
	}
	operationIDs := map[string]string{}
	paths := jsv.AsObj(jsv.Prop(doc, "paths"))
	for _, path := range entries(paths) {
		rawItem, _ := paths.Get(path)
		item := jsv.AsObj(deref(doc, rawItem, 0))
		if item == nil {
			continue
		}
		pathPointer := "/paths/" + strings.ReplaceAll(strings.ReplaceAll(path, "~", "~0"), "/", "~1")
		var templated []string
		for _, m := range reTemplate.FindAllStringSubmatch(path, -1) {
			templated = append(templated, m[1])
		}
		if !versionedServer && !reVersionSeg.MatchString(path) {
			add("path-unversioned", pathPointer, path+" has no version segment and no server URL is versioned")
		}
		pathParams, _ := jsv.Prop(item, "parameters").([]any)
		for _, method := range methods {
			op := jsv.AsObj(jsv.Prop(item, method))
			if op == nil {
				continue
			}
			opPointer := pathPointer + "/" + method
			label := strings.ToUpper(method) + " " + path
			if id, ok := jsv.Prop(op, "operationId").(string); !ok || id == "" {
				add("operation-no-operation-id", opPointer, label+" has no operationId")
			} else if first, seen := operationIDs[id]; seen {
				add("duplicate-operation-id", opPointer, fmt.Sprintf("operationId %s is also used by %s", jsv.Stringify(id, 0), first))
			} else {
				operationIDs[id] = label
			}
			if isBlankString(jsv.Prop(op, "summary")) && isBlankString(jsv.Prop(op, "description")) {
				add("operation-no-description", opPointer, label+" has neither summary nor description")
			}
			if jsv.Prop(op, "deprecated") == true {
				add("deprecated-operation", opPointer, label+" is deprecated")
			}
			if tags, ok := jsv.Prop(op, "tags").([]any); ok {
				for _, t := range tags {
					if !declaredTags[jsv.ToString(t)] {
						add("tag-undefined", opPointer+"/tags", fmt.Sprintf("%s uses undeclared tag %s", label, jsv.Stringify(t, 0)))
					}
				}
			}

			security, hasSecurity := op.Get("security")
			if !hasSecurity {
				if !globalSecurity {
					add("operation-no-security", opPointer, label+" has no security requirement and there is no global security")
				}
			} else if arr, ok := security.([]any); ok && len(arr) == 0 {
				add("operation-public", opPointer+"/security", label+" is explicitly public (security: [])")
			} else {
				checkSecurity(security, opPointer+"/security")
			}

			params := parameterList(doc, pathParams, op)
			declaredPath := map[string]bool{}
			for _, p := range params {
				if jsv.Prop(p, "in") == "path" {
					name, nameOK := p.Get("name")
					declaredPath[jsv.StringOrUndefined(name, nameOK)] = true
				}
			}
			for _, t := range templated {
				if !declaredPath[t] {
					add("path-parameter-undefined", opPointer, fmt.Sprintf("%s: path parameter {%s} is not declared", label, t))
				}
			}
			for i, p := range params {
				pp := fmt.Sprintf("%s/parameters/%d", opPointer, i)
				name, nameOK := p.Get("name")
				pname := jsv.StringOrUndefined(name, nameOK)
				if jsv.Prop(p, "in") == "path" && jsv.Prop(p, "required") != true {
					add("path-parameter-not-required", pp, fmt.Sprintf("%s: path parameter %s must be required", label, pname))
				}
				if isBlankString(jsv.Prop(p, "description")) {
					add("parameter-no-description", pp, fmt.Sprintf("%s: parameter %s has no description", label, pname))
				}
				if !p.Has("schema") && !p.Has("content") {
					add("parameter-no-schema", pp, fmt.Sprintf("%s: parameter %s has no schema", label, pname))
				}
			}

			if body := jsv.AsObj(deref(doc, jsv.Prop(op, "requestBody"), 0)); body != nil {
				content := jsv.AsObj(jsv.Prop(body, "content"))
				if content == nil || len(content.Keys()) == 0 {
					add("request-body-no-content", opPointer+"/requestBody", label+": requestBody has no content")
				}
			}

			responses := jsv.AsObj(jsv.Prop(op, "responses"))
			if responses == nil || len(responses.Keys()) == 0 {
				add("operation-no-responses", opPointer+"/responses", label+" documents no responses")
			} else {
				codes := responses.Keys()
				success := false
				for _, c := range codes {
					if c == "default" || reSuccessCode.MatchString(c) {
						success = true
					}
				}
				if !success {
					add("operation-no-success-response", opPointer+"/responses", label+" documents only "+strings.Join(codes, ", "))
				}
				for _, code := range codes {
					raw, _ := responses.Get(code)
					if r := jsv.AsObj(deref(doc, raw, 0)); r != nil && isBlankString(jsv.Prop(r, "description")) {
						add("response-no-description", opPointer+"/responses/"+code, fmt.Sprintf("%s: response %s has no description", label, code))
					}
				}
			}
		}
	}
	for _, name := range schemes {
		if !used[name] {
			add("unused-security-scheme", "/components/securitySchemes/"+name, fmt.Sprintf("security scheme %s is never referenced", jsv.Stringify(name, 0)))
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if severityOrders[a.Severity] != severityOrders[b.Severity] {
			return severityOrders[a.Severity] < severityOrders[b.Severity]
		}
		return localeCompare(a.Path, b.Path) < 0
	})
	return findings
}

// localeCompare approximates String.prototype.localeCompare under the root collation for
// the ASCII range that JSON Pointer paths use: punctuation, then digits, then letters
// compared case-insensitively first, with lower case ahead of upper case on a tie.
const collationPunctuation = "_-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

func primaryWeight(r rune) int {
	if i := strings.IndexRune(collationPunctuation, r); i >= 0 {
		return 100 + i
	}
	switch {
	case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		return 10
	case r >= '0' && r <= '9':
		return 200 + int(r-'0')
	case r >= 'a' && r <= 'z':
		return 300 + int(r-'a')
	case r >= 'A' && r <= 'Z':
		return 300 + int(r-'A')
	}
	return 1000 + int(r)
}

func localeCompare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		wa, wb := primaryWeight(ra[i]), primaryWeight(rb[i])
		if wa != wb {
			if wa < wb {
				return -1
			}
			return 1
		}
	}
	if len(ra) != len(rb) {
		if len(ra) < len(rb) {
			return -1
		}
		return 1
	}
	for i := range ra {
		la := ra[i] >= 'a' && ra[i] <= 'z'
		lb := rb[i] >= 'a' && rb[i] <= 'z'
		if la != lb {
			if la {
				return -1
			}
			return 1
		}
	}
	return strings.Compare(a, b)
}
