package ajv

import (
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// validateArray applies the keyword's schema to every item.
func (c *kwCxt) validateArray() (step, error) {
	_, sub, err := c.sub(subArgs{})
	if err != nil {
		return nil, err
	}
	allErrors := c.it.allErrors
	return func(r *run, f *frame, d dpos) {
		for i, v := range d.data.([]any) {
			if !sub(r, f, d.index(i, v)) && !allErrors {
				return
			}
		}
	}, nil
}

// validateTuple applies the i-th schema to the i-th item.
func (c *kwCxt) validateTuple(list []any) (step, error) {
	it := c.it
	var merge step
	if it.a.unevaluated && len(list) > 0 && it.items.kind != evTrue {
		merge, it.items = it.mergeItems(evItems{kind: evStatic, n: len(list)}, it.items, false)
	}
	type tupleStep struct {
		i   int
		sub vstep
	}
	var steps []tupleStep
	for i, s := range list {
		if it.a.alwaysValid(s) {
			continue
		}
		_, sub, err := c.sub(subArgs{index: i, hasIndex: true})
		if err != nil {
			return nil, err
		}
		steps = append(steps, tupleStep{i, sub})
	}
	return func(r *run, f *frame, d dpos) {
		runOp(merge, r, f, d)
		arr := d.data.([]any)
		for _, s := range steps {
			if len(arr) > s.i {
				if !s.sub(r, f, d.index(s.i, arr[s.i])) && !it.allErrors {
					return
				}
			}
		}
	}, nil
}

func codeItems(c *kwCxt) (step, error) {
	if list, ok := c.schema.([]any); ok {
		return c.validateTuple(list)
	}
	c.it.items = evItems{kind: evTrue}
	if c.it.a.alwaysValid(c.schema) {
		return nil, nil
	}
	return c.validateArray()
}

func codePrefixItems(c *kwCxt) (step, error) { return c.validateTuple(c.schema.([]any)) }

func codeItems2020(c *kwCxt) (step, error) {
	c.it.items = evItems{kind: evTrue}
	if c.it.a.alwaysValid(c.schema) {
		return nil, nil
	}
	if prefix, ok := c.parent.Get("prefixItems"); ok && jsv.Truthy(prefix, true) {
		list, _ := prefix.([]any)
		return c.additionalItems(len(list))
	}
	return c.validateArray()
}

func codeAdditionalItems(c *kwCxt) (step, error) {
	list, ok := jsv.Prop(c.parent, "items").([]any)
	if !ok {
		return nil, nil
	}
	return c.additionalItems(len(list))
}

// additionalItems is validateAdditionalItems for the items after the first n.
func (c *kwCxt) additionalItems(n int) (step, error) {
	it := c.it
	it.items = evItems{kind: evTrue}
	if c.schema == false {
		params := jsv.O("limit", float64(n))
		msg := "must NOT have more than " + itoa(n) + " items"
		return c.errorStep(func(dpos) *jsv.Obj { return params }, func(dpos) string { return msg }, func(d dpos) bool {
			return len(d.data.([]any)) > n
		}), nil
	}
	if !jsv.IsObject(c.schema) || it.a.alwaysValid(c.schema) {
		return nil, nil
	}
	_, sub, err := c.sub(subArgs{})
	if err != nil {
		return nil, err
	}
	return func(r *run, f *frame, d dpos) {
		arr := d.data.([]any)
		for i := n; i < len(arr); i++ {
			if !sub(r, f, d.index(i, arr[i])) && !it.allErrors {
				return
			}
		}
	}, nil
}

func codeContains(c *kwCxt) (step, error) {
	it := c.it
	minV, maxV := 1.0, -1.0
	hasMax := false
	if it.a.next {
		if v, ok := c.parent.Get("minContains"); ok {
			minV, _ = v.(float64)
		}
		if v, ok := c.parent.Get("maxContains"); ok {
			maxV, hasMax = v.(float64)
		}
	}
	params := jsv.O("minContains", minV)
	msg := "must contain at least " + num(minV) + " valid item(s)"
	if hasMax {
		params.Set("maxContains", maxV)
		msg = "must contain at least " + num(minV) + " and no more than " + num(maxV) + " valid item(s)"
	}
	report := func(r *run, d dpos) { it.report(r, d, "contains", params, msg) }
	if !hasMax && minV == 0 {
		return nil, nil
	}
	if hasMax && minV > maxV {
		return func(r *run, _ *frame, d dpos) { report(r, d) }, nil
	}
	if it.a.alwaysValid(c.schema) {
		return func(r *run, _ *frame, d dpos) {
			n := float64(len(d.data.([]any)))
			if !(n >= minV && (!hasMax || n <= maxV)) {
				report(r, d)
			}
		}, nil
	}
	it.items = evItems{kind: evTrue}
	_, sub, err := c.sub(subArgs{composite: true})
	if err != nil {
		return nil, err
	}
	return func(r *run, f *frame, d dpos) {
		start := len(r.errs)
		arr := d.data.([]any)
		valid := false
		switch {
		case !hasMax && minV == 1:
			for i, v := range arr {
				if valid = sub(r, f, d.index(i, v)); valid {
					break
				}
			}
		default:
			if minV == 0 {
				valid = true
				if len(arr) == 0 {
					break
				}
			}
			count := 0.0
			for i, v := range arr {
				if !sub(r, f, d.index(i, v)) {
					continue
				}
				count++
				if !hasMax {
					if count >= minV {
						valid = true
						break
					}
					continue
				}
				if count > maxV {
					valid = false
					break
				}
				if minV == 1 || count >= minV {
					valid = true
				}
			}
		}
		if valid {
			reset(r, start)
		} else {
			report(r, d)
		}
	}, nil
}

func codeDependencies(c *kwCxt) (step, error) {
	o := jsv.AsObj(c.schema)
	var propKeys, schemaKeys []string
	deps := map[string][]any{}
	for _, k := range o.Keys() {
		if k == "__proto__" {
			continue
		}
		if list, ok := jsv.Prop(o, k).([]any); ok {
			propKeys = append(propKeys, k)
			deps[k] = list
		} else {
			schemaKeys = append(schemaKeys, k)
		}
	}
	var propStep step
	if len(propKeys) > 0 {
		propStep = c.propertyDeps(propKeys, deps)
	}
	schStep, err := c.schemaDeps(schemaKeys)
	if err != nil {
		return nil, err
	}
	return seq(propStep, schStep), nil
}

func codeDependentSchemas(c *kwCxt) (step, error) { return c.schemaDeps(jsv.AsObj(c.schema).Keys()) }

// schemaDeps is validateSchemaDeps: a sub-schema applies when its property is present.
func (c *kwCxt) schemaDeps(keys []string) (step, error) {
	it := c.it
	type depStep struct {
		prop  string
		sub   vstep
		merge step
	}
	var steps []depStep
	for _, k := range keys {
		if it.a.alwaysValid(lookupSchema(c.schema, k)) {
			continue
		}
		name := k
		child, sub, err := c.sub(subArgs{prop: &name})
		if err != nil {
			return nil, err
		}
		merge, _ := it.mergeValidEvaluated(child)
		steps = append(steps, depStep{k, sub, merge})
	}
	if len(steps) == 0 {
		return nil, nil
	}
	return func(r *run, f *frame, d dpos) {
		for _, s := range steps {
			if _, ok := lookup(d.data, s.prop); !ok {
				continue
			}
			if s.sub(r, f, d) {
				runOp(s.merge, r, f, d)
			} else if !it.allErrors {
				return
			}
		}
	}, nil
}
