package main

// package-lock.json (lockfileVersion 1 to 3), read with JavaScript semantics so that a
// lockfile of any JSON shape gives the original's packages, skips and errors.

import (
	"strconv"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// entries is Object.entries(v) for a truthy unit-form value.
func entries(v any) ([]string, []any) {
	switch x := v.(type) {
	case *jsv.Obj:
		keys := x.Keys()
		vals := make([]any, len(keys))
		for i, k := range keys {
			vals[i] = jsv.Prop(x, k)
		}
		return keys, vals
	case []any:
		keys := make([]string, len(x))
		for i := range x {
			keys[i] = strconv.Itoa(i)
		}
		return keys, x
	case string:
		var keys []string
		var vals []any
		for i, r := range []rune(x) {
			keys = append(keys, strconv.Itoa(i))
			vals = append(vals, string(r))
		}
		return keys, vals
	}
	return nil, nil
}

func isObject(v any) bool { return jsv.IsObject(v) || jsv.IsArray(v) }

func parsePackageLock(content string) (parsedLockfile, error) {
	var p parsedLockfile
	parsed, err := jsv.Parse(fromUnits(content))
	if err != nil {
		return p, osvPlainError("package-lock.json is not valid JSON: " + toUnits(err.Error()))
	}
	doc := deepToUnits(parsed)
	pkgs, pkgsOK, err := prop(doc, "packages")
	if err != nil {
		return p, err
	}
	if truthy(pkgs, pkgsOK) && isObject(pkgs) {
		keys, vals := entries(pkgs)
		for i, key := range keys {
			entry := vals[i]
			if key == "" || !truthy(entry, true) || !isObject(entry) {
				continue
			}
			if link, ok, _ := prop(entry, "link"); truthy(link, ok) {
				p.skipped = append(p.skipped, skippedEntry{key, "workspace link"})
				continue
			}
			name, nameOK, _ := prop(entry, "name")
			if nullish(name, nameOK) {
				name = key
				if idx := strings.LastIndex(key, "node_modules/"); idx >= 0 {
					name = key[idx+len("node_modules/"):]
				}
			}
			version, versionOK, _ := prop(entry, "version")
			if !truthy(version, versionOK) {
				p.skipped = append(p.skipped, skippedEntry{key, "no version"})
				continue
			}
			p.packages = append(p.packages, lockPackage{"npm", name, version})
		}
		return p, nil
	}
	deps, depsOK, err := prop(doc, "dependencies")
	if err != nil {
		return p, err
	}
	if !truthy(deps, depsOK) || !isObject(deps) {
		return p, osvPlainError("package-lock.json has neither a packages nor a dependencies map")
	}
	return p, p.walkDependencies(deps)
}

func (p *parsedLockfile) walkDependencies(deps any) error {
	names, raws := entries(deps)
	for i, name := range names {
		version, versionOK, err := prop(raws[i], "version")
		if err != nil {
			return err
		}
		switch {
		case truthy(version, versionOK) && !reNonRegistry.MatchString(toString(version)):
			p.packages = append(p.packages, lockPackage{"npm", name, version})
		case truthy(version, versionOK):
			p.skipped = append(p.skipped, skippedEntry{name, "non-registry version " + toString(version)})
		default:
			p.skipped = append(p.skipped, skippedEntry{name, "no version"})
		}
		nested, nestedOK, _ := prop(raws[i], "dependencies")
		if truthy(nested, nestedOK) {
			if err := p.walkDependencies(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

// osvPlainError is a plain Error thrown by the lockfile parsers.
func osvPlainError(message string) *jsError { return &jsError{name: "Error", message: message} }
