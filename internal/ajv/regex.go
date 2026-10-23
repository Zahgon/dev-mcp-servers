package ajv

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsregex"
)

// Pattern keywords and the regex format run on the ECMAScript engine in internal/jsregex.
// The fixed format patterns of ajv-formats use no lookaround or backreferences, so they run
// on Go's RE2 engine after translating the syntax that differs: "." and \s use JavaScript's
// line terminators and white space, \uXXXX and \u{X} escapes become \x{X}, named groups
// become (?P<name>...), and "[" inside a class is literal.

const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

type regexSyntaxError struct{ msg string }

func (e regexSyntaxError) Error() string { return e.msg }

type reTranslator struct {
	src         []rune
	pos         int
	out         strings.Builder
	unicode     bool
	unsupported bool
}

func (t *reTranslator) fail(msg string) error { return regexSyntaxError{msg} }

func (t *reTranslator) peek(off int) rune {
	if t.pos+off < len(t.src) {
		return t.src[t.pos+off]
	}
	return -1
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// hexRun reads exactly n hex digits at the cursor.
func (t *reTranslator) hexRun(n int) (rune, bool) {
	if t.pos+n > len(t.src) {
		return 0, false
	}
	var v rune
	for _, r := range t.src[t.pos : t.pos+n] {
		if !isHexRune(r) {
			return 0, false
		}
		d, _ := strconv.ParseUint(string(r), 16, 8)
		v = v*16 + rune(d)
	}
	t.pos += n
	return v, true
}

// unicodeEscape reads the part after \u and returns the code point.
func (t *reTranslator) unicodeEscape() (rune, error) {
	if t.unicode && t.peek(0) == '{' {
		end := t.pos + 1
		for end < len(t.src) && isHexRune(t.src[end]) {
			end++
		}
		if end == t.pos+1 || end >= len(t.src) || t.src[end] != '}' {
			return 0, t.fail("Invalid Unicode escape")
		}
		v, err := strconv.ParseUint(string(t.src[t.pos+1:end]), 16, 32)
		if err != nil || v > 0x10ffff {
			return 0, t.fail("Invalid Unicode escape")
		}
		t.pos = end + 1
		return rune(v), nil
	}
	v, ok := t.hexRun(4)
	if !ok {
		if t.unicode {
			return 0, t.fail("Invalid Unicode escape")
		}
		return 'u', nil
	}
	if t.unicode && v >= 0xd800 && v <= 0xdbff && t.peek(0) == '\\' && t.peek(1) == 'u' {
		save := t.pos
		t.pos += 2
		if lo, ok := t.hexRun(4); ok && lo >= 0xdc00 && lo <= 0xdfff {
			return 0x10000 + (v-0xd800)<<10 + lo - 0xdc00, nil
		}
		t.pos = save
	}
	return v, nil
}

func literal(r rune) string {
	if r >= 0xd800 && r <= 0xdfff {
		return ""
	}
	if r < 0x80 && strings.ContainsRune(`\.+*?()|[]{}^$-/&~#`, r) {
		return `\` + string(r)
	}
	if r < 0x20 || r == 0x7f || r >= 0x80 {
		return fmt.Sprintf(`\x{%x}`, r)
	}
	return string(r)
}

// escape translates the escape whose backslash was just consumed.
func (t *reTranslator) escape(inClass bool) error {
	if t.pos >= len(t.src) {
		return t.fail(`\ at end of pattern`)
	}
	c := t.src[t.pos]
	t.pos++
	switch c {
	case 'd', 'D', 'w', 'W':
		t.out.WriteString(`\` + string(c))
	case 'b', 'B':
		if inClass {
			switch {
			case c == 'b':
				t.out.WriteString(`\x{8}`)
			case t.unicode:
				return t.fail("Invalid class escape")
			default:
				t.out.WriteString("B")
			}
		} else {
			t.out.WriteString(`\` + string(c))
		}
	case 's':
		if inClass {
			t.out.WriteString(jsSpaceClass)
		} else {
			t.out.WriteString("[" + jsSpaceClass + "]")
		}
	case 'S':
		if inClass {
			t.unsupported = true
		}
		t.out.WriteString("[^" + jsSpaceClass + "]")
	case 't':
		t.out.WriteString(`\t`)
	case 'n':
		t.out.WriteString(`\n`)
	case 'v':
		t.out.WriteString(`\v`)
	case 'f':
		t.out.WriteString(`\f`)
	case 'r':
		t.out.WriteString(`\r`)
	case 'c':
		n := t.peek(0)
		if (n >= 'a' && n <= 'z') || (n >= 'A' && n <= 'Z') {
			t.pos++
			t.out.WriteString(literal(n % 32))
		} else if t.unicode {
			return t.fail("Invalid unicode escape")
		} else {
			t.out.WriteString(`\\c`)
		}
	case 'x':
		if v, ok := t.hexRun(2); ok {
			t.out.WriteString(literal(v))
		} else if t.unicode {
			return t.fail("Invalid escape")
		} else {
			t.out.WriteString("x")
		}
	case 'u':
		v, err := t.unicodeEscape()
		if err != nil {
			return err
		}
		if v >= 0xd800 && v <= 0xdfff {
			t.unsupported = true
		}
		t.out.WriteString(literal(v))
	case '0':
		if n := t.peek(0); n >= '0' && n <= '9' {
			if t.unicode {
				return t.fail("Invalid decimal escape")
			}
			t.unsupported = true
		}
		t.out.WriteString(`\x{0}`)
	case 'p', 'P':
		if !t.unicode {
			t.out.WriteString(string(c))
			return nil
		}
		end := t.pos
		for end < len(t.src) && t.src[end] != '}' {
			end++
		}
		if t.peek(0) != '{' || end >= len(t.src) {
			return t.fail("Invalid property name")
		}
		t.out.WriteString(`\` + string(c) + string(t.src[t.pos:end+1]))
		t.pos = end + 1
	case 'k':
		if t.unicode {
			t.unsupported = true
		}
		t.out.WriteString("k")
	default:
		switch {
		case c >= '1' && c <= '9':
			t.unsupported = true
		case strings.ContainsRune(`^$\.*+?()[]{}|/`, c), c == '-' && inClass:
			t.out.WriteString(literal(c))
		case t.unicode:
			return t.fail("Invalid escape")
		default:
			t.out.WriteString(literal(c))
		}
	}
	return nil
}

func (t *reTranslator) class() error {
	if t.peek(0) == '^' {
		t.pos++
		if t.peek(0) == ']' {
			t.pos++
			t.out.WriteString(`[\x{0}-\x{10ffff}]`)
			return nil
		}
		t.out.WriteString("[^")
	} else {
		if t.peek(0) == ']' {
			t.pos++
			t.out.WriteString(`[^\x{0}-\x{10ffff}]`)
			return nil
		}
		t.out.WriteString("[")
	}
	for t.pos < len(t.src) {
		c := t.src[t.pos]
		t.pos++
		switch c {
		case ']':
			t.out.WriteString("]")
			return nil
		case '\\':
			if err := t.escape(true); err != nil {
				return err
			}
		case '-':
			t.out.WriteString("-")
		default:
			t.out.WriteString(literal(c))
		}
	}
	return t.fail("Unterminated character class")
}

var quantifierRe = regexp.MustCompile(`^\{[0-9]+(,[0-9]*)?\}`)

func (t *reTranslator) group() error {
	if t.peek(0) != '?' {
		t.out.WriteString("(")
		return nil
	}
	switch {
	case t.peek(1) == ':':
		t.pos += 2
		t.out.WriteString("(?:")
	case t.peek(1) == '=' || t.peek(1) == '!' || (t.peek(1) == '<' && (t.peek(2) == '=' || t.peek(2) == '!')):
		t.unsupported = true
		t.pos++
		t.out.WriteString("(")
	case t.peek(1) == '<':
		end := t.pos + 2
		for end < len(t.src) && t.src[end] != '>' {
			end++
		}
		if end >= len(t.src) || end == t.pos+2 {
			return t.fail("Invalid capture group name")
		}
		t.out.WriteString("(?P<" + string(t.src[t.pos+2:end]) + ">")
		t.pos = end + 1
	default:
		return t.fail("Invalid group")
	}
	return nil
}

func (t *reTranslator) run() error {
	for t.pos < len(t.src) {
		c := t.src[t.pos]
		t.pos++
		var err error
		switch c {
		case '\\':
			err = t.escape(false)
		case '.':
			t.out.WriteString(`[^\n\r\x{2028}\x{2029}]`)
		case '[':
			err = t.class()
		case '(':
			err = t.group()
		case '{':
			if m := quantifierRe.FindString(string(t.src[t.pos-1:])); m != "" {
				t.out.WriteString(m)
				t.pos += utf8.RuneCountInString(m) - 1
			} else if t.unicode && t.peek(0) >= '0' && t.peek(0) <= '9' {
				err = t.fail("Incomplete quantifier")
			} else if t.unicode {
				err = t.fail("Lone quantifier brackets")
			} else {
				t.out.WriteString(`\{`)
			}
		case '}', ']':
			if t.unicode {
				err = t.fail("Lone quantifier brackets")
			} else {
				t.out.WriteString(literal(c))
			}
		case '^', '$', '|', '*', '+', '?', ')':
			t.out.WriteString(string(c))
		default:
			t.out.WriteString(literal(c))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func translateRegex(src string, unicode bool) (string, bool, error) {
	t := &reTranslator{src: []rune(src), unicode: unicode}
	if err := t.run(); err != nil {
		return "", false, err
	}
	return t.out.String(), t.unsupported, nil
}

// compileJSRegex is new RegExp(src, "u") for a pattern keyword.
func compileJSRegex(src string) (*jsPattern, error) {
	re, err := jsregex.Compile(src, "u")
	if err != nil {
		return nil, err
	}
	return &jsPattern{re}, nil
}

// jsPattern is a compiled pattern keyword; MatchString is RegExp.prototype.test.
type jsPattern struct{ re *jsregex.Regexp }

func (p *jsPattern) MatchString(s string) bool {
	matched, _ := p.re.Test(s, time.Time{})
	return matched
}

// validJSRegex is whether new RegExp(src) (no flags) compiles.
func validJSRegex(src string) bool {
	_, err := jsregex.Compile(src, "")
	return err == nil
}
