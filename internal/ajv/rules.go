package ajv

// kwDef is an Ajv keyword definition: the data types whose rule groups hold it, the
// allowed schema value types, ordering, and the code generator (nil for annotations).
type kwDef struct {
	keywords       []string
	types          []string
	schemaType     []string
	allowUndefined bool
	before         string
	dependencies   []string
	code           func(c *kwCxt) (step, error)
}

type rule struct {
	keyword string
	def     *kwDef
}

type ruleGroup struct {
	typ   string
	rules []*rule
}

type rules struct {
	groups   []*ruleGroup
	all      map[string]*rule
	keywords map[string]bool
}

func newRules() *rules {
	r := &rules{all: map[string]*rule{}, keywords: map[string]bool{}}
	for _, t := range []string{"", "number", "string", "array", "object"} {
		r.groups = append(r.groups, &ruleGroup{typ: t})
	}
	return r
}

// group is RULES.types[t] when it is a rule group (number, string, array, object).
func (r *rules) group(t string) *ruleGroup {
	if t == "" {
		return nil
	}
	for _, g := range r.groups {
		if g.typ == t {
			return g
		}
	}
	return nil
}

func (r *rules) findGroup(t string) *ruleGroup {
	for _, g := range r.groups {
		if g.typ == t {
			return g
		}
	}
	g := &ruleGroup{typ: t}
	r.groups = append(r.groups, g)
	return g
}

// addName registers a keyword without a definition (an annotation-only keyword).
func (r *rules) addName(names ...string) {
	for _, n := range names {
		r.findGroup("")
		r.keywords[n] = true
	}
}

func (r *rules) add(defs ...*kwDef) {
	for _, def := range defs {
		for _, kw := range def.keywords {
			types := def.types
			if len(types) == 0 {
				types = []string{""}
			}
			for _, t := range types {
				g := r.findGroup(t)
				r.keywords[kw] = true
				ru := &rule{keyword: kw, def: def}
				pos := -1
				if def.before != "" {
					for i, x := range g.rules {
						if x.keyword == def.before {
							pos = i
							break
						}
					}
				}
				if pos >= 0 {
					g.rules = append(g.rules[:pos], append([]*rule{ru}, g.rules[pos:]...)...)
				} else {
					g.rules = append(g.rules, ru)
				}
				r.all[kw] = ru
			}
		}
	}
}

func kw(names ...string) []string { return names }

func coreVocabulary(r *rules) {
	r.addName("$schema", "$id", "$defs", "$vocabulary")
	r.add(&kwDef{keywords: kw("$comment")})
	r.addName("definitions")
	r.add(&kwDef{keywords: kw("id"), code: codeID}, &kwDef{keywords: kw("$ref"), schemaType: kw("string"), code: codeRef})
}

func validationVocabulary(r *rules) {
	r.add(
		&kwDef{keywords: kw("maximum", "minimum", "exclusiveMaximum", "exclusiveMinimum"), types: kw("number"), schemaType: kw("number"), code: codeLimitNumber},
		&kwDef{keywords: kw("multipleOf"), types: kw("number"), schemaType: kw("number"), code: codeMultipleOf},
		&kwDef{keywords: kw("maxLength", "minLength"), types: kw("string"), schemaType: kw("number"), code: codeLimitLength},
		&kwDef{keywords: kw("pattern"), types: kw("string"), schemaType: kw("string"), code: codePattern},
		&kwDef{keywords: kw("maxProperties", "minProperties"), types: kw("object"), schemaType: kw("number"), code: codeLimitProperties},
		&kwDef{keywords: kw("required"), types: kw("object"), schemaType: kw("array"), code: codeRequired},
		&kwDef{keywords: kw("maxItems", "minItems"), types: kw("array"), schemaType: kw("number"), code: codeLimitItems},
		&kwDef{keywords: kw("uniqueItems"), types: kw("array"), schemaType: kw("boolean"), code: codeUniqueItems},
		&kwDef{keywords: kw("type"), schemaType: kw("string", "array")},
		&kwDef{keywords: kw("nullable"), schemaType: kw("boolean")},
		&kwDef{keywords: kw("const"), code: codeConst},
		&kwDef{keywords: kw("enum"), schemaType: kw("array"), code: codeEnum},
	)
}

func applicatorVocabulary(r *rules, draft2020 bool) {
	r.add(
		&kwDef{keywords: kw("not"), schemaType: kw("object", "boolean"), code: codeNot},
		&kwDef{keywords: kw("anyOf"), schemaType: kw("array"), code: codeAnyOf},
		&kwDef{keywords: kw("oneOf"), schemaType: kw("array"), code: codeOneOf},
		&kwDef{keywords: kw("allOf"), schemaType: kw("array"), code: codeAllOf},
		&kwDef{keywords: kw("if"), schemaType: kw("object", "boolean"), code: codeIf},
		&kwDef{keywords: kw("then", "else"), schemaType: kw("object", "boolean"), code: codeThenElse},
		&kwDef{keywords: kw("propertyNames"), types: kw("object"), schemaType: kw("object", "boolean"), code: codePropertyNames},
		&kwDef{keywords: kw("additionalProperties"), types: kw("object"), schemaType: kw("boolean", "object"), allowUndefined: true, code: codeAdditionalProperties},
		&kwDef{keywords: kw("dependencies"), types: kw("object"), schemaType: kw("object"), code: codeDependencies},
		&kwDef{keywords: kw("properties"), types: kw("object"), schemaType: kw("object"), code: codeProperties},
		&kwDef{keywords: kw("patternProperties"), types: kw("object"), schemaType: kw("object"), code: codePatternProperties},
	)
	if draft2020 {
		r.add(
			&kwDef{keywords: kw("prefixItems"), types: kw("array"), schemaType: kw("array"), before: "uniqueItems", code: codePrefixItems},
			&kwDef{keywords: kw("items"), types: kw("array"), schemaType: kw("object", "boolean"), before: "uniqueItems", code: codeItems2020},
		)
	} else {
		r.add(
			&kwDef{keywords: kw("additionalItems"), types: kw("array"), schemaType: kw("boolean", "object"), before: "uniqueItems", code: codeAdditionalItems},
			&kwDef{keywords: kw("items"), types: kw("array"), schemaType: kw("object", "array", "boolean"), before: "uniqueItems", code: codeItems},
		)
	}
	r.add(&kwDef{keywords: kw("contains"), types: kw("array"), schemaType: kw("object", "boolean"), before: "uniqueItems", code: codeContains})
}

func formatVocabulary(r *rules) {
	r.add(&kwDef{keywords: kw("format"), types: kw("number", "string"), schemaType: kw("string"), code: codeFormat})
}

func metadataVocabularies(r *rules) {
	r.addName("title", "description", "default", "deprecated", "readOnly", "writeOnly", "examples")
	r.addName("contentMediaType", "contentEncoding", "contentSchema")
}

func dynamicVocabulary(r *rules) {
	r.add(
		&kwDef{keywords: kw("$dynamicAnchor"), schemaType: kw("string"), code: codeDynamicAnchor},
		&kwDef{keywords: kw("$dynamicRef"), schemaType: kw("string"), code: codeDynamicRef},
		&kwDef{keywords: kw("$recursiveAnchor"), schemaType: kw("boolean"), code: codeRecursiveAnchor},
		&kwDef{keywords: kw("$recursiveRef"), schemaType: kw("string"), code: codeDynamicRef},
	)
}

func nextVocabulary(r *rules) {
	r.add(
		&kwDef{keywords: kw("dependentRequired"), types: kw("object"), schemaType: kw("object"), code: codeDependentRequired},
		&kwDef{keywords: kw("dependentSchemas"), types: kw("object"), schemaType: kw("object"), code: codeDependentSchemas},
		&kwDef{keywords: kw("maxContains", "minContains"), types: kw("array"), schemaType: kw("number")},
	)
}

func unevaluatedVocabulary(r *rules) {
	r.add(
		&kwDef{keywords: kw("unevaluatedProperties"), types: kw("object"), schemaType: kw("boolean", "object"), code: codeUnevaluatedProperties},
		&kwDef{keywords: kw("unevaluatedItems"), types: kw("array"), schemaType: kw("boolean", "object"), code: codeUnevaluatedItems},
	)
}

func formatLimitVocabulary(r *rules) {
	r.add(&kwDef{
		keywords:     kw("formatMaximum", "formatMinimum", "formatExclusiveMaximum", "formatExclusiveMinimum"),
		types:        kw("string"),
		schemaType:   kw("string"),
		dependencies: kw("format"),
		code:         codeFormatLimit,
	})
}

// rulesFor builds the rule set of Ajv (draft-07), Ajv2019 or Ajv2020 plus ajv-formats.
func rulesFor(d Draft) *rules {
	r := newRules()
	r.addName("$async")
	switch d {
	case Draft2020:
		dynamicVocabulary(r)
		coreVocabulary(r)
		validationVocabulary(r)
		applicatorVocabulary(r, true)
		formatVocabulary(r)
		metadataVocabularies(r)
		nextVocabulary(r)
		unevaluatedVocabulary(r)
	case Draft2019:
		dynamicVocabulary(r)
		coreVocabulary(r)
		validationVocabulary(r)
		applicatorVocabulary(r, false)
		formatVocabulary(r)
		metadataVocabularies(r)
		nextVocabulary(r)
		unevaluatedVocabulary(r)
	default:
		coreVocabulary(r)
		validationVocabulary(r)
		applicatorVocabulary(r, false)
		formatVocabulary(r)
		metadataVocabularies(r)
	}
	formatLimitVocabulary(r)
	return r
}
