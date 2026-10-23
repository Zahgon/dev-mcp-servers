package main

// JavaScript value semantics the TypeScript code relies on when it reads JSON of any
// shape: property reads that throw on null, for-of and spread iteration, String()
// conversion, Set membership and the values JSON.stringify drops.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// special is a JavaScript value with no JSON form.
type special int

const (
	// undefined is the value of a missing property kept in an array or Set.
	undefined special = iota
	// stringMethod is String.prototype.fixed, the one string method among the property
	// names the server reads.
	stringMethod
)

// jsError is a thrown JavaScript error: its class name, its message, and whether it is
// the server's own OsvError.
type jsError struct {
	name, message string
	osv           bool
}

func (e *jsError) Error() string { return e.message }

// String is String(err).
func (e *jsError) String() string { return e.name + ": " + e.message }

func typeError(format string, args ...any) *jsError {
	return &jsError{name: "TypeError", message: fmt.Sprintf(format, args...)}
}

func osvError(format string, args ...any) *jsError {
	return &jsError{name: "Error", message: fmt.Sprintf(format, args...), osv: true}
}

// prop is v[key]; reading a property of null or undefined throws a TypeError.
func prop(v any, key string) (any, bool, error) {
	switch x := v.(type) {
	case nil:
		return nil, false, typeError("Cannot read properties of null (reading '%s')", key)
	case special:
		if x == undefined {
			return nil, false, typeError("Cannot read properties of undefined (reading '%s')", key)
		}
	case *jsv.Obj:
		val, ok := x.Get(key)
		return val, ok, nil
	case []any:
		if i, isIndex := jsv.ArrayIndex(key); isIndex && i < uint64(len(x)) {
			return x[i], true, nil
		}
	case string:
		if key == "fixed" {
			return stringMethod, true, nil
		}
		if i, isIndex := jsv.ArrayIndex(key); isIndex {
			units := jsv.CodeUnits(x)
			if i < uint64(len(units)) {
				return units[i], true, nil
			}
		}
	}
	return nil, false, nil
}

// optionalProp is v?.[key]: nullish v gives undefined.
func optionalProp(v any, present bool, key string) (any, bool, error) {
	if !present || v == nil || v == undefined {
		return nil, false, nil
	}
	return prop(v, key)
}

func nullish(v any, present bool) bool { return !present || v == nil || v == undefined }

func truthy(v any, present bool) bool {
	switch v {
	case undefined:
		return false
	case stringMethod:
		return true
	}
	return jsv.Truthy(v, present)
}

// describe is how V8 names a non-iterable value in "... is not iterable".
func describe(v any) string {
	switch x := v.(type) {
	case float64:
		return "number " + jsv.FormatNumber(x)
	case bool:
		return "boolean " + strconv.FormatBool(x)
	case special:
		return "function fixed() { [native code] }"
	}
	return "object"
}

// iterate is the items of `for (const x of v ?? [])` when orEmpty is set, or of
// `for (const x of v)` written as expr otherwise.
func iterate(v any, present, orEmpty bool, expr string) ([]any, error) {
	if nullish(v, present) {
		if orEmpty {
			return nil, nil
		}
		return nil, typeError("%s is not iterable", expr)
	}
	switch x := v.(type) {
	case []any:
		return x, nil
	case string:
		return jsv.CodePoints(x), nil
	}
	if orEmpty {
		return nil, typeError("%s is not iterable (cannot read property Symbol(Symbol.iterator))", describe(v))
	}
	return nil, typeError("%s is not iterable", expr)
}

// spread is the items of `...(v ?? [])`.
func spread(v any, present bool) ([]any, error) {
	if nullish(v, present) {
		return nil, nil
	}
	switch x := v.(type) {
	case []any:
		return x, nil
	case string:
		return jsv.CodePoints(x), nil
	}
	return nil, typeError("Spread syntax requires ...iterable[Symbol.iterator] to be a function")
}

// toString is String(v) for a present value.
func toString(v any) string {
	switch x := v.(type) {
	case special:
		if x == undefined {
			return "undefined"
		}
		return "function fixed() { [native code] }"
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item != nil && item != undefined {
				parts[i] = toString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return jsv.ToString(v)
}

func templateString(v any, present bool) string {
	if !present {
		return "undefined"
	}
	return toString(v)
}

// valueSet is a JavaScript Set: SameValueZero for primitives, identity for objects.
type valueSet struct {
	items []any
	seen  map[any]bool
}

func (s *valueSet) add(v any) {
	if s.seen == nil {
		s.seen = map[any]bool{}
	}
	switch v.(type) {
	case []any, *jsv.Obj:
		s.items = append(s.items, v)
		return
	}
	key := v
	if f, isNum := v.(float64); isNum && f == 0 {
		key = float64(0)
	}
	if !s.seen[key] {
		s.seen[key] = true
		s.items = append(s.items, v)
	}
}

// toJSON prepares a value for JSON.stringify: object properties holding undefined or a
// function are dropped, array items holding them become null.
func toJSON(v any) any {
	switch x := v.(type) {
	case special:
		return nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = toJSON(item)
		}
		return out
	case *jsv.Obj:
		out := jsv.NewObj()
		for _, k := range x.Keys() {
			item := jsv.Prop(x, k)
			if _, dropped := item.(special); dropped {
				continue
			}
			out.Set(k, toJSON(item))
		}
		return out
	}
	return v
}
