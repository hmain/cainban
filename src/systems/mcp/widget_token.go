package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
)

// Widget tokens are READ-ONLY, repo-scoped bearer credentials for the
// embeddable board widget. They are deliberately SEPARATE from the user JWTs
// the rest of the MCP server validates:
//
//   - a user JWT is an RS256 token signed by the IdP (Cognito) carrying a
//     `repos` claim and granting full read+write to those repos;
//   - a widget token is an opaque `wt_`-prefixed string this server mints and
//     verifies itself with a symmetric HMAC-SHA256 secret, scoped to exactly
//     ONE repo and allowed to call only the read tools.
//
// This lets a site owner paste a token into an <embed> on a public page to
// surface a private board's counts WITHOUT handing out a login-capable token.
// A leaked widget token can only read one repo and can be revoked by id.
//
// # Token format
//
//	wt_<base64url(payload)>.<base64url(hmac)>
//
// where payload is JSON {"id","repo","exp"} and hmac = HMAC-SHA256(secret,
// "<base64url(payload)>") over the ENCODED payload segment (so verification
// re-encodes nothing). The `wt_` prefix is what the auth middleware keys on to
// route a request down the widget path instead of the JWT path; it is stripped
// before decoding.
//
// The server secret comes from CAINBAN_WIDGET_SECRET. Mint and verify share one
// secret; there is no asymmetric key because the issuer and the verifier are the
// same server.

const (
	// widgetTokenPrefix marks a bearer token as a widget token. The auth
	// middleware routes on this prefix before attempting JWT validation.
	widgetTokenPrefix = "wt_"

	// EnvWidgetSecret is the env var holding the HMAC-SHA256 signing secret for
	// widget tokens. Required to mint or validate a widget token; absent, the
	// widget-token path is disabled (no token can be created or accepted).
	EnvWidgetSecret = "CAINBAN_WIDGET_SECRET"

	// defaultWidgetExpiryDays / maxWidgetExpiryDays bound a token's lifetime.
	defaultWidgetExpiryDays = 30
	maxWidgetExpiryDays     = 365
)

// widgetReadOnlyTools is the exact set of tools a widget token may invoke. A
// request authenticated with a widget token calling any tool NOT in this set is
// rejected before the handler runs (see readOnlyGate). Keeping it an explicit
// allowlist (rather than a write denylist) fails closed: a tool added later is
// forbidden to widget tokens until deliberately listed here.
var widgetReadOnlyTools = map[string]struct{}{
	"list_tasks":    {},
	"get_task":      {},
	"list_activity": {},
}

// widgetToolAllowed reports whether a widget (read-only) token may call the
// named tool.
func widgetToolAllowed(tool string) bool {
	_, ok := widgetReadOnlyTools[tool]
	return ok
}

// widgetTokenPayload is the signed, repo-scoped claim set carried inside a
// widget token. It is tiny on purpose: identity (id), scope (repo) and lifetime
// (exp) are all a read-only widget needs.
type widgetTokenPayload struct {
	// ID is a random, URL-safe token id used for revocation lookups. It is the
	// DynamoDB WIDGET_TOKEN#<id> key suffix.
	ID string `json:"id"`
	// Repo is the single owner/repo this token authorizes (read-only).
	Repo string `json:"repo"`
	// Exp is the Unix expiry timestamp (seconds).
	Exp int64 `json:"exp"`
}

var widgetB64 = base64.RawURLEncoding

// newWidgetTokenID returns a random, URL-safe token id (128 bits, hex).
func newWidgetTokenID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate token id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// mintWidgetToken builds a signed widget token for repo, valid for ttl from now,
// and returns the token string alongside its id and expiry (for storing the
// revocation metadata). The repo MUST already be normalized by the caller.
//
// A zero/empty secret is an error: a widget token must never be mintable
// unsigned.
func mintWidgetToken(secret []byte, id, repo string, ttl time.Duration, now time.Time) (string, int64, error) {
	if len(secret) == 0 {
		return "", 0, fmt.Errorf("widget token secret is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return "", 0, fmt.Errorf("widget token id is required")
	}
	if strings.TrimSpace(repo) == "" {
		return "", 0, fmt.Errorf("widget token repo is required")
	}
	exp := now.Add(ttl).Unix()
	payload := widgetTokenPayload{ID: id, Repo: repo, Exp: exp}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal widget token payload: %w", err)
	}
	payloadSeg := widgetB64.EncodeToString(payloadJSON)
	sig := widgetHMAC(secret, payloadSeg)
	token := widgetTokenPrefix + payloadSeg + "." + widgetB64.EncodeToString(sig)
	return token, exp, nil
}

// widgetHMAC computes HMAC-SHA256(secret, payloadSeg) over the ENCODED payload
// segment. Signing and verifying use the identical input so no re-encoding
// round-trip can change the bytes under the MAC.
func widgetHMAC(secret []byte, payloadSeg string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payloadSeg))
	return mac.Sum(nil)
}

// isWidgetToken reports whether a raw bearer token is (claims to be) a widget
// token, by its prefix. It does NOT validate the token — the middleware calls
// validateWidgetToken for that.
func isWidgetToken(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), widgetTokenPrefix)
}

// validateWidgetToken verifies a widget token's HMAC signature FIRST, then its
// expiry, and returns the decoded payload. Signature failure, a malformed
// token, or an expired token is an error and NO payload is returned, so a
// caller can never act on an unverified or stale token. Revocation is checked
// separately (against the metadata store) because it needs I/O; this function
// is pure.
//
// The ordering mirrors the JWT validator: nothing about the payload (repo/exp)
// is trusted until the MAC over the payload segment verifies with the server
// secret.
func validateWidgetToken(secret []byte, raw string, now time.Time) (*widgetTokenPayload, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("widget token secret is not configured")
	}
	tok := strings.TrimSpace(raw)
	if !strings.HasPrefix(tok, widgetTokenPrefix) {
		return nil, fmt.Errorf("not a widget token")
	}
	body := strings.TrimPrefix(tok, widgetTokenPrefix)

	parts := strings.Split(body, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed widget token: expected payload.signature")
	}
	payloadSeg, sigSeg := parts[0], parts[1]

	// Signature FIRST.
	actualSig, err := widgetB64.DecodeString(sigSeg)
	if err != nil {
		return nil, fmt.Errorf("malformed widget token signature")
	}
	expectedSig := widgetHMAC(secret, payloadSeg)
	if !hmac.Equal(expectedSig, actualSig) {
		return nil, fmt.Errorf("invalid widget token signature")
	}

	// Signature valid — now decode + check claims.
	payloadJSON, err := widgetB64.DecodeString(payloadSeg)
	if err != nil {
		return nil, fmt.Errorf("malformed widget token payload")
	}
	var payload widgetTokenPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("malformed widget token payload")
	}

	if payload.Exp == 0 {
		return nil, fmt.Errorf("widget token missing expiry")
	}
	if now.After(time.Unix(payload.Exp, 0)) {
		return nil, fmt.Errorf("widget token expired")
	}
	norm, err := auth.NormalizeRepo(payload.Repo)
	if err != nil {
		return nil, fmt.Errorf("widget token has invalid repo: %w", err)
	}
	payload.Repo = norm
	return &payload, nil
}

// widgetTenant builds the read-only, repo-scoped tenant a validated widget
// token resolves to. It carries ReadOnly=true so the write tools are blocked,
// and its partition prefix isolates its reads to the one authorized repo —
// identical structural isolation to a user JWT tenant, minus write access.
func widgetTenant(payload *widgetTokenPayload) *auth.Tenant {
	return &auth.Tenant{
		Repo:            payload.Repo,
		PartitionPrefix: auth.PartitionPrefixFor(payload.Repo),
		Subject:         "widget:" + payload.ID,
		Actor:           "widget",
		AuthorizedRepos: []string{payload.Repo},
		ReadOnly:        true,
	}
}

// The stored, human-inspectable record of a minted widget token
// (store.WidgetTokenMeta) and the store interface (store.WidgetTokenStore) live
// in the store package alongside TaskStore/BoardStore, so both the DynamoDB and
// in-memory backends satisfy them without the mcp package importing a backend.
