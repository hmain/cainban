package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubValidator validates GitHub access tokens (gho_*) by calling the GitHub
// API. Unlike JWT validation, this requires a network call on every request.
// The token is trusted if GitHub's /user endpoint returns a valid user.
type GitHubValidator struct {
	httpClient *http.Client
}

// NewGitHubValidator creates a validator for GitHub access tokens.
func NewGitHubValidator() *GitHubValidator {
	return &GitHubValidator{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// GitHubUser is the response from GitHub's /user endpoint.
type GitHubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Validate checks a GitHub access token by calling GitHub's /user API.
// Returns an Identity if the token is valid, or an error if not.
//
// The Identity is built from GitHub's response:
//   - Subject: "github:<id>" (stable user identifier)
//   - Email: from GitHub (may be empty if user hasn't set a public email)
//   - GitHubUser: the login (username)
//   - GitHubToken: the validated token itself (for downstream GitHub API calls)
//   - Repos: empty (authorization is done via GitHub API, not claims)
func (v *GitHubValidator) Validate(ctx context.Context, token string) (*Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, unauthenticated("missing bearer token", nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, unauthenticated("internal error", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cainban")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, unauthenticated("GitHub API unreachable", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		// Token is valid
	case http.StatusUnauthorized:
		return nil, unauthenticated("invalid GitHub token", nil)
	case http.StatusForbidden:
		// Rate limited or token lacks required scopes
		return nil, unauthenticated("GitHub token forbidden (rate limited or insufficient scopes)", nil)
	default:
		return nil, unauthenticated(fmt.Sprintf("GitHub API returned %d", resp.StatusCode), nil)
	}

	var user GitHubUser
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, unauthenticated("invalid GitHub API response", err)
	}
	if user.ID == 0 || user.Login == "" {
		return nil, unauthenticated("GitHub API returned invalid user", nil)
	}

	return &Identity{
		Subject:     fmt.Sprintf("github:%d", user.ID),
		Email:       user.Email,
		GitHubUser:  user.Login,
		GitHubToken: token,
		Repos:       make(map[string]struct{}), // Empty: use GitHub API for authz
		DefaultRepo: "",
	}, nil
}

// IsGitHubToken returns true if the token looks like a GitHub access token.
// GitHub tokens start with specific prefixes:
//   - gho_ : OAuth user access tokens
//   - ghu_ : User access tokens (GitHub Apps)
//   - ghs_ : Server-to-server tokens (GitHub Apps)
//   - ghp_ : Personal access tokens (classic)
//   - github_pat_ : Fine-grained personal access tokens
func IsGitHubToken(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "gho_") ||
		strings.HasPrefix(token, "ghu_") ||
		strings.HasPrefix(token, "ghs_") ||
		strings.HasPrefix(token, "ghp_") ||
		strings.HasPrefix(token, "github_pat_")
}
