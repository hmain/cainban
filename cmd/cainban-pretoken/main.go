// Command cainban-pretoken is the AWS Cognito PRE-TOKEN-GENERATION Lambda
// trigger for cainban's auth. It exists to bridge one specific gap:
//
// The auth validator (src/systems/auth) authorizes a request from claims in the
// JWT. This trigger populates those claims at token generation time.
//
// # Mode Selection (CAINBAN_USE_GITHUB_PERMISSIONS)
//
// When CAINBAN_USE_GITHUB_PERMISSIONS=true (GitHub-native auth):
//   - The trigger extracts github_user and github_token from Cognito's GitHub
//     federation and emits them as claims
//   - Authorization is checked at request time via the GitHub API (ghperm package)
//   - The repos claim is NOT populated (not needed)
//   - User management happens entirely in GitHub (repo collaborator settings)
//
// When CAINBAN_USE_GITHUB_PERMISSIONS is unset or false (legacy mode):
//   - The trigger falls back to custom:repos/custom:default_repo attributes
//   - Authorization is checked against the repos claim in the JWT
//   - The trigger emits repos and default_repo claims
//
// # GitHub-Native Auth Claims
//
// When GitHub-native auth is enabled, the trigger emits:
//   - github_user:  the GitHub username from Cognito federation identities
//   - github_token: the GitHub access token (from Cognito's providerData)
//   - default_repo: (optional) from custom:default_repo attribute
//
// The GitHub token is obtained from Cognito's federation; it requires the
// GitHub identity provider to be configured with the repo scope.
//
// Build (arm64, provided.al2023, pure Go — no CGO, no SQLite):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-pretoken
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"unicode"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// Cognito attribute names.
const (
	attrRepos       = "custom:repos"
	attrDefaultRepo = "custom:default_repo"
	attrSub         = "sub"
	// attrIdentities holds the federated identity provider info as JSON.
	// Format: [{"providerName":"GitHub","userId":"12345","providerType":"OIDC"}]
	attrIdentities = "identities"
)

// Top-level claim names the validator reads.
const (
	claimRepos       = "repos"
	claimDefaultRepo = "default_repo"
	claimGitHubUser  = "github_user"
	claimGitHubToken = "github_token"
)

// Environment variable for GitHub-native auth mode.
const envUseGitHubPermissions = "CAINBAN_USE_GITHUB_PERMISSIONS"

// useGitHubPermissions is set at cold start from env.
var useGitHubPermissions = os.Getenv(envUseGitHubPermissions) == "true"

func main() {
	if useGitHubPermissions {
		log.Printf("cainban-pretoken: GitHub-native auth mode enabled")
	} else {
		log.Printf("cainban-pretoken: legacy mode (custom:repos attribute)")
	}
	lambda.Start(handler)
}

func handler(ctx context.Context, event events.CognitoEventUserPoolsPreTokenGenV2_0) (events.CognitoEventUserPoolsPreTokenGenV2_0, error) {
	var overrides map[string]string

	if useGitHubPermissions {
		overrides = buildGitHubClaims(event)
	} else {
		overrides = buildLegacyClaims(event.Request.UserAttributes)
	}

	if len(overrides) > 0 {
		// V2 trigger: claims must be written to BOTH the id token AND the
		// access token. The MCP API's bearer is the ACCESS token, so claims
		// MUST land on the access token or the validator sees nothing.
		idClaims := make(map[string]interface{}, len(overrides))
		acClaims := make(map[string]interface{}, len(overrides))
		for k, v := range overrides {
			idClaims[k] = v
			acClaims[k] = v
		}
		event.Response.ClaimsAndScopeOverrideDetails.IDTokenGeneration.ClaimsToAddOrOverride = idClaims
		event.Response.ClaimsAndScopeOverrideDetails.AccessTokenGeneration.ClaimsToAddOrOverride = acClaims
		log.Printf("cainban-pretoken: wrote claims sub=%q keys=%v to id+access tokens", eventSubject(event), keysOf(overrides))
	} else {
		log.Printf("cainban-pretoken: NO overrides for sub=%q", eventSubject(event))
	}
	return event, nil
}

// keysOf returns the claim keys (not values) for diagnostic logging.
func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// eventSubject returns the subject from the event.
func eventSubject(event events.CognitoEventUserPoolsPreTokenGenV2_0) string {
	if sub := strings.TrimSpace(event.Request.UserAttributes[attrSub]); sub != "" {
		return sub
	}
	return strings.TrimSpace(event.UserName)
}

// buildGitHubClaims extracts GitHub identity from Cognito federation and builds
// the claims for GitHub-native auth mode.
func buildGitHubClaims(event events.CognitoEventUserPoolsPreTokenGenV2_0) map[string]string {
	attrs := event.Request.UserAttributes
	out := make(map[string]string, 3)

	// Extract GitHub username from identities
	// The identities attribute contains federated provider info
	githubUser := extractGitHubUsername(attrs)
	if githubUser != "" {
		out[claimGitHubUser] = githubUser
	}

	// Extract GitHub token from the trigger context
	// Note: Cognito provides the provider tokens via different mechanisms:
	// - For user pools with hosted UI: tokens are in the response
	// - For direct federation: may need custom handling
	// This is a placeholder - the actual token extraction depends on your
	// Cognito configuration (hosted UI vs direct federation)
	githubToken := extractGitHubToken(event)
	if githubToken != "" {
		out[claimGitHubToken] = githubToken
	}

	// Include default_repo if set (still useful for identifying which repo to use)
	if def := strings.TrimSpace(attrs[attrDefaultRepo]); def != "" {
		out[claimDefaultRepo] = def
	}

	// Log (without token) for debugging
	if githubUser == "" {
		log.Printf("cainban-pretoken: WARNING no GitHub username found in identities")
	}
	if githubToken == "" {
		log.Printf("cainban-pretoken: WARNING no GitHub token available (federation may need configuration)")
	}

	return out
}

// extractGitHubUsername parses the identities attribute to find the GitHub username.
// The identities attribute is a JSON array like:
// [{"providerName":"GitHub","userId":"12345","providerType":"OIDC","primary":true}]
func extractGitHubUsername(attrs map[string]string) string {
	identities := strings.TrimSpace(attrs[attrIdentities])
	if identities == "" {
		return ""
	}

	var providers []struct {
		ProviderName string `json:"providerName"`
		UserID       string `json:"userId"`
		ProviderType string `json:"providerType"`
	}
	if err := json.Unmarshal([]byte(identities), &providers); err != nil {
		log.Printf("cainban-pretoken: failed to parse identities: %v", err)
		return ""
	}

	for _, p := range providers {
		if strings.EqualFold(p.ProviderName, "GitHub") {
			// The userId from GitHub is the numeric user ID, not the username.
			// We need to look for a username attribute instead, or the trigger
			// would need to call GitHub API to resolve ID -> username.
			//
			// For now, check if there's a preferred_username or name attribute
			// that contains the GitHub username.
			return p.UserID // Return ID for now; may need username lookup
		}
	}

	// Also check for username in attributes (some configs put it there)
	if username := strings.TrimSpace(attrs["preferred_username"]); username != "" {
		return username
	}
	if username := strings.TrimSpace(attrs["name"]); username != "" {
		return username
	}
	if username := strings.TrimSpace(attrs["custom:github_username"]); username != "" {
		return username
	}

	return ""
}

// extractGitHubToken attempts to get the GitHub access token.
// NOTE: Cognito's pre-token trigger does NOT directly receive the IdP tokens.
// To get the GitHub token, you typically need one of:
// 1. Store it during the post-authentication trigger
// 2. Use Cognito Identity Pools to get AWS credentials + GitHub token
// 3. Have the client pass it separately
//
// This function returns empty - the actual implementation depends on your
// specific Cognito configuration. The resolver will fail closed (401) if
// the token is missing.
func extractGitHubToken(event events.CognitoEventUserPoolsPreTokenGenV2_0) string {
	// Cognito pre-token trigger doesn't have direct access to IdP tokens.
	// The token needs to be obtained through other means:
	// - A post-authentication trigger that stores it
	// - A custom attribute populated during sign-up
	// - The client obtaining it separately and passing via API

	// Check if stored in a custom attribute (if configured that way)
	if token := strings.TrimSpace(event.Request.UserAttributes["custom:github_token"]); token != "" {
		return token
	}

	return ""
}

// buildLegacyClaims is the legacy fallback: reads custom:repos attribute.
// Alias for backwards compatibility with tests.
func buildLegacyClaims(userAttributes map[string]string) map[string]string {
	return buildClaims(userAttributes)
}

// buildClaims is the pure, unit-testable fallback core: given the user's Cognito
// attributes, it returns the top-level claims to add/override.
func buildClaims(userAttributes map[string]string) map[string]string {
	out := make(map[string]string, 2)

	repos := parseRepoList(userAttributes[attrRepos])
	if len(repos) > 0 {
		encoded, _ := json.Marshal(repos)
		out[claimRepos] = string(encoded)
	}

	if def := strings.TrimSpace(userAttributes[attrDefaultRepo]); def != "" {
		out[claimDefaultRepo] = def
	}

	return out
}

// parseRepoList tokenizes the custom:repos attribute into a de-duplicated,
// order-preserving list of "owner/repo" tokens.
func parseRepoList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var tokens []string
	if raw[0] == '[' {
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			tokens = arr
		} else {
			tokens = splitDelimited(raw)
		}
	} else {
		tokens = splitDelimited(raw)
	}

	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		for _, t := range splitDelimited(tok) {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitDelimited splits a string on whitespace and commas.
func splitDelimited(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// --- Legacy support for tests (grants table reader interface) ---

// grantsReader is the interface for the legacy grants table path.
// Kept for backwards compatibility with existing tests.
type grantsReader interface {
	ListReposForSubject(ctx context.Context, subject string) ([]string, error)
	GetDefaultRepo(ctx context.Context, subject string) (string, error)
}

// makeHandler creates a handler with an optional grants reader.
// This is the legacy API for tests; new code should use the handler directly.
func makeHandler(reader grantsReader) func(context.Context, events.CognitoEventUserPoolsPreTokenGenV2_0) (events.CognitoEventUserPoolsPreTokenGenV2_0, error) {
	return func(ctx context.Context, event events.CognitoEventUserPoolsPreTokenGenV2_0) (events.CognitoEventUserPoolsPreTokenGenV2_0, error) {
		overrides := resolveClaims(ctx, reader, event)
		if len(overrides) > 0 {
			idClaims := make(map[string]interface{}, len(overrides))
			acClaims := make(map[string]interface{}, len(overrides))
			for k, v := range overrides {
				idClaims[k] = v
				acClaims[k] = v
			}
			event.Response.ClaimsAndScopeOverrideDetails.IDTokenGeneration.ClaimsToAddOrOverride = idClaims
			event.Response.ClaimsAndScopeOverrideDetails.AccessTokenGeneration.ClaimsToAddOrOverride = acClaims
		}
		return event, nil
	}
}

// resolveClaims is the legacy claim resolver supporting both grants table and
// attribute fallback. Used for backwards compatibility with tests.
func resolveClaims(ctx context.Context, reader grantsReader, event events.CognitoEventUserPoolsPreTokenGenV2_0) map[string]string {
	attrs := event.Request.UserAttributes

	// No table configured: pure attribute fallback.
	if reader == nil {
		return buildClaims(attrs)
	}

	subject := eventSubject(event)
	if subject == "" {
		return buildClaims(attrs)
	}

	repos, err := reader.ListReposForSubject(ctx, subject)
	if err != nil {
		// FAIL CLOSED: table error -> no claims
		log.Printf("cainban-pretoken: grants table read failed — failing closed: %v", err)
		return map[string]string{}
	}

	if len(repos) == 0 {
		// Table empty: fallback to attributes
		return buildClaims(attrs)
	}

	// Build claims from table
	out := make(map[string]string, 2)
	encoded, _ := json.Marshal(repos)
	out[claimRepos] = string(encoded)

	if def, derr := reader.GetDefaultRepo(ctx, subject); derr == nil && def != "" {
		out[claimDefaultRepo] = def
	} else if derr != nil {
		log.Printf("cainban-pretoken: grants table default_repo read failed — omitting: %v", derr)
	}
	return out
}
