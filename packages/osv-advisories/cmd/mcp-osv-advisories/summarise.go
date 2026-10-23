package main

// summarise condenses an OSV record the way the TypeScript summarise() does, for a record
// of any JSON shape: properties the code reads are read with JavaScript semantics, so a
// malformed record fails with the TypeError V8 would throw.

import (
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

func summarise(v any) (*jsv.Obj, error) {
	var severity []any
	sev, sevOK, err := prop(v, "severity")
	if err != nil {
		return nil, err
	}
	items, err := iterate(sev, sevOK, true, "v.severity")
	if err != nil {
		return nil, err
	}
	for _, s := range items {
		typ, typOK, err := prop(s, "type")
		if err != nil {
			return nil, err
		}
		score, scoreOK, err := prop(s, "score")
		if err != nil {
			return nil, err
		}
		severity = append(severity, templateString(typ, typOK)+": "+templateString(score, scoreOK))
	}
	db, dbOK, err := prop(v, "database_specific")
	if err != nil {
		return nil, err
	}
	dbSeverity, _, err := optionalProp(db, dbOK, "severity")
	if err != nil {
		return nil, err
	}
	if s, isString := dbSeverity.(string); isString {
		severity = append(severity, "database: "+s)
	}
	ranges, fixed, err := affectedRanges(v)
	if err != nil {
		return nil, err
	}
	out := jsv.NewObj()
	if id, ok, _ := prop(v, "id"); ok {
		out.Set("id", id)
	}
	aliases, aliasesOK, _ := prop(v, "aliases")
	if nullish(aliases, aliasesOK) {
		aliases = []any{}
	}
	out.Set("aliases", aliases)
	summary, err := summaryText(v)
	if err != nil {
		return nil, err
	}
	out.Set("summary", summary)
	out.Set("severity", append([]any{}, severity...))
	for _, key := range []string{"published", "modified", "withdrawn"} {
		if value, ok, _ := prop(v, key); ok {
			out.Set(key, value)
		}
	}
	out.Set("affected_ranges", ranges)
	out.Set("fixed_versions", append([]any{}, fixed.items...))
	references, err := referenceURLs(v)
	if err != nil {
		return nil, err
	}
	out.Set("references", references)
	return out, nil
}

// summaryText is v.summary ?? (v.details ? v.details.slice(0, 300) : "").
func summaryText(v any) (any, error) {
	summary, ok, _ := prop(v, "summary")
	if !nullish(summary, ok) {
		return summary, nil
	}
	details, ok, _ := prop(v, "details")
	if !truthy(details, ok) {
		return "", nil
	}
	switch d := details.(type) {
	case string:
		return jsv.UTF16Slice(d, 300), nil
	case []any:
		return append([]any{}, d[:min(300, len(d))]...), nil
	}
	return nil, typeError("v.details.slice is not a function")
}

func affectedRanges(v any) ([]any, *valueSet, error) {
	ranges := []any{}
	fixed := &valueSet{}
	affected, ok, err := prop(v, "affected")
	if err != nil {
		return nil, nil, err
	}
	list, err := iterate(affected, ok, true, "v.affected")
	if err != nil {
		return nil, nil, err
	}
	for _, a := range list {
		rs, rsOK, err := prop(a, "ranges")
		if err != nil {
			return nil, nil, err
		}
		rangeList, err := iterate(rs, rsOK, true, "a.ranges")
		if err != nil {
			return nil, nil, err
		}
		pkg, pkgOK, _ := prop(a, "package")
		for _, r := range rangeList {
			entry := jsv.NewObj()
			for _, key := range []string{"name", "ecosystem"} {
				value, present, err := optionalProp(pkg, pkgOK, key)
				if err != nil {
					return nil, nil, err
				}
				if nullish(value, present) {
					value = ""
				}
				entry.Set(map[string]string{"name": "package", "ecosystem": "ecosystem"}[key], value)
			}
			events, eventsOK, err := prop(r, "events")
			if err != nil {
				return nil, nil, err
			}
			eventList, err := iterate(events, eventsOK, false, "r.events")
			if err != nil {
				return nil, nil, err
			}
			for _, e := range eventList {
				for _, key := range []string{"introduced", "fixed", "last_affected"} {
					value, present, err := prop(e, key)
					if err != nil {
						return nil, nil, err
					}
					if !present {
						continue
					}
					entry.Set(key, value)
					if key == "fixed" {
						fixed.add(value)
					}
				}
			}
			ranges = append(ranges, entry)
		}
	}
	return ranges, fixed, nil
}

// referenceURLs is (v.references ?? []).slice(0, 8).map((r) => r.url).
func referenceURLs(v any) ([]any, error) {
	refs, ok, _ := prop(v, "references")
	if nullish(refs, ok) {
		refs = []any{}
	}
	var list []any
	switch r := refs.(type) {
	case []any:
		list = r[:min(8, len(r))]
	case string:
		return nil, typeError("(v.references ?? []).slice(...).map is not a function")
	default:
		return nil, typeError("(v.references ?? []).slice is not a function")
	}
	out := []any{}
	for _, r := range list {
		url, present, err := prop(r, "url")
		if err != nil {
			return nil, err
		}
		if !present {
			url = undefined
		}
		out = append(out, url)
	}
	return out, nil
}
