package main

import (
	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

func (df *differ) properties(b, a *jsv.Obj, base string, depth int) error {
	bProps, aProps := orEmpty(field(b, "properties")), orEmpty(field(a, "properties"))
	bReq, aReq := requiredSet(b), requiredSet(a)
	closed := field(a, "additionalProperties")
	isClosed := closed.ok && closed.v == false
	for _, key := range jsKeys(bProps) {
		in, err := jsIn(key, aProps)
		if err != nil {
			return err
		}
		if !in {
			df.push(base+"/properties/"+key, "property removed", pick(isClosed, "breaking", "review"), none, none,
				pick(isClosed, "documents that still send it are rejected by additionalProperties: false", "the property is no longer described; readers that depend on it lose its contract"))
		}
	}
	for _, key := range jsKeys(aProps) {
		sub := base + "/properties/" + key
		in, err := jsIn(key, bProps)
		if err != nil {
			return err
		}
		if !in {
			req := contains(aReq, key)
			df.push(sub, "property added", pick(req, "breaking", "compatible"), none, none,
				pick(req, "it is required, so existing documents without it are rejected", "optional, so existing documents stay valid"))
			continue
		}
		if err := df.diff(valueAt(bProps, key), valueAt(aProps, key), sub, depth+1); err != nil {
			return err
		}
	}
	for _, key := range aReq {
		if contains(bReq, key) {
			continue
		}
		in, err := jsIn(key, bProps)
		if err != nil {
			return err
		}
		if in {
			df.push(base+"/required", "required added", "breaking", none, opt{key, true}, "documents without "+key+" are now rejected")
		}
	}
	for _, key := range bReq {
		if !contains(aReq, key) {
			df.push(base+"/required", "required removed", "compatible", opt{key, true}, none, key+" became optional; readers must handle its absence")
		}
	}
	return nil
}

func (df *differ) additional(b, a *jsv.Obj, base string) {
	bv, av := field(b, "additionalProperties"), field(a, "additionalProperties")
	if identical(bv, av) {
		return
	}
	nowClosed := av.ok && av.v == false
	wasClosed := bv.ok && bv.v == false
	df.push(base+"/additionalProperties", "additionalProperties", pick(nowClosed, "breaking", pick(wasClosed, "compatible", "review")), bv, av,
		pick(nowClosed, "unknown properties are now rejected", "unknown properties are now allowed or constrained differently"))
}

func enumList(o *jsv.Obj) opt {
	if arr, ok := jsv.Prop(o, "enum").([]any); ok {
		return opt{arr, true}
	}
	return none
}

func minus(xs, ys opt) []any {
	out := []any{}
	var xl, yl []any
	if xs.ok {
		xl = xs.v.([]any)
	}
	if ys.ok {
		yl = ys.v.([]any)
	}
	for _, v := range xl {
		found := false
		for _, w := range yl {
			if same(opt{v, true}, opt{w, true}) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}

func (df *differ) enum(b, a *jsv.Obj, base string) {
	if !b.Has("enum") && !a.Has("enum") {
		return
	}
	be, ae := enumList(b), enumList(a)
	if same(be, ae) {
		return
	}
	removed, added := minus(be, ae), minus(ae, be)
	impact := pick(!be.ok, "breaking", pick(!ae.ok, "compatible", pick(len(removed) > 0, "breaking", "compatible")))
	reason := pick(!be.ok, "values are now restricted to a list", pick(!ae.ok, "the restriction was lifted",
		pick(len(removed) > 0, "values removed: "+jsv.Stringify(removed, 0), "values added: "+jsv.Stringify(added, 0))))
	df.push(base+"/enum", "enum", impact, be, ae, reason)
}

var tighten = []struct {
	kw    string
	lower bool
}{
	{"minimum", true}, {"exclusiveMinimum", true}, {"minLength", true}, {"minItems", true}, {"minProperties", true},
	{"maximum", false}, {"exclusiveMaximum", false}, {"maxLength", false}, {"maxItems", false}, {"maxProperties", false},
}

func (df *differ) bounds(b, a *jsv.Obj, base string) {
	for _, t := range tighten {
		bv, av := field(b, t.kw), field(a, t.kw)
		if identical(bv, av) {
			continue
		}
		bn, bNum := bv.v.(float64)
		an, aNum := av.v.(float64)
		impact := "compatible"
		switch {
		case !aNum:
		case !bNum:
			impact = "breaking"
		case t.lower && an > bn, !t.lower && an < bn:
			impact = "breaking"
		}
		df.push(base+"/"+t.kw, t.kw, impact, bv, av, pick(impact == "breaking", "the constraint is tighter than before", "the constraint is looser or removed"))
	}
}

func (df *differ) items(b, a *jsv.Obj, base string, depth int) error {
	bi, ai := field(b, "items"), field(a, "items")
	if !bi.ok && !ai.ok {
		return nil
	}
	_, bArr := bi.v.([]any)
	_, aArr := ai.v.([]any)
	switch {
	case ai.ok && !bi.ok:
		df.push(base+"/items", "items added", "breaking", none, none, "array elements are now constrained")
	case !ai.ok && bi.ok:
		df.push(base+"/items", "items removed", "compatible", none, none, "array elements are no longer constrained")
	case !bArr && !aArr:
		return df.diff(bi.v, ai.v, base+"/items", depth+1)
	case !same(bi, ai):
		df.push(base+"/items", "items", "review", bi, ai, "tuple-style items changed")
	}
	return nil
}
