package jsregex

import (
	"unicode"
)

// charSet is a set of characters as a predicate. Matching a character against a class also tries the
// characters that are case-equivalent to it when the i flag is set; a negated class ([^...]) inverts
// the result after that.
type charSet struct {
	has    func(rune) bool
	negate bool
}

func ranges(rs ...[2]rune) func(rune) bool {
	return func(c rune) bool {
		for _, r := range rs {
			if c >= r[0] && c <= r[1] {
				return true
			}
		}
		return false
	}
}

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

func isWordBasic(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// isSpace is WhiteSpace or LineTerminator.
func isSpace(c rune) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func isLineTerminator(c rune) bool { return c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029 }

func isIDStart(c rune) bool {
	return c == '$' || c == '_' || unicode.IsLetter(c) || unicode.Is(unicode.Nl, c) || unicode.Is(unicode.Other_ID_Start, c)
}

func isIDPart(c rune) bool {
	return isIDStart(c) || unicode.Is(unicode.Mn, c) || unicode.Is(unicode.Mc, c) || unicode.Is(unicode.Nd, c) ||
		unicode.Is(unicode.Pc, c) || c == 0x200C || c == 0x200D || unicode.Is(unicode.Other_ID_Continue, c)
}

// canonNonUnicode is Canonicalize for non-unicode case-insensitive matching: the character's
// upper case when that is a single code unit, unless it maps a non-ASCII character to ASCII.
func canonNonUnicode(c rune) rune {
	u := upperSingle(c)
	if u > 0xFFFF || (c >= 128 && u < 128) {
		return c
	}
	return u
}

// upperSingle is String.prototype.toUpperCase for one character when the result is one character;
// characters whose full upper case has several characters map to themselves.
func upperSingle(c rune) rune {
	if _, multi := specialUpper[c]; multi {
		return c
	}
	return unicode.ToUpper(c)
}

// specialUpper lists characters whose full upper-case mapping (SpecialCasing.txt, unconditional)
// has more than one character.
var specialUpper = map[rune]bool{
	0x00DF: true, 0x0149: true, 0x01F0: true, 0x0390: true, 0x03B0: true, 0x0587: true, 0x1E96: true, 0x1E97: true,
	0x1E98: true, 0x1E99: true, 0x1E9A: true, 0x1F50: true, 0x1F52: true, 0x1F54: true, 0x1F56: true, 0x1F80: true,
	0x1F81: true, 0x1F82: true, 0x1F83: true, 0x1F84: true, 0x1F85: true, 0x1F86: true, 0x1F87: true, 0x1F88: true,
	0x1F89: true, 0x1F8A: true, 0x1F8B: true, 0x1F8C: true, 0x1F8D: true, 0x1F8E: true, 0x1F8F: true, 0x1F90: true,
	0x1F91: true, 0x1F92: true, 0x1F93: true, 0x1F94: true, 0x1F95: true, 0x1F96: true, 0x1F97: true, 0x1F98: true,
	0x1F99: true, 0x1F9A: true, 0x1F9B: true, 0x1F9C: true, 0x1F9D: true, 0x1F9E: true, 0x1F9F: true, 0x1FA0: true,
	0x1FA1: true, 0x1FA2: true, 0x1FA3: true, 0x1FA4: true, 0x1FA5: true, 0x1FA6: true, 0x1FA7: true, 0x1FA8: true,
	0x1FA9: true, 0x1FAA: true, 0x1FAB: true, 0x1FAC: true, 0x1FAD: true, 0x1FAE: true, 0x1FAF: true, 0x1FB2: true,
	0x1FB3: true, 0x1FB4: true, 0x1FB6: true, 0x1FB7: true, 0x1FBC: true, 0x1FC2: true, 0x1FC3: true, 0x1FC4: true,
	0x1FC6: true, 0x1FC7: true, 0x1FCC: true, 0x1FD2: true, 0x1FD3: true, 0x1FD6: true, 0x1FD7: true, 0x1FE2: true,
	0x1FE3: true, 0x1FE4: true, 0x1FE6: true, 0x1FE7: true, 0x1FF2: true, 0x1FF3: true, 0x1FF4: true, 0x1FF6: true,
	0x1FF7: true, 0x1FFC: true, 0xFB00: true, 0xFB01: true, 0xFB02: true, 0xFB03: true, 0xFB04: true, 0xFB05: true,
	0xFB06: true, 0xFB13: true, 0xFB14: true, 0xFB15: true, 0xFB16: true, 0xFB17: true,
}

// variants are the characters case-equivalent to c (c included) under the i flag.
func variants(c rune, unicodeMode bool) []rune {
	out := []rune{c}
	if unicodeMode {
		for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
			out = append(out, f)
		}
		return out
	}
	want := canonNonUnicode(c)
	seen := map[rune]bool{c: true}
	try := func(x rune) {
		if !seen[x] && x <= 0xFFFF && canonNonUnicode(x) == want {
			seen[x] = true
			out = append(out, x)
		}
	}
	try(want)
	for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
		try(f)
	}
	for f := unicode.SimpleFold(want); f != want; f = unicode.SimpleFold(f) {
		try(f)
	}
	return out
}

func sameChar(a, b rune, icase, unicodeMode bool) bool {
	if a == b {
		return true
	}
	if !icase {
		return false
	}
	if unicodeMode {
		for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
			if f == b {
				return true
			}
		}
		return false
	}
	return canonNonUnicode(a) == canonNonUnicode(b)
}

func (s *charSet) matches(c rune, icase, unicodeMode bool) bool {
	hit := s.has(c)
	if !hit && icase {
		for _, v := range variants(c, unicodeMode)[1:] {
			if s.has(v) {
				hit = true
				break
			}
		}
	}
	return hit != s.negate
}

// wordFunc is \w: with the u (or v) and i flags it also holds the characters that case-fold onto an
// ASCII word character (U+017F, U+212A).
func wordFunc(unicodeIcase bool) func(rune) bool {
	if !unicodeIcase {
		return isWordBasic
	}
	return func(c rune) bool { return isWordBasic(c) || c == 0x017F || c == 0x212A }
}

func not(f func(rune) bool) func(rune) bool { return func(c rune) bool { return !f(c) } }

func union(fs ...func(rune) bool) func(rune) bool {
	return func(c rune) bool {
		for _, f := range fs {
			if f(c) {
				return true
			}
		}
		return false
	}
}

func single(c rune) func(rune) bool { return func(x rune) bool { return x == c } }

// property resolves a \p{...} body to a predicate, or nil when V8 does not know it.
func property(body string) func(rune) bool {
	name, value := body, ""
	for i := 0; i < len(body); i++ {
		if body[i] == '=' {
			name, value = body[:i], body[i+1:]
			break
		}
	}
	if value != "" {
		switch name {
		case "General_Category", "gc":
			return generalCategory(value)
		case "Script", "sc", "Script_Extensions", "scx":
			if t := script(value); t != nil {
				return func(c rune) bool { return unicode.Is(t, c) }
			}
		}
		return nil
	}
	if f := generalCategory(name); f != nil {
		return f
	}
	return binaryProperty(name)
}

var categoryAliases = map[string]string{
	"Letter": "L", "Cased_Letter": "LC", "Uppercase_Letter": "Lu", "Lowercase_Letter": "Ll", "Titlecase_Letter": "Lt",
	"Modifier_Letter": "Lm", "Other_Letter": "Lo", "Mark": "M", "Combining_Mark": "M", "Nonspacing_Mark": "Mn",
	"Spacing_Mark": "Mc", "Enclosing_Mark": "Me", "Number": "N", "Decimal_Number": "Nd", "digit": "Nd",
	"Letter_Number": "Nl", "Other_Number": "No", "Punctuation": "P", "punct": "P", "Connector_Punctuation": "Pc",
	"Dash_Punctuation": "Pd", "Open_Punctuation": "Ps", "Close_Punctuation": "Pe", "Initial_Punctuation": "Pi",
	"Final_Punctuation": "Pf", "Other_Punctuation": "Po", "Symbol": "S", "Math_Symbol": "Sm", "Currency_Symbol": "Sc",
	"Modifier_Symbol": "Sk", "Other_Symbol": "So", "Separator": "Z", "Space_Separator": "Zs", "Line_Separator": "Zl",
	"Paragraph_Separator": "Zp", "Other": "C", "Control": "Cc", "cntrl": "Cc", "Format": "Cf", "Surrogate": "Cs",
	"Private_Use": "Co", "Unassigned": "Cn",
}

func generalCategory(v string) func(rune) bool {
	if a, ok := categoryAliases[v]; ok {
		v = a
	}
	switch v {
	case "LC":
		return func(c rune) bool { return unicode.In(c, unicode.Lu, unicode.Ll, unicode.Lt) }
	case "Cn":
		return func(c rune) bool { return !assigned(c) }
	case "C":
		return func(c rune) bool { return unicode.Is(unicode.C, c) || !assigned(c) }
	case "Cs":
		return func(c rune) bool { return c >= 0xD800 && c <= 0xDFFF }
	}
	if t, ok := unicode.Categories[v]; ok {
		return func(c rune) bool { return unicode.Is(t, c) }
	}
	return nil
}

func assigned(c rune) bool {
	for _, t := range unicode.Categories {
		if unicode.Is(t, c) {
			return true
		}
	}
	return c >= 0xD800 && c <= 0xDFFF
}

func script(v string) *unicode.RangeTable {
	if t, ok := unicode.Scripts[v]; ok {
		return t
	}
	if long, ok := scriptCodes[v]; ok {
		return unicode.Scripts[long]
	}
	return nil
}

var scriptCodes = map[string]string{
	"Latn": "Latin", "Grek": "Greek", "Cyrl": "Cyrillic", "Arab": "Arabic", "Hebr": "Hebrew", "Hani": "Han",
	"Hira": "Hiragana", "Kana": "Katakana", "Hang": "Hangul", "Thai": "Thai", "Deva": "Devanagari", "Zyyy": "Common",
	"Zinh": "Inherited", "Armn": "Armenian", "Geor": "Georgian", "Ethi": "Ethiopic", "Beng": "Bengali", "Taml": "Tamil",
}

func binaryProperty(v string) func(rune) bool {
	switch v {
	case "Any":
		return func(rune) bool { return true }
	case "ASCII":
		return func(c rune) bool { return c < 0x80 }
	case "Assigned":
		return assigned
	case "Alphabetic", "Alpha":
		return func(c rune) bool {
			return unicode.IsLetter(c) || unicode.Is(unicode.Nl, c) || unicode.Is(unicode.Other_Alphabetic, c)
		}
	case "Uppercase", "Upper":
		return func(c rune) bool { return unicode.Is(unicode.Lu, c) || unicode.Is(unicode.Other_Uppercase, c) }
	case "Lowercase", "Lower":
		return func(c rune) bool { return unicode.Is(unicode.Ll, c) || unicode.Is(unicode.Other_Lowercase, c) }
	case "White_Space", "space":
		return func(c rune) bool { return unicode.Is(unicode.White_Space, c) }
	case "ASCII_Hex_Digit", "AHex":
		return func(c rune) bool { return unicode.Is(unicode.ASCII_Hex_Digit, c) }
	case "Hex_Digit", "Hex":
		return func(c rune) bool { return unicode.Is(unicode.Hex_Digit, c) }
	case "Dash":
		return func(c rune) bool { return unicode.Is(unicode.Dash, c) }
	case "Ideographic", "Ideo":
		return func(c rune) bool { return unicode.Is(unicode.Ideographic, c) }
	}
	if t, ok := unicode.Properties[v]; ok {
		return func(c rune) bool { return unicode.Is(t, c) }
	}
	return nil
}
