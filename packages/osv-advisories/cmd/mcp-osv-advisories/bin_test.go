package main

// The upstream suite test/bin.test.mjs, ported: an installed bin is a symlink to the
// server, and the server must still start when run that way.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStartsOnStdioWhenRunThroughASymlinkedBin(t *testing.T) {
	target, env := serverCommand(t)
	link := filepath.Join(t.TempDir(), "server-bin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink(target, link): %v", err)
	}
	c := startAt(t, link, env)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != "osv-advisories" {
		t.Fatalf("initialize through the symlink: %s", init.raw)
	}
	reply := c.request("tools/list", map[string]any{})
	var list struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &list); err != nil || len(list.Tools) == 0 {
		t.Fatalf("tools/list through the symlink: %s", reply.raw)
	}
}
