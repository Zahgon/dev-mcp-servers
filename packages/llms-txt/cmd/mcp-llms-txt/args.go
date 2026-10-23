package main

// Reading tool arguments with the checks of the tools' zod schemas.

import (
	"fmt"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

// args reads tool arguments and remembers the first validation failure.
type args struct {
	a   mcp.Args
	err error
}

func (r *args) str(key string, min, max int) optString {
	s, err := r.a.String(key, min, max)
	if r.err == nil {
		r.err = err
	}
	if s == nil {
		return optString{}
	}
	return optString{toUnits(*s), true}
}

func (r *args) required(key string, min, max int) string {
	s := r.str(key, min, max)
	if r.err == nil && !s.set {
		r.err = mcp.Invalid(r.a.Tool, key+": Required")
	}
	return s.value
}

func (r *args) strings(key string, min, max int) []string {
	items, _, err := r.a.Array(key, 0, 50)
	if err != nil {
		if r.err == nil {
			r.err = err
		}
		return nil
	}
	out := []string{}
	for k, item := range items {
		s, isString := item.(string)
		n := jsv.UTF16Len(s)
		if !isString || n < min || n > max {
			if r.err == nil {
				r.err = mcp.Invalid(r.a.Tool, fmt.Sprintf("%s.%d: Invalid input", key, k))
			}
			return nil
		}
		out = append(out, toUnits(s))
	}
	return out
}

func (r *args) integer(key string, max float64, fallback int) int {
	n, err := r.a.Number(key, 1, max, true)
	if r.err == nil {
		r.err = err
	}
	if n == nil {
		return fallback
	}
	return int(*n)
}
