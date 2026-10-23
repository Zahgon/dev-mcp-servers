package mcp

import (
	"fmt"
	"math"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// ArgError is a tool argument that fails the tool's input schema.
type ArgError struct{ msg string }

func (e ArgError) Error() string { return e.msg }

// Invalid is the SDK's input validation failure for one argument.
func Invalid(tool, why string) error {
	return ArgError{fmt.Sprintf("MCP error -32602: Input validation error: Invalid arguments for tool %s: %s", tool, why)}
}

// Args reads tool arguments with zod-like checks. Lengths count UTF-16 code units.
type Args struct {
	Tool string
	Obj  *jsv.Obj
}

func (a Args) raw(key string) (any, bool) {
	// zod's optional() accepts undefined only, so a present null fails the type check.
	return a.Obj.Get(key)
}

// String reads an optional string with a length range; max < 0 means unbounded.
func (a Args) String(key string, min, max int) (*string, error) {
	raw, ok := a.raw(key)
	if !ok {
		return nil, nil
	}
	s, isString := raw.(string)
	if !isString {
		return nil, Invalid(a.Tool, key+": Expected string")
	}
	n := jsv.UTF16Len(s)
	if n < min {
		return nil, Invalid(a.Tool, fmt.Sprintf("%s: String must contain at least %d character(s)", key, min))
	}
	if max >= 0 && n > max {
		return nil, Invalid(a.Tool, fmt.Sprintf("%s: String must contain at most %d character(s)", key, max))
	}
	return &s, nil
}

// RequiredString reads a string that must be present.
func (a Args) RequiredString(key string, min, max int) (string, error) {
	s, err := a.String(key, min, max)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", Invalid(a.Tool, key+": Required")
	}
	return *s, nil
}

// Enum reads an optional string that must be one of allowed.
func (a Args) Enum(key string, allowed []string) (*string, error) {
	raw, ok := a.raw(key)
	if !ok {
		return nil, nil
	}
	if s, isString := raw.(string); isString {
		for _, x := range allowed {
			if x == s {
				return &s, nil
			}
		}
	}
	return nil, Invalid(a.Tool, key+": Invalid enum value")
}

// Number reads an optional number in [min, max]; integer requires a whole number.
func (a Args) Number(key string, min, max float64, integer bool) (*float64, error) {
	raw, ok := a.raw(key)
	if !ok {
		return nil, nil
	}
	f, isNum := raw.(float64)
	if !isNum || math.IsNaN(f) {
		return nil, Invalid(a.Tool, key+": Expected number")
	}
	if integer && (f != math.Trunc(f) || math.IsInf(f, 0)) {
		return nil, Invalid(a.Tool, key+": Expected integer")
	}
	if f < min || f > max {
		return nil, Invalid(a.Tool, key+": Number out of range")
	}
	return &f, nil
}

// Bool reads an optional boolean.
func (a Args) Bool(key string) (*bool, error) {
	raw, ok := a.raw(key)
	if !ok {
		return nil, nil
	}
	b, isBool := raw.(bool)
	if !isBool {
		return nil, Invalid(a.Tool, key+": Expected boolean")
	}
	return &b, nil
}

// Array reads an optional array with an item count range; max < 0 means unbounded.
func (a Args) Array(key string, min, max int) ([]any, bool, error) {
	raw, ok := a.raw(key)
	if !ok {
		return nil, false, nil
	}
	arr, isArr := raw.([]any)
	if !isArr {
		return nil, true, Invalid(a.Tool, key+": Expected array")
	}
	if len(arr) < min || (max >= 0 && len(arr) > max) {
		return nil, true, Invalid(a.Tool, key+": Array length out of range")
	}
	return arr, true, nil
}
