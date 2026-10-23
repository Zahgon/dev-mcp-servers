package ajv

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// formatDef is one ajv-formats "full" mode format: a string or number validator and,
// for the date and time formats, the compare function formatMaximum and friends use.
type formatDef struct {
	number   bool
	validate func(any) bool
	compare  func(a, b string) (float64, bool)
	ignore   bool
}

func jsRegex(src string, fold bool) *regexp.Regexp {
	goSrc, _, err := translateRegex(src, false)
	if err != nil {
		panic(err)
	}
	if fold {
		goSrc = "(?i)" + goSrc
	}
	return regexp.MustCompile(goSrc)
}

func strFormat(test func(string) bool) func(any) bool {
	return func(v any) bool {
		s, ok := v.(string)
		return !ok || test(s)
	}
}

func reFormat(re *regexp.Regexp) func(any) bool { return strFormat(re.MatchString) }

var (
	dateRe     = regexp.MustCompile(`^(\d\d\d\d)-(\d\d)-(\d\d)$`)
	timeRe     = regexp.MustCompile(`(?i)^(\d\d):(\d\d):(\d\d(?:\.\d+)?)(z|([+-])(\d\d)(?::?(\d\d))?)?$`)
	durationRe = regexp.MustCompile(`^P(?:(?:\d+Y)?(?:\d+M)?(?:\d+D)?(?:T(?:\d+H(?:\d+M)?(?:\d+S)?|\d+M(?:\d+S)?|\d+S))?|(?:\d+W)?)$`)
	uriFull    = jsRegex(`^(?:[a-z][a-z0-9+\-.]*:)(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)(?:\?(?:[a-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$`, true)
	uriRefRe   = jsRegex(`^(?:[a-z][a-z0-9+\-.]*:)?(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'"()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?(?:\?(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?$`, true)
	uriTplRe   = jsRegex(`^(?:(?:[^\x00-\x20"'<>%\\^`+"`"+`{|}]|%[0-9a-f]{2})|\{[+#./;?&=,!@|]?(?:[a-z0-9_]|%[0-9a-f]{2})+(?::[1-9][0-9]{0,3}|\*)?(?:,(?:[a-z0-9_]|%[0-9a-f]{2})+(?::[1-9][0-9]{0,3}|\*)?)*\})*$`, true)
	urlRe      = regexp.MustCompile(`(?i)^(?:https?|ftp)://(?:[^` + jsSpaceClass + `]+(?::[^` + jsSpaceClass + `]*)?@)?(?:((?:[1-9]\d?|1\d\d|2[01]\d|22[0-3])(?:\.(?:1?\d{1,2}|2[0-4]\d|25[0-5])){2}(?:\.(?:[1-9]\d?|1\d\d|2[0-4]\d|25[0-4])))|(?:(?:[a-z0-9\x{a1}-\x{ffff}]+-)*[a-z0-9\x{a1}-\x{ffff}]+)(?:\.(?:[a-z0-9\x{a1}-\x{ffff}]+-)*[a-z0-9\x{a1}-\x{ffff}]+)*(?:\.(?:[a-z\x{a1}-\x{ffff}]{2,})))(?::\d{2,5})?(?:/[^` + jsSpaceClass + `]*)?$`)
	urlPrivate = regexp.MustCompile(`^(?:(?:10|127)(?:\.\d{1,3}){3}|(?:169\.254|192\.168)(?:\.\d{1,3}){2}|172\.(?:1[6-9]|2\d|3[0-1])(?:\.\d{1,3}){2})`)
	emailRe    = jsRegex(`^[a-z0-9!#$%&'*+/=?^_`+"`"+`{|}~-]+(?:\.[a-z0-9!#$%&'*+/=?^_`+"`"+`{|}~-]+)*@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`, true)
	hostRe     = jsRegex(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[-0-9a-z]{0,61}[0-9a-z])?)*\.?$`, true)
	ipv4Fmt    = jsRegex(`^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$`, false)
	ipv6Fmt    = jsRegex(`^((([0-9a-f]{1,4}:){7}([0-9a-f]{1,4}|:))|(([0-9a-f]{1,4}:){6}(:[0-9a-f]{1,4}|((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|(([0-9a-f]{1,4}:){5}(((:[0-9a-f]{1,4}){1,2})|:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|(([0-9a-f]{1,4}:){4}(((:[0-9a-f]{1,4}){1,3})|((:[0-9a-f]{1,4})?:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){3}(((:[0-9a-f]{1,4}){1,4})|((:[0-9a-f]{1,4}){0,2}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){2}(((:[0-9a-f]{1,4}){1,5})|((:[0-9a-f]{1,4}){0,3}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){1}(((:[0-9a-f]{1,4}){1,6})|((:[0-9a-f]{1,4}){0,4}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(:(((:[0-9a-f]{1,4}){1,7})|((:[0-9a-f]{1,4}){0,5}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:)))$`, true)
	uuidFmt    = jsRegex(`^(?:urn:uuid:)?[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$`, true)
	jptrRe     = jsRegex(`^(?:\/(?:[^~/]|~0|~1)*)*$`, false)
	jptrFragRe = jsRegex(`^#(?:\/(?:[a-z0-9_\-.!$&'()*+,;:=@]|%[0-9a-f]{2}|~0|~1)*)*$`, true)
	relJptrRe  = jsRegex(`^(?:0|[1-9][0-9]*)(?:#|(?:\/(?:[^~/]|~0|~1)*)*)$`, false)
	byteLineRe = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`)
	zAnchorRe  = regexp.MustCompile(`[^\\]\\Z`)
)

var daysInMonth = []int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func validDate(s string) bool {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if mo < 1 || mo > 12 || d < 1 {
		return false
	}
	limit := daysInMonth[mo]
	if mo == 2 && isLeap(y) {
		limit = 29
	}
	return d <= limit
}

func validTime(strictTZ bool) func(string) bool {
	return func(s string) bool {
		m := timeRe.FindStringSubmatch(s)
		if m == nil {
			return false
		}
		hr, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		sec, _ := strconv.ParseFloat(m[3], 64)
		sign := 1
		if m[5] == "-" {
			sign = -1
		}
		tzH, _ := strconv.Atoi(m[6])
		tzM, _ := strconv.Atoi(m[7])
		if tzH > 23 || tzM > 59 || (strictTZ && m[4] == "") {
			return false
		}
		if hr <= 23 && mi <= 59 && sec < 60 {
			return true
		}
		utcMin := mi - tzM*sign
		utcHr := hr - tzH*sign
		if utcMin < 0 {
			utcHr--
		}
		return (utcHr == 23 || utcHr == -1) && (utcMin == 59 || utcMin == -1) && sec < 61
	}
}

const jsSpaceRunes = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

func splitDT(s string) []string {
	var parts []string
	start := 0
	for i, r := range s {
		if r == 't' || r == 'T' || strings.ContainsRune(jsSpaceRunes, r) {
			parts = append(parts, s[start:i])
			start = i + utf8.RuneLen(r)
		}
	}
	return append(parts, s[start:])
}

func validDateTime(strictTZ bool) func(string) bool {
	tm := validTime(strictTZ)
	return func(s string) bool {
		p := splitDT(s)
		return len(p) == 2 && validDate(p[0]) && tm(p[1])
	}
}

func validDuration(s string) bool { return s != "P" && durationRe.MatchString(s) }

func validURI(s string) bool { return strings.ContainsAny(s, "/:") && uriFull.MatchString(s) }

func validURL(s string) bool {
	m := urlRe.FindStringSubmatch(s)
	return m != nil && (m[1] == "" || !urlPrivate.MatchString(m[1]))
}

func hasLineTerminator(s string) bool { return strings.ContainsAny(s, "\n\r\u2028\u2029") }

func validHostname(s string) bool {
	n := len([]rune(s))
	okLen := !hasLineTerminator(s) && ((n >= 1 && n <= 253) || (n == 254 && strings.HasSuffix(s, ".")))
	return okLen && hostRe.MatchString(s)
}

func validRegexFormat(s string) bool { return !zAnchorRe.MatchString(s) && validJSRegex(s) }

func validByte(s string) bool {
	start := 0
	for i, r := range s {
		if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 {
			if byteLineRe.MatchString(s[start:i]) {
				return true
			}
			start = i + utf8.RuneLen(r)
		}
	}
	return byteLineRe.MatchString(s[start:])
}

func numFormat(test func(float64) bool) func(any) bool {
	return func(v any) bool {
		f, ok := v.(float64)
		return !ok || test(f)
	}
}

func isInteger(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) }

func cmpStrings(a, b string) (float64, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	switch {
	case a > b:
		return 1, true
	case a < b:
		return -1, true
	}
	return 0, true
}

var isoDateTimeRe = regexp.MustCompile(`^(\d{4})-(\d\d)-(\d\d)(?:T(\d\d):(\d\d)(?::(\d\d)(?:\.(\d+))?)?(Z|[+-]\d\d:\d\d)?)?$`)

// jsDateValue is new Date(s).valueOf() for ISO 8601 strings; others are NaN.
func jsDateValue(s string) (float64, bool) {
	m := isoDateTimeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	num := func(x string) int { n, _ := strconv.Atoi(x); return n }
	loc := time.UTC
	if m[4] != "" && m[8] == "" {
		loc = time.Local
	}
	ms := 0
	if m[7] != "" {
		frac := (m[7] + "00")[:3]
		ms = num(frac)
	}
	mo, d, h, mi, sec := num(m[2]), num(m[3]), num(m[4]), num(m[5]), num(m[6])
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 24 || mi > 59 || sec > 59 || (h == 24 && (mi > 0 || sec > 0 || ms > 0)) {
		return 0, false
	}
	t := time.Date(num(m[1]), time.Month(mo), d, h, mi, sec, ms*1e6, loc)
	if t.Day() != d && h != 24 {
		return 0, false
	}
	v := float64(t.UnixMilli())
	if len(m[8]) == 6 {
		sign := 1.0
		if m[8][0] == '-' {
			sign = -1
		}
		v -= sign * float64(num(m[8][1:3])*3600000+num(m[8][4:6])*60000)
	}
	return v, true
}

func cmpDates(a, b string) (float64, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	x, ok1 := jsDateValue(a)
	y, ok2 := jsDateValue(b)
	if !ok1 || !ok2 || x == 0 || y == 0 {
		return 0, false
	}
	return x - y, true
}

func cmpTime(a, b string) (float64, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	return cmpDates("2020-01-01T"+a, "2020-01-01T"+b)
}

func cmpIsoTime(a, b string) (float64, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	x, y := timeRe.FindStringSubmatch(a), timeRe.FindStringSubmatch(b)
	if x == nil || y == nil {
		return 0, false
	}
	return cmpStrings(x[1]+x[2]+x[3], y[1]+y[2]+y[3])
}

func cmpIsoDateTime(a, b string) (float64, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	pa, pb := splitDT(a), splitDT(b)
	t1, t2 := "", ""
	if len(pa) > 1 {
		t1 = pa[1]
	}
	if len(pb) > 1 {
		t2 = pb[1]
	}
	r, ok := cmpStrings(pa[0], pb[0])
	if !ok {
		return 0, false
	}
	if r != 0 {
		return r, true
	}
	return cmpTime(t1, t2)
}

func fullFormats() map[string]*formatDef {
	s := func(f func(string) bool) *formatDef { return &formatDef{validate: strFormat(f)} }
	r := func(re *regexp.Regexp) *formatDef { return &formatDef{validate: reFormat(re)} }
	withCmp := func(f func(string) bool, c func(a, b string) (float64, bool)) *formatDef {
		return &formatDef{validate: strFormat(f), compare: c}
	}
	return map[string]*formatDef{
		"date":                      withCmp(validDate, cmpStrings),
		"time":                      withCmp(validTime(true), cmpTime),
		"date-time":                 withCmp(validDateTime(true), cmpDates),
		"iso-time":                  withCmp(validTime(false), cmpIsoTime),
		"iso-date-time":             withCmp(validDateTime(false), cmpIsoDateTime),
		"duration":                  s(validDuration),
		"uri":                       s(validURI),
		"uri-reference":             r(uriRefRe),
		"uri-template":              r(uriTplRe),
		"url":                       s(validURL),
		"email":                     r(emailRe),
		"hostname":                  s(validHostname),
		"ipv4":                      r(ipv4Fmt),
		"ipv6":                      r(ipv6Fmt),
		"regex":                     s(validRegexFormat),
		"uuid":                      r(uuidFmt),
		"json-pointer":              r(jptrRe),
		"json-pointer-uri-fragment": r(jptrFragRe),
		"relative-json-pointer":     r(relJptrRe),
		"byte":                      s(validByte),
		"int32":                     {number: true, validate: numFormat(func(f float64) bool { return isInteger(f) && f <= math.MaxInt32 && f >= math.MinInt32 })},
		"int64":                     {number: true, validate: numFormat(isInteger)},
		"float":                     {number: true, validate: numFormat(func(float64) bool { return true })},
		"double":                    {number: true, validate: numFormat(func(float64) bool { return true })},
		"password":                  {ignore: true},
		"binary":                    {ignore: true},
	}
}
