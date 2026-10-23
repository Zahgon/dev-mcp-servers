package main

// Test client for the stdio server. Every test drives a real server process over
// newline-delimited JSON-RPC: the executable named by MCP_SERVER_BIN when it is set,
// otherwise this test binary started again in server mode.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	serveEnv     = "GIT_INSIGHTS_TEST_SERVE"
	replyTimeout = 20 * time.Second
)

func TestMain(m *testing.M) {
	if os.Getenv(serveEnv) == "1" {
		if err := newServer().Serve(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// serverCommand is the executable under test and the environment it needs.
func serverCommand(t *testing.T) (string, []string) {
	t.Helper()
	if bin := os.Getenv("MCP_SERVER_BIN"); bin != "" {
		return bin, nil
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	return self, []string{serveEnv + "=1"}
}

type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
	raw []byte
}

type toolReply struct {
	IsError bool
	Text    string
	raw     []byte
	call    string // the tool and its arguments, for failure messages
}

type mcpClient struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	nextID int
	calls  []string // the tool calls made so far, reported when the test fails
}

func startServer(t *testing.T) *mcpClient {
	t.Helper()
	path, env := serverCommand(t)
	return startAt(t, path, env)
}

func startAt(t *testing.T, path string, env []string) *mcpClient {
	t.Helper()
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe for %s: %v", path, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe for %s: %v", path, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", path, err)
	}
	c := &mcpClient{t: t, cmd: cmd, stdin: stdin, lines: make(chan []byte, 64), nextID: 100}
	t.Cleanup(func() {
		if t.Failed() && len(c.calls) > 0 {
			t.Logf("last tool calls of the failed test (newest last):\n%s", strings.Join(c.calls[max(0, len(c.calls)-5):], "\n"))
		}
	})
	go func() {
		defer close(c.lines)
		reader := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, err := reader.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				c.lines <- line
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		stdin.Close()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	return c
}

func (c *mcpClient) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.stdin, line+"\n"); err != nil {
		c.t.Fatalf("write to server: %v", err)
	}
}

// request sends one request and waits for the reply with the same id; replies may arrive out of order.
func (c *mcpClient) request(method string, params any) rpcReply {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	line, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("encode %s request: %v", method, err)
	}
	c.send(string(line))
	return c.await(id)
}

func (c *mcpClient) await(id int) rpcReply {
	c.t.Helper()
	deadline := time.After(replyTimeout)
	for {
		select {
		case line, open := <-c.lines:
			if !open {
				c.t.Fatalf("server closed stdout before answering request %d", id)
			}
			var reply rpcReply
			if json.Unmarshal(line, &reply) != nil || string(reply.ID) != fmt.Sprint(id) {
				continue
			}
			reply.raw = line
			return reply
		case <-deadline:
			c.t.Fatalf("no reply to request %d within %s", id, replyTimeout)
		}
	}
}

func (c *mcpClient) initialize(version string) rpcReply {
	c.t.Helper()
	reply := c.request("initialize", map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "go-test", "version": "0.0.0"},
	})
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return reply
}

func (c *mcpClient) call(name string, args any) toolReply {
	c.t.Helper()
	call := describeCall(name, args)
	c.calls = append(c.calls, call)
	reply := c.request("tools/call", map[string]any{"name": name, "arguments": args})
	r := c.toolResult(reply)
	r.call = call
	return r
}

// describeCall names a tool call in failure messages: the tool and its arguments as JSON, cut at 300 bytes.
func describeCall(name string, args any) string {
	text, err := json.Marshal(args)
	if err != nil {
		return name
	}
	if len(text) > 300 {
		text = append(text[:300], "..."...)
	}
	return name + " " + string(text)
}

func (c *mcpClient) toolResult(reply rpcReply) toolReply {
	c.t.Helper()
	if reply.Error != nil || reply.Result == nil {
		c.t.Fatalf("expected a tool result, got %s", reply.raw)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil || len(result.Content) != 1 || result.Content[0].Type != "text" {
		c.t.Fatalf("malformed tool result: %s", reply.raw)
	}
	return toolReply{IsError: result.IsError, Text: result.Content[0].Text, raw: reply.raw}
}

// connected starts a server and runs the initialize handshake.
func connected(t *testing.T) *mcpClient {
	t.Helper()
	c := startServer(t)
	if reply := c.initialize("2025-06-18"); reply.Error != nil {
		t.Fatalf("initialize: got the error reply %s, want a result", reply.raw)
	}
	return c
}

// decodeResult decodes a successful tool result.
func decodeResult[T any](t *testing.T, r toolReply) T {
	t.Helper()
	if r.IsError {
		t.Fatalf("%s: unexpected tool error, want a result: %s", r.call, r.Text)
	}
	var v T
	if err := json.Unmarshal([]byte(r.Text), &v); err != nil {
		t.Fatalf("%s: tool result is not JSON: %v: %s", r.call, err, r.Text)
	}
	return v
}

// decodeRaw decodes the result of a JSON-RPC reply.
func decodeRaw[T any](t *testing.T, reply rpcReply) T {
	t.Helper()
	var v T
	if reply.Error != nil || json.Unmarshal(reply.Result, &v) != nil {
		t.Fatalf("unexpected reply: %s", reply.raw)
	}
	return v
}
