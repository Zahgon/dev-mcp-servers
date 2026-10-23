package main

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/ajv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// drafts are the values of validate_json's draft argument.
var drafts = []string{"auto", "draft-07", "2019-09", "2020-12"}

const maxErrors = 200

func detectDraft(schema *jsv.Obj) string {
	id, _ := jsv.Prop(schema, "$schema").(string)
	switch {
	case strings.Contains(id, "2020-12"):
		return "2020-12"
	case strings.Contains(id, "2019-09"):
		return "2019-09"
	}
	return "draft-07"
}

var oldDraftRe = regexp.MustCompile(`draft-0[46]`)

func ajvDraft(d string) ajv.Draft {
	switch d {
	case "2020-12":
		return ajv.Draft2020
	case "2019-09":
		return ajv.Draft2019
	}
	return ajv.Draft07
}

// validate compiles the schema with Ajv for the draft and validates data.
func validate(schema *jsv.Obj, data any, draft string) (*jsv.Obj, error) {
	d := draft
	if d == "auto" {
		d = detectDraft(schema)
	}
	if s, ok := jsv.Prop(schema, "$schema").(string); ok && oldDraftRe.MatchString(s) {
		return nil, errors.New(s + " is not supported; Ajv supports draft-07, 2019-09 and 2020-12. Remove $schema or update it.")
	}
	fn, err := ajv.New(ajvDraft(d)).Compile(schema)
	if err != nil {
		return nil, errors.New("schema does not compile: " + err.Error())
	}
	valid, errs, err := fn.Validate(data)
	if err != nil {
		return nil, err
	}
	list := []any{}
	for i, e := range errs {
		if i == maxErrors {
			break
		}
		path, _ := jsv.Prop(e, "instancePath").(string)
		shown, label := path, path
		if shown == "" {
			shown, label = "/", "(root)"
		}
		msg := ""
		if m, ok := e.Get("message"); ok && m != nil {
			msg = jsv.ToString(m)
		}
		list = append(list, jsv.O("path", shown, "schema_path", jsv.Prop(e, "schemaPath"), "keyword", jsv.Prop(e, "keyword"),
			"message", jsv.Trim(label+" "+msg), "params", jsv.Prop(e, "params")))
	}
	return jsv.O("valid", valid, "draft", d, "error_count", float64(len(errs)), "errors", list), nil
}

type format struct {
	name string
	re   *regexp.Regexp
}

const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var formats = []format{
	{"date-time", regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt ][0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]+)?)?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)},
	{"date", regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)},
	{"time", regexp.MustCompile(`^[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})?$`)},
	{"email", regexp.MustCompile(`^[^@` + jsSpace + `]+@[^@` + jsSpace + `]+\.[^@` + jsSpace + `]+$`)},
	{"uuid", regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)},
	{"uri", regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://[^` + jsSpace + `]+$`)},
	{"ipv4", regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)},
}

type inferOptions struct {
	enumThreshold float64
	detectFormats bool
}

func typeOf(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "null", nil
	case []any:
		return "array", nil
	case bool:
		return "boolean", nil
	case float64:
		if !math.IsInf(x, 0) && math.Trunc(x) == x {
			return "integer", nil
		}
		return "number", nil
	case string:
		return "string", nil
	case *jsv.Obj, protoValue:
		return "object", nil
	}
	return "", errors.New("cannot infer a schema for a function value")
}

// lessUTF16 is the default Array#sort order: UTF-16 code unit comparison.
func lessUTF16(a, b string) bool {
	x, y := jsv.UTF16Units(a), jsv.UTF16Units(b)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// protoValue is Object.prototype, read as o.__proto__ from an object without that key.
type protoValue struct{}

// funcValue is a function inherited from Object.prototype.
type funcValue struct{}

var protoNames = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "__proto__": true, "toLocaleString": true,
}

// getProp is o[key] on a plain object, with the values inherited from Object.prototype.
func getProp(o any, key string) (any, bool) {
	if obj, ok := o.(*jsv.Obj); ok {
		if v, has := obj.Get(key); has {
			return v, true
		}
	}
	if key == "__proto__" {
		if _, isProto := o.(protoValue); isProto {
			return nil, true
		}
		return protoValue{}, true
	}
	if protoNames[key] {
		return funcValue{}, true
	}
	return nil, false
}

func inferSchema(samples []any, opts inferOptions, depth int) (*jsv.Obj, error) {
	schema := jsv.NewObj()
	if len(samples) == 0 || depth > 32 {
		return schema, nil
	}
	var types []string
	seen := map[string]bool{}
	for _, s := range samples {
		t, err := typeOf(s)
		if err != nil {
			return nil, err
		}
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	if seen["number"] && seen["integer"] {
		for i, t := range types {
			if t == "integer" {
				types = append(types[:i], types[i+1:]...)
				break
			}
		}
		delete(seen, "integer")
	}
	if len(types) == 1 {
		schema.Set("type", types[0])
	} else {
		schema.Set("type", jsv.Strings(types))
	}
	if seen["string"] {
		inferStrings(schema, samples, opts)
	}
	if seen["object"] {
		if err := inferObjects(schema, samples, opts, depth); err != nil {
			return nil, err
		}
	}
	if seen["array"] {
		var items []any
		for _, s := range samples {
			if arr, ok := s.([]any); ok {
				items = append(items, arr...)
			}
		}
		if len(items) == 0 {
			schema.Set("items", jsv.NewObj())
		} else {
			sub, err := inferSchema(items, opts, depth+1)
			if err != nil {
				return nil, err
			}
			schema.Set("items", sub)
		}
	}
	return schema, nil
}

func inferStrings(schema *jsv.Obj, samples []any, opts inferOptions) {
	var strs []string
	for _, s := range samples {
		if str, ok := s.(string); ok {
			strs = append(strs, str)
		}
	}
	if opts.detectFormats && len(strs) > 0 {
		for _, f := range formats {
			all := true
			for _, s := range strs {
				if !f.re.MatchString(s) {
					all = false
					break
				}
			}
			if all {
				schema.Set("format", f.name)
				break
			}
		}
	}
	if opts.enumThreshold > 0 && len(strs) >= 3 && !schema.Has("format") {
		var distinct []string
		have := map[string]bool{}
		for _, s := range strs {
			if !have[s] {
				have[s] = true
				distinct = append(distinct, s)
			}
		}
		if float64(len(distinct)) <= opts.enumThreshold && len(distinct) < len(strs) {
			sort.SliceStable(distinct, func(i, j int) bool { return lessUTF16(distinct[i], distinct[j]) })
			schema.Set("enum", jsv.Strings(distinct))
		}
	}
}

func inferObjects(schema *jsv.Obj, samples []any, opts inferOptions, depth int) error {
	var objects []any
	var keys []string
	have := map[string]bool{}
	for _, s := range samples {
		switch o := s.(type) {
		case *jsv.Obj:
			objects = append(objects, o)
			for _, k := range o.Keys() {
				if !have[k] {
					have[k] = true
					keys = append(keys, k)
				}
			}
		case protoValue:
			objects = append(objects, o)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
	properties := jsv.NewObj()
	required := []any{}
	for _, key := range keys {
		var values []any
		for _, o := range objects {
			if v, ok := getProp(o, key); ok {
				values = append(values, v)
			}
		}
		sub, err := inferSchema(values, opts, depth+1)
		if err != nil {
			return err
		}
		if key != "__proto__" {
			properties.Set(key, sub)
		}
		if len(values) == len(objects) {
			required = append(required, key)
		}
	}
	schema.Set("properties", properties)
	if len(required) > 0 {
		schema.Set("required", required)
	}
	return nil
}
