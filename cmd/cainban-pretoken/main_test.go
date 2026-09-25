package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

// TestBuildClaims covers the mapping from Cognito custom attributes to the
// top-level claims the validator reads, across the shapes an operator might set
// custom:repos to, and the critical empty/absent cases (which MUST NOT emit a
// repos claim — no invented grants).
func TestBuildClaims(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
		// wantRepos is the DECODED repos set expected inside the JSON-array
		// string claim; nil means the repos claim must be ABSENT.
		wantRepos       []string
		wantDefaultRepo string // "" means default_repo claim must be absent
	}{
		{
			name:            "json array + default",
			attrs:           map[string]string{"custom:repos": `["acme/repo-a","acme/repo-b"]`, "custom:default_repo": "acme/repo-a"},
			wantRepos:       []string{"acme/repo-a", "acme/repo-b"},
			wantDefaultRepo: "acme/repo-a",
		},
		{
			name:      "space-delimited",
			attrs:     map[string]string{"custom:repos": "acme/repo-a acme/repo-b"},
			wantRepos: []string{"acme/repo-a", "acme/repo-b"},
		},
		{
			name:      "comma-delimited",
			attrs:     map[string]string{"custom:repos": "acme/repo-a,acme/repo-b"},
			wantRepos: []string{"acme/repo-a", "acme/repo-b"},
		},
		{
			name:      "comma+space, trims and dedups",
			attrs:     map[string]string{"custom:repos": " acme/repo-a , acme/repo-a, acme/repo-b "},
			wantRepos: []string{"acme/repo-a", "acme/repo-b"},
		},
		{
			name:      "single repo",
			attrs:     map[string]string{"custom:repos": "acme/repo-a"},
			wantRepos: []string{"acme/repo-a"},
		},
		{
			name:            "default only, no repos -> only default_repo claim",
			attrs:           map[string]string{"custom:default_repo": "acme/repo-a"},
			wantRepos:       nil,
			wantDefaultRepo: "acme/repo-a",
		},
		{
			name:      "empty repos attr -> no claim (no invented grant)",
			attrs:     map[string]string{"custom:repos": ""},
			wantRepos: nil,
		},
		{
			name:      "whitespace repos attr -> no claim",
			attrs:     map[string]string{"custom:repos": "   "},
			wantRepos: nil,
		},
		{
			name:      "absent attrs -> empty claims",
			attrs:     map[string]string{},
			wantRepos: nil,
		},
		{
			name:      "nil attrs -> empty claims",
			attrs:     nil,
			wantRepos: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildClaims(tc.attrs)

			// repos claim
			raw, hasRepos := got[claimRepos]
			if tc.wantRepos == nil {
				if hasRepos {
					t.Errorf("repos claim present (%q) but want absent", raw)
				}
			} else {
				if !hasRepos {
					t.Fatalf("repos claim absent, want %v", tc.wantRepos)
				}
				var decoded []string
				if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
					t.Fatalf("repos claim %q is not a JSON array string: %v", raw, err)
				}
				if !reflect.DeepEqual(decoded, tc.wantRepos) {
					t.Errorf("repos = %v, want %v (raw claim %q)", decoded, tc.wantRepos, raw)
				}
			}

			// default_repo claim
			def, hasDef := got[claimDefaultRepo]
			if tc.wantDefaultRepo == "" {
				if hasDef {
					t.Errorf("default_repo claim present (%q) but want absent", def)
				}
			} else if def != tc.wantDefaultRepo {
				t.Errorf("default_repo = %q, want %q", def, tc.wantDefaultRepo)
			}
		})
	}
}

// TestBuildClaims_ReposIsJSONArrayEncodedString locks the exact wire shape the
// validator's reposClaim decoder consumes: a JSON-array-ENCODED STRING, not a
// native array.
func TestBuildClaims_ReposIsJSONArrayEncodedString(t *testing.T) {
	got := buildClaims(map[string]string{"custom:repos": "acme/repo-a acme/repo-b"})
	raw := got[claimRepos]
	if raw != `["acme/repo-a","acme/repo-b"]` {
		t.Fatalf("repos claim = %q, want a JSON-array-encoded string", raw)
	}
}

// TestHandler_SetsOverrides proves the full event handler wires buildClaims onto
// the response's ClaimsToAddOrOverride.
func TestHandler_SetsOverrides(t *testing.T) {
	ev := events.CognitoEventUserPoolsPreTokenGen{}
	ev.Request.UserAttributes = map[string]string{
		"custom:repos":        `["acme/repo-a"]`,
		"custom:default_repo": "acme/repo-a",
		"email":               "user@example.com",
	}

	out, err := handler(context.Background(), ev)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	claims := out.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride
	if claims["repos"] != `["acme/repo-a"]` {
		t.Errorf("repos override = %q, want [\"acme/repo-a\"]", claims["repos"])
	}
	if claims["default_repo"] != "acme/repo-a" {
		t.Errorf("default_repo override = %q, want acme/repo-a", claims["default_repo"])
	}
}

// TestHandler_NoGrantsNoOverrides proves a user with no grants gets NO claim
// overrides at all (an empty override map is not attached).
func TestHandler_NoGrantsNoOverrides(t *testing.T) {
	ev := events.CognitoEventUserPoolsPreTokenGen{}
	ev.Request.UserAttributes = map[string]string{"email": "user@example.com"}

	out, err := handler(context.Background(), ev)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(out.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride) != 0 {
		t.Errorf("expected no claim overrides, got %v", out.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride)
	}
}
