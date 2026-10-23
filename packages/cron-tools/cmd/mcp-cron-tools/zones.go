package main

// Time zones as Intl.DateTimeFormat in Node 22 (ICU 77.1, tz data 2025b) accepts them.
//
// zoneinfo.zip holds tzdata 2025b compiled with `zic -b fat` (africa antarctica asia
// australasia europe northamerica southamerica etcetera backward) plus icu-extra.zi,
// the IDs ICU knows beyond tzdata (the Java three-letter aliases, the SystemV zones and
// two removed tzdata links), packed with Go's lib/time/mkzip.go. Names match without
// regard to ASCII case, as ICU matches them. UTC offset strings (+05:30, -0800, +05,
// with "-" or U+2212) are fixed zones.

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

//go:embed zoneinfo.zip
var zoneinfoZip []byte

var zoneIndex = sync.OnceValue(func() map[string]*zip.File {
	r, err := zip.NewReader(bytes.NewReader(zoneinfoZip), int64(len(zoneinfoZip)))
	if err != nil {
		panic("embedded zoneinfo.zip is unreadable: " + err.Error())
	}
	index := make(map[string]*zip.File, len(r.File))
	for _, f := range r.File {
		index[asciiLower(f.Name)] = f
	}
	return index
})

var (
	zoneMu    sync.Mutex
	zoneCache = map[string]*time.Location{}
)

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// lookupZone resolves a time zone name the way Intl.DateTimeFormat validates timeZone.
func lookupZone(name string) (*time.Location, bool) {
	if loc, ok := offsetZone(name); ok {
		return loc, true
	}
	key := asciiLower(name)
	zoneMu.Lock()
	defer zoneMu.Unlock()
	if loc, ok := zoneCache[key]; ok {
		return loc, true
	}
	f, ok := zoneIndex()[key]
	if !ok {
		return nil, false
	}
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, false
	}
	loc, err := time.LoadLocationFromTZData(f.Name, data)
	if err != nil {
		return nil, false
	}
	zoneCache[key] = loc
	return loc, true
}

// offsetZone parses a UTC offset time zone: a sign, two hour digits (00-23) and
// optionally two minute digits (00-59), with or without a colon.
func offsetZone(name string) (*time.Location, bool) {
	units := jsv.UTF16Units(name)
	if len(units) < 3 {
		return nil, false
	}
	sign := 0
	switch units[0] {
	case '+':
		sign = 1
	case '-', 0x2212:
		sign = -1
	default:
		return nil, false
	}
	digits := units[1:]
	if len(digits) == 5 && digits[2] == ':' {
		digits = append(append([]uint16{}, digits[:2]...), digits[3:]...)
	}
	if len(digits) != 2 && len(digits) != 4 {
		return nil, false
	}
	value := make([]int, len(digits))
	for i, d := range digits {
		if d < '0' || d > '9' {
			return nil, false
		}
		value[i] = int(d - '0')
	}
	hours, minutes := value[0]*10+value[1], 0
	if len(value) == 4 {
		minutes = value[2]*10 + value[3]
	}
	if hours > 23 || minutes > 59 {
		return nil, false
	}
	return time.FixedZone(name, sign*(hours*3600+minutes*60)), true
}
