package main

import (
	"fmt"
	"strconv"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// opt is a JavaScript property read: the value and whether it is defined.
type opt struct {
	v  any
	ok bool
}

func field(o *jsv.Obj, key string) opt {
	v, ok := o.Get(key)
	return opt{v, ok}
}

// same is JSON.stringify(a) === JSON.stringify(b).
func same(a, b opt) bool {
	if !a.ok || !b.ok {
		return a.ok == b.ok
	}
	return jsv.Stringify(a.v, 0) == jsv.Stringify(b.v, 0)
}

// identical is a !== b negated: primitives by value, objects by reference.
func identical(a, b opt) bool {
	if !a.ok || !b.ok {
		return a.ok == b.ok
	}
	switch x := a.v.(type) {
	case *jsv.Obj:
		y, ok := b.v.(*jsv.Obj)
		return ok && x == y
	case []any:
		return false
	}
	if _, isArr := b.v.([]any); isArr {
		return false
	}
	if _, isObj := b.v.(*jsv.Obj); isObj {
		return false
	}
	return a.v == b.v
}

func asSchema(v any) *jsv.Obj {
	if o, ok := v.(*jsv.Obj); ok {
		return o
	}
	return jsv.NewObj()
}

func asTypes(t opt) []string {
	switch x := t.v.(type) {
	case []any:
		out := make([]string, len(x))
		for i, v := range x {
			out[i] = jsv.ToString(v)
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// jsKeys is Object.keys on any JSON value.
func jsKeys(v any) []string {
	switch x := v.(type) {
	case *jsv.Obj:
		return x.Keys()
	case []any:
		out := make([]string, len(x))
		for i := range x {
			out[i] = strconv.Itoa(i)
		}
		return out
	case string:
		n := jsv.UTF16Len(x)
		out := make([]string, n)
		for i := range out {
			out[i] = strconv.Itoa(i)
		}
		return out
	}
	return nil
}

var arrayProtoNames = map[string]bool{
	"length": true, "at": true, "concat": true, "copyWithin": true, "entries": true, "every": true, "fill": true, "filter": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "flat": true, "flatMap": true, "forEach": true,
	"includes": true, "indexOf": true, "join": true, "keys": true, "lastIndexOf": true, "map": true, "pop": true, "push": true,
	"reduce": true, "reduceRight": true, "reverse": true, "shift": true, "slice": true, "some": true, "sort": true, "splice": true,
	"toReversed": true, "toSorted": true, "toSpliced": true, "unshift": true, "values": true, "with": true,
}

// jsIn is the `in` operator; a primitive right-hand side is a TypeError.
func jsIn(key string, v any) (bool, error) {
	switch x := v.(type) {
	case *jsv.Obj:
		return x.Has(key) || protoNames[key], nil
	case []any:
		if i, ok := jsv.ArrayIndex(key); ok && int(i) < len(x) {
			return true, nil
		}
		return arrayProtoNames[key] || protoNames[key], nil
	}
	return false, fmt.Errorf("Cannot use 'in' operator to search for '%s' in %s", key, jsv.ToString(v))
}

func valueAt(v any, key string) any {
	if o, ok := v.(*jsv.Obj); ok {
		return jsv.Prop(o, key)
	}
	if arr, ok := v.([]any); ok {
		if i, isIdx := jsv.ArrayIndex(key); isIdx && int(i) < len(arr) {
			return arr[i]
		}
	}
	return nil
}

type differ struct{ out []*jsv.Obj }

func (df *differ) push(path, change, impact string, before, after opt, reason string) {
	c := jsv.O("path", path, "change", change, "impact", impact)
	if before.ok {
		c.Set("before", before.v)
	}
	if after.ok {
		c.Set("after", after.v)
	}
	c.Set("reason", reason)
	df.out = append(df.out, c)
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func requiredSet(o *jsv.Obj) []string {
	arr, ok := jsv.Prop(o, "required").([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range arr {
		s := jsv.ToString(v)
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func orEmpty(o opt) any {
	if !o.ok || o.v == nil {
		return jsv.NewObj()
	}
	return o.v
}

var none = opt{}

// diff compares before and after from the point of view of data valid under before.
func (df *differ) diff(before, after any, path string, depth int) error {
	if depth > 64 {
		return nil
	}
	b, a := asSchema(before), asSchema(after)
	p := path
	if p == "" {
		p = "/"
	}
	base := pick(p == "/", "", p)
	if bt, at := field(b, "type"), field(a, "type"); !same(bt, at) {
		bts, ats := asTypes(bt), asTypes(at)
		widened := true
		for _, t := range bts {
			if contains(ats, t) {
				continue
			}
			ok, err := widensTo(t, ats)
			if err != nil {
				return err
			}
			if !ok {
				widened = false
				break
			}
		}
		impact := pick(len(bts) == 0, "breaking", pick(widened, "compatible", "breaking"))
		df.push(p, "type", impact, bt, at, pick(widened, "every previous type is still accepted", "values of a previously accepted type are now rejected"))
	}
	if err := df.properties(b, a, base, depth); err != nil {
		return err
	}
	df.additional(b, a, base)
	df.enum(b, a, base)
	df.bounds(b, a, base)
	for _, kw := range []string{"pattern", "format", "const", "multipleOf", "uniqueItems", "$ref", "contentEncoding"} {
		bv, av := field(b, kw), field(a, kw)
		if same(bv, av) {
			continue
		}
		impact := pick(!av.ok, "compatible", pick(!bv.ok, "breaking", "review"))
		reason := pick(!av.ok, "the constraint was removed", pick(!bv.ok, "a new constraint applies", "the constraint changed; whether old values still pass depends on the data"))
		df.push(base+"/"+kw, kw, impact, bv, av, reason)
	}
	if err := df.items(b, a, base, depth); err != nil {
		return err
	}
	for _, kw := range []string{"oneOf", "anyOf", "allOf", "not", "if", "then", "else", "patternProperties", "dependentRequired", "$defs", "definitions"} {
		if bv, av := field(b, kw), field(a, kw); !same(bv, av) {
			df.push(base+"/"+kw, kw, "review", bv, av, "composition keywords are compared as a whole; inspect the change by hand")
		}
	}
	for _, kw := range []string{"title", "description", "examples", "default", "deprecated", "$comment"} {
		if bv, av := field(b, kw), field(a, kw); !same(bv, av) {
			df.push(base+"/"+kw, kw, "compatible", bv, av, "annotation only, no effect on validation")
		}
	}
	return nil
}

// widensTo is (TYPE_WIDENING[t] ?? []).some((w) => at.includes(w)).
func widensTo(t string, ats []string) (bool, error) {
	if t == "integer" {
		return contains(ats, "number"), nil
	}
	if protoNames[t] {
		return false, fmt.Errorf("(TYPE_WIDENING[t] ?? []).some is not a function")
	}
	return false, nil
}
