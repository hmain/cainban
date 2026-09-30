package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureHandler records the headers and body it received, so tests can assert
// what the shim passed downstream.
type captureHandler struct {
	method string
	name   string
	body   string
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.method = r.Header.Get(mcpMethodHeader)
	c.name = r.Header.Get(mcpNameHeader)
	b, _ := io.ReadAll(r.Body)
	c.body = string(b)
	w.WriteHeader(http.StatusOK)
}

func doPost(t *testing.T, h http.Handler, body string, hdr map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestBackfill_SubscriptionsListen_RejectedFast(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	// subscriptions/listen must be REJECTED here with a JSON-RPC -32601, before
	// the SDK sees it — otherwise the SDK opens a 30s-billed SSE stream (it
	// decides that from the body method, not the header). The downstream handler
	// must NOT be reached, and the response must echo the request id.
	body := `{"jsonrpc":"2.0","id":7,"method":"subscriptions/listen","params":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	h.ServeHTTP(rec, req)

	if cap.method != "" || cap.body != "" {
		t.Errorf("downstream handler was reached (method=%q body=%q); listen must be short-circuited", cap.method, cap.body)
	}
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601 (method not found)", resp.Error.Code)
	}
	if string(resp.ID) != "7" {
		t.Errorf("response id = %s, want 7 (echoed request id)", resp.ID)
	}
}

func TestBackfill_Initialize_StillBackfilled(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	// initialize/tools/list are ordinary request/response POSTs (no stream), and
	// back-filling them is what keeps the connection stable. The listen exclusion
	// must NOT regress these.
	body := `{"jsonrpc":"2.0","id":9,"method":"initialize","params":{}}`
	doPost(t, h, body, nil)

	if cap.method != "initialize" {
		t.Errorf("Mcp-Method = %q, want %q (initialize must still be back-filled)", cap.method, "initialize")
	}
}

func TestBackfill_ToolsCall_SetsMethodAndName(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`
	doPost(t, h, body, nil)

	if cap.method != "tools/call" {
		t.Errorf("Mcp-Method = %q, want %q", cap.method, "tools/call")
	}
	if cap.name != "list_tasks" {
		t.Errorf("Mcp-Name = %q, want %q", cap.name, "list_tasks")
	}
}

func TestBackfill_ResourcesRead_SetsNameFromURI(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	body := `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"file:///x"}}`
	doPost(t, h, body, nil)

	if cap.method != "resources/read" {
		t.Errorf("Mcp-Method = %q, want %q", cap.method, "resources/read")
	}
	if cap.name != "file:///x" {
		t.Errorf("Mcp-Name = %q, want %q", cap.name, "file:///x")
	}
}

func TestBackfill_NeverOverwritesClientHeader(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	// Client set a (deliberately mismatched) header — the shim must NOT touch
	// it, so the SDK still sees the mismatch and can reject -32020.
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`
	doPost(t, h, body, map[string]string{mcpMethodHeader: "tools/list"})

	if cap.method != "tools/list" {
		t.Errorf("Mcp-Method = %q, want client value %q preserved", cap.method, "tools/list")
	}
}

func TestBackfill_DoesNotOverwriteClientName(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	body := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`
	// Header absent for method (so we back-fill method) but Name pre-set: keep it.
	doPost(t, h, body, map[string]string{mcpNameHeader: "client_name"})

	if cap.method != "tools/call" {
		t.Errorf("Mcp-Method = %q, want %q", cap.method, "tools/call")
	}
	if cap.name != "client_name" {
		t.Errorf("Mcp-Name = %q, want client value %q preserved", cap.name, "client_name")
	}
}

func TestBackfill_BatchLeftAlone(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	body := `[{"jsonrpc":"2.0","id":6,"method":"tools/list","params":{}}]`
	doPost(t, h, body, nil)

	if cap.method != "" {
		t.Errorf("Mcp-Method = %q, want empty (batch not touched)", cap.method)
	}
	if cap.body != body {
		t.Errorf("downstream body = %q, want unchanged %q", cap.body, body)
	}
}

func TestBackfill_NonPostLeftAlone(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if cap.method != "" {
		t.Errorf("Mcp-Method = %q, want empty (GET not touched)", cap.method)
	}
}

func TestBackfill_MalformedBodyLeftAlone(t *testing.T) {
	cap := &captureHandler{}
	h := BackfillMirrorHeaders(cap)
	body := `{not json`
	doPost(t, h, body, nil)

	if cap.method != "" {
		t.Errorf("Mcp-Method = %q, want empty (malformed body not touched)", cap.method)
	}
	if cap.body != body {
		t.Errorf("downstream body = %q, want unchanged %q", cap.body, body)
	}
}
