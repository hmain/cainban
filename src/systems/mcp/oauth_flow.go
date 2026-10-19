package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthFlowConfig configures the local OAuth flow server.
type OAuthFlowConfig struct {
	// CallbackPort is the local port to listen on for the OAuth callback.
	// Default: 3334 (matches MCPOAuthRedirectURIHint)
	CallbackPort int

	// CallbackPath is the path component of the callback URL.
	// Default: /oauth/callback
	CallbackPath string

	// AuthorizeURL is the OAuth authorization endpoint.
	AuthorizeURL string

	// TokenURL is the OAuth token endpoint.
	TokenURL string

	// ClientID is the OAuth client ID.
	ClientID string

	// Scopes is the list of OAuth scopes to request.
	// Default: ["openid"]
	Scopes []string

	// SkipBrowserOpen skips opening the browser (for testing).
	SkipBrowserOpen bool

	// ShutdownTimeout is how long to wait for graceful shutdown.
	// Default: 5 seconds
	ShutdownTimeout time.Duration

	// PageServeTimeout is how long to keep the server running after serving
	// the success page, so the user can see it before the server shuts down.
	// Default: 2 seconds
	PageServeTimeout time.Duration
}

// OAuthFlowResult contains the tokens received from the OAuth flow.
type OAuthFlowResult struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	Scope        string
}

// OAuthFlow manages a local OAuth authorization code flow with PKCE.
type OAuthFlow struct {
	cfg           OAuthFlowConfig
	state         string
	codeVerifier  string
	codeChallenge string

	mu         sync.Mutex
	result     *OAuthFlowResult
	flowErr    error
	doneCh     chan struct{}
	pageServed bool
}

// NewOAuthFlow creates a new OAuth flow with the given configuration.
func NewOAuthFlow(cfg OAuthFlowConfig) *OAuthFlow {
	// Apply defaults
	if cfg.CallbackPort == 0 {
		cfg.CallbackPort = 3334
	}
	if cfg.CallbackPath == "" {
		cfg.CallbackPath = "/oauth/callback"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid"}
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if cfg.PageServeTimeout == 0 {
		cfg.PageServeTimeout = 2 * time.Second
	}

	// Generate PKCE values
	verifier := generateRandomString(43)
	challenge := generateCodeChallenge(verifier)
	state := generateRandomString(32)

	return &OAuthFlow{
		cfg:           cfg,
		state:         state,
		codeVerifier:  verifier,
		codeChallenge: challenge,
		doneCh:        make(chan struct{}),
	}
}

// State returns the state parameter used for CSRF protection.
func (f *OAuthFlow) State() string {
	return f.state
}

// AuthorizeURL returns the full authorization URL the user should visit.
func (f *OAuthFlow) AuthorizeURL() string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", f.cfg.ClientID)
	params.Set("redirect_uri", f.redirectURI())
	params.Set("scope", strings.Join(f.cfg.Scopes, " "))
	params.Set("state", f.state)
	params.Set("code_challenge", f.codeChallenge)
	params.Set("code_challenge_method", "S256")

	return f.cfg.AuthorizeURL + "?" + params.Encode()
}

// Run starts the local server, optionally opens the browser, and waits for
// the OAuth callback. It returns the tokens on success or an error on failure.
// The context can be used to cancel the flow.
func (f *OAuthFlow) Run(ctx context.Context) (*OAuthFlowResult, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", f.cfg.CallbackPort)

	mux := http.NewServeMux()
	mux.HandleFunc(f.cfg.CallbackPath, f.handleCallback)
	mux.HandleFunc("/health", f.handleHealth)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in background
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != http.ErrServerClosed {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	// Open browser if not skipped
	if !f.cfg.SkipBrowserOpen {
		authURL := f.AuthorizeURL()
		if err := openBrowser(authURL); err != nil {
			// Non-fatal: print URL for manual navigation
			fmt.Printf("Could not open browser. Please visit:\n%s\n", authURL)
		}
	}

	// Wait for callback, context cancellation, or server error
	select {
	case <-f.doneCh:
		// Flow completed (success or error)
	case <-ctx.Done():
		f.mu.Lock()
		f.flowErr = ctx.Err()
		f.mu.Unlock()
	case err := <-serverErrCh:
		f.mu.Lock()
		f.flowErr = fmt.Errorf("server error: %w", err)
		f.mu.Unlock()
	}

	// If page was served, wait a bit so user can see it
	f.mu.Lock()
	pageServed := f.pageServed
	f.mu.Unlock()
	if pageServed {
		time.Sleep(f.cfg.PageServeTimeout)
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), f.cfg.ShutdownTimeout)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flowErr != nil {
		return nil, f.flowErr
	}
	return f.result, nil
}

func (f *OAuthFlow) redirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", f.cfg.CallbackPort, f.cfg.CallbackPath)
}

func (f *OAuthFlow) handleCallback(w http.ResponseWriter, r *http.Request) {
	// Only handle GET
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	// Check for error response
	if errCode := q.Get("error"); errCode != "" {
		errDesc := q.Get("error_description")
		f.mu.Lock()
		f.flowErr = fmt.Errorf("OAuth error: %s - %s", errCode, errDesc)
		f.mu.Unlock()
		close(f.doneCh)
		http.Error(w, "Authorization failed: "+errDesc, http.StatusBadRequest)
		return
	}

	// Validate state
	state := q.Get("state")
	if state != f.state {
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	// Get authorization code
	code := q.Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	// Exchange code for tokens
	result, err := f.exchangeCode(r.Context(), code)
	if err != nil {
		f.mu.Lock()
		f.flowErr = fmt.Errorf("token exchange failed: %w", err)
		f.mu.Unlock()
		close(f.doneCh)
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}

	f.mu.Lock()
	f.result = result
	f.pageServed = true
	f.mu.Unlock()

	// Serve success page
	successHandler := NewOAuthSuccessHandler()
	successHandler.ServeHTTP(w, r)

	// Signal completion
	close(f.doneCh)
}

func (f *OAuthFlow) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (f *OAuthFlow) exchangeCode(ctx context.Context, code string) (*OAuthFlowResult, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", f.redirectURI())
	data.Set("client_id", f.cfg.ClientID)
	data.Set("code_verifier", f.codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &OAuthFlowResult{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scope:        tokenResp.Scope,
	}, nil
}

// generateRandomString generates a URL-safe random string of the given length.
func generateRandomString(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:length]
}

// generateCodeChallenge generates a PKCE code challenge from a verifier using S256.
func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// openBrowser attempts to open the given URL in the default browser.
// This is a stub that should be implemented per-platform.
func openBrowser(url string) error {
	// This is intentionally a no-op in the library. The CLI should implement
	// platform-specific browser opening (exec.Command("open", url) on macOS, etc.)
	// For now, we just return nil to indicate no error.
	return nil
}
