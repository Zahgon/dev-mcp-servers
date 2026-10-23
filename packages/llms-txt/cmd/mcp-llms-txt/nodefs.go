package main

// Node.js file-system behaviour the server relies on: path.resolve against the process
// working directory, fs error messages as Node words them, and UTF-8 decoding with the
// WHATWG replacement rules that Buffer#toString uses.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

// resolvePath is path.resolve(p) on POSIX: relative paths are joined to the working
// directory as getcwd(3) reports it, then normalised.
func resolvePath(p string) string {
	if strings.HasPrefix(p, "/") {
		return filepath.Clean(p)
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		cwd = "/"
	}
	return filepath.Clean(cwd + "/" + p)
}

// fsPath is the byte path Node hands to the operating system for a JavaScript string:
// each unpaired surrogate becomes U+FFFD.
func fsPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); {
		r, n := jsv.DecodeWTF8(p, i)
		if r >= 0xD800 && r <= 0xDFFF {
			r = utf8.RuneError
		}
		b.WriteRune(r)
		i += n
	}
	return b.String()
}

var errnoText = map[syscall.Errno][2]string{
	syscall.ENOENT:       {"ENOENT", "no such file or directory"},
	syscall.EACCES:       {"EACCES", "permission denied"},
	syscall.EPERM:        {"EPERM", "operation not permitted"},
	syscall.ENOTDIR:      {"ENOTDIR", "not a directory"},
	syscall.EISDIR:       {"EISDIR", "illegal operation on a directory"},
	syscall.ELOOP:        {"ELOOP", "too many symbolic links encountered"},
	syscall.ENAMETOOLONG: {"ENAMETOOLONG", "name too long"},
	syscall.EMFILE:       {"EMFILE", "too many open files"},
	syscall.EIO:          {"EIO", "i/o error"},
	syscall.EINVAL:       {"EINVAL", "invalid argument"},
	syscall.ENXIO:        {"ENXIO", "no such device or address"},
	syscall.EBUSY:        {"EBUSY", "resource busy or locked"},
}

// nodeFSMessage is err.message of the error Node's fs promise API rejects with when the
// operation named by syscallName fails on path (the JavaScript string the caller passed).
func nodeFSMessage(err error, syscallName, path string) string {
	if strings.ContainsRune(path, 0) {
		return "The argument 'path' must be a string, Uint8Array, or URL without null bytes. Received " + inspectString(path)
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err.Error()
	}
	text, known := errnoText[errno]
	if !known {
		text = [2]string{"UNKNOWN", errno.Error()}
	}
	if errno == syscall.EISDIR && syscallName == "read" {
		return fmt.Sprintf("%s: %s, read", text[0], text[1])
	}
	return fmt.Sprintf("%s: %s, %s '%s'", text[0], text[1], syscallName, path)
}

// inspectString is util.inspect for a string, as Node prints it in argument errors.
func inspectString(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch {
		case r == '\'':
			b.WriteString(`\'`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// statFile is fs.promises.stat; the message of a failure is Node's.
func statFile(path string) (fs.FileInfo, error) {
	st, err := os.Stat(fsPath(path))
	if err != nil {
		return nil, errors.New(nodeFSMessage(err, "stat", path))
	}
	return st, nil
}

// readUTF8 is fs.promises.readFile(path, "utf8").
func readUTF8(path string) (string, error) {
	data, err := os.ReadFile(fsPath(path))
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) && pe.Op == "read" {
			return "", errors.New(nodeFSMessage(err, "read", path))
		}
		return "", errors.New(nodeFSMessage(err, "open", path))
	}
	return decodeUTF8(data), nil
}

// decodeUTF8 decodes bytes as the WHATWG UTF-8 decoder does: each maximal ill-formed
// subsequence becomes one U+FFFD and a byte order mark is kept.
func decodeUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	for i := 0; i < len(data); {
		r, n := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || n > 1 {
			b.WriteRune(r)
			i += n
			continue
		}
		b.WriteRune(utf8.RuneError)
		i += maximalSubpart(data[i:])
	}
	return b.String()
}

// maximalSubpart is the length of the longest prefix of b that starts a well-formed
// sequence, at least one byte.
func maximalSubpart(b []byte) int {
	lead := b[0]
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 1
	case lead >= 0xE0 && lead <= 0xEF:
		need = 2
		if lead == 0xE0 {
			lo = 0xA0
		} else if lead == 0xED {
			hi = 0x9F
		}
	case lead >= 0xF0 && lead <= 0xF4:
		need = 3
		if lead == 0xF0 {
			lo = 0x90
		} else if lead == 0xF4 {
			hi = 0x8F
		}
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(b); k++ {
		c := b[n]
		if c < lo || c > hi {
			break
		}
		lo, hi = 0x80, 0xBF
		n++
	}
	return n
}
