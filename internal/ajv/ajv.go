package ajv

import (
	"embed"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// Draft selects the Ajv class: Ajv (draft-07), Ajv2019 or Ajv2020.
type Draft int

const (
	Draft07 Draft = iota
	Draft2019
	Draft2020
)

//go:embed meta
var metaFS embed.FS

// schemaEnv is Ajv's SchemaEnv: a schema, its root, base URI and compiled function.
type schemaEnv struct {
	schema         any
	root           *schemaEnv
	baseID         string
	localRefs      map[string]any
	meta           bool
	refs           map[string]any
	dynamicAnchors map[string]bool
	fn             *compiledFn
}

func newEnv(schema any, root *schemaEnv, baseID string) *schemaEnv {
	e := &schemaEnv{schema: schema, root: root, baseID: baseID, refs: map[string]any{}, dynamicAnchors: map[string]bool{}}
	if root == nil {
		e.root = e
	}
	return e
}

// Ajv is one validator instance with allErrors, strict off, ajv-formats and the
// meta-schemas of its draft.
type Ajv struct {
	draft        Draft
	rules        *rules
	formats      map[string]*formatDef
	schemas      map[string]*schemaEnv
	refs         map[string]any
	compilations []*schemaEnv
	unevaluated  bool
	next         bool
	inMeta       bool
	metaID       string
}

// New is new Ajv(options) for the draft followed by addFormats(ajv).
func New(d Draft) *Ajv {
	a := &Ajv{draft: d, rules: rulesFor(d), formats: fullFormats(), schemas: map[string]*schemaEnv{}, refs: map[string]any{}}
	var files []string
	switch d {
	case Draft2020:
		a.unevaluated, a.next = true, true
		a.metaID = "https://json-schema.org/draft/2020-12/schema"
		files = []string{"schema", "applicator", "unevaluated", "content", "core", "format-annotation", "meta-data", "validation"}
		for i, f := range files {
			files[i] = "meta/2020-12/" + f + ".json"
		}
	case Draft2019:
		a.unevaluated, a.next = true, true
		a.metaID = "https://json-schema.org/draft/2019-09/schema"
		files = []string{"schema", "applicator", "content", "core", "format", "meta-data", "validation"}
		for i, f := range files {
			files[i] = "meta/2019-09/" + f + ".json"
		}
	default:
		a.metaID = "http://json-schema.org/draft-07/schema"
		files = []string{"meta/draft-07.json"}
	}
	for _, f := range files {
		raw, err := metaFS.ReadFile(f)
		if err != nil {
			panic(err)
		}
		sch, err := jsv.Parse(string(raw))
		if err != nil {
			panic(err)
		}
		key := ""
		if d == Draft07 {
			key = a.metaID
		}
		if err := a.addMetaSchema(sch, key); err != nil {
			panic(err)
		}
	}
	a.refs["http://json-schema.org/schema"] = a.metaID
	return a
}

func (a *Ajv) addMetaSchema(schema any, key string) error {
	if key == "" {
		key, _ = jsv.Prop(jsv.AsObj(schema), "$id").(string)
	}
	key = normalizeID(key)
	if err := a.checkUnique(key); err != nil {
		return err
	}
	env, err := a.addSchemaEnv(schema, true, key, false)
	if err != nil {
		return err
	}
	a.schemas[key] = env
	return nil
}

var trailingHash = regexp.MustCompile(`#/?$`)

func normalizeID(id string) string { return trailingHash.ReplaceAllString(id, "") }

// normalizeAnyID is normalizeId on a schema's $id value of any type.
func normalizeAnyID(v any, present bool) (string, error) {
	if !jsv.Truthy(v, present) {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New("id.replace is not a function")
	}
	return normalizeID(s), nil
}

func resolveURL(base, id string) (string, error) { return uriResolve(base, normalizeID(id)) }

func fullPath(id string) (string, error) {
	s, err := uriSerialize(uriParse(normalizeID(id)))
	if err != nil {
		return "", err
	}
	return strings.SplitN(s, "#", 2)[0] + "#", nil
}

func (a *Ajv) checkUnique(id string) error {
	if a.schemas[id] != nil || a.refs[id] != nil {
		return fmt.Errorf("schema with key or id %q already exists", id)
	}
	return nil
}

// addSchemaEnv is Ajv's _addSchema with addUsedSchema on.
func (a *Ajv) addSchemaEnv(schema any, meta bool, baseID string, validate bool) (*schemaEnv, error) {
	if _, isBool := schema.(bool); !isBool && !jsv.IsObject(schema) {
		return nil, errors.New("schema must be object or boolean")
	}
	id, present := jsv.AsObj(schema).Get("$id")
	if jsv.Truthy(id, present) {
		n, err := normalizeAnyID(id, present)
		if err != nil {
			return nil, err
		}
		baseID = n
	} else {
		baseID = normalizeID(baseID)
	}
	env := newEnv(schema, nil, baseID)
	env.meta = meta
	local, err := a.schemaRefs(schema, baseID)
	if err != nil {
		return nil, err
	}
	env.localRefs = local
	if !strings.HasPrefix(baseID, "#") {
		if baseID != "" {
			if err := a.checkUnique(baseID); err != nil {
				return nil, err
			}
		}
		a.refs[baseID] = env
	}
	if validate {
		if err := a.validateSchema(schema); err != nil {
			return nil, err
		}
	}
	return env, nil
}

func (a *Ajv) validateSchema(schema any) error {
	o := jsv.AsObj(schema)
	if o == nil {
		return nil
	}
	ref := a.metaID
	if v, ok := o.Get("$schema"); ok {
		s, isStr := v.(string)
		if !isStr {
			return errors.New("$schema must be a string")
		}
		if s != "" {
			ref = s
		}
	}
	fn, err := a.getSchema(ref)
	if err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("no schema with key or ref %q", ref)
	}
	valid, errs, err := (&Validator{fn: fn}).Validate(schema)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("schema is invalid: " + errorsText(errs, "data"))
	}
	return nil
}

// errorsText is ajv.errorsText(errors, {dataVar}).
func errorsText(errs []*jsv.Obj, dataVar string) string {
	if len(errs) == 0 {
		return "No errors"
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = dataVar + jsv.ToString(jsv.Prop(e, "instancePath")) + " " + jsv.ToString(jsv.Prop(e, "message"))
	}
	return strings.Join(parts, ", ")
}

func (a *Ajv) getSchema(keyRef string) (*compiledFn, error) {
	var sch any
	for {
		key := normalizeID(keyRef)
		if e := a.schemas[key]; e != nil {
			sch = e
		} else {
			sch = a.refs[key]
		}
		s, isStr := sch.(string)
		if !isStr {
			break
		}
		keyRef = s
	}
	env, _ := sch.(*schemaEnv)
	if env == nil {
		root := newEnv(jsv.NewObj(), nil, "")
		found, err := a.resolveSchema(root, keyRef)
		if err != nil || found == nil {
			return nil, err
		}
		a.refs[keyRef] = found
		env = found
	}
	if env.fn != nil && env.fn.done {
		return env.fn, nil
	}
	if env.meta {
		saved := a.inMeta
		a.inMeta = true
		defer func() { a.inMeta = saved }()
	}
	if _, err := a.compileEnv(env); err != nil {
		return nil, err
	}
	return env.fn, nil
}

// Compile is ajv.compile(schema): meta-schema validation, reference collection and
// compilation, with the error Ajv throws.
func (a *Ajv) Compile(schema any) (*Validator, error) {
	env, err := a.addSchemaEnv(schema, false, "", true)
	if err != nil {
		return nil, err
	}
	if env.fn == nil || !env.fn.done {
		if _, err := a.compileEnv(env); err != nil {
			return nil, err
		}
	}
	return &Validator{fn: env.fn}, nil
}

// Validator is a compiled validation function.
type Validator struct{ fn *compiledFn }

// Validate runs the function; the error is a JavaScript exception raised while
// validating (a stack overflow on runaway recursion).
func (v *Validator) Validate(data any) (bool, []*jsv.Obj, error) {
	r := &run{anchors: map[string]*schemaEnv{}}
	valid, _, _ := v.fn.call(r, dpos{data: data})
	if r.fatal != nil {
		return false, nil, r.fatal
	}
	errs := make([]*jsv.Obj, len(r.errs))
	for i, e := range r.errs {
		if o, ok := e.(*jsv.Obj); ok {
			errs[i] = o
		} else {
			errs[i] = jsv.NewObj()
		}
	}
	return valid, errs, nil
}

func (fn *compiledFn) call(r *run, d dpos) (bool, any, any) {
	if r.fatal != nil {
		return false, nil, nil
	}
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > maxCallDepth {
		r.fatal = errStackOverflow
		return false, nil, nil
	}
	f := &frame{slots: make([]any, fn.nslots)}
	valid := fn.body(r, f, d)
	return valid, propsValue(fn.props, f), itemsRuntime(fn.items, f)
}

func propsValue(p evProps, f *frame) any {
	switch p.kind {
	case evTrue:
		return true
	case evStatic:
		m := map[string]bool{}
		for _, n := range p.set {
			m[n] = true
		}
		return m
	case evName:
		return f.slots[p.slot]
	}
	return nil
}

func itemsRuntime(i evItems, f *frame) any {
	if i.kind == evName {
		return f.slots[i.slot]
	}
	return itemsValue(i)
}

func sameSchema(x, y any) bool {
	switch a := x.(type) {
	case *jsv.Obj:
		b, ok := y.(*jsv.Obj)
		return ok && a == b
	case bool:
		b, ok := y.(bool)
		return ok && a == b
	}
	return false
}

// compileEnv is compileSchema: it reuses an environment being compiled for the same
// schema, root and base URI, which is how recursive references terminate.
func (a *Ajv) compileEnv(env *schemaEnv) (*schemaEnv, error) {
	for _, c := range a.compilations {
		if sameSchema(c.schema, env.schema) && c.root == env.root && c.baseID == env.baseID {
			return c, nil
		}
	}
	rootID, err := fullPath(env.root.baseID)
	if err != nil {
		return nil, err
	}
	fn := &compiledFn{env: env}
	env.fn = fn
	a.compilations = append(a.compilations, env)
	defer func() {
		for i, c := range a.compilations {
			if c == env {
				a.compilations = append(a.compilations[:i], a.compilations[i+1:]...)
				break
			}
		}
	}()
	base := env.baseID
	if base == "" {
		base = rootID
	}
	it := &schCxt{a: a, fn: fn, env: env, schema: env.schema, baseID: base, errSchemaPath: "#", allErrors: true, createErrors: true}
	body, err := it.topCode()
	if err != nil {
		env.fn = nil
		return nil, err
	}
	fn.body, fn.props, fn.items, fn.done = body, it.props, it.items, true
	return env, nil
}

func (it *schCxt) topCode() (vstep, error) {
	if b, isBool := it.schema.(bool); isBool || !it.a.hasRules(it.schema) {
		if isBool && !b {
			return func(r *run, f *frame, d dpos) bool {
				it.reportAt(r, d, "false schema", "#/false schema", jsv.NewObj(), "boolean schema is false")
				return false
			}, nil
		}
		return func(*run, *frame, dpos) bool { return true }, nil
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
