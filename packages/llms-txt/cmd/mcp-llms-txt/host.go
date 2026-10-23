package main

// Host and port validation of the WHATWG URL parser: userinfo, ports, IPv4 and IPv6
// addresses, forbidden code points and punycode labels.

import (
	"strconv"
	"strings"
)

// authority validates userinfo, host and port and returns the input after them.
func authority(in []rune, special bool) ([]rune, bool) {
	end := len(in)
	for i, r := range in {
		if r == '/' || r == '?' || r == '#' || (special && r == '\\') {
			end = i
			break
		}
	}
	auth := in[:end]
	at := -1
	for i, r := range auth {
		if r == '@' {
			at = i
		}
	}
	hostPort := auth[at+1:]
	if at >= 0 && len(hostPort) == 0 {
		return nil, false
	}
	inBrackets := false
	split := len(hostPort)
	for i, r := range hostPort {
		if r == '[' {
			inBrackets = true
		} else if r == ']' {
			inBrackets = false
		} else if r == ':' && !inBrackets {
			split = i
			break
		}
	}
	host := string(hostPort[:split])
	if split < len(hostPort) {
		if host == "" {
			return nil, false
		}
		port := string(hostPort[split+1:])
		for _, r := range port {
			if r < '0' || r > '9' {
				return nil, false
			}
		}
		if port != "" {
			if n, err := strconv.Atoi(strings.TrimLeft(port, "0")); len(strings.TrimLeft(port, "0")) > 5 || (err == nil && n > 65535) {
				return nil, false
			}
		}
	}
	if !validHost(host, special) {
		return nil, false
	}
	return in[end:], true
}

func validHost(host string, special bool) bool {
	if strings.HasPrefix(host, "[") {
		return strings.HasSuffix(host, "]") && validIPv6(host[1:len(host)-1])
	}
	if !special {
		return !strings.ContainsAny(host, "\x00\t\n\r #/:<>?@[\\]^|")
	}
	if host == "" {
		return false
	}
	domain, ok := domainToASCII(percentDecode(host))
	if !ok || domain == "" {
		return false
	}
	for _, r := range domain {
		if r <= 0x20 || r == 0x7f || strings.ContainsRune("#%/:<>?@[\\]^|", r) {
			return false
		}
	}
	if endsInNumber(domain) {
		return validIPv4(domain)
	}
	return true
}

func percentDecode(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			out = append(out, byte(v))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return decodeUTF8(out)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
