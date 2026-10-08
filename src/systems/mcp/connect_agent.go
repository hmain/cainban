package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file is the CLIENT side of the auto-config flow: the `cainban
// connect-agent` CLI (cmd/cainban) uses it to FETCH the client-config document
// from a running MCP server and emit a config in the caller's chosen format.
// The SERVER side (what the document contains) lives in client_config.go.
//
// The split matters: discovery validates that the URL the user gave is really
// the MCP server (not the Connect API — the #1 setup mistake) by requiring the
// RFC 9728 protected-resource document to resolve, THEN reads the richer
// client-config document for the pre-computed snippets.

// FetchedClientConfig is the parsed client-config document plus the resolved
// base URL it was fetched from. It is the public result of DiscoverClientConfig.
type FetchedClientConfig struct {
	MCPURL                string
	ClientID              string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RedirectURIHint       string
	// Raw is the decoded document, exposing the per-client config snippets.
	Raw clientConfigDoc
}

// discoverHTTPClient is the HTTP client used for discovery: a short timeout, no
// redirects suppressed (discovery endpoints are plain GETs). Package-level so a
// test can swap the transport.
var discoverHTTPClient = &http.Client{Timeout: 10 * time.Second}

// DiscoverClientConfig fetches cainban's client-config document from a base MCP
// URL. It FIRST confirms the URL is really the MCP server by requiring the RFC
// 9728 protected-resource document to resolve there (a Connect API URL — the
// usual mistake — 404s this, so we fail with a clear message instead of writing
// a broken config), then reads /.well-known/mcp-client-config.
//
// baseURL may carry a trailing slash or a path; only its origin+path base is
// used to build the well-known URLs.
func DiscoverClientConfig(ctx context.Context, baseURL string) (*FetchedClientConfig, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("empty MCP URL")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("invalid MCP URL %q: %w", base, err)
	}

	// Step 1: confirm this is the MCP server (not the Connect API). The RFC 9728
	// doc is PUBLIC and only the MCP server serves it; a wrong URL 404s here.
	prURL := base + WellKnownProtectedResourcePath
	if err := probeJSON(ctx, prURL); err != nil {
		return nil, fmt.Errorf(
			"%s is not a cainban MCP server (its %s did not resolve: %v).\n"+
				"Make sure you used the MCP API URL, not the Connect API URL — "+
				"they look alike but only the MCP API serves OAuth discovery",
			base, WellKnownProtectedResourcePath, err)
	}

	// Step 2: read the client-config document.
	ccURL := base + WellKnownClientConfigPath
	body, err := getBody(ctx, ccURL)
	if err != nil {
		return nil, fmt.Errorf(
			"MCP server confirmed, but %s is unavailable (%v).\n"+
				"The server may predate the auto-config endpoint — ask the "+
				"operator to redeploy, or configure manually from the connect page",
			ccURL, err)
	}

	var doc clientConfigDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("client-config document at %s is not valid JSON: %w", ccURL, err)
	}
	if doc.MCPURL == "" {
		return nil, fmt.Errorf("client-config document at %s has an empty mcp_url", ccURL)
	}

	return &FetchedClientConfig{
		MCPURL:                doc.MCPURL,
		ClientID:              doc.ClientID,
		AuthorizationEndpoint: doc.AuthorizationEndpoint,
		TokenEndpoint:         doc.TokenEndpoint,
		RedirectURIHint:       doc.RedirectURIHint,
		Raw:                   doc,
	}, nil
}

// RenderConfig returns the config text for the named format, ready to write to
// the client's config file (or stdout). format is one of: json, kirocrew,
// kirocrew-exfil-gate, kirocrew-dashboard, claude, kiro-ide, cursor, vscode.
//
// The returned string is the exact file content for file-based formats, the
// shell command for `claude`, and the full document for `json`.
func (f *FetchedClientConfig) RenderConfig(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return marshalIndent(f.Raw)
	case "kirocrew":
		return marshalIndent(f.Raw.Configs.Kirocrew)
	case "kirocrew-exfil-gate":
		return marshalIndent(f.Raw.Configs.KirocrewExfilGate)
	case "kirocrew-dashboard":
		return marshalIndent(f.Raw.Configs.KirocrewDashboard)
	case "kiro-ide", "kiro":
		return marshalIndent(f.Raw.Configs.KiroIDE)
	case "cursor":
		return marshalIndent(f.Raw.Configs.Cursor)
	case "vscode", "vs-code":
		return marshalIndent(f.Raw.Configs.VSCode)
	case "claude", "claude-code":
		if f.Raw.Configs.ClaudeCodeCommand == "" {
			return "", fmt.Errorf("no claude command available (server did not advertise a client id)")
		}
		return f.Raw.Configs.ClaudeCodeCommand, nil
	default:
		return "", fmt.Errorf("unknown format %q (want: json, kirocrew, kirocrew-exfil-gate, kirocrew-dashboard, claude, kiro-ide, cursor, vscode)", format)
	}
}

func marshalIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// probeJSON does a GET and confirms a 2xx + JSON-ish response, discarding the
// body. Used to validate the MCP server identity cheaply.
func probeJSON(ctx context.Context, u string) error {
	body, err := getBody(ctx, u)
	if err != nil {
		return err
	}
	// A minimal sanity check: the protected-resource doc is a JSON object.
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("response was not JSON")
	}
	return nil
}

// getBody GETs u and returns the body on a 2xx, or an error describing the
// status. Bounded read so a misconfigured endpoint cannot stream unbounded data.
func getBody(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := discoverHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// 256 KiB cap — these documents are a few KB at most.
	return io.ReadAll(io.LimitReader(resp.Body, 256*1024))
}
