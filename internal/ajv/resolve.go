package ajv

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var (
	traverseKeywords = map[string]bool{"additionalItems": true, "items": true, "contains": true, "additionalProperties": true,
		"propertyNames": true, "not": true, "if": true, "then": true, "else": true}
	traverseArray = map[string]bool{"items": true, "allOf": true, "anyOf": true, "oneOf": true}
	traverseProps = map[string]bool{"$defs": true, "definitions": true, "properties": true, "patternProperties": true, "dependencies": true}
	traverseSkip  = map[string]bool{"default": true, "enum": true, "const": true, "required": true, "maximum": true, "minimum": true,
		"exclusiveMaximum": true, "exclusiveMinimum": true, "multipleOf": true, "maxLength": true, "minLength": true, "pattern": true,
		"format": true, "maxItems": true, "minItems": true, "uniqueItems": true, "maxProperties": true, "minProperties": true}
	anchorRe = regexp.MustCompile(`(?i)^[a-z_][-a-z0-9._]*$`)
)

type refCollector struct {
	a          *Ajv
	pathPrefix string
	baseIDs    map[string]string
	localRefs  map[string]any
	seen       map[string]bool
}

// schemaRefs is getSchemaRefs: it records sub-schema $id, $anchor and $dynamicAnchor
// targets in ajv.refs and the local reference table.
func (a *Ajv) schemaRefs(schema any, baseID string) (map[string]any, error) {
	o := jsv.AsObj(schema)
	if o == nil {
		return map[string]any{}, nil
	}
	schID := baseID
	if v, ok := o.Get("$id"); ok && jsv.Truthy(v, true) {
		n, err := normalizeAnyID(v, true)
		if err != nil {
			return nil, err
		}
		schID = n
	}
	schID = normalizeID(schID)
	s, err := uriSerialize(uriParse(schID))
	if err != nil {
		return nil, err
	}
	c := &refCollector{a: a, pathPrefix: strings.SplitN(s, "#", 2)[0] + "#", baseIDs: map[string]string{"": schID}, localRefs: map[string]any{}, seen: map[string]bool{}}
	if err := c.walk(o, "", nil); err != nil {
		return nil, err
	}
	return c.localRefs, nil
}

func (c *refCollector) walk(v any, ptr string, parent *string) error {
	o := jsv.AsObj(v)
	if o == nil {
		return nil
	}
	if parent != nil {
		if err := c.visit(o, ptr, *parent); err != nil {
			return err
		}
	}
	for _, key := range o.Keys() {
		sch := jsv.Prop(o, key)
		proto := objectProtoNames[key]
		switch arr, isArr := sch.([]any); {
		case isArr:
			if traverseArray[key] || proto {
				for i, el := range arr {
					if err := c.walk(el, ptr+"/"+key+"/"+itoa(i), &ptr); err != nil {
						return err
					}
				}
			}
		case traverseProps[key] || proto:
			if m := jsv.AsObj(sch); m != nil {
				for _, p := range m.Keys() {
					if err := c.walk(jsv.Prop(m, p), ptr+"/"+key+"/"+escapeJSONPointer(p), &ptr); err != nil {
						return err
					}
				}
			}
		case traverseKeywords[key] || !traverseSkip[key]:
			if err := c.walk(sch, ptr+"/"+key, &ptr); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *refCollector) visit(sch *jsv.Obj, ptr, parent string) error {
	full := c.pathPrefix + ptr
	inner := c.baseIDs[parent]
	if id, ok := jsv.Prop(sch, "$id").(string); ok {
		r, err := c.addRef(sch, inner, id, full)
		if err != nil {
			return err
		}
		inner = r
	}
	for _, k := range []string{"$anchor", "$dynamicAnchor"} {
		if anchor, ok := jsv.Prop(sch, k).(string); ok {
			if !anchorRe.MatchString(anchor) {
				return fmt.Errorf("invalid anchor %q", anchor)
			}
			if _, err := c.addRef(sch, inner, "#"+anchor, full); err != nil {
				return err
			}
		}
	}
	c.baseIDs[ptr] = inner
	return nil
}

func ambiguous(ref string) error {
	return fmt.Errorf("reference %q resolves to more than one schema", ref)
}

func (c *refCollector) addRef(sch *jsv.Obj, inner, ref, full string) (string, error) {
	if inner != "" {
		r, err := uriResolve(inner, ref)
		if err != nil {
			return "", err
		}
		ref = r
	}
	ref = normalizeID(ref)
	if c.seen[ref] {
		return "", ambiguous(ref)
	}
	c.seen[ref] = true
	target := c.a.refs[ref]
	if s, isStr := target.(string); isStr {
		target = c.a.refs[s]
	}
	switch {
	case target != nil:
		if env, ok := target.(*schemaEnv); ok && !deepEqual(sch, env.schema) {
			return "", ambiguous(ref)
		}
	case ref != normalizeID(full):
		if strings.HasPrefix(ref, "#") {
			if prev, ok := c.localRefs[ref]; ok && !deepEqual(sch, prev) {
				return "", ambiguous(ref)
			}
			c.localRefs[ref] = sch
		} else {
			c.a.refs[ref] = full
		}
	}
	return ref, nil
}

var preventScopeChange = map[string]bool{"properties": true, "patternProperties": true, "enum": true, "dependencies": true, "definitions": true}

// resolveSchema finds the environment of a reference URI below root or in ajv.refs.
func (a *Ajv) resolveSchema(root *schemaEnv, ref string) (*schemaEnv, error) {
	p := uriParse(ref)
	ser, err := uriSerialize(p)
	if err != nil {
		return nil, err
	}
	refPath := strings.SplitN(ser, "#", 2)[0] + "#"
	baseID, err := fullPath(root.baseID)
	if err != nil {
		return nil, err
	}
	if o := jsv.AsObj(root.schema); o != nil && o.Len() > 0 && refPath == baseID {
		return a.jsonPointer(p, root)
	}
	id := normalizeID(refPath)
	target := a.refs[id]
	if target == nil {
		if e := a.schemas[id]; e != nil {
			target = e
		}
	}
	if s, isStr := target.(string); isStr {
		sch, err := a.resolveSchema(root, s)
		if err != nil || sch == nil || jsv.AsObj(sch.schema) == nil {
			return nil, err
		}
		return a.jsonPointer(p, sch)
	}
	env, _ := target.(*schemaEnv)
	if env == nil || jsv.AsObj(env.schema) == nil {
		return nil, nil
	}
	if env.fn == nil {
		if _, err := a.compileEnv(env); err != nil {
			return nil, err
		}
	}
	if id == normalizeID(ref) {
		schema := env.schema
		if v, ok := jsv.AsObj(schema).Get("$id"); ok && jsv.Truthy(v, true) {
			n, err := normalizeAnyID(v, true)
			if err != nil {
				return nil, err
			}
			if baseID, err = uriResolve(baseID, n); err != nil {
				return nil, err
			}
		}
		return newEnv(schema, root, baseID), nil
	}
	return a.jsonPointer(p, env)
}

func (a *Ajv) jsonPointer(p *uriComp, env *schemaEnv) (*schemaEnv, error) {
	if p.fragment == nil || !strings.HasPrefix(*p.fragment, "/") {
		return nil, nil
	}
	schema, baseID := env.schema, env.baseID
	for _, part := range strings.Split((*p.fragment)[1:], "/") {
		if _, isBool := schema.(bool); isBool {
			return nil, nil
		}
		dec, err := decodeURIComponent(part)
		if err != nil {
			return nil, err
		}
		key := unescapeJSONPointer(dec)
		var next any
		found := false
		switch s := schema.(type) {
		case *jsv.Obj:
			next, found = s.Get(key)
		case []any:
			if i, ok := jsv.ArrayIndex(key); ok && int(i) < len(s) {
				next, found = s[i], true
			}
		}
		if !found {
			return nil, nil
		}
		schema = next
		if id, ok := jsv.AsObj(schema).Get("$id"); ok && jsv.Truthy(id, true) && !preventScopeChange[part] {
			n, err := normalizeAnyID(id, true)
			if err != nil {
				return nil, err
			}
			if baseID, err = uriResolve(baseID, n); err != nil {
				return nil, err
			}
		}
	}
	var found *schemaEnv
	if o := jsv.AsObj(schema); o != nil {
		if ref, ok := o.Get("$ref"); ok && jsv.Truthy(ref, true) {
			it := &schCxt{a: a, schema: schema}
			if !it.hasRulesButRef() {
				rs, _ := ref.(string)
				target, err := resolveURL(baseID, rs)
				if err != nil {
					return nil, err
				}
				if found, err = a.resolveSchema(env.root, target); err != nil {
					return nil, err
				}
			}
		}
	}
	if found == nil {
		found = newEnv(schema, env.root, baseID)
	}
	if !sameSchema(found.schema, found.root.schema) {
		return found, nil
	}
	return nil, nil
}

// resolveRef is Ajv's resolveRef: the inlined schema or the environment of a $ref.
func (a *Ajv) resolveRef(root *schemaEnv, baseID, ref string) (any, error) {
	ref, err := resolveURL(baseID, ref)
	if err != nil {
		return nil, err
	}
	if cached, ok := root.refs[ref]; ok {
		return cached, nil
	}
	var env *schemaEnv
	target := a.refs[ref]
	for {
		s, isStr := target.(string)
		if !isStr {
			break
		}
		ref = s
		target = a.refs[ref]
	}
	switch t := target.(type) {
	case *schemaEnv:
		env = t
	default:
		if e := a.schemas[ref]; e != nil {
			env = e
		} else if env, err = a.resolveSchema(root, ref); err != nil {
			return nil, err
		}
	}
	if env == nil {
		if sch, ok := root.localRefs[ref]; ok {
			env = newEnv(sch, root, baseID)
		}
	}
	if env == nil {
		return nil, nil
	}
	if inlineRef(env.schema) {
		root.refs[ref] = env.schema
		return env.schema, nil
	}
	if env.fn == nil || !env.fn.done {
		c, err := a.compileEnv(env)
		if err != nil {
			return nil, err
		}
		env = c
	}
	root.refs[ref] = env
	return env, nil
}

func inlineRef(schema any) bool {
	if _, isBool := schema.(bool); isBool {
		return true
	}
	return !hasRef(schema)
}

var refKeywords = map[string]bool{"$ref": true, "$recursiveRef": true, "$recursiveAnchor": true, "$dynamicRef": true, "$dynamicAnchor": true}

func hasRef(v any) bool {
	switch x := v.(type) {
	case *jsv.Obj:
		for _, k := range x.Keys() {
			if refKeywords[k] {
				return true
			}
			if hasRef(jsv.Prop(x, k)) {
				return true
			}
		}
	case []any:
		for _, el := range x {
			if hasRef(el) {
				return true
			}
		}
	}
	return false
}
