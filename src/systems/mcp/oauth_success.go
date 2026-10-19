package mcp

import (
	"net/http"
)

// OAuthSuccessHandler serves a styled success page after the OAuth callback
// captures the authorization code. This eliminates the "site can't be reached"
// confusion when the browser redirects to a loopback callback and the MCP
// client has already closed its server.
//
// The page:
//   - Indicates success clearly (✅)
//   - Tells the user they can close the tab
//   - Reminds them about the "new session" requirement (some MCP clients
//     require a new chat session to load newly configured servers)
//   - Provides actionable next steps
type OAuthSuccessHandler struct{}

// NewOAuthSuccessHandler creates a new OAuth success page handler.
func NewOAuthSuccessHandler() *OAuthSuccessHandler {
	return &OAuthSuccessHandler{}
}

// ServeHTTP serves the OAuth success page. Only GET is allowed.
func (h *OAuthSuccessHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	// Only write body for GET (HEAD should be headers only)
	if r.Method == http.MethodHead {
		return
	}

	_, _ = w.Write([]byte(oauthSuccessHTML))
}

// oauthSuccessHTML is the styled success page served after OAuth completes.
// It follows the RFC's specification: clear success indication, close-tab
// guidance, new-session reminder, and actionable next steps.
const oauthSuccessHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>✅ cainban connected</title>
  <style>
    * {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
    }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
      background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
      color: #e0e0e0;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 2rem;
    }
    .container {
      max-width: 480px;
      text-align: center;
      background: rgba(255, 255, 255, 0.05);
      border-radius: 16px;
      padding: 3rem 2rem;
      box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
      border: 1px solid rgba(255, 255, 255, 0.1);
    }
    .success-icon {
      font-size: 4rem;
      margin-bottom: 1.5rem;
      animation: pulse 2s ease-in-out infinite;
    }
    @keyframes pulse {
      0%, 100% { transform: scale(1); }
      50% { transform: scale(1.05); }
    }
    h1 {
      font-size: 1.75rem;
      font-weight: 600;
      margin-bottom: 1rem;
      color: #4ade80;
    }
    .subtitle {
      font-size: 1.1rem;
      color: #a0a0a0;
      margin-bottom: 2rem;
    }
    .next-steps {
      text-align: left;
      background: rgba(0, 0, 0, 0.2);
      border-radius: 12px;
      padding: 1.5rem;
      margin-bottom: 1.5rem;
    }
    .next-steps h2 {
      font-size: 0.9rem;
      font-weight: 600;
      color: #a0a0a0;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      margin-bottom: 1rem;
    }
    .step {
      display: flex;
      align-items: flex-start;
      gap: 0.75rem;
      margin-bottom: 0.75rem;
    }
    .step:last-child {
      margin-bottom: 0;
    }
    .step-number {
      background: #4ade80;
      color: #1a1a2e;
      width: 24px;
      height: 24px;
      border-radius: 50%;
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 0.8rem;
      font-weight: 600;
      flex-shrink: 0;
    }
    .step-text {
      font-size: 0.95rem;
      line-height: 1.5;
    }
    .step-text code {
      background: rgba(74, 222, 128, 0.1);
      color: #4ade80;
      padding: 0.15rem 0.4rem;
      border-radius: 4px;
      font-family: 'SF Mono', Monaco, 'Cascadia Code', monospace;
      font-size: 0.85rem;
    }
    .hint {
      font-size: 0.85rem;
      color: #808080;
      line-height: 1.6;
    }
    .hint strong {
      color: #f59e0b;
    }
    .close-hint {
      margin-top: 2rem;
      padding-top: 1.5rem;
      border-top: 1px solid rgba(255, 255, 255, 0.1);
      font-size: 0.9rem;
      color: #808080;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="success-icon">✅</div>
    <h1>Authentication complete</h1>
    <p class="subtitle">cainban is now connected to your AI agent.</p>
    
    <div class="next-steps">
      <h2>Next steps</h2>
      <div class="step">
        <span class="step-number">1</span>
        <span class="step-text">Return to your AI agent</span>
      </div>
      <div class="step">
        <span class="step-number">2</span>
        <span class="step-text">Run <code>list_tasks</code> to verify the connection</span>
      </div>
      <div class="step">
        <span class="step-number">3</span>
        <span class="step-text">Start managing your board with natural language</span>
      </div>
    </div>
    
    <p class="hint">
      <strong>Note:</strong> If your agent doesn't see cainban yet, start a 
      <strong>new session</strong>. Some MCP clients require a fresh session 
      to load newly configured servers.
    </p>
    
    <p class="close-hint">
      You can safely close this tab and return to your AI agent.
    </p>
  </div>
</body>
</html>`
