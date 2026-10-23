package ajv

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// jsFunction stands for a function inherited from Object.prototype, which a generated
// validator reads when it looks up a property such as "toString" on a plain object.
type jsFunction struct{ name string }

// protoObject stands for Object.prototype itself, read through obj.__proto__.
type protoObject struct{}

var objectProtoNames = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "__proto__": true, "toLocaleString": true,
}

var errURIMalformed = errors.New("URI malformed")

// lookup is data[key] !== undefined plus the value read, for an object value.
func lookup(data any, key string) (any, bool) {
	switch o := data.(type) {
	case *jsv.Obj:
		if v, ok := o.Get(key); ok {
			return v, true
		}
	case *protoObject:
		if key == "__proto__" {
			return nil, true
		}
	default:
		return nil, false
	}
	if !objectProtoNames[key] {
		return nil, false
	}
	if key == "__proto__" {
		return &protoObject{}, true
	}
	return &jsFunction{key}, true
}

// ownKeys is the for-in key order of an object value.
func ownKeys(data any) []string {
	if o, ok := data.(*jsv.Obj); ok {
		return o.Keys()
	}
	return nil
}

func isObjectValue(v any) bool {
	switch v.(type) {
	case *jsv.Obj, *protoObject:
		return true
	}
	return false
}

func isNumber(v any) bool {
	_, ok := v.(float64)
	return ok
}

// isJSType is Ajv's checkDataType for one JSON type (strictNumbers off).
func isJSType(t string, v any) bool {
	switch t {
	case "null":
		return v == nil
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		return isObjectValue(v)
	case "integer":
		f, ok := v.(float64)
		return ok && !math.IsNaN(f) && math.Mod(f, 1) == 0
	case "number":
		return isNumber(v)
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	}
	return false
}

// isAnyType is Ajv's checkDataTypes.
func isAnyType(types []string, v any) bool {
	for _, t := range types {
		if isJSType(t, v) {
			return true
		}
	}
	return false
}

// deepEqual is fast-deep-equal on JSON values.
func deepEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && (x == y || (math.IsNaN(x) && math.IsNaN(y)))
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := len(x) - 1; i >= 0; i-- {
			if !deepEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case *jsv.Obj:
		if _, isProto := b.(*protoObject); isProto {
			return x.Len() == 0
		}
		y, ok := b.(*jsv.Obj)
		if !ok {
			return false
		}
		if x == y {
			return true
		}
		keys := x.Keys()
		if len(keys) != y.Len() {
			return false
		}
		for _, k := range keys {
			if !y.Has(k) {
				return false
			}
		}
		for i := len(keys) - 1; i >= 0; i-- {
			if !deepEqual(jsv.Prop(x, keys[i]), jsv.Prop(y, keys[i])) {
				return false
			}
		}
		return true
	case *protoObject:
		switch y := b.(type) {
		case *protoObject:
			return true
		case *jsv.Obj:
			return y.Len() == 0
		}
		return false
	case *jsFunction:
		y, ok := b.(*jsFunction)
		return ok && x.name == y.name
	}
	return false
}

// ucs2length counts code points the way Ajv's ucs2length does on UTF-16 units.
func ucs2length(s string) int {
	units := jsv.UTF16Units(s)
	n := 0
	for i := 0; i < len(units); {
		n++
		v := units[i]
		i++
		if v >= 0xd800 && v <= 0xdbff && i < len(units) && units[i]&0xfc00 == 0xdc00 {
			i++
		}
	}
	return n
}

func escapeJSONPointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func unescapeJSONPointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

const uriUnreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"

// encodeURIComponent works on UTF-16 code units; a lone surrogate is a URIError.
func encodeURIComponent(s string) (string, error) {
	units := jsv.UTF16Units(s)
	var b strings.Builder
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u < 0x80 && strings.IndexByte(uriUnreserved, byte(u)) >= 0 {
			b.WriteByte(byte(u))
			continue
		}
		r := rune(u)
		if u >= 0xdc00 && u <= 0xdfff {
			return "", errURIMalformed
		}
		if u >= 0xd800 && u <= 0xdbff {
			if i+1 >= len(units) || units[i+1]&0xfc00 != 0xdc00 {
				return "", errURIMalformed
			}
			r = 0x10000 + (rune(u)-0xd800)<<10 + rune(units[i+1]) - 0xdc00
			i++
		}
		var buf [4]byte
		n := utf8.EncodeRune(buf[:], r)
		for _, c := range buf[:n] {
			b.WriteString(byteHex(c))
		}
	}
	return b.String(), nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func pctByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	h, ok1 := hexVal(s[i+1])
	l, ok2 := hexVal(s[i+2])
	return h<<4 | l, ok1 && ok2
}

// decodeURIComponent decodes %XX escapes as UTF-8; a malformed escape is a URIError.
func decodeURIComponent(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		c, ok := pctByte(s, i)
		if !ok {
			return "", errURIMalformed
		}
		if c < 0x80 {
			b.WriteByte(c)
			i += 3
			continue
		}
		n := 0
		switch {
		case c&0xe0 == 0xc0:
			n = 2
		case c&0xf0 == 0xe0:
			n = 3
		case c&0xf8 == 0xf0:
			n = 4
		default:
			return "", errURIMalformed
		}
		seq := []byte{c}
		for k := 1; k < n; k++ {
			j := i + 3*k
			cc, ok := pctByte(s, j)
			if !ok || cc&0xc0 != 0x80 {
				return "", errURIMalformed
			}
			seq = append(seq, cc)
		}
		r, size := utf8.DecodeRune(seq)
		if r == utf8.RuneError || size != n {
			return "", errURIMalformed
		}
		b.Write(seq)
		i += 3 * n
	}
	return b.String(), nil
}

// isJSInteger is parseInt(String(x)) === x for a finite or infinite number x.
func isJSInteger(x float64) bool {
	s := jsv.FormatNumber(x)
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	start := end
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == start {
		return false
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	return err == nil && v == x
}
