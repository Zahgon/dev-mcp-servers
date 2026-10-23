package ajv

import (
	"errors"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// Compilation follows Ajv's code generator: each schema object becomes a closure that
// runs its keywords in Ajv's rule order, and compile-time state (which properties and
// items are already evaluated) is tracked the way the generator tracks it, so the
// closures report the same errors in the same order as the generated function.

// errStackOverflow is the RangeError a runaway recursive schema raises in JavaScript.
var errStackOverflow = errors.New("Maximum call stack size exceeded")

const maxCallDepth = 4000

type run struct {
	errs    []any // error objects; nil marks an error created with createErrors: false
	anchors map[string]*schemaEnv
	depth   int
	fatal   error
}

// frame is one call of a compiled function: its variables, and the evaluated
// properties and items the last referenced function returned.
type frame struct {
	slots       []any
	calleeProps any
	calleeItems any
}

// dpos is the data being validated and its JSON Pointer instance path.
type dpos struct {
	data     any
	path     string
	propName *string
}

func (d dpos) prop(key string, v any) dpos {
	return dpos{data: v, path: d.path + "/" + escapeJSONPointer(key), propName: d.propName}
}

func (d dpos) index(i int, v any) dpos {
	return dpos{data: v, path: d.path + "/" + itoa(i), propName: d.propName}
}

func itoa(i int) string { return jsv.FormatNumber(float64(i)) }

// vstep validates and reports whether it added no errors.
type vstep func(r *run, f *frame, d dpos) bool

type step func(r *run, f *frame, d dpos)

type evKind int

const (
	evUndef evKind = iota
	evTrue
	evStatic
	evName
)

// evProps is Ajv's it.props: undefined, true, a static set of names, or a runtime variable.
type evProps struct {
	kind evKind
	set  []string
	slot int
}

// evItems is Ajv's it.items: undefined, true, a static count, or a runtime variable.
type evItems struct {
	kind evKind
	n    int
	slot int
}

type compiledFn struct {
	env    *schemaEnv
	body   vstep
	nslots int
	done   bool
	props  evProps
	items  evItems
}

type schCxt struct {
	a             *Ajv
	fn            *compiledFn
	env           *schemaEnv
	schema        any
	baseID        string
	errSchemaPath string
	compositeRule bool
	createErrors  bool
	allErrors     bool
	propertyName  bool
	props         evProps
	items         evItems
}

func (it *schCxt) slot() int {
	it.fn.nslots++
	return it.fn.nslots - 1
}

func (it *schCxt) obj() *jsv.Obj { return jsv.AsObj(it.schema) }

func (it *schCxt) has(key string) bool {
	o := it.obj()
	_, ok := o.Get(key)
	return ok
}

func (it *schCxt) get(key string) any { return jsv.Prop(it.obj(), key) }

// report adds one error object for keyword at the current schema path.
func (it *schCxt) report(r *run, d dpos, keyword string, params *jsv.Obj, msg string) {
	it.reportAt(r, d, keyword, it.errSchemaPath+"/"+keyword, params, msg)
}

func (it *schCxt) reportAt(r *run, d dpos, keyword, schemaPath string, params *jsv.Obj, msg string) {
	if !it.createErrors {
		r.errs = append(r.errs, nil)
		return
	}
	e := jsv.O("instancePath", d.path, "schemaPath", schemaPath, "keyword", keyword, "params", params, "message", msg)
	if it.propertyName && d.propName != nil {
		e.Set("propertyName", *d.propName)
	}
	r.errs = append(r.errs, e)
}

type subArgs struct {
	keyword       string
	prop          *string
	index         int
	hasIndex      bool
	schema        any
	errSchemaPath string
	explicit      bool
	composite     bool
	noErrors      bool
	propertyName  bool
}

// sub is KeywordCxt.subschema: the child context for a subschema of this schema.
func (it *schCxt) sub(a subArgs) (*schCxt, error) {
	c := *it
	c.props, c.items = evProps{}, evItems{}
	switch {
	case a.explicit:
		c.schema, c.errSchemaPath = a.schema, a.errSchemaPath
	case a.prop != nil:
		frag, err := escapeFragment(*a.prop)
		if err != nil {
			return nil, err
		}
		c.schema = lookupSchema(it.get(a.keyword), *a.prop)
		c.errSchemaPath = it.errSchemaPath + "/" + a.keyword + "/" + frag
	case a.hasIndex:
		arr, _ := it.get(a.keyword).([]any)
		c.schema = arr[a.index]
		c.errSchemaPath = it.errSchemaPath + "/" + a.keyword + "/" + itoa(a.index)
	default:
		c.schema = it.get(a.keyword)
		c.errSchemaPath = it.errSchemaPath + "/" + a.keyword
	}
	if a.composite {
		c.compositeRule = true
	}
	if a.noErrors {
		c.createErrors, c.allErrors = false, false
	}
	if a.propertyName {
		c.propertyName = true
	}
	return &c, nil
}

func lookupSchema(m any, key string) any {
	o := jsv.AsObj(m)
	if v, ok := o.Get(key); ok {
		return v
	}
	return nil
}

func escapeFragment(s string) (string, error) { return encodeURIComponent(escapeJSONPointer(s)) }

// hasRules is schemaCxtHasRules / !alwaysValidSchema for an object schema.
func (a *Ajv) hasRules(schema any) bool {
	switch s := schema.(type) {
	case bool:
		return !s
	case *jsv.Obj:
		for _, k := range s.Keys() {
			if a.rules.all[k] != nil {
				return true
			}
		}
	}
	return false
}

func (a *Ajv) alwaysValid(schema any) bool {
	if b, ok := schema.(bool); ok {
		return b
	}
	return !a.hasRules(schema)
}

// compileSub is subschemaCode: a schema object with rules, or a boolean or empty schema.
func (it *schCxt) compileSub() (vstep, error) {
	if b, isBool := it.schema.(bool); isBool && !b {
		return func(r *run, f *frame, d dpos) bool {
			it.reportAt(r, d, "false schema", it.errSchemaPath+"/false schema", jsv.NewObj(), "boolean schema is false")
			return false
		}, nil
	}
	if !it.a.hasRules(it.schema) {
		return func(*run, *frame, dpos) bool { return true }, nil
	}
	if id, ok := it.obj().Get("$id"); ok && jsv.Truthy(id, true) {
		s, isStr := id.(string)
		if !isStr {
			return nil, errors.New("id.replace is not a function")
		}
		base, err := uriResolve(it.baseID, normalizeID(s))
		if err != nil {
			return nil, err
		}
		it.baseID = base
	}
	if async, ok := it.obj().Get("$async"); ok && jsv.Truthy(async, true) {
		return nil, errors.New("async schema in sync schema")
	}
	body, err := it.typeAndKeywords()
	if err != nil {
		return nil, err
	}
	return func(r *run, f *frame, d dpos) bool {
		n := len(r.errs)
		body(r, f, d)
		return len(r.errs) == n
	}, nil
}

// mergeProps is mergeEvaluated.props: it returns the runtime step (nil when the merge
// is resolved at compile time) and the merged compile-time state.
func (it *schCxt) mergeProps(from, to evProps, toName bool) (step, evProps) {
	var op step
	var res evProps
	switch {
	case to.kind == evUndef:
		res = from
	case to.kind == evName && from.kind == evName:
		op = func(_ *run, f *frame, _ dpos) { mergePropNames(f, from.slot, to.slot) }
		res = to
	case to.kind == evName:
		op = propsToName(from, to.slot)
		res = to
	case from.kind == evName:
		op = propsToName(to, from.slot)
		res = from
	case from.kind == evTrue:
		res = evProps{kind: evTrue}
	default:
		res = evProps{kind: evStatic, set: unionNames(from.set, to.set)}
	}
	if toName && res.kind != evName {
		s := it.slot()
		init := res
		prev := op
		op = func(r *run, f *frame, d dpos) {
			if prev != nil {
				prev(r, f, d)
			}
			if init.kind == evTrue {
				f.slots[s] = true
				return
			}
			m := map[string]bool{}
			for _, p := range init.set {
				m[p] = true
			}
			f.slots[s] = m
		}
		res = evProps{kind: evName, slot: s}
	}
	return op, res
}

func unionNames(a, b []string) []string {
	out := append([]string{}, a...)
	for _, n := range b {
		found := false
		for _, x := range out {
			if x == n {
				found = true
				break
			}
		}
		if !found {
			out = append(out, n)
		}
	}
	return out
}

func mergePropNames(f *frame, from, to int) {
	if f.slots[to] == true || f.slots[from] == nil {
		return
	}
	if f.slots[from] == true {
		f.slots[to] = true
		return
	}
	m, _ := f.slots[to].(map[string]bool)
	if m == nil {
		m = map[string]bool{}
		f.slots[to] = m
	}
	for k := range f.slots[from].(map[string]bool) {
		m[k] = true
	}
}

func propsToName(from evProps, to int) step {
	return func(_ *run, f *frame, _ dpos) {
		if f.slots[to] == true {
			return
		}
		if from.kind == evTrue {
			f.slots[to] = true
			return
		}
		m, _ := f.slots[to].(map[string]bool)
		if m == nil {
			m = map[string]bool{}
			f.slots[to] = m
		}
		for _, p := range from.set {
			m[p] = true
		}
	}
}

// mergeItems is mergeEvaluated.items.
func (it *schCxt) mergeItems(from, to evItems, toName bool) (step, evItems) {
	var op step
	var res evItems
	switch {
	case to.kind == evUndef:
		res = from
	case to.kind == evName && from.kind == evName:
		op = func(_ *run, f *frame, _ dpos) {
			if f.slots[to.slot] != true && f.slots[from.slot] != nil {
				f.slots[to.slot] = maxItems(f.slots[from.slot], f.slots[to.slot])
			}
		}
		res = to
	case to.kind == evName:
		op = itemsToName(from, to.slot)
		res = to
	case from.kind == evName:
		op = itemsToName(to, from.slot)
		res = from
	case from.kind == evTrue:
		res = evItems{kind: evTrue}
	default:
		n := from.n
		if to.n > n {
			n = to.n
		}
		res = evItems{kind: evStatic, n: n}
	}
	if toName && res.kind != evName {
		s := it.slot()
		init := res
		prev := op
		op = func(r *run, f *frame, d dpos) {
			if prev != nil {
				prev(r, f, d)
			}
			f.slots[s] = itemsValue(init)
		}
		res = evItems{kind: evName, slot: s}
	}
	return op, res
}

func itemsValue(e evItems) any {
	switch e.kind {
	case evTrue:
		return true
	case evStatic:
		return float64(e.n)
	}
	return nil
}

// maxItems is `from === true ? true : to > from ? to : from` on runtime values.
func maxItems(from, to any) any {
	if from == true {
		return true
	}
	if t, ok := to.(float64); ok {
		if fv, ok := from.(float64); ok && t > fv {
			return t
		}
	}
	return from
}

func itemsToName(from evItems, to int) step {
	return func(_ *run, f *frame, _ dpos) {
		if f.slots[to] == true {
			return
		}
		if from.kind == evTrue {
			f.slots[to] = true
			return
		}
		f.slots[to] = maxItems(float64(from.n), f.slots[to])
	}
}

// mergeEvaluated is KeywordCxt.mergeEvaluated from a child context.
func (it *schCxt) mergeEvaluated(child *schCxt, toName bool) step {
	if !it.a.unevaluated {
		return nil
	}
	var ops []step
	if it.props.kind != evTrue && child.props.kind != evUndef {
		op, res := it.mergeProps(child.props, it.props, toName)
		it.props = res
		ops = append(ops, op)
	}
	if it.items.kind != evTrue && child.items.kind != evUndef {
		op, res := it.mergeItems(child.items, it.items, toName)
		it.items = res
		ops = append(ops, op)
	}
	return seq(ops...)
}

// mergeValidEvaluated reports whether merges happen only when the child is valid.
func (it *schCxt) mergeValidEvaluated(child *schCxt) (step, bool) {
	if it.a.unevaluated && (it.props.kind != evTrue || it.items.kind != evTrue) {
		return it.mergeEvaluated(child, true), true
	}
	return nil, false
}

func seq(ops ...step) step {
	var live []step
	for _, op := range ops {
		if op != nil {
			live = append(live, op)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return func(r *run, f *frame, d dpos) {
		for _, op := range live {
			op(r, f, d)
		}
	}
}

func runOp(op step, r *run, f *frame, d dpos) {
	if op != nil {
		op(r, f, d)
	}
}
