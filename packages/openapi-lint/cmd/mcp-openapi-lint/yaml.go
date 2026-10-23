package main

// YAML loading with the semantics of the yaml package 2.9.1 that the TypeScript server
// used: parse(text, { maxAliasCount: 100, uniqueKeys: false }). gopkg.in/yaml.v3 only
// builds the node tree; resolution of scalars, explicit tags, keys and aliases follows
// the yaml package's YAML 1.2 core schema and its toJS conversion.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const maxAliasCount = 100

var (
	reCoreNull     = regexp.MustCompile(`^(?:~|[Nn]ull|NULL)?$`)
	reCoreBool     = regexp.MustCompile(`^(?:[Tt]rue|TRUE|[Ff]alse|FALSE)$`)
	reCoreOct      = regexp.MustCompile(`^0o[0-7]+$`)
	reCoreInt      = regexp.MustCompile(`^[-+]?[0-9]+$`)
	reCoreHex      = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	reCoreNaN      = regexp.MustCompile(`^(?:[-+]?\.(?:inf|Inf|INF)|\.nan|\.NaN|\.NAN)$`)
	reCoreFloatExp = regexp.MustCompile(`^[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)[eE][-+]?[0-9]+$`)
	reCoreFloat    = regexp.MustCompile(`^[-+]?(?:\.[0-9]+|[0-9]+\.[0-9]*)$`)
	reTimestamp    = regexp.MustCompile(`^([0-9]{4})-([0-9]{1,2})-([0-9]{1,2})` +
		`(?:(?:t|T|[ \t]+)([0-9]{1,2}):([0-9]{1,2}):([0-9]{1,2}(\.[0-9]+)?)` +
		`(?:[ \t]*(Z|[-+][012]?[0-9](?::[0-9]{2})?))?)?$`)
)

// scalarType is one tag of the core schema: the test an untagged plain scalar (or a
// scalar with that explicit tag) must pass, and how the matching text resolves.
type scalarType struct {
	tag     string
	test    *regexp.Regexp
	resolve func(string) any
}

// coreTypes is the core schema in the yaml package's order: the first test that
// matches an untagged plain scalar decides its type.
var coreTypes = []scalarType{
	{"!!null", reCoreNull, func(string) any { return nil }},
	{"!!bool", reCoreBool, func(s string) any { return s[0] == 't' || s[0] == 'T' }},
	{"!!int", reCoreOct, func(s string) any { return parseRadix(s[2:], 8) }},
	{"!!int", reCoreInt, func(s string) any { return parseRadix(s, 10) }},
	{"!!int", reCoreHex, func(s string) any { return parseRadix(s[2:], 16) }},
	{"!!float", reCoreNaN, func(s string) any {
		switch {
		case strings.HasSuffix(s, "n") || strings.HasSuffix(s, "N"):
			return math.NaN()
		case s[0] == '-':
			return math.Inf(-1)
		}
		return math.Inf(1)
	}},
	{"!!float", reCoreFloatExp, parseFloatText},
	{"!!float", reCoreFloat, parseFloatText},
}

func parseFloatText(s string) any {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// parseRadix is parseInt(digits, radix) for an optionally signed digit string: the
// exact integer rounded to the nearest double.
func parseRadix(s string, radix int) any {
	neg := strings.HasPrefix(s, "-")
	n, ok := new(big.Int).SetString(strings.TrimLeft(s, "+-"), radix)
	if !ok {
		return math.NaN()
	}
	f, _ := new(big.Float).SetInt(n).Float64()
	if neg {
		return -f
	}
	return f
}

func resolvePlain(s string) any {
	for _, t := range coreTypes {
		if t.test.MatchString(s) {
			return t.resolve(s)
		}
	}
	return s
}

// resolveTagged resolves a scalar carrying an explicit tag. Core tags apply only when
// one of their tests matches the text; otherwise, and for unknown tags, the text stays
// a string. !!binary and !!timestamp are the yaml-1.1 tags the package knows.
func resolveTagged(tag, value string) (any, error) {
	switch tag {
	case "!!str", "!":
		return value, nil
	case "!!binary":
		return bufferJSON(decodeBase64(value)), nil
	case "!!timestamp":
		return timestamp(value)
	}
	for _, t := range coreTypes {
		if t.tag == tag && t.test.MatchString(value) {
			return t.resolve(value), nil
		}
	}
	return value, nil
}

// decodeBase64 is Buffer.from(text, "base64"): whitespace and characters outside the
// alphabet are skipped, the URL-safe alphabet is accepted, decoding stops at "=".
func decodeBase64(text string) []byte {
	var clean strings.Builder
	for _, c := range text {
		switch {
		case c == '=':
			goto done
		case c == '-':
			clean.WriteByte('+')
		case c == '_':
			clean.WriteByte('/')
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			clean.WriteRune(c)
		}
	}
done:
	s := clean.String()
	if len(s)%4 == 1 {
		s = s[:len(s)-1]
	}
	out, _ := base64.RawStdEncoding.DecodeString(s)
	return out
}

// bufferJSON is the JSON form of a Node Buffer: {"type":"Buffer","data":[...]}.
func bufferJSON(data []byte) *jsv.Obj {
	bytesList := make([]any, len(data))
	for i, b := range data {
		bytesList[i] = float64(b)
	}
	o := jsv.NewObj()
	o.Set("type", "Buffer")
	o.Set("data", bytesList)
	return o
}

// timestamp resolves !!timestamp to the Date's JSON form, toISOString().
func timestamp(value string) (any, error) {
	m := reTimestamp.FindStringSubmatch(value)
	if m == nil {
		return nil, errors.New("!!timestamp expects a date, starting with yyyy-mm-dd")
	}
	num := func(s string) int {
		f, _ := strconv.ParseFloat(s, 64)
		return int(f)
	}
	year, month, day := num(m[1]), num(m[2]), num(m[3])
	if year >= 0 && year <= 99 {
		year += 1900 // Date.UTC maps two-digit years into the twentieth century
	}
	ms := 0
	if m[7] != "" {
		ms = num((m[7] + "00")[1:4])
	}
	t := time.Date(year, time.Month(month), day, num(m[4]), num(m[5]), num(m[6]), ms*int(time.Millisecond), time.UTC)
	if tz := m[8]; tz != "" && tz != "Z" {
		minutes := sexagesimal(tz)
		if math.Abs(minutes) < 30 {
			minutes *= 60
		}
		t = t.Add(-time.Duration(minutes) * time.Minute)
	}
	return isoString(t), nil
}

func sexagesimal(s string) float64 {
	sign := 1.0
	if s[0] == '-' || s[0] == '+' {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	total := 0.0
	for _, part := range strings.Split(strings.ReplaceAll(s, "_", ""), ":") {
		n, _ := strconv.ParseFloat(part, 64)
		total = total*60 + n
	}
	return sign * total
}

func isoString(t time.Time) string {
	y := t.Year()
	var year string
	switch {
	case y >= 0 && y <= 9999:
		year = fmt.Sprintf("%04d", y)
	case y < 0:
		year = fmt.Sprintf("-%06d", -y)
	default:
		year = fmt.Sprintf("+%06d", y)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", year, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/int(time.Millisecond))
}

// anchorData mirrors the yaml package's per-anchor record: the converted value, how
// many times it was produced (its definition counts once) and its alias weight.
type anchorData struct {
	res        any
	count      int
	aliasCount int
}

type yamlConverter struct {
	anchors map[*yaml.Node]*anchorData
	lines   []string // the source lines, for error positions
}

// sourceLines splits a YAML text at CR LF, CR and LF.
func sourceLines(text string) []string {
	return strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
}

// tagColumn is the 1-based UTF-16 column of the explicit tag of node n, as the yaml
// package reports it: gopkg.in/yaml.v3 marks the start of the node's properties (its
// anchor or its tag, whichever comes first) in code points.
func (c *yamlConverter) tagColumn(n *yaml.Node) int {
	if n.Line < 1 || n.Line > len(c.lines) {
		return n.Column
	}
	runes := []rune(c.lines[n.Line-1])
	i := min(max(n.Column-1, 0), len(runes))
	if i < len(runes) && runes[i] == '&' {
		for i < len(runes) && runes[i] != ' ' && runes[i] != '\t' {
			i++
		}
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
	}
	return jsv.UTF16Len(string(runes[:i])) + 1
}

func (c *yamlConverter) convert(n *yaml.Node, depth int) (any, error) {
	if depth > 1000 {
		return nil, errors.New("document nests too deeply")
	}
	if n.Kind == yaml.AliasNode {
		return c.alias(n, depth)
	}
	if n.Anchor == "" {
		return c.build(n, depth, nil)
	}
	data := &anchorData{count: 1}
	c.anchors[n] = data
	return c.build(n, depth, data)
}

// build converts a node; for an anchored collection the result is registered before
// its children are converted, as the yaml package does.
func (c *yamlConverter) build(n *yaml.Node, depth int, data *anchorData) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return c.convert(n.Content[0], depth+1)
	case yaml.MappingNode:
		obj := jsv.NewObj()
		if data != nil {
			data.res = obj
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, err := c.convert(n.Content[i], depth+1)
			if err != nil {
				return nil, err
			}
			value, err := c.convert(n.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			name := ""
			if key != nil {
				name = jsv.ToString(key)
			}
			obj.Set(name, value)
		}
		return obj, nil
	case yaml.SequenceNode:
		arr := make([]any, len(n.Content))
		if data != nil {
			data.res = arr
		}
		for i, item := range n.Content {
			value, err := c.convert(item, depth+1)
			if err != nil {
				return nil, err
			}
			arr[i] = value
		}
		return arr, nil
	case yaml.ScalarNode:
		var v any
		var err error
		switch {
		case n.Style&yaml.TaggedStyle != 0:
			v, err = resolveTagged(n.Tag, n.Value)
			if err != nil && n.Tag == "!!timestamp" {
				err = fmt.Errorf("%w at line %d, column %d", err, n.Line, c.tagColumn(n))
			}
		case n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0:
			v = n.Value
		default:
			v = resolvePlain(n.Value)
		}
		if err != nil {
			return nil, err
		}
		if data != nil {
			data.res = v
		}
		return v, nil
	}
	return nil, fmt.Errorf("unsupported YAML node kind %d", n.Kind)
}

func (c *yamlConverter) alias(n *yaml.Node, depth int) (any, error) {
	source := n.Alias
	if source == nil {
		return nil, fmt.Errorf("Unresolved alias (the anchor must be set before the alias): %s", n.Value)
	}
	data, ok := c.anchors[source]
	if !ok {
		if _, err := c.convert(source, depth+1); err != nil {
			return nil, err
		}
		data = c.anchors[source]
	}
	data.count++
	if data.aliasCount == 0 {
		data.aliasCount = c.aliasCount(source)
	}
	if data.count*data.aliasCount > maxAliasCount {
		return nil, errors.New("Excessive alias count indicates a resource exhaustion attack")
	}
	return data.res, nil
}

// aliasCount is the yaml package's getAliasCount: the weight of the aliases a node holds.
func (c *yamlConverter) aliasCount(n *yaml.Node) int {
	switch n.Kind {
	case yaml.AliasNode:
		if d, ok := c.anchors[n.Alias]; ok {
			return d.count * d.aliasCount
		}
		return 0
	case yaml.MappingNode, yaml.SequenceNode:
		most := 0
		for _, item := range n.Content {
			most = max(most, c.aliasCount(item))
		}
		return most
	}
	return 1
}

// parseYAML is the yaml package's parse(text, { maxAliasCount: 100, uniqueKeys: false }):
// a stream holding more than one document is an error.
func parseYAML(text string) (any, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	// A second document is an error whatever it holds: the yaml package reports it at the
	// start of that document before looking at its content.
	var second yaml.Node
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		line := secondDocumentLine(text)
		if err != nil && line == 0 {
			return nil, err
		}
		msg := "Source contains multiple documents; please use YAML.parseAllDocuments()"
		if line > 0 {
			msg += fmt.Sprintf(" at line %d, column 1", line)
		}
		return nil, errors.New(msg)
	}
	c := &yamlConverter{anchors: map[*yaml.Node]*anchorData{}, lines: sourceLines(text)}
	return c.convert(&root, 0)
}

// secondDocumentLine is the line the yaml package reports for a second document: the line
// of its "---" marker, or, after a "..." end marker, the first later line that holds more
// than blanks, comments or directives. 0 when no second document starts.
func secondDocumentLine(text string) int {
	marker := func(line, m string) bool {
		return strings.HasPrefix(line, m) && (len(line) == 3 || line[3] == ' ' || line[3] == '\t')
	}
	started, ended := false, false
	for i, line := range sourceLines(text) {
		trimmed := strings.Trim(line, " \t")
		switch {
		case marker(line, "---"):
			if started || ended {
				return i + 1
			}
			started = true
		case marker(line, "..."):
			ended = true
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case strings.HasPrefix(line, "%") && (ended || !started):
		case ended:
			return i + 1
		default:
			started = true
		}
	}
	return 0
}
