package ajv

import (
	"fmt"
	"math"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

func codeUnevaluatedProperties(c *kwCxt) (step, error) {
	it := c.it
	props := it.props
	var sub vstep
	if c.schema != false && !it.a.alwaysValid(c.schema) {
		var err error
		if _, sub, err = c.sub(subArgs{}); err != nil {
			return nil, err
		}
	}
	it.props = evProps{kind: evTrue}
	if props.kind == evTrue {
		return nil, nil
	}
	static := map[string]bool{}
	for _, p := range props.set {
		static[p] = true
	}
	unevaluated := func(f *frame, key string) bool {
		switch props.kind {
		case evUndef:
			return true
		case evStatic:
			return !static[key]
		}
		switch m := f.slots[props.slot].(type) {
		case nil:
			return true
		case map[string]bool:
			return !m[key] && !objectProtoNames[key]
		}
		return false
	}
	schemaFalse := c.schema == false
	return func(r *run, f *frame, d dpos) {
		if props.kind == evName && f.slots[props.slot] == true {
			return
		}
		for _, key := range ownKeys(d.data) {
			if !unevaluated(f, key) {
				continue
			}
			if schemaFalse {
				it.report(r, d, "unevaluatedProperties", jsv.O("unevaluatedProperty", key), "must NOT have unevaluated properties")
				if !it.allErrors {
					return
				}
				continue
			}
			if sub != nil {
				v, _ := lookup(d.data, key)
				if !sub(r, f, d.prop(key, v)) && !it.allErrors {
					return
				}
			}
		}
	}, nil
}

// jsNumber is the numeric value of an evaluated-items runtime value in a comparison.
func jsNumber(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	}
	return math.NaN()
}

func codeUnevaluatedItems(c *kwCxt) (step, error) {
	it := c.it
	items := it.items
	if items.kind == evTrue {
		return nil, nil
	}
	var sub vstep
	if c.schema != false && jsv.IsObject(c.schema) && !it.a.alwaysValid(c.schema) {
		var err error
		if _, sub, err = c.sub(subArgs{}); err != nil {
			return nil, err
		}
	}
	it.items = evItems{kind: evTrue}
	limit := func(f *frame) any {
		if items.kind == evName {
			return f.slots[items.slot]
		}
		return float64(items.n)
	}
	if c.schema == false {
		return func(r *run, f *frame, d dpos) {
			lim := limit(f)
			if float64(len(d.data.([]any))) > jsNumber(lim) {
				it.report(r, d, "unevaluatedItems", jsv.O("limit", lim), "must NOT have more than "+jsv.StringOrUndefined(lim, lim != nil || items.kind != evName)+" items")
			}
		}, nil
	}
	if sub == nil {
		return nil, nil
	}
	return func(r *run, f *frame, d dpos) {
		arr := d.data.([]any)
		from := jsNumber(limit(f))
		if float64(len(arr)) <= from {
			return
		}
		for i := int(from); i < len(arr); i++ {
			if !sub(r, f, d.index(i, arr[i])) && !it.allErrors {
				return
			}
		}
	}, nil
}

func codeFormat(c *kwCxt) (step, error) {
	it := c.it
	if it.a.inMeta {
		return nil, nil
	}
	name := c.schema.(string)
	def := it.a.formats[name]
	if def == nil || def.ignore {
		return nil, nil
	}
	fmtType := "string"
	if def.number {
		fmtType = "number"
	}
	if fmtType != c.ruleType {
		return nil, nil
	}
	params := jsv.O("format", name)
	msg := `must match format "` + name + `"`
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
		return !def.validate(d.data)
	}), nil
}

var formatLimitOps = map[string]struct {
	okStr string
	fail  func(x float64) bool
}{
	"formatMaximum":          {"<=", func(x float64) bool { return x > 0 }},
	"formatMinimum":          {">=", func(x float64) bool { return x < 0 }},
	"formatExclusiveMaximum": {"<", func(x float64) bool { return x >= 0 }},
	"formatExclusiveMinimum": {">", func(x float64) bool { return x <= 0 }},
}

func codeFormatLimit(c *kwCxt) (step, error) {
	it := c.it
	if it.a.inMeta {
		return nil, nil
	}
	format, isStr := c.parent.Get("format")
	name, _ := format.(string)
	if !isStr || !validSchemaType(format, []string{"string"}, false) {
		return nil, fmt.Errorf(`format value must be ["string"]`)
	}
	def := it.a.formats[name]
	if def == nil || def.ignore {
		return nil, nil
	}
	if def.compare == nil {
		return nil, fmt.Errorf(`"%s": format "%s" does not define "compare" function`, c.keyword, name)
	}
	limit := c.schema.(string)
	op := formatLimitOps[c.keyword]
	params := jsv.O("comparison", op.okStr, "limit", limit)
	msg := "should be " + op.okStr + " " + limit
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
		v, ok := def.compare(d.data.(string), limit)
		return ok && op.fail(v)
	}), nil
}
