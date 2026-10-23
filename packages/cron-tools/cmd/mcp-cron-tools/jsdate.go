package main

// The parts of the JavaScript Date object the server relies on: MakeDay, MakeDate,
// TimeClip, Date.UTC, Date.prototype.toISOString and the local-time conversion that
// Date.parse applies to date-times written without an offset.

import (
	"fmt"
	"math"
	"os"
	"time"
)

var daysBeforeMonth = [2][12]int{
	{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334},
	{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335},
}

// makeDay is V8's MakeDay: the day number of year, month (0-based, may overflow) and date.
func makeDay(year, month, date float64) float64 {
	if !(year >= -1000000 && year <= 1000000 && month >= -10000000 && month <= 10000000 && !math.IsInf(date, 0) && !math.IsNaN(date)) {
		return math.NaN()
	}
	y, m := int(year), int(month)
	y += m / 12
	m %= 12
	if m < 0 {
		m += 12
		y--
	}
	const delta = 399999
	base := 365*(1970+delta) + (1970+delta)/4 - (1970+delta)/100 + (1970+delta)/400
	dayFromYear := 365*(y+delta) + (y+delta)/4 - (y+delta)/100 + (y+delta)/400 - base
	leap := 0
	if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
		leap = 1
	}
	return float64(dayFromYear+daysBeforeMonth[leap][m]) + date - 1
}

func makeDate(day, ms float64) float64 {
	if math.IsInf(day, 0) || math.IsNaN(day) || math.IsInf(ms, 0) || math.IsNaN(ms) {
		return math.NaN()
	}
	return day*msPerDay + ms
}

// timeClip is TimeClip: NaN outside the representable range, otherwise truncated.
func timeClip(t float64) float64 {
	if math.IsNaN(t) || math.Abs(t) > maxTimeMs {
		return math.NaN()
	}
	return math.Trunc(t) + 0
}

// dateUTC is Date.UTC(year, month, day, hour, minute) for whole-number arguments.
func dateUTC(year, month, day, hour, minute float64) float64 {
	if !math.IsNaN(year) {
		if y := math.Trunc(year); y >= 0 && y <= 99 {
			year = 1900 + y
		}
	}
	return timeClip(makeDate(makeDay(year, month, day), hour*3600000+minute*60000))
}

func validTime(ms float64) bool { return !math.IsNaN(ms) && math.Abs(ms) <= maxTimeMs }

// isoString is Date.prototype.toISOString for a valid time value.
func isoString(ms float64) string {
	t := time.UnixMilli(int64(ms)).UTC()
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
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", year, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e6)
}

// localZone is the process time zone: TZ when it names a zone Intl knows, else the host's.
func localZone() *time.Location {
	if name := os.Getenv("TZ"); name != "" {
		if loc, ok := lookupZone(name); ok {
			return loc
		}
	}
	return time.Local
}

func offsetMs(loc *time.Location, ms float64) float64 {
	_, off := time.UnixMilli(int64(ms)).In(loc).Zone()
	return float64(off) * 1000
}

// localToUTC converts a local wall-clock time value to UTC the way ICU's
// getOffsetFromLocal does with the "former" policy: a time inside a gap uses the
// offset before the transition, a repeated time takes its first occurrence.
func localToUTC(local float64) float64 {
	loc := localZone()
	before := offsetMs(loc, local-msPerDay)
	after := offsetMs(loc, local+msPerDay)
	if offsetMs(loc, local-before) == before {
		return local - before
	}
	if offsetMs(loc, local-after) == after {
		return local - after
	}
	return local - before
}
