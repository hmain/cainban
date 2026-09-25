// Command cainban-pretoken is the AWS Cognito PRE-TOKEN-GENERATION Lambda
// trigger for cainban's Phase 3 auth. It exists to bridge one specific gap:
//
//	The auth validator (src/systems/auth) authorizes a request from a TOP-LEVEL
//	`repos` claim (the set of "owner/repo" the caller may touch) plus an optional
//	top-level `default_repo`. Cognito, however, stores a user's grants in the
//	CUSTOM attributes custom:repos / custom:default_repo, and it does NOT surface
//	custom attributes as top-level claims — its tokens carry them as
//	"custom:repos" (a string), never as top-level "repos". Without this trigger,
//	no user's grants ever reach the validated claim, so every request 403s.
//
// This trigger runs during token generation, reads the user's custom:repos /
// custom:default_repo attributes off the event, and returns
// claimsToAddOrOverride with:
//
//	repos:        a JSON-array-ENCODED STRING of the user's "owner/repo" grants
//	              (Cognito claim-override values are always strings; the
//	              validator's reposClaim decoder accepts this string shape).
//	default_repo: the user's default repo (string), when set.
//
// It is deliberately a pure REFLECTOR of the user's stored attributes: it NEVER
// invents a grant. If custom:repos is empty/absent, no `repos` claim is emitted
// and the user simply has no grants (→ 403 later, which is correct). Grants are
// administered by setting the user's custom:repos attribute (see infra/README.md
// "Granting a user access to a repo"); no extra store, table or IAM is needed.
//
// Build (arm64, provided.al2023, pure Go — no CGO, no SQLite, no AWS SDK):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-pretoken
//
// The trigger is wired to the user pool as its PreTokenGeneration trigger by the
// CDK stack (infra/stack.go). It needs no permissions beyond basic CloudWatch
// Logs — it reads only the attributes Cognito hands it in the event.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// attrRepos / attrDefaultRepo are the Cognito custom-attribute names holding a
// user's repo grants. Cognito prefixes custom attributes with "custom:".
const (
	attrRepos       = "custom:repos"
	attrDefaultRepo = "custom:default_repo"

	// claimRepos / claimDefaultRepo are the TOP-LEVEL claim names the validator
	// reads. The trigger maps the custom attributes onto these.
	claimRepos       = "repos"
	claimDefaultRepo = "default_repo"
)

func main() {
	lambda.Start(handler)
}

// handler adapts the Cognito PreTokenGeneration (v1) event to buildClaims and
// writes the resulting overrides back onto the event response. Using the v1
// event/response shape keeps the emitted `repos`/`default_repo` as top-level
// ID-token claims (ClaimsToAddOrOverride is map[string]string), which is exactly
// the string-valued shape the validator's reposClaim decoder consumes.
func handler(_ context.Context, event events.CognitoEventUserPoolsPreTokenGen) (events.CognitoEventUserPoolsPreTokenGen, error) {
	overrides := buildClaims(event.Request.UserAttributes)
	if len(overrides) > 0 {
		event.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride = overrides
	}
	return event, nil
}

// buildClaims is the pure, unit-testable core: given the user's Cognito
// attributes, it returns the top-level claims to add/override. It emits:
//
//   - `repos`        only when custom:repos parses to at least one grant, as a
//     JSON-array-encoded string (e.g. `["acme/a","acme/b"]`).
//   - `default_repo` only when custom:default_repo is non-empty.
//
// It NEVER fabricates a grant: empty/absent attributes yield an empty map, so
// the token carries no repos claim and the caller is unauthorized (403) — the
// safe default. custom:repos is parsed defensively, accepting the same shapes
// an operator might set it to: a JSON array string, or a space/comma-delimited
// list.
func buildClaims(userAttributes map[string]string) map[string]string {
	out := make(map[string]string, 2)

	repos := parseRepoList(userAttributes[attrRepos])
	if len(repos) > 0 {
		// Emit as a JSON-array-encoded string. json.Marshal of a []string never
		// fails, so the error is discarded deliberately.
		encoded, _ := json.Marshal(repos)
		out[claimRepos] = string(encoded)
	}

	if def := strings.TrimSpace(userAttributes[attrDefaultRepo]); def != "" {
		out[claimDefaultRepo] = def
	}

	return out
}

// parseRepoList tokenizes the custom:repos attribute into a de-duplicated,
// order-preserving list of "owner/repo" tokens. It accepts:
//
//	JSON array:       ["acme/a","acme/b"]
//	space-delimited:  acme/a acme/b
//	comma-delimited:  acme/a,acme/b
//	empty/absent:     no tokens
//
// It does not validate owner/repo form here (the validator canonicalizes and
// the resolver authorizes); it only splits and trims, dropping empties.
func parseRepoList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var tokens []string
	if raw[0] == '[' {
		// JSON array shape.
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			tokens = arr
		} else {
			// Malformed JSON array: fall back to delimiter splitting on the raw
			// text rather than dropping everything.
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

// splitDelimited splits a string on whitespace and commas, trimming and
// dropping empty tokens.
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
