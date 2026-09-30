package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Standard MCP transport headers (spec 2026-07-28). These MIRROR the JSON-RPC
// body: under a modern connection the server SDK requires Mcp-Method to be
// present and to equal the body's "method", and Mcp-Name to equal the target
// name for the name-bearing methods (tools/call, resources/read, prompts/get).
// They are a routing aid for gateways, "a mirror, not a control surface".
const (
	mcpMethodHeader = "Mcp-Method"
	mcpNameHeader   = "Mcp-Name"
)

// maxBackfillPeekBytes bounds how much of the request body the shim will read
// to derive the mirror headers. A JSON-RPC envelope's method/name live in the
// first few hundred bytes; anything larger than this is almost certainly a
// tool-call payload we don't need to inspect (the SDK re-reads the full body
// after us either way). Keeps the peek cheap and DoS-safe.
const maxBackfillPeekBytes = 64 << 10 // 64 KiB

// backfillEnvelope is the minimal shape we decode from a single JSON-RPC
// request to derive its mirror headers. Only "method" and the name-bearing
// params fields are read; everything else is ignored.
type backfillEnvelope struct {
	Method string `json:"method"`
	Params struct {
		Name string `json:"name"` // tools/call, prompts/get
		URI  string `json:"uri"`  // resources/read
	} `json:"params"`
}

// BackfillMirrorHeaders wraps an http.Handler and, for a POST whose body is a
// single JSON-RPC request MISSING the Mcp-Method header, derives Mcp-Method
// (and Mcp-Name where applicable) from the body and sets them before calling
// next. It is a no-op when:
//   - the method is not POST;
//   - Mcp-Method is already present (we NEVER overwrite a client-set header, so
//     a genuine header/body mismatch still reaches the SDK and is rejected
//     -32020, as the spec requires);
//   - the body is empty, oversized, a batch, or not a single JSON-RPC request.
//
// # Why this exists (and why it is safe)
//
// Under a 2026-07-28 connection the go-sdk's Streamable-HTTP handler requires
// the Mcp-Method header on every request (validateMcpHeaders -> -32020). Some
// clients — notably Claude Code — send ordinary requests (initialize,
// tools/list, tools/call) WITHOUT that header, so the SDK rejects them and the
// connection churns in a ~30s reconnect loop. cainban owns neither the client
// nor the SDK validator, and StreamableHTTPOptions exposes no knob to relax the
// check.
//
// This shim removes the churn at the only layer cainban controls, WITHOUT
// weakening the check: the header the spec demands is by definition the body's
// own method, so setting it from the body can only ever produce the value the
// SDK would accept. A request that DID carry the header is untouched, so a real
// mismatch is still the SDK's to reject. The body is restored verbatim for the
// downstream handler.
//
// subscriptions/listen is handled specially: it is REJECTED here with a
// JSON-RPC method-not-found, before the SDK sees it. The SDK decides to hold a
// listen POST open as a long-lived SSE stream from the BODY method (not the
// header), so omitting the header does not stop the stream — only short-
// circuiting the request does. cainban is stateless and advertises no
// subscription capability, so a fast reject is the correct answer.
func BackfillMirrorHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only POST requests with a body carry a JSON-RPC method. Anything else
		// (GET, DELETE, empty body) is not ours to inspect.
		if r.Method != http.MethodPost || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Peek a bounded prefix of the body, then restore the FULL body for the
		// downstream handler regardless of what we find. We peek BEFORE the
		// Mcp-Method-header guard because the subscriptions/listen rejection
		// below must fire whether or not the client set that header — Claude
		// Code sets Mcp-Method: subscriptions/listen on the request, so a guard
		// that skips on a present header would skip the rejection too.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBackfillPeekBytes+1))
		if err != nil {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
			next.ServeHTTP(w, r)
			return
		}
		rest := r.Body // may still hold bytes beyond the peek limit
		restore := func() {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), rest))
		}

		// A request larger than the cap is a tool-call payload we don't parse.
		if len(body) > maxBackfillPeekBytes {
			restore()
			next.ServeHTTP(w, r)
			return
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			// Empty, or a batch/array ('[') — not a single request. Leave it.
			restore()
			next.ServeHTTP(w, r)
			return
		}

		var env backfillEnvelope
		if err := json.Unmarshal(trimmed, &env); err != nil || env.Method == "" {
			restore()
			next.ServeHTTP(w, r)
			return
		}

		// Reject subscriptions/listen HERE, before the SDK ever sees it, and
		// BEFORE the Mcp-Method-header guard below — Claude Code sends this
		// request WITH Mcp-Method: subscriptions/listen set, so gating on an
		// absent header would let it through to the SDK (confirmed in the
		// deployed diagnostic logs).
		//
		// The SDK decides to hold this POST open as a long-lived stream from the
		// BODY method (ephemeralConnectOpts sets isSubscriptionsListen by parsing
		// the body), so the request otherwise blocks until the Lambda's 30s
		// timeout. cainban is mounted Stateless:true and advertises no
		// subscription/list-changed capability, so it has nothing to stream.
		// Returning a JSON-RPC method-not-found short-circuits the SDK entirely:
		// the client gets an instant answer and no 30s-billed invocation is made.
		if env.Method == "subscriptions/listen" {
			writeMethodNotFound(w, trimmed)
			return
		}

		// Back-fill the Mcp-Method / Mcp-Name headers only when the client did
		// not set Mcp-Method. This is the original shim behavior that keeps the
		// connection stable; it must never overwrite a client-set header.
		if r.Header.Get(mcpMethodHeader) != "" {
			restore()
			next.ServeHTTP(w, r)
			return
		}

		r.Header.Set(mcpMethodHeader, env.Method)
		if name := mirrorName(env); name != "" {
			// Only set Mcp-Name when the client didn't; never overwrite.
			if r.Header.Get(mcpNameHeader) == "" {
				r.Header.Set(mcpNameHeader, name)
			}
		}

		restore()
		next.ServeHTTP(w, r)
	})
}

// writeMethodNotFound replies to a single JSON-RPC request with a -32601
// (method not found) error, echoing the request's id so the client can match
// the response. cainban implements no subscriptions/listen, so this is the
// correct spec answer — and returning it here (instead of passing the request
// to the SDK) is what prevents the SDK from opening a long-lived stream.
func writeMethodNotFound(w http.ResponseWriter, reqBody []byte) {
	var idHolder struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(reqBody, &idHolder)
	id := idHolder.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id}
	resp.Error.Code = -32601 // JSON-RPC method not found
	resp.Error.Message = "method not found: subscriptions/listen (server advertises no subscription capability)"

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors ride a 200; the error is in-band
	_ = json.NewEncoder(w).Encode(resp)
}

// mirrorName returns the Mcp-Name value the spec requires for the name-bearing
// methods, or "" for methods that carry no name (subscriptions/listen,
// tools/list, initialize, ...).
func mirrorName(env backfillEnvelope) string {
	switch env.Method {
	case "tools/call", "prompts/get":
		return env.Params.Name
	case "resources/read":
		return env.Params.URI
	default:
		return ""
	}
}
