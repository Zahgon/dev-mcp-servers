package ajv

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var jsonTypes = map[string]bool{"string": true, "number": true, "integer": true, "boolean": true, "null": true, "object": true, "array": true}

// getSchemaTypes reads type and nullable like Ajv's getSchemaTypes.
func (it *schCxt) getSchemaTypes() ([]string, error) {
	raw, _ := it.obj().Get("type")
	var items []any
	switch t := raw.(type) {
	case []any:
		items = t
	default:
		if jsv.Truthy(raw, it.has("type")) {
			items = []any{raw}
		}
	}
	types := []string{}
	valid := true
	for _, x := range items {
		s, ok := x.(string)
		if !ok || !jsonTypes[s] {
			valid = false
		}
		types = append(types, jsv.ToString(x))
	}
	if !valid {
		return nil, errors.New("type must be JSONType or JSONType[]: " + joinStrings(items))
	}
	nullable, hasNullable := it.obj().Get("nullable")
	hasNull := false
	for _, t := range types {
		if t == "null" {
			hasNull = true
		}
	}
	if hasNull {
		if hasNullable && nullable == false {
			return nil, errors.New("type: null contradicts nullable: false")
		}
	} else {
		if len(types) == 0 && hasNullable {
			return nil, errors.New(`"nullable" cannot be used without "type"`)
		}
		if hasNullable && nullable == true {
			types = append(types, "null")
		}
	}
	return types, nil
}

// joinStrings is Array.prototype.join(",") on arbitrary values.
func joinStrings(items []any) string { return jsv.ToString(items) }

func (it *schCxt) shouldUseGroup(g *ruleGroup) bool {
	for _, r := range g.rules {
		if it.has(r.keyword) {
			return true
		}
	}
	return false
}

func (it *schCxt) hasRulesButRef() bool {
	for _, k := range it.obj().Keys() {
		if k != "$ref" && it.a.rules.all[k] != nil {
			return true
		}
	}
	return false
}

func (it *schCxt) reportType(r *run, d dpos) {
	t := it.get("type")
	it.report(r, d, "type", jsv.O("type", t), "must be "+jsv.ToString(t))
}

// typeAndKeywords is Ajv's typeAndKeywords plus schemaKeywords for one schema object.
func (it *schCxt) typeAndKeywords() (step, error) {
	types, err := it.getSchemaTypes()
	if err != nil {
		return nil, err
	}
	checkTypes := len(types) > 0
	if len(types) == 1 {
		if g := it.a.rules.group(types[0]); g != nil && it.shouldUseGroup(g) {
			checkTypes = false
		}
	}
	var steps []step
	if checkTypes {
		steps = append(steps, func(r *run, _ *frame, d dpos) {
			if !isAnyType(types, d.data) {
				it.reportType(r, d)
			}
		})
	}
	if ref, ok := it.obj().Get("$ref"); ok && jsv.Truthy(ref, true) && !it.hasRulesButRef() {
		s, err := it.keywordCode(it.a.rules.all["$ref"], "")
		if err != nil {
			return nil, err
		}
		return seq(append(steps, s)...), nil
	}
	type groupStep struct {
		typ      string
		body     step
		elseType bool
	}
	var groups []groupStep
	for _, g := range it.a.rules.groups {
		if !it.shouldUseGroup(g) {
			continue
		}
		var kws []step
		for _, rule := range g.rules {
			if !it.has(rule.keyword) {
				continue
			}
			s, err := it.keywordCode(rule, g.typ)
			if err != nil {
				return nil, err
			}
			if s != nil {
				kws = append(kws, s)
			}
		}
		groups = append(groups, groupStep{g.typ, seq(kws...), len(types) == 1 && types[0] == g.typ && !checkTypes})
	}
	allErrors := it.allErrors
	return func(r *run, f *frame, d dpos) {
		start := len(r.errs)
		for _, s := range steps {
			s(r, f, d)
		}
		for _, g := range groups {
			switch {
			case g.typ == "" || isJSType(g.typ, d.data):
				runOp(g.body, r, f, d)
			case g.elseType:
				it.reportType(r, d)
			}
			if !allErrors && len(r.errs) != start {
				return
			}
		}
	}, nil
}

// kwCxt is Ajv's KeywordCxt for one keyword of one schema object.
type kwCxt struct {
	it       *schCxt
	keyword  string
	schema   any
	parent   *jsv.Obj
	def      *kwDef
	ruleType string
}

func schemaTypeJSON(t []string) string {
	b, _ := json.Marshal(t)
	return string(b)
}

func validSchemaType(v any, types []string, allowUndefined bool) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		switch t {
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if jsv.IsObject(v) {
				return true
			}
		case "number":
			if isNumber(v) {
				return true
			}
		case "string", "boolean":
			if isJSType(t, v) {
				return true
			}
		}
	}
	return false
}

func (it *schCxt) keywordCode(rule *rule, ruleType string) (step, error) {
	def := rule.def
	for _, dep := range def.dependencies {
		if !it.has(dep) {
			return nil, fmt.Errorf("parent schema must have dependencies of %s: %s", rule.keyword, joinStrings(jsv.Strings(def.dependencies)))
		}
	}
	v := it.get(rule.keyword)
	if !validSchemaType(v, def.schemaType, def.allowUndefined) {
		return nil, fmt.Errorf("%s value must be %s", rule.keyword, schemaTypeJSON(def.schemaType))
	}
	if def.code == nil {
		return nil, nil
	}
	return def.code(&kwCxt{it: it, keyword: rule.keyword, schema: v, parent: it.obj(), def: def, ruleType: ruleType})
}

// fail reports this keyword's error when cond holds.
func (c *kwCxt) errorStep(params func(d dpos) *jsv.Obj, msg func(d dpos) string, cond func(d dpos) bool) step {
	it, kw := c.it, c.keyword
	return func(r *run, _ *frame, d dpos) {
		if cond(d) {
			it.report(r, d, kw, params(d), msg(d))
		}
	}
}

func (c *kwCxt) sub(a subArgs) (*schCxt, vstep, error) {
	if a.keyword == "" && !a.explicit {
		a.keyword = c.keyword
	}
	child, err := c.it.sub(a)
	if err != nil {
		return nil, nil, err
	}
	s, err := child.compileSub()
	if err != nil {
		return nil, nil, err
	}
	return child, s, nil
}
