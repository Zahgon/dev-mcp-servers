package main

// String.prototype.localeCompare with the default locale: the ICU root collation at
// tertiary strength. Weights for the Latin, Greek, Cyrillic, Armenian, punctuation,
// symbol, fullwidth and common emoji blocks come from collationTable; other characters
// (CJK ideographs, other scripts, unpaired surrogates) sort after them by code point.

type collationElement struct {
	primary, secondary, tertiary uint32
}

const implicitPrimary = 1 << 16

func collationElements(s string) []collationElement {
	var out []collationElement
	units := []rune(s)
	for i := 0; i < len(units); i++ {
		r := units[i]
		if r >= 0xD800+surrogateRune && r <= 0xDFFF+surrogateRune {
			r -= surrogateRune
			if r <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00+surrogateRune && units[i+1] <= 0xDFFF+surrogateRune {
				r = 0x10000 + (r-0xD800)<<10 + (units[i+1] - surrogateRune - 0xDC00)
				i++
			}
		}
		if elems, known := collationTable[r]; known {
			out = append(out, elems...)
			continue
		}
		out = append(out, collationElement{primary: implicitPrimary + uint32(r), secondary: 1, tertiary: 1})
	}
	return out
}

func compareLevel(a, b []collationElement, weight func(collationElement) uint32) int {
	i, j := 0, 0
	for {
		for i < len(a) && weight(a[i]) == 0 {
			i++
		}
		for j < len(b) && weight(b[j]) == 0 {
			j++
		}
		switch {
		case i == len(a) && j == len(b):
			return 0
		case i == len(a):
			return -1
		case j == len(b):
			return 1
		}
		if x, y := weight(a[i]), weight(b[j]); x != y {
			if x < y {
				return -1
			}
			return 1
		}
		i++
		j++
	}
}

// localeCompare compares two unit-form strings as a.localeCompare(b) does.
func localeCompare(a, b string) int {
	x, y := collationElements(a), collationElements(b)
	for _, weight := range []func(collationElement) uint32{
		func(e collationElement) uint32 { return e.primary },
		func(e collationElement) uint32 { return e.secondary },
		func(e collationElement) uint32 { return e.tertiary },
	} {
		if c := compareLevel(x, y, weight); c != 0 {
			return c
		}
	}
	return 0
}
