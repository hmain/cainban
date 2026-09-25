package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listToolsWire connects an in-memory client to a freshly built cainban server
// and returns the tools/list result as the client sees it on the wire.
func listToolsWire(t *testing.T) []*mcp.Tool {
	t.Helper()
	srv := NewStateless()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.mcpServer.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	return res.Tools
}

// TestToolsListStable asserts that the MCP server advertises exactly the eight
// cainban tools. This is the regression guard for the "tool schema unchanged"
// exit criterion of the stateless refactor: if a tool is added, removed, or
// renamed, this test fails.
func TestToolsListStable(t *testing.T) {
	want := []string{
		"change_board",
		"create_task",
		"get_task",
		"list_boards",
		"list_tasks",
		"update_task",
		"update_task_priority",
		"update_task_status",
	}

	tools := listToolsWire(t)
	got := make([]string, 0, len(tools))
	for _, tl := range tools {
		got = append(got, tl.Name)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("tool count changed: got %d %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestToolInputSchemasPresent asserts every tool carries a non-empty JSON input
// schema, so a serialization regression that drops schemas is caught.
func TestToolInputSchemasPresent(t *testing.T) {
	for _, tl := range listToolsWire(t) {
		if tl.InputSchema == nil {
			t.Errorf("tool %q has nil input schema", tl.Name)
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Errorf("tool %q input schema failed to marshal: %v", tl.Name, err)
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("tool %q input schema is not valid JSON object: %v", tl.Name, err)
			continue
		}
		if m["type"] != "object" {
			t.Errorf("tool %q input schema type = %v, want object", tl.Name, m["type"])
		}
	}
}

func TestLoopbackOnly(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{":8080", "127.0.0.1:8080", false},
		{"0.0.0.0:9090", "127.0.0.1:9090", false},
		{"127.0.0.1:7000", "127.0.0.1:7000", false},
		{"[::]:5000", "127.0.0.1:5000", false},
		{"", "", true},
		{"8080", "", true}, // bare port has no colon -> SplitHostPort errors
	}
	for _, c := range cases {
		got, err := loopbackOnly(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("loopbackOnly(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("loopbackOnly(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("loopbackOnly(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
