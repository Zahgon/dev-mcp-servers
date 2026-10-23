package main

// Upcoming runs in a time zone, computed as the TypeScript server does with
// Intl.DateTimeFormat#formatToParts: proleptic Gregorian local fields, era years
// (1 BC is year 1), and Date.UTC arithmetic on those fields.

import (
	"errors"
	"math"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	maxRuns       = 100
	maxIterations = 200000
	horizonMs     = 10 * 365.25 * msPerDay
	minuteMs      = 60000.0
)

// errInvalidTime is the RangeError Intl throws when asked to format an invalid Date.
var errInvalidTime = errors.New("Invalid time value")

type localParts struct {
	year, month, day, hour, minute, weekday int
}

func partsAt(ms float64, loc *time.Location) (localParts, error) {
	if !validTime(ms) {
		return localParts{}, errInvalidTime
	}
	t := time.UnixMilli(int64(ms)).In(loc)
	year := t.Year()
	if year <= 0 {
		year = 1 - year
	}
	return localParts{year: year, month: int(t.Month()), day: t.Day(), hour: t.Hour(), minute: t.Minute(), weekday: int(t.Weekday())}, nil
}

func wallMs(p localParts) float64 {
	return dateUTC(float64(p.year), float64(p.month-1), float64(p.day), float64(p.hour), float64(p.minute))
}

// wallToUtc is the UTC instant at which the wall-clock time occurs (approximate in gaps).
func wallToUtc(wall float64, loc *time.Location) (float64, error) {
	offsetAt := func(t float64) (float64, error) {
		p, err := partsAt(t, loc)
		if err != nil {
			return 0, err
		}
		return wallMs(p) - t, nil
	}
	first, err := offsetAt(wall)
	if err != nil {
		return 0, err
	}
	second, err := offsetAt(wall - first)
	if err != nil {
		return 0, err
	}
	return wall - second, nil
}

func dayMatches(p localParts, c *parsedCron) bool {
	dom, dow := c.fields[dayOfMonthField], c.fields[dayOfWeekField]
	domOK, dowOK := dom.has(p.day), dow.has(p.weekday)
	switch {
	case dom.wildcard && dow.wildcard:
		return true
	case dom.wildcard:
		return dowOK
	case dow.wildcard:
		return domOK
	}
	return domOK || dowOK
}

// nextRuns lists up to count run instants strictly after fromMs, as UTC milliseconds.
func nextRuns(c *parsedCron, fromMs float64, loc *time.Location, count int) ([]float64, error) {
	out := []float64{}
	f := c.fields
	t := math.Floor(fromMs/minuteMs)*minuteMs + minuteMs
	horizon := fromMs + horizonMs
	for iter := 0; iter < maxIterations && len(out) < count; iter++ {
		if t > horizon {
			break
		}
		p, err := partsAt(t, loc)
		if err != nil {
			return nil, err
		}
		advance := func(wall float64) error {
			u, err := wallToUtc(wall, loc)
			if err != nil {
				return err
			}
			t = math.Max(u, t+minuteMs)
			return nil
		}
		switch {
		case !f[monthField].has(p.month):
			err = advance(dateUTC(float64(p.year), float64(p.month), 1, 0, 0))
		case !dayMatches(p, c):
			err = advance(dateUTC(float64(p.year), float64(p.month-1), float64(p.day+1), 0, 0))
		case !f[hourField].has(p.hour):
			// Step to the next hour boundary in UTC; this visits both occurrences of an
			// hour that repeats when clocks fall back.
			t = t - float64(p.minute)*minuteMs + 3600000
		case !f[minuteField].has(p.minute):
			t += minuteMs
		default:
			out = append(out, t)
			t += minuteMs
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// jsRound is Math.round.
func jsRound(x float64) float64 {
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

func padNumber(n float64) string {
	s := jsv.FormatNumber(n)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

func formatLocal(ms float64, loc *time.Location) (string, error) {
	p, err := partsAt(ms, loc)
	if err != nil {
		return "", err
	}
	offsetMin := jsRound((wallMs(p) - ms) / minuteMs)
	sign := "+"
	if offsetMin < 0 {
		sign = "-"
	}
	abs := math.Abs(offsetMin)
	return jsv.FormatNumber(float64(p.year)) + "-" + padNumber(float64(p.month)) + "-" + padNumber(float64(p.day)) +
		"T" + padNumber(float64(p.hour)) + ":" + padNumber(float64(p.minute)) + ":00" +
		sign + padNumber(math.Floor(abs/60)) + ":" + padNumber(math.Mod(abs, 60)), nil
}
