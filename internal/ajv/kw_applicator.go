package ajv

import (
	"fmt"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

func reset(r *run, n int) { r.errs = r.errs[:n] }

func codeNot(c *kwCxt) (step, error) {
	it := c.it
	if it.a.alwaysValid(c.schema) {
		return func(r *run, _ *frame, d dpos) { it.report(r, d, "not", jsv.NewObj(), "must NOT be valid") }, nil
	}
	_, sub, err := c.sub(subArgs{composite: true, noErrors: true})
	if err != nil {
		return nil, err
	}
	return func(r *run, f *frame, d dpos) {
		n := len(r.errs)
		if sub(r, f, d) {
			it.report(r, d, "not", jsv.NewObj(), "must NOT be valid")
		} else {
			reset(r, n)
		}
	}, nil
}

type branch struct {
	sub    vstep
	merge  step
	always bool
}

func codeAnyOf(c *kwCxt) (step, error) {
	it := c.it
	list := c.schema.([]any)
	for _, s := range list {
		if it.a.alwaysValid(s) && !it.a.unevaluated {
			return nil, nil
		}
	}
	var branches []branch
	shortCircuit := []bool{}
	for i := range list {
		child, sub, err := c.sub(subArgs{index: i, hasIndex: true, composite: true})
		if err != nil {
			return nil, err
		}
		merge, merged := it.mergeValidEvaluated(child)
		branches = append(branches, branch{sub: sub, merge: merge})
		shortCircuit = append(shortCircuit, !merged)
	}
	return func(r *run, f *frame, d dpos) {
		n := len(r.errs)
		valid := false
		for i, b := range branches {
			ok := b.sub(r, f, d)
			valid = valid || ok
			if ok {
				runOp(b.merge, r, f, d)
			}
			if shortCircuit[i] && valid {
				break
			}
		}
		if valid {
			reset(r, n)
		} else {
			it.report(r, d, "anyOf", jsv.NewObj(), "must match a schema in anyOf")
		}
	}, nil
}

func codeOneOf(c *kwCxt) (step, error) {
	it := c.it
	list := c.schema.([]any)
	var branches []branch
	for i, s := range list {
		if it.a.alwaysValid(s) {
			branches = append(branches, branch{always: true})
			continue
		}
		child, sub, err := c.sub(subArgs{index: i, hasIndex: true, composite: true})
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch{sub: sub, merge: it.mergeEvaluated(child, true)})
	}
	return func(r *run, f *frame, d dpos) {
		n := len(r.errs)
		valid := false
		var passing any
		for i, b := range branches {
			ok := b.always || b.sub(r, f, d)
			if i > 0 && ok && valid {
				valid = false
				passing = []any{passing, float64(i)}
				break
			}
			if ok {
				valid = true
				passing = float64(i)
				runOp(b.merge, r, f, d)
			}
		}
		if valid {
			reset(r, n)
		} else {
			it.report(r, d, "oneOf", jsv.O("passingSchemas", passing), "must match exactly one schema in oneOf")
		}
	}, nil
}

func codeAllOf(c *kwCxt) (step, error) {
	it := c.it
	var steps []step
	for i, s := range c.schema.([]any) {
		if it.a.alwaysValid(s) {
			continue
		}
		child, sub, err := c.sub(subArgs{index: i, hasIndex: true})
		if err != nil {
			return nil, err
		}
		merge := it.mergeEvaluated(child, false)
		steps = append(steps, func(r *run, f *frame, d dpos) {
			sub(r, f, d)
			runOp(merge, r, f, d)
		})
	}
	return seq(steps...), nil
}

func codeIf(c *kwCxt) (step, error) {
	it := c.it
	hasClause := func(kw string) bool {
		s, ok := c.parent.Get(kw)
		return ok && !it.a.alwaysValid(s)
	}
	hasThen, hasElse := hasClause("then"), hasClause("else")
	if !hasThen && !hasElse {
		return nil, nil
	}
	ifChild, ifSub, err := c.sub(subArgs{composite: true, noErrors: true})
	if err != nil {
		return nil, err
	}
	ifMerge := it.mergeEvaluated(ifChild, false)
	clause := func(kw string) (vstep, step, error) {
		child, sub, err := c.sub(subArgs{keyword: kw})
		if err != nil {
			return nil, nil, err
		}
		merge, _ := it.mergeValidEvaluated(child)
		return sub, merge, nil
	}
	var thenSub, elseSub vstep
	var thenMerge, elseMerge step
	if hasThen {
		if thenSub, thenMerge, err = clause("then"); err != nil {
			return nil, err
		}
	}
	if hasElse {
		if elseSub, elseMerge, err = clause("else"); err != nil {
			return nil, err
		}
	}
	return func(r *run, f *frame, d dpos) {
		n := len(r.errs)
		cond := ifSub(r, f, d)
		runOp(ifMerge, r, f, d)
		reset(r, n)
		valid := true
		failing := ""
		switch {
		case cond && hasThen:
			valid, failing = thenSub(r, f, d), "then"
			if valid {
				runOp(thenMerge, r, f, d)
			}
		case !cond && hasElse:
			valid, failing = elseSub(r, f, d), "else"
			if valid {
				runOp(elseMerge, r, f, d)
			}
		}
		if !valid {
			it.report(r, d, "if", jsv.O("failingKeyword", failing), `must match "`+failing+`" schema`)
		}
	}, nil
}

func codeThenElse(*kwCxt) (step, error) { return nil, nil }

func codePropertyNames(c *kwCxt) (step, error) {
	it := c.it
	if it.a.alwaysValid(c.schema) {
		return nil, nil
	}
	_, sub, err := c.sub(subArgs{composite: true, propertyName: true})
	if err != nil {
		return nil, err
	}
	return func(r *run, f *frame, d dpos) {
		for _, key := range ownKeys(d.data) {
			k := key
			if !sub(r, f, dpos{data: k, path: d.path, propName: &k}) {
				it.report(r, d, "propertyNames", jsv.O("propertyName", k), "property name must be valid")
				if !it.allErrors {
					return
				}
			}
		}
	}, nil
}

// schemaProps is allSchemaProperties: the keys of a schema map without "__proto__".
func schemaProps(m any) []string {
	var out []string
	for _, k := range jsv.AsObj(m).Keys() {
		if k != "__proto__" {
			out = append(out, k)
		}
	}
	return out
}

func codeAdditionalProperties(c *kwCxt) (step, error) {
	it := c.it
	it.props = evProps{kind: evTrue}
	if it.a.alwaysValid(c.schema) {
		return nil, nil
	}
	propsSchema := jsv.AsObj(jsv.Prop(c.parent, "properties"))
	props := schemaProps(propsSchema)
	patProps := schemaProps(jsv.Prop(c.parent, "patternProperties"))
	var pats []*jsPattern
	for _, p := range patProps {
		re, err := compileJSRegex(p)
		if err != nil {
			return nil, err
		}
		pats = append(pats, re)
	}
	isAdditional := func(key string) bool {
		if len(props) > 8 {
			if propsSchema.Has(key) {
				return false
			}
		} else {
			for _, p := range props {
				if p == key {
					return false
				}
			}
		}
		for _, re := range pats {
			if re.MatchString(key) {
				return false
			}
		}
		return true
	}
	var sub vstep
	if c.schema != false {
		if jsv.IsObject(c.schema) && !it.a.alwaysValid(c.schema) {
			var err error
			if _, sub, err = c.sub(subArgs{}); err != nil {
				return nil, err
			}
		}
	}
	return func(r *run, f *frame, d dpos) {
		for _, key := range ownKeys(d.data) {
			if !isAdditional(key) {
				continue
			}
			if c.schema == false {
				it.report(r, d, "additionalProperties", jsv.O("additionalProperty", key), "must NOT have additional properties")
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

func codeProperties(c *kwCxt) (step, error) {
	it := c.it
	all := schemaProps(c.schema)
	var merge step
	if it.a.unevaluated && len(all) > 0 && it.props.kind != evTrue {
		merge, it.props = it.mergeProps(evProps{kind: evStatic, set: all}, it.props, false)
	}
	type propStep struct {
		name string
		sub  vstep
	}
	var steps []propStep
	for _, p := range all {
		if it.a.alwaysValid(lookupSchema(c.schema, p)) {
			continue
		}
		name := p
		_, sub, err := c.sub(subArgs{prop: &name})
		if err != nil {
			return nil, err
		}
		steps = append(steps, propStep{name, sub})
	}
	return func(r *run, f *frame, d dpos) {
		runOp(merge, r, f, d)
		for _, s := range steps {
			if v, ok := lookup(d.data, s.name); ok {
				if !s.sub(r, f, d.prop(s.name, v)) && !it.allErrors {
					return
				}
			}
		}
	}, nil
}

func codePatternProperties(c *kwCxt) (step, error) {
	it := c.it
	patterns := schemaProps(c.schema)
	allAlways := true
	for _, p := range patterns {
		if !it.a.alwaysValid(lookupSchema(c.schema, p)) {
			allAlways = false
		}
	}
	if len(patterns) == 0 || (allAlways && (!it.a.unevaluated || it.props.kind == evTrue)) {
		return nil, nil
	}
	var init step
	track := it.a.unevaluated && it.props.kind != evTrue
	if track && it.props.kind != evName {
		init, it.props = it.mergeProps(it.props, evProps{}, true)
	}
	slot := it.props.slot
	type patStep struct {
		re  *jsPattern
		sub vstep
	}
	var steps []patStep
	for _, p := range patterns {
		re, err := compileJSRegex(p)
		if err != nil {
			return nil, err
		}
		var sub vstep
		if !it.a.alwaysValid(lookupSchema(c.schema, p)) {
			name := p
			if _, sub, err = c.sub(subArgs{prop: &name}); err != nil {
				return nil, err
			}
		}
		steps = append(steps, patStep{re, sub})
	}
	return func(r *run, f *frame, d dpos) {
		runOp(init, r, f, d)
		for _, s := range steps {
			for _, key := range ownKeys(d.data) {
				if !s.re.MatchString(key) {
					continue
				}
				if s.sub != nil {
					v, _ := lookup(d.data, key)
					s.sub(r, f, d.prop(key, v))
				}
				if track {
					switch m := f.slots[slot].(type) {
					case map[string]bool:
						m[key] = true
					case nil:
						r.fatal = fmt.Errorf("Cannot set properties of undefined (setting '%s')", key)
						return
					}
				}
			}
		}
	}, nil
}
