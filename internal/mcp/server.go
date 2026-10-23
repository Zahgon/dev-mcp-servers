// Package mcp serves Model Context Protocol tools over stdio the way the TypeScript SDK
// (@modelcontextprotocol/sdk 1.32) does: newline-delimited JSON-RPC 2.0 on stdin and stdout,
// strict message envelopes, and tool results with one text item.
package mcp

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

const (
	latestVersion      = "2025-11-25"
	codeMethodNotFound = -32601
	codeInternalError  = -32603
	maxSafeInteger     = 1<<53 - 1
)

var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05", "2024-10-07"}

// Result is a tool result: one text item, flagged as an error or not.
type Result struct {
	Text    string
	IsError bool
}

// OK is the result of a tool that succeeded with value: JSON.stringify(value, null, 1).
// A value that contains itself cannot be serialised and is a tool error, as in JavaScript.
func OK(v any) Result {
	if cyclic(v, map[any]bool{}) {
		return Fail("Converting circular structure to JSON")
	}
	return Result{Text: jsv.Stringify(v, 1)}
}

// Fail is a tool error result with the given message.
func Fail(msg string) Result { return Result{Text: msg, IsError: true} }

func cyclic(v any, open map[any]bool) bool {
	var key any
	var children []any
	switch x := v.(type) {
	case *jsv.Obj:
		key = x
		for _, k := range x.Keys() {
			children = append(children, jsv.Prop(x, k))
		}
	case []any:
		if len(x) == 0 {
			return false
		}
		key = &x[0]
		children = x
	default:
		return false
	}
	if open[key] {
		return true
	}
	open[key] = true
	defer delete(open, key)
	for _, c := range children {
		if cyclic(c, open) {
			return true
		}
	}
	return false
}

// Tool is one registered tool. Spec holds the tools/list entry without its name.
type Tool struct {
	Name    string
	Spec    *jsv.Obj
	Handler func(args *jsv.Obj) Result
}

// Server is one MCP server identity and its tools.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Tools        []Tool
}

type request struct {
	id     any
	method string
	params *jsv.Obj
}

// parseRequest applies the SDK's strict message schemas. It returns nil for every line
// that gets no reply: text that is not JSON, notifications, responses, and anything
// that is not a well-formed request.
func parseRequest(line string) *request {
	v, err := jsv.Parse(line)
	if err != nil {
		return nil
	}
	msg := jsv.AsObj(v)
	if msg == nil {
		return nil
	}
	for _, k := range msg.Keys() {
		switch k {
		case "jsonrpc", "id", "method", "params":
		default:
			return nil
		}
	}
	if jsv.Prop(msg, "jsonrpc") != "2.0" {
		return nil
	}
	method, ok := jsv.Prop(msg, "method").(string)
	if !ok {
		return nil
	}
	id, hasID := msg.Get("id")
	if !hasID {
		return nil
	}
	switch x := id.(type) {
	case string:
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > maxSafeInteger {
			return nil
		}
	default:
		return nil
	}
	params := jsv.NewObj()
	if raw, present := msg.Get("params"); present {
		params = jsv.AsObj(raw)
		if params == nil {
			return nil
		}
		if meta, has := params.Get("_meta"); has && !jsv.IsObject(meta) {
			return nil
		}
	}
	return &request{id: id, method: method, params: params}
}

func rpcError(code float64, message string) *jsv.Obj {
	return jsv.O("code", code, "message", message)
}

func invalidParams(method, why string) *jsv.Obj {
	return rpcError(codeInternalError, fmt.Sprintf("Invalid params for %s: %s", method, why))
}

func (s *Server) initialize(params *jsv.Obj) (any, *jsv.Obj) {
	requested, ok := jsv.Prop(params, "protocolVersion").(string)
	if !ok {
		return nil, invalidParams("initialize", "protocolVersion must be a string")
	}
	if !jsv.IsObject(jsv.Prop(params, "capabilities")) {
		return nil, invalidParams("initialize", "capabilities must be an object")
	}
	client := jsv.AsObj(jsv.Prop(params, "clientInfo"))
	if client == nil {
		return nil, invalidParams("initialize", "clientInfo must be an object")
	}
	for _, key := range []string{"name", "version"} {
		if _, isString := jsv.Prop(client, key).(string); !isString {
			return nil, invalidParams("initialize", "clientInfo."+key+" must be a string")
		}
	}
	version := latestVersion
	for _, v := range supportedVersions {
		if v == requested {
			version = requested
		}
	}
	return jsv.O(
		"protocolVersion", version,
		"capabilities", jsv.O("tools", jsv.O("listChanged", true)),
		"serverInfo", jsv.O("name", s.Name, "version", s.Version),
		"instructions", s.Instructions,
	), nil
}

func (s *Server) catalogue() []any {
	out := make([]any, 0, len(s.Tools))
	for _, t := range s.Tools {
		entry := jsv.O("name", t.Name)
		for _, k := range t.Spec.Keys() {
			entry.Set(k, jsv.Prop(t.Spec, k))
		}
		out = append(out, entry)
	}
	return out
}

func (s *Server) callTool(params *jsv.Obj) (any, *jsv.Obj) {
	name, ok := jsv.Prop(params, "name").(string)
	if !ok {
		return nil, invalidParams("tools/call", "name must be a string")
	}
	args := jsv.NewObj()
	if raw, present := params.Get("arguments"); present {
		if args = jsv.AsObj(raw); args == nil {
			return nil, invalidParams("tools/call", "arguments must be an object")
		}
	}
	r := Fail(fmt.Sprintf("MCP error -32602: Tool %s not found", name))
	for _, t := range s.Tools {
		if t.Name == name {
			r = t.Handler(args)
			break
		}
	}
	content := []any{jsv.O("type", "text", "text", r.Text)}
	if r.IsError {
		return jsv.O("content", content, "isError", true), nil
	}
	return jsv.O("content", content), nil
}

func (s *Server) handle(req *request) (any, *jsv.Obj) {
	switch req.method {
	case "initialize":
		return s.initialize(req.params)
	case "ping":
		return jsv.NewObj(), nil
	case "tools/list":
		if cursor, present := req.params.Get("cursor"); present {
			if _, isString := cursor.(string); !isString {
				return nil, invalidParams("tools/list", "cursor must be a string")
			}
		}
		return jsv.O("tools", s.catalogue()), nil
	case "tools/call":
		return s.callTool(req.params)
	}
	return nil, rpcError(codeMethodNotFound, "Method not found")
}

func writeReply(w *bufio.Writer, id any, result any, rpcErr *jsv.Obj) error {
	reply := jsv.O("jsonrpc", "2.0", "id", id)
	if rpcErr != nil {
		reply.Set("error", rpcErr)
	} else {
		reply.Set("result", result)
	}
	if _, err := w.WriteString(jsv.Stringify(reply, 0) + "\n"); err != nil {
		return err
	}
	return w.Flush()
}

// Serve answers every newline-terminated request line in order; a trailing line with
// no newline is never read as a message, as in the SDK's stdio transport.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 1<<20)
	writer := bufio.NewWriter(out)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		text := strings.TrimSuffix(jsv.ValidUTF8(line[:len(line)-1]), "\r")
		req := parseRequest(text)
		if req == nil {
			continue
		}
		result, rpcErr := s.handle(req)
		if werr := writeReply(writer, req.id, result, rpcErr); werr != nil {
			return werr
		}
	}
}

// Main serves stdin and stdout and exits with status 1 and a fatal message on stderr when
// the transport fails.
func Main(s *Server) {
	if err := s.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] fatal: %s\n", s.Name, err.Error())
		os.Exit(1)
	}
}
