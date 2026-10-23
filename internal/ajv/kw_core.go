package ajv

import (
	"errors"
	"fmt"
)

func codeID(*kwCxt) (step, error) {
	return nil, errors.New(`NOT SUPPORTED: keyword "id", use "$id" for schema ID`)
}

// callRef is Ajv's callRef for a synchronous schema: the callee's errors join this
// function's errors, and on success its evaluated properties and items are merged.
func (c *kwCxt) callRef(target func(r *run) *compiledFn, sch *schemaEnv) step {
	it := c.it
	var onValid []step
	if it.a.unevaluated {
		static := sch != nil && sch.fn != nil && sch.fn.done
		if it.props.kind != evTrue {
			if static && sch.fn.props.kind != evName {
				if sch.fn.props.kind != evUndef {
					op, res := it.mergeProps(sch.fn.props, it.props, false)
					it.props = res
					onValid = append(onValid, op)
				}
			} else {
				s := it.slot()
				op, res := it.mergeProps(evProps{kind: evName, slot: s}, it.props, true)
				it.props = res
				onValid = append(onValid, func(_ *run, f *frame, _ dpos) { f.slots[s] = f.calleeProps }, op)
			}
		}
		if it.items.kind != evTrue {
			if static && sch.fn.items.kind != evName {
				if sch.fn.items.kind != evUndef {
					op, res := it.mergeItems(sch.fn.items, it.items, false)
					it.items = res
					onValid = append(onValid, op)
				}
			} else {
				s := it.slot()
				op, res := it.mergeItems(evItems{kind: evName, slot: s}, it.items, true)
				it.items = res
				onValid = append(onValid, func(_ *run, f *frame, _ dpos) { f.slots[s] = f.calleeItems }, op)
			}
		}
	}
	success := seq(onValid...)
	return func(r *run, f *frame, d dpos) {
		fn := target(r)
		valid, props, items := fn.call(r, d)
		if valid && success != nil {
			f.calleeProps, f.calleeItems = props, items
			success(r, f, d)
		}
	}
}

func envFn(env *schemaEnv) func(*run) *compiledFn {
	return func(*run) *compiledFn { return env.fn }
}

func codeRef(c *kwCxt) (step, error) {
	it := c.it
	ref := c.schema.(string)
	env := it.env
	root := env.root
	if (ref == "#" || ref == "#/") && it.baseID == root.baseID {
		if env == root {
			fn := it.fn
			return c.callRef(func(*run) *compiledFn { return fn }, env), nil
		}
		return c.callRef(envFn(root), root), nil
	}
	target, err := it.a.resolveRef(root, it.baseID, ref)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("can't resolve reference %s from id %s", ref, it.baseID)
	}
	if e, ok := target.(*schemaEnv); ok {
		return c.callRef(envFn(e), e), nil
	}
	child, sub, err := c.sub(subArgs{explicit: true, schema: target, errSchemaPath: ref})
	if err != nil {
		return nil, err
	}
	merge := it.mergeEvaluated(child, false)
	return func(r *run, f *frame, d dpos) {
		sub(r, f, d)
		runOp(merge, r, f, d)
	}, nil
}

func (c *kwCxt) dynamicAnchor(anchor string) (step, error) {
	it := c.it
	it.env.root.dynamicAnchors[anchor] = true
	var target *schemaEnv
	if it.errSchemaPath == "#" {
		target = it.fn.env
	} else {
		root := it.env.root
		e := newEnv(it.schema, root.root, root.baseID)
		e.localRefs, e.meta = root.localRefs, root.meta
		compiled, err := it.a.compileEnv(e)
		if err != nil {
			return nil, err
		}
		target = compiled
	}
	return func(r *run, _ *frame, _ dpos) {
		if r.anchors[anchor] == nil {
			r.anchors[anchor] = target
		}
	}, nil
}

func codeDynamicAnchor(c *kwCxt) (step, error) { return c.dynamicAnchor(c.schema.(string)) }

func codeRecursiveAnchor(c *kwCxt) (step, error) {
	if c.schema == true {
		return c.dynamicAnchor("")
	}
	return nil, nil
}

func codeDynamicRef(c *kwCxt) (step, error) {
	ref := c.schema.(string)
	if ref == "" || ref[0] != '#' {
		return nil, fmt.Errorf("%q only supports hash fragment reference", c.keyword)
	}
	anchor := ref[1:]
	it := c.it
	self := it.fn
	selfRef := func(*run) *compiledFn { return self }
	if !it.env.root.dynamicAnchors[anchor] {
		return c.callRef(selfRef, nil), nil
	}
	viaAnchor := c.callRef(func(r *run) *compiledFn { return r.anchors[anchor].fn }, nil)
	viaSelf := c.callRef(selfRef, nil)
	return func(r *run, f *frame, d dpos) {
		if r.anchors[anchor] != nil {
			viaAnchor(r, f, d)
		} else {
			viaSelf(r, f, d)
		}
	}, nil
}
