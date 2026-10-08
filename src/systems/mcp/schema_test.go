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

// TestToolsListStable asserts that the MCP server advertises exactly the
// seventeen cainban tools. This is the regression guard for the "tool schema
// unchanged" exit criterion of the stateless refactor: if a tool is added,
// removed, or renamed, this test fails. P5A.4 added the read-only list_activity
// tool (8 -> 9); the repo/board scope RFC added the read-only whoami tool
// (9 -> 10); the CRUD-completeness pass added delete_task, restore_task,
// search_tasks (10 -> 13); the task-links pass added link_tasks, unlink_tasks,
// get_task_links (13 -> 16); the dependency-graph pass added the read-only
// list_links tool (16 -> 17).
func TestToolsListStable(t *testing.T) {
	want := []string{
		"change_board",
		"create_task",
		"delete_task",
		"get_task",
		"get_task_links",
		"link_tasks",
		"list_activity",
		"list_boards",
		"list_links",
		"list_tasks",
		"restore_task",
		"search_tasks",
		"unlink_tasks",
		"update_task",
		"update_task_priority",
		"update_task_status",
		"whoami",
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

// TestToolOutputSchemasPresent asserts that the eight read tools declare an
// outputSchema (derived from their concrete Out type) and the nine write/control
// tools do not. A new tool added without classification here fails the test.
func TestToolOutputSchemasPresent(t *testing.T) {
	schemaTools := map[string]bool{
		"list_tasks":     true,
		"get_task":       true,
		"list_boards":    true,
		"whoami":         true,
		"search_tasks":   true,
		"get_task_links": true,
		"list_activity":  true,
		"list_links":     true,
	}
	noSchemaTools := map[string]bool{
		"create_task":          true,
		"update_task":          true,
		"update_task_status":   true,
		"update_task_priority": true,
		"delete_task":          true,
		"restore_task":         true,
		"link_tasks":           true,
		"unlink_tasks":         true,
		"change_board":         true,
	}

	for _, tl := range listToolsWire(t) {
		switch {
		case schemaTools[tl.Name]:
			if tl.OutputSchema == nil {
				t.Errorf("tool %q should have outputSchema", tl.Name)
				continue
			}
			raw, err := json.Marshal(tl.OutputSchema)
			if err != nil {
				t.Errorf("tool %q outputSchema failed to marshal: %v", tl.Name, err)
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Errorf("tool %q outputSchema is not a valid JSON object: %v", tl.Name, err)
				continue
			}
			// The top-level type should be "object", "array", or a nullable
			// variant like ["null","array"] depending on whether the handler
			// returns a struct/pointer or a slice. The SDK emits nullable
			// types as JSON arrays, e.g. ["null","object"].
			if !schemaHasType(m, "object") && !schemaHasType(m, "array") {
				t.Errorf("tool %q outputSchema type = %v, want object or array (possibly nullable)", tl.Name, m["type"])
			}

		case noSchemaTools[tl.Name]:
			if tl.OutputSchema != nil {
				t.Errorf("tool %q should NOT have outputSchema (writer/control tool)", tl.Name)
			}

		default:
			t.Errorf("tool %q is not classified for output schema; add it to schemaTools or noSchemaTools", tl.Name)
		}
	}
}

// TestToolOutputSchemaTimeFields asserts that the list_tasks outputSchema
// derives time.Time / *time.Time fields as string types. The go-sdk's
// jsonschema package renders time.Time as {"type":"string"} (no format tag)
// and *time.Time as {"type":["null","string"]}. This test confirms the fields
// are present and typed correctly.
func TestToolOutputSchemaTimeFields(t *testing.T) {
	var listTasksTool *mcp.Tool
	for _, tl := range listToolsWire(t) {
		if tl.Name == "list_tasks" {
			listTasksTool = tl
			break
		}
	}
	if listTasksTool == nil {
		t.Fatal("list_tasks tool not found")
	}
	if listTasksTool.OutputSchema == nil {
		t.Fatal("list_tasks has no outputSchema")
	}

	raw, err := json.Marshal(listTasksTool.OutputSchema)
	if err != nil {
		t.Fatalf("failed to marshal outputSchema: %v", err)
	}

	schemaJSON := string(raw)

	// Parse and walk: the schema is {"items":{...properties...},"type":[...]}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("failed to unmarshal schema: %v", err)
	}

	items, _ := schema["items"].(map[string]any)
	if items == nil {
		t.Fatalf("list_tasks outputSchema has no items; schema: %s", schemaJSON)
	}
	props, _ := items["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("list_tasks outputSchema items has no properties; schema: %s", schemaJSON)
	}

	// created_at and updated_at are non-pointer time.Time → "type":"string".
	for _, field := range []string{"created_at", "updated_at"} {
		prop, ok := props[field].(map[string]any)
		if !ok {
			t.Errorf("field %q not found in schema properties", field)
			continue
		}
		if !schemaHasType(prop, "string") {
			t.Errorf("field %q type = %v, want string", field, prop["type"])
		}
	}

	// deleted_at is *time.Time → nullable string: "type":["null","string"].
	deletedProp, ok := props["deleted_at"].(map[string]any)
	if !ok {
		t.Errorf("field deleted_at not found in schema properties")
	} else if !schemaHasType(deletedProp, "string") {
		t.Errorf("field deleted_at type = %v, want string (nullable)", deletedProp["type"])
	}
}

// schemaHasType reports whether a JSON Schema node's "type" field includes the
// given type name. The go-sdk emits a plain string ("object") for a required
// type and a JSON array (["null","array"]) for a nullable one, so this handles
// both forms.
func schemaHasType(node map[string]any, want string) bool {
	switch t := node["type"].(type) {
	case string:
		return t == want
	case []any:
		for _, v := range t {
			if s, ok := v.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// TestToolAnnotationsClassified asserts every tool carries annotations and that
// each is classified correctly: the six read-only tools set ReadOnlyHint, the
// four writers do not, and every tool declares a closed interaction world
// (OpenWorldHint false). This is the regression guard that a NEW tool must be
// classified — an unannotated or mis-annotated tool fails here.
func TestToolAnnotationsClassified(t *testing.T) {
	readOnlyTools := map[string]bool{
		"list_tasks":     true,
		"get_task":       true,
		"list_boards":    true,
		"list_activity":  true,
		"whoami":         true,
		"change_board":   true,
		"search_tasks":   true,
		"get_task_links": true,
		"list_links":     true,
	}
	writers := map[string]bool{
		"create_task":          true,
		"update_task":          true,
		"update_task_status":   true,
		"update_task_priority": true,
		"delete_task":          true,
		"restore_task":         true,
		"link_tasks":           true,
		"unlink_tasks":         true,
	}

	for _, tl := range listToolsWire(t) {
		a := tl.Annotations
		if a == nil {
			t.Errorf("tool %q has no annotations", tl.Name)
			continue
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("tool %q OpenWorldHint = %v, want false (closed world)", tl.Name, a.OpenWorldHint)
		}
		switch {
		case readOnlyTools[tl.Name]:
			if !a.ReadOnlyHint {
				t.Errorf("tool %q should be ReadOnlyHint=true", tl.Name)
			}
		case writers[tl.Name]:
			if a.ReadOnlyHint {
				t.Errorf("tool %q should be ReadOnlyHint=false (it writes)", tl.Name)
			}
			if a.DestructiveHint == nil {
				t.Errorf("tool %q (writer) should set DestructiveHint", tl.Name)
			}
		default:
			t.Errorf("tool %q is not classified read-only or writer; classify it", tl.Name)
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
