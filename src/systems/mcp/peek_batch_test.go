package mcp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPeekToolName_BatchArrayIsInspected is the RFC L1 guard: a JSON-RPC BATCH
// (top-level array) must not slip past the read-only widget gate. peekToolName
// previously failed to unmarshal a batch into its single-object struct and
// returned ("", body, nil), so a batched write tool was never seen by the
// allowlist at the edge. It must now surface the first tools/call tool name so
// the gate can reject a batched write.
func TestPeekToolName_BatchArrayIsInspected(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // tool name peekToolName should surface ("" = none)
	}{
		{
			name: "batch with a write tool surfaces that write tool",
			body: `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_task","arguments":{}}}]`,
			want: "create_task",
		},
		{
			name: "batch read then write surfaces the write tool",
			body: `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_tasks"}},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"delete_task"}}]`,
			want: "delete_task",
		},
		{
			name: "batch of only read tools surfaces a read tool",
			body: `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_tasks"}}]`,
			want: "list_tasks",
		},
		{
			name: "single object still works",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_task"}}`,
			want: "create_task",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			got, _, err := peekToolName(req)
			if err != nil {
				t.Fatalf("peekToolName error: %v", err)
			}
			if got != tc.want {
				t.Errorf("peekToolName = %q, want %q", got, tc.want)
			}
			// For a write tool, the gate's allowlist must reject it.
			if tc.want == "create_task" || tc.want == "delete_task" {
				if widgetToolAllowed(got) {
					t.Errorf("write tool %q must NOT be widget-allowed", got)
				}
			}
		})
	}
}
