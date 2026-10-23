package ajv

import (
	"errors"
	"math"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var limitOps = map[string]struct {
	okStr string
	fail  func(x, y float64) bool
}{
	"maximum":          {"<=", func(x, y float64) bool { return x > y }},
	"minimum":          {">=", func(x, y float64) bool { return x < y }},
	"exclusiveMaximum": {"<", func(x, y float64) bool { return x >= y }},
	"exclusiveMinimum": {">", func(x, y float64) bool { return x <= y }},
}

func num(v any) string { return jsv.ToString(v) }

func codeLimitNumber(c *kwCxt) (step, error) {
	limit := c.schema.(float64)
	op := limitOps[c.keyword]
	params := jsv.O("comparison", op.okStr, "limit", limit)
	msg := "must be " + op.okStr + " " + num(limit)
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
		x := d.data.(float64)
		return op.fail(x, limit) || math.IsNaN(x)
	}), nil
}

func codeMultipleOf(c *kwCxt) (step, error) {
	m := c.schema.(float64)
	params := jsv.O("multipleOf", m)
	msg := "must be multiple of " + num(m)
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
		return m == 0 || !isJSInteger(d.data.(float64)/m)
	}), nil
}

func limitWord(keyword, maxKw string) string {
	if keyword == maxKw {
		return "more"
	}
	return "fewer"
}

func (c *kwCxt) countLimit(maxKw, noun string, count func(any) int) step {
	limit := c.schema.(float64)
	params := jsv.O("limit", limit)
	msg := "must NOT have " + limitWord(c.keyword, maxKw) + " than " + num(limit) + " " + noun
	isMax := c.keyword == maxKw
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
		n := float64(count(d.data))
		if isMax {
			return n > limit
		}
		return n < limit
	})
}

func codeLimitLength(c *kwCxt) (step, error) {
	return c.countLimit("maxLength", "characters", func(v any) int { return ucs2length(v.(string)) }), nil
}

func codeLimitProperties(c *kwCxt) (step, error) {
	return c.countLimit("maxProperties", "properties", func(v any) int { return len(ownKeys(v)) }), nil
}

func codeLimitItems(c *kwCxt) (step, error) {
	return c.countLimit("maxItems", "items", func(v any) int { return len(v.([]any)) }), nil
}

func codePattern(c *kwCxt) (step, error) {
	src := c.schema.(string)
	re, err := compileJSRegex(src)
	if err != nil {
		return nil, err
	}
	params := jsv.O("pattern", src)
	msg := `must match pattern "` + src + `"`
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
		return !re.MatchString(d.data.(string))
	}), nil
}

func missingMsg(p string) string { return "must have required property '" + p + "'" }

func codeRequired(c *kwCxt) (step, error) {
	list := c.schema.([]any)
	if len(list) == 0 {
		return nil, nil
	}
	it := c.it
	return func(r *run, _ *frame, d dpos) {
		for _, p := range list {
			name := jsv.ToString(p)
			if _, ok := lookup(d.data, name); !ok {
				it.report(r, d, "required", jsv.O("missingProperty", p), missingMsg(name))
				if !it.allErrors {
					return
				}
			}
		}
	}, nil
}

func uniqueKey(item any, multi bool) string {
	if s, ok := item.(string); ok && multi {
		return s + "_"
	}
	return jsv.ToString(item)
}

func codeUniqueItems(c *kwCxt) (step, error) {
	if c.schema != true {
		return nil, nil
	}
	var itemTypes []string
	if items, ok := c.parent.Get("items"); ok && jsv.Truthy(items, true) {
		sub := &schCxt{a: c.it.a, schema: items}
		if jsv.IsObject(items) {
			t, err := sub.getSchemaTypes()
			if err != nil {
				return nil, err
			}
			itemTypes = t
		}
	}
	optimize := len(itemTypes) > 0
	for _, t := range itemTypes {
		if t == "object" || t == "array" {
			optimize = false
		}
	}
	it := c.it
	fail := func(r *run, d dpos, i, j int) {
		it.report(r, d, "uniqueItems", jsv.O("i", float64(i), "j", float64(j)),
			"must NOT have duplicate items (items ## "+itoa(j)+" and "+itoa(i)+" are identical)")
	}
	return func(r *run, _ *frame, d dpos) {
		arr := d.data.([]any)
		if len(arr) <= 1 {
			return
		}
		if optimize {
			indices := map[string]int{}
			for i := len(arr) - 1; i >= 0; i-- {
				item := arr[i]
				if !isAnyType(itemTypes, item) {
					continue
				}
				key := uniqueKey(item, len(itemTypes) > 1)
				if j, seen := indices[key]; seen {
					fail(r, d, i, j)
					return
				}
				if key != "__proto__" {
					indices[key] = i
				}
			}
			return
		}
		for i := len(arr) - 1; i > 0; i-- {
			for j := i - 1; j >= 0; j-- {
				if deepEqual(arr[i], arr[j]) {
					fail(r, d, i, j)
					return
				}
			}
		}
	}, nil
}

func isObjectLike(v any) bool {
	switch v.(type) {
	case *jsv.Obj, []any:
		return true
	}
	return false
}

// strictEqual is === between a primitive schema value and data.
func strictEqual(a, b any) bool {
	if isObjectLike(a) || isObjectLike(b) {
		return false
	}
	return deepEqual(a, b) && !(isNaNValue(a))
}

func isNaNValue(v any) bool {
	f, ok := v.(float64)
	return ok && math.IsNaN(f)
}

func codeConst(c *kwCxt) (step, error) {
	want := c.schema
	params := jsv.O("allowedValue", want)
	deep := isObjectLike(want)
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return "must be equal to constant" }, func(d dpos) bool {
		if deep {
			return !deepEqual(d.data, want)
		}
		return !strictEqual(want, d.data)
	}), nil
}

func codeEnum(c *kwCxt) (step, error) {
	list := c.schema.([]any)
	if len(list) == 0 {
		return nil, errors.New("enum must have non-empty array")
	}
	params := jsv.O("allowedValues", c.schema)
	loop := len(list) >= 200
	return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return "must be equal to one of the allowed values" }, func(d dpos) bool {
		for _, v := range list {
			if loop || isObjectLike(v) {
				if deepEqual(d.data, v) {
					return false
				}
			} else if strictEqual(v, d.data) {
				return false
			}
		}
		return true
	}), nil
}

// propertyDeps is validatePropertyDeps for dependencies and dependentRequired.
func (c *kwCxt) propertyDeps(keys []string, deps map[string][]any) step {
	it, keyword := c.it, c.keyword
	return func(r *run, _ *frame, d dpos) {
		for _, prop := range keys {
			list := deps[prop]
			if len(list) == 0 {
				continue
			}
			if _, ok := lookup(d.data, prop); !ok {
				continue
			}
			joined := make([]string, len(list))
			for i, x := range list {
				joined[i] = jsv.ToString(x)
			}
			depsStr := strings.Join(joined, ", ")
			word := "properties"
			if len(list) == 1 {
				word = "property"
			}
			for _, dep := range list {
				if _, ok := lookup(d.data, jsv.ToString(dep)); ok {
					continue
				}
				it.report(r, d, keyword, jsv.O("property", prop, "missingProperty", dep, "depsCount", float64(len(list)), "deps", depsStr),
					"must have "+word+" "+depsStr+" when property "+prop+" is present")
				if !it.allErrors {
					return
				}
			}
		}
	}
}

func codeDependentRequired(c *kwCxt) (step, error) {
	o := jsv.AsObj(c.schema)
	deps := map[string][]any{}
	for _, k := range o.Keys() {
		list, _ := jsv.Prop(o, k).([]any)
		deps[k] = list
	}
	return c.propertyDeps(o.Keys(), deps), nil
}
