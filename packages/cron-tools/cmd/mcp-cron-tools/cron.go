package main

// Five-field cron (minute hour day-of-month month day-of-week) in the Vixie cron dialect:
// lists, ranges, steps, names for months and weekdays, 0 or 7 for Sunday, and the @ macros.
// When both day fields are restricted, a day matches if either field matches.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

type fieldName int

const (
	minuteField fieldName = iota
	hourField
	dayOfMonthField
	monthField
	dayOfWeekField
)

var fieldNames = [...]string{"minute", "hour", "day_of_month", "month", "day_of_week"}

func (f fieldName) String() string { return fieldNames[f] }

var fieldRanges = [...][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

var (
	monthAbbr = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	dayAbbr   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	monthName = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	dayName   = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
)

type macro struct{ name, expansion string }

var macros = []macro{
	{"@yearly", "0 0 1 1 *"}, {"@annually", "0 0 1 1 *"}, {"@monthly", "0 0 1 * *"}, {"@weekly", "0 0 * * 0"},
	{"@daily", "0 0 * * *"}, {"@midnight", "0 0 * * *"}, {"@hourly", "0 * * * *"},
}

type shapeKind int

const (
	shapeAll shapeKind = iota
	shapeStep
	shapeSingle
	shapeRange
	shapeList
)

type shape struct {
	kind               shapeKind
	from, to, step, at int
	values             []int
}

type parsedField struct {
	raw      string
	values   []int
	wildcard bool
	shape    shape
}

func (p parsedField) has(v int) bool {
	for _, x := range p.values {
		if x == v {
			return true
		}
	}
	return false
}

type parsedCron struct {
	expression string
	normalised string
	fields     [5]parsedField
	macro      string
}

// cronSyntaxError is an invalid expression; its text is what the tools report.
type cronSyntaxError struct{ msg string }

func (e cronSyntaxError) Error() string { return e.msg }

func syntaxErr(format string, args ...any) error {
	return cronSyntaxError{fmt.Sprintf(format, args...)}
}

func quote(s string) string { return jsv.Stringify(s, 0) }

// jsLower is String.prototype.toLowerCase for the comparisons made here.
func jsLower(s string) string {
	var b []byte
	for i := 0; i < len(s); {
		r, n := jsv.DecodeWTF8(s, i)
		i += n
		if r == 0x130 {
			b = append(b, "i\u0307"...)
			continue
		}
		b = jsv.AppendWTF8(b, unicode.ToLower(r))
	}
	return string(b)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func digitsValue(s string) float64 {
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func parseValue(token string, field fieldName) (int, error) {
	t := jsLower(token)
	if field == monthField {
		if i := indexOf(monthAbbr, t); i >= 0 {
			return i + 1, nil
		}
	}
	if field == dayOfWeekField {
		if i := indexOf(dayAbbr, t); i >= 0 {
			return i, nil
		}
	}
	if !allDigits(t) {
		suffix := ""
		switch field {
		case monthField:
			suffix = " or month name"
		case dayOfWeekField:
			suffix = " or weekday name"
		}
		return 0, syntaxErr("%s: %s is not a number%s", field, quote(token), suffix)
	}
	n := digitsValue(t)
	if field == dayOfWeekField && n == 7 {
		n = 0
	}
	lo, hi := fieldRanges[field][0], fieldRanges[field][1]
	if n < float64(lo) || n > float64(hi) {
		return 0, syntaxErr("%s: %s is outside %d-%d", field, jsv.FormatNumber(n), lo, hi)
	}
	return int(n), nil
}

// quartzToken is /^(\d*[LW]|LW)$/i.
func quartzToken(tok string) bool {
	if len(tok) == 2 && (tok[0]|0x20) == 'l' && (tok[1]|0x20) == 'w' {
		return true
	}
	if tok == "" {
		return false
	}
	last := tok[len(tok)-1]
	if last != 'L' && last != 'l' && last != 'W' && last != 'w' {
		return false
	}
	head := tok[:len(tok)-1]
	return head == "" || allDigits(head)
}

func parseField(raw string, field fieldName) (parsedField, error) {
	lo, hi := fieldRanges[field][0], fieldRanges[field][1]
	dayField := field == dayOfMonthField || field == dayOfWeekField
	if raw == "*" || (raw == "?" && dayField) {
		values := make([]int, 0, hi-lo+1)
		for v := lo; v <= hi; v++ {
			values = append(values, v)
		}
		return parsedField{raw: raw, values: values, wildcard: true, shape: shape{kind: shapeAll}}, nil
	}
	if strings.Contains(raw, "#") {
		return parsedField{}, syntaxErr("%s: L, W and # are Quartz extensions and are not part of 5-field cron", field)
	}
	for _, tok := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '-' || r == '/' }) {
		if quartzToken(tok) {
			return parsedField{}, syntaxErr("%s: L, W and # are Quartz extensions and are not part of 5-field cron", field)
		}
	}
	set := map[int]bool{}
	var sh *shape
	for _, item := range strings.Split(raw, ",") {
		if item == "" {
			return parsedField{}, syntaxErr("%s: empty list item in %s", field, quote(raw))
		}
		pieces := strings.Split(item, "/")
		if len(pieces) > 2 {
			return parsedField{}, syntaxErr("%s: more than one / in %s", field, quote(item))
		}
		base := pieces[0]
		hasStep := len(pieces) == 2
		step := 1
		var stepValue float64 = 1
		if hasStep {
			if !allDigits(pieces[1]) || digitsValue(pieces[1]) == 0 {
				return parsedField{}, syntaxErr("%s: step must be a positive number in %s", field, quote(item))
			}
			stepValue = digitsValue(pieces[1])
		}
		var from, to int
		switch {
		case base == "*":
			from, to = lo, hi
		case strings.Contains(base, "-"):
			ends := strings.Split(base, "-")
			if len(ends) > 2 || ends[0] == "" || ends[1] == "" {
				return parsedField{}, syntaxErr("%s: bad range %s", field, quote(base))
			}
			var err error
			if from, err = parseValue(ends[0], field); err != nil {
				return parsedField{}, err
			}
			if to, err = parseValue(ends[1], field); err != nil {
				return parsedField{}, err
			}
			if field == dayOfWeekField && ends[1] == "7" {
				to = 7
			}
			if to < from {
				return parsedField{}, syntaxErr("%s: range %s runs backwards", field, base)
			}
		default:
			var err error
			if from, err = parseValue(base, field); err != nil {
				return parsedField{}, err
			}
			to = from
			if hasStep {
				to = hi
			}
		}
		if stepValue > float64(hi-lo+1) {
			return parsedField{}, syntaxErr("%s: step %s is larger than the field range", field, jsv.FormatNumber(stepValue))
		}
		step = int(stepValue)
		for v := from; v <= to; v += step {
			if field == dayOfWeekField && v == 7 {
				set[0] = true
			} else {
				set[v] = true
			}
		}
		var item shape
		switch {
		case hasStep:
			item = shape{kind: shapeStep, from: from, to: to, step: step}
		case from == to:
			item = shape{kind: shapeSingle, at: from}
		default:
			item = shape{kind: shapeRange, from: from, to: to}
		}
		if sh == nil {
			sh = &item
		} else {
			sh = &shape{kind: shapeList}
		}
	}
	values := make([]int, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Ints(values)
	if sh.kind == shapeList {
		sh.values = values
	}
	return parsedField{raw: raw, values: values, shape: *sh}, nil
}

// collapseSpace is s.trim().replace(/\s+/g, " ").
func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range jsv.Trim(s) {
		if jsv.IsSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parseCron(expression string) (*parsedCron, error) {
	trimmed := collapseSpace(expression)
	if trimmed == "" {
		return nil, syntaxErr("expression is empty")
	}
	source := trimmed
	macroName := ""
	if strings.HasPrefix(trimmed, "@") {
		lower := jsLower(trimmed)
		found := false
		for _, m := range macros {
			if m.name == lower {
				source, macroName, found = m.expansion, lower, true
			}
		}
		if !found {
			if lower == "@reboot" {
				return nil, syntaxErr("@reboot runs at daemon start and has no schedule to compute")
			}
			names := make([]string, len(macros))
			for i, m := range macros {
				names[i] = m.name
			}
			return nil, syntaxErr("unknown macro %s; known: %s", trimmed, strings.Join(names, ", "))
		}
	}
	parts := strings.Split(source, " ")
	if len(parts) != 5 {
		note := ""
		if len(parts) == 6 {
			note = "; a leading seconds field is not supported"
		}
		return nil, syntaxErr("expected 5 fields (minute hour day-of-month month day-of-week), got %d%s", len(parts), note)
	}
	p := &parsedCron{expression: expression, normalised: strings.Join(parts, " "), macro: macroName}
	for i := range p.fields {
		f, err := parseField(parts[i], fieldName(i))
		if err != nil {
			return nil, err
		}
		p.fields[i] = f
	}
	return p, nil
}

type validation struct {
	valid    bool
	errors   []string
	warnings []string
	parsed   *parsedCron
}

var daysInMonth = [...]int{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// sundaySeven is /(^|-)7(\/|$)/.
func sundaySeven(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] == '7' && (i == 0 || p[i-1] == '-') && (i == len(p)-1 || p[i+1] == '/') {
			return true
		}
	}
	return false
}

func validateCron(expression string) validation {
	parsed, err := parseCron(expression)
	if err != nil {
		return validation{errors: []string{err.Error()}, warnings: []string{}}
	}
	warnings := []string{}
	dom, dow, month := parsed.fields[dayOfMonthField], parsed.fields[dayOfWeekField], parsed.fields[monthField]
	if !dom.wildcard && !dow.wildcard {
		warnings = append(warnings, "Both day-of-month and day-of-week are restricted. In Vixie cron (Linux crontab) the job runs when EITHER matches, not both. Some schedulers (for example Quartz) require ? in one of the two fields.")
	}
	if !dom.wildcard {
		reachable, long := false, false
		for _, d := range dom.values {
			for _, m := range month.values {
				if d <= daysInMonth[m-1] {
					reachable = true
				}
			}
			if d > 28 {
				long = true
			}
		}
		if !reachable {
			warnings = append(warnings, fmt.Sprintf("Day-of-month %s never occurs in month %s; the job never runs.", dom.raw, month.raw))
		} else if long && dow.wildcard {
			warnings = append(warnings, fmt.Sprintf("Day-of-month %s does not exist in every month; the job is skipped in shorter months.", dom.raw))
		}
	}
	for _, p := range strings.Split(dow.raw, ",") {
		if sundaySeven(p) {
			warnings = append(warnings, "7 was used for Sunday; it is accepted and treated as 0, but 0 is more portable.")
			break
		}
	}
	if strings.Contains(parsed.normalised, "?") {
		warnings = append(warnings, "? is a Quartz spelling of *; Vixie cron rejects it.")
	}
	if parsed.macro != "" {
		warnings = append(warnings, fmt.Sprintf("%s expands to %s.", parsed.macro, parsed.normalised))
	}
	return validation{valid: true, errors: []string{}, warnings: warnings, parsed: parsed}
}

func ordinal(n int) string {
	suffixes := []string{"th", "st", "nd", "rd"}
	v := n % 100
	if i := (v - 20) % 10; i >= 0 && i < len(suffixes) {
		return strconv.Itoa(n) + suffixes[i]
	}
	if v >= 0 && v < len(suffixes) {
		return strconv.Itoa(n) + suffixes[v]
	}
	return strconv.Itoa(n) + "th"
}

func joinList(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// nameOf mirrors MONTH_NAMES[v - 1] and DAY_NAMES[v]: an index past the end is undefined.
func nameOf(field fieldName, v int) string {
	var list []string
	idx := v
	switch field {
	case monthField:
		list, idx = monthName, v-1
	case dayOfWeekField:
		list = dayName
	default:
		return strconv.Itoa(v)
	}
	if idx < 0 || idx >= len(list) {
		return "undefined"
	}
	return list[idx]
}

func namedField(f fieldName) bool { return f == monthField || f == dayOfWeekField }

func describeField(pf parsedField, field fieldName, unit string) string {
	s := pf.shape
	switch s.kind {
	case shapeAll:
		return "every " + unit
	case shapeSingle:
		if namedField(field) {
			return nameOf(field, s.at)
		}
		return fmt.Sprintf("%s %d", unit, s.at)
	case shapeRange:
		if namedField(field) {
			return nameOf(field, s.from) + " through " + nameOf(field, s.to)
		}
		return fmt.Sprintf("every %s from %d through %d", unit, s.from, s.to)
	case shapeStep:
		lo, hi := fieldRanges[field][0], fieldRanges[field][1]
		span := ""
		switch {
		case s.from == lo && s.to == hi:
		case s.to == hi:
			span = " from " + nameOf(field, s.from)
		default:
			span = " from " + nameOf(field, s.from) + " through " + nameOf(field, s.to)
		}
		return fmt.Sprintf("every %s %s%s", ordinal(s.step), unit, span)
	case shapeList:
		names := make([]string, len(s.values))
		for i, v := range s.values {
			if namedField(field) {
				names[i] = nameOf(field, v)
			} else {
				names[i] = strconv.Itoa(v)
			}
		}
		if namedField(field) {
			return joinList(names)
		}
		return unit + "s " + joinList(names)
	}
	panic("unreachable shape kind")
}

func pad2(n int) string { return fmt.Sprintf("%02d", n) }

func explainCron(expression string) (string, error) {
	p, err := parseCron(expression)
	if err != nil {
		return "", err
	}
	f := p.fields
	minute, hour := f[minuteField].shape, f[hourField].shape
	clock := func(h, m int) string { return pad2(h) + ":" + pad2(m) }
	var timeText string
	switch {
	case minute.kind == shapeAll && hour.kind == shapeAll:
		timeText = "Every minute"
	case minute.kind == shapeStep && minute.from == 0 && minute.to == 59 && hour.kind == shapeAll:
		timeText = fmt.Sprintf("Every %d minutes", minute.step)
	case minute.kind == shapeSingle && hour.kind == shapeSingle:
		timeText = "At " + clock(hour.at, minute.at)
	case minute.kind == shapeSingle && hour.kind == shapeList:
		items := make([]string, len(hour.values))
		for i, h := range hour.values {
			items[i] = clock(h, minute.at)
		}
		timeText = "At " + joinList(items)
	case minute.kind == shapeList && hour.kind == shapeSingle:
		items := make([]string, len(minute.values))
		for i, m := range minute.values {
			items[i] = clock(hour.at, m)
		}
		timeText = "At " + joinList(items)
	case minute.kind == shapeAll:
		timeText = "Every minute of " + describeField(f[hourField], hourField, "hour")
	default:
		timeText = "At " + describeField(f[minuteField], minuteField, "minute") + " past " + describeField(f[hourField], hourField, "hour")
	}
	parts := []string{timeText}
	dom, dow := "", ""
	if !f[dayOfMonthField].wildcard {
		dom = "on " + describeField(f[dayOfMonthField], dayOfMonthField, "day-of-month")
	}
	if !f[dayOfWeekField].wildcard {
		dow = "on " + describeField(f[dayOfWeekField], dayOfWeekField, "day-of-week")
	}
	switch {
	case dom != "" && dow != "":
		parts = append(parts, dom+" or "+dow)
	case dom != "":
		parts = append(parts, dom)
	case dow != "":
		parts = append(parts, dow)
	}
	if !f[monthField].wildcard {
		parts = append(parts, "in "+describeField(f[monthField], monthField, "month"))
	}
	return strings.Join(parts, " ") + ".", nil
}
