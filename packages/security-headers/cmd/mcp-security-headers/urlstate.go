package main

// The WHATWG basic URL parser state machine (no state override).

import (
	"strconv"
	"strings"
)

type urlState int

const (
	stSchemeStart urlState = iota
	stScheme
	stNoScheme
	stSpecialRelativeOrAuthority
	stPathOrAuthority
	stRelative
	stRelativeSlash
	stSpecialAuthoritySlashes
	stSpecialAuthorityIgnoreSlashes
	stAuthority
	stHost
	stPort
	stFile
	stFileSlash
	stFileHost
	stPathStart
	stPath
	stOpaquePath
	stQuery
	stFragment
)

const eof rune = -1

type urlParser struct {
	in   []rune
	base *webURL
	u    *webURL
	p    int
	buf  []rune
	seg  strings.Builder

	atSignSeen, insideBrackets, passwordTokenSeen bool
}

func (s *urlParser) at(i int) rune {
	if i >= 0 && i < len(s.in) {
		return s.in[i]
	}
	return eof
}

func (s *urlParser) remainingStartsWith(r rune) bool { return s.at(s.p+1) == r }

func (s *urlParser) copyAuthorityFromBase() {
	s.u.username, s.u.password, s.u.host, s.u.port = s.base.username, s.base.password, s.base.host, s.base.port
}

func (s *urlParser) run() error {
	state := stSchemeStart
	for s.p = 0; ; s.p++ {
		c := s.at(s.p)
		next, err := s.step(state, c)
		if err != nil {
			return err
		}
		state = next
		if s.p >= len(s.in) {
			return nil
		}
	}
}

func isEndOfAuthority(c rune, special bool) bool {
	return c == eof || c == '/' || c == '?' || c == '#' || (special && c == '\\')
}

// step processes one code point and returns the next state. It mirrors the specification text
// state by state; "decrease pointer by 1" is s.p-- before the loop's increment.
func (s *urlParser) step(state urlState, c rune) (urlState, error) {
	u := s.u
	switch state {
	case stSchemeStart:
		if isASCIIAlpha(c) {
			s.buf = append(s.buf, lowerASCII(c))
			return stScheme, nil
		}
		s.p--
		return stNoScheme, nil
	case stScheme:
		if isASCIIAlnum(c) || c == '+' || c == '-' || c == '.' {
			s.buf = append(s.buf, lowerASCII(c))
			return stScheme, nil
		}
		if c == ':' {
			u.scheme = string(s.buf)
			s.buf = nil
			switch {
			case u.scheme == "file":
				return stFile, nil
			case u.special() && s.base != nil && s.base.scheme == u.scheme:
				return stSpecialRelativeOrAuthority, nil
			case u.special():
				return stSpecialAuthoritySlashes, nil
			case s.remainingStartsWith('/'):
				s.p++
				return stPathOrAuthority, nil
			}
			u.opaque = strPtr("")
			return stOpaquePath, nil
		}
		s.buf = nil
		s.p = -1
		return stNoScheme, nil
	case stNoScheme:
		b := s.base
		if b == nil || (b.opaque != nil && c != '#') {
			return 0, errInvalidURL
		}
		if b.opaque != nil {
			u.scheme, u.opaque, u.query, u.fragment = b.scheme, b.opaque, b.query, strPtr("")
			return stFragment, nil
		}
		s.p--
		if b.scheme != "file" {
			return stRelative, nil
		}
		return stFile, nil
	case stSpecialRelativeOrAuthority:
		if c == '/' && s.remainingStartsWith('/') {
			s.p++
			return stSpecialAuthorityIgnoreSlashes, nil
		}
		s.p--
		return stRelative, nil
	case stPathOrAuthority:
		if c == '/' {
			return stAuthority, nil
		}
		s.p--
		return stPath, nil
	case stRelative:
		u.scheme = s.base.scheme
		if c == '/' || (u.special() && c == '\\') {
			return stRelativeSlash, nil
		}
		s.copyAuthorityFromBase()
		u.path = append([]string(nil), s.base.path...)
		u.query = s.base.query
		switch c {
		case '?':
			u.query = strPtr("")
			return stQuery, nil
		case '#':
			u.fragment = strPtr("")
			return stFragment, nil
		case eof:
			return stRelative, nil
		}
		u.query = nil
		u.shortenPath()
		s.p--
		return stPath, nil
	case stRelativeSlash:
		if u.special() && (c == '/' || c == '\\') {
			return stSpecialAuthorityIgnoreSlashes, nil
		}
		if c == '/' {
			return stAuthority, nil
		}
		s.copyAuthorityFromBase()
		s.p--
		return stPath, nil
	case stSpecialAuthoritySlashes:
		if c == '/' && s.remainingStartsWith('/') {
			s.p++
		} else {
			s.p--
		}
		return stSpecialAuthorityIgnoreSlashes, nil
	case stSpecialAuthorityIgnoreSlashes:
		if c != '/' && c != '\\' {
			s.p--
			return stAuthority, nil
		}
		return stSpecialAuthorityIgnoreSlashes, nil
	case stAuthority:
		return s.authority(c)
	case stHost:
		return s.hostState(c)
	case stPort:
		return s.portState(c)
	case stFile, stFileSlash, stFileHost:
		return s.fileStates(state, c)
	case stPathStart:
		switch {
		case u.special():
			if c != '/' && c != '\\' {
				s.p--
			}
			return stPath, nil
		case c == '?':
			u.query = strPtr("")
			return stQuery, nil
		case c == '#':
			u.fragment = strPtr("")
			return stFragment, nil
		case c != eof:
			if c != '/' {
				s.p--
			}
			return stPath, nil
		}
		return stPathStart, nil
	case stPath:
		return s.pathState(c)
	case stOpaquePath:
		switch c {
		case '?':
			u.query = strPtr("")
			return stQuery, nil
		case '#':
			u.fragment = strPtr("")
			return stFragment, nil
		case eof:
			return stOpaquePath, nil
		}
		var b strings.Builder
		b.WriteString(*u.opaque)
		percentEncode(&b, c, c0Set)
		u.opaque = strPtr(b.String())
		return stOpaquePath, nil
	case stQuery:
		if c == eof || c == '#' {
			set := querySet
			if u.special() {
				set = specialQuerySet
			}
			var b strings.Builder
			b.WriteString(*u.query)
			for _, r := range s.buf {
				percentEncode(&b, r, set)
			}
			u.query = strPtr(b.String())
			s.buf = nil
			if c == '#' {
				u.fragment = strPtr("")
				return stFragment, nil
			}
			return stQuery, nil
		}
		s.buf = append(s.buf, c)
		return stQuery, nil
	case stFragment:
		if c != eof {
			var b strings.Builder
			b.WriteString(*u.fragment)
			percentEncode(&b, c, fragmentSet)
			u.fragment = strPtr(b.String())
		}
		return stFragment, nil
	}
	return state, nil
}

func (s *urlParser) authority(c rune) (urlState, error) {
	u := s.u
	if c == '@' {
		if s.atSignSeen {
			s.buf = append([]rune("%40"), s.buf...)
		}
		s.atSignSeen = true
		var user, pass strings.Builder
		user.WriteString(u.username)
		pass.WriteString(u.password)
		for _, r := range s.buf {
			if r == ':' && !s.passwordTokenSeen {
				s.passwordTokenSeen = true
				continue
			}
			if s.passwordTokenSeen {
				percentEncode(&pass, r, userinfoSet)
			} else {
				percentEncode(&user, r, userinfoSet)
			}
		}
		u.username, u.password = user.String(), pass.String()
		s.buf = nil
		return stAuthority, nil
	}
	if isEndOfAuthority(c, u.special()) {
		if s.atSignSeen && len(s.buf) == 0 {
			return 0, errInvalidURL
		}
		s.p -= len(s.buf) + 1
		s.buf = nil
		return stHost, nil
	}
	s.buf = append(s.buf, c)
	return stAuthority, nil
}

func (s *urlParser) hostState(c rune) (urlState, error) {
	u := s.u
	if c == ':' && !s.insideBrackets {
		if len(s.buf) == 0 {
			return 0, errInvalidURL
		}
		host, err := parseHost(s.buf, !u.special())
		if err != nil {
			return 0, err
		}
		u.host, s.buf = &host, nil
		return stPort, nil
	}
	if isEndOfAuthority(c, u.special()) {
		s.p--
		if u.special() && len(s.buf) == 0 {
			return 0, errInvalidURL
		}
		host, err := parseHost(s.buf, !u.special())
		if err != nil {
			return 0, err
		}
		u.host, s.buf = &host, nil
		return stPathStart, nil
	}
	if c == '[' {
		s.insideBrackets = true
	} else if c == ']' {
		s.insideBrackets = false
	}
	s.buf = append(s.buf, c)
	return stHost, nil
}

func (s *urlParser) portState(c rune) (urlState, error) {
	u := s.u
	if isASCIIDigit(c) {
		s.buf = append(s.buf, c)
		return stPort, nil
	}
	if !isEndOfAuthority(c, u.special()) {
		return 0, errInvalidURL
	}
	if len(s.buf) > 0 {
		port := 0
		for _, d := range s.buf {
			port = port*10 + int(d-'0')
			if port > 65535 {
				return 0, errInvalidURL
			}
		}
		u.port = strconv.Itoa(port)
		if def, ok := specialPorts[u.scheme]; ok && def == u.port {
			u.port = ""
		}
		s.buf = nil
	}
	s.p--
	return stPathStart, nil
}

func (s *urlParser) fileStates(state urlState, c rune) (urlState, error) {
	u, b := s.u, s.base
	switch state {
	case stFile:
		u.scheme, u.host = "file", strPtr("")
		if c == '/' || c == '\\' {
			return stFileSlash, nil
		}
		if b == nil || b.scheme != "file" {
			s.p--
			return stPath, nil
		}
		u.host, u.path, u.query = b.host, append([]string(nil), b.path...), b.query
		switch c {
		case '?':
			u.query = strPtr("")
			return stQuery, nil
		case '#':
			u.fragment = strPtr("")
			return stFragment, nil
		case eof:
			return stFile, nil
		}
		u.query = nil
		if startsWithDriveLetter(s.in[s.p:]) {
			u.path = nil
		} else {
			u.shortenPath()
		}
		s.p--
		return stPath, nil
	case stFileSlash:
		if c == '/' || c == '\\' {
			return stFileHost, nil
		}
		if b != nil && b.scheme == "file" {
			u.host = b.host
			if !startsWithDriveLetter(s.in[s.p:]) && len(b.path) > 0 && isWindowsDriveLetter([]rune(b.path[0]), true) {
				u.path = append(u.path, b.path[0])
			}
		}
		s.p--
		return stPath, nil
	}
	if c == eof || c == '/' || c == '\\' || c == '?' || c == '#' {
		s.p--
		if isWindowsDriveLetter(s.buf, false) {
			// The path state goes on with the same buffer.
			s.seg.WriteString(string(s.buf))
			s.buf = nil
			return stPath, nil
		}
		if len(s.buf) == 0 {
			u.host = strPtr("")
			return stPathStart, nil
		}
		host, err := parseHost(s.buf, false)
		if err != nil {
			return 0, err
		}
		if host == "localhost" {
			host = ""
		}
		u.host, s.buf = &host, nil
		return stPathStart, nil
	}
	s.buf = append(s.buf, c)
	return stFileHost, nil
}

func (s *urlParser) pathState(c rune) (urlState, error) {
	u := s.u
	slash := c == '/' || (u.special() && c == '\\')
	if !(c == eof || slash || c == '?' || c == '#') {
		percentEncode(&s.seg, c, pathSet)
		return stPath, nil
	}
	seg := s.seg.String()
	s.seg.Reset()
	switch {
	case isDoubleDot(seg):
		u.shortenPath()
		if !slash {
			u.path = append(u.path, "")
		}
	case isSingleDot(seg):
		if !slash {
			u.path = append(u.path, "")
		}
	default:
		if u.scheme == "file" && len(u.path) == 0 && isWindowsDriveLetter([]rune(seg), false) {
			seg = seg[:1] + ":"
		}
		u.path = append(u.path, seg)
	}
	switch c {
	case '?':
		u.query = strPtr("")
		return stQuery, nil
	case '#':
		u.fragment = strPtr("")
		return stFragment, nil
	}
	return stPath, nil
}
