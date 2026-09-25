package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

// TestBuildClaims covers the FALLBACK mapping from Cognito custom attributes to
// the top-level claims the validator reads, across the shapes an operator might
// set custom:repos to, and the critical empty/absent cases (which MUST NOT emit
// a repos claim — no invented grants). This is the pre-P4.1 attribute path,
// still used when the grants table is empty for a subject.
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
			assertReposClaim(t, got, tc.wantRepos)
			assertDefaultRepoClaim(t, got, tc.wantDefaultRepo)
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

// --- P4.1: grants-table read path ------------------------------------------

// fakeReader is an in-memory grantsReader for the handler tests. failErr makes
// ListReposForSubject fail (to drive the fail-closed test); defaultErr does the
// same for GetDefaultRepo.
type fakeReader struct {
	repos      map[string][]string
	defaults   map[string]string
	failErr    error
	defaultErr error
	listedSubs []string
}

func (f *fakeReader) ListReposForSubject(_ context.Context, subject string) ([]string, error) {
	f.listedSubs = append(f.listedSubs, subject)
	if f.failErr != nil {
		return nil, f.failErr
	}
	return f.repos[subject], nil
}

func (f *fakeReader) GetDefaultRepo(_ context.Context, subject string) (string, error) {
	if f.defaultErr != nil {
		return "", f.defaultErr
	}
	return f.defaults[subject], nil
}

func eventWith(sub string, attrs map[string]string) events.CognitoEventUserPoolsPreTokenGen {
	ev := events.CognitoEventUserPoolsPreTokenGen{}
	if attrs == nil {
		attrs = map[string]string{}
	}
	if sub != "" {
		attrs["sub"] = sub
	}
	ev.Request.UserAttributes = attrs
	return ev
}

// TestResolveClaims_TableHasGrants: when the table has grants for the subject,
// the repos claim is built FROM THE TABLE (not the attributes), even if the
// attributes disagree — the table is authoritative when populated.
func TestResolveClaims_TableHasGrants(t *testing.T) {
	reader := &fakeReader{
		repos:    map[string][]string{"sub-1": {"acme/from-table-a", "acme/from-table-b"}},
		defaults: map[string]string{"sub-1": "acme/from-table-a"},
	}
	// Attributes name a DIFFERENT repo; it must be ignored in favor of the table.
	ev := eventWith("sub-1", map[string]string{"custom:repos": "acme/from-attr", "custom:default_repo": "acme/from-attr"})

	got := resolveClaims(context.Background(), reader, ev)
	assertReposClaim(t, got, []string{"acme/from-table-a", "acme/from-table-b"})
	assertDefaultRepoClaim(t, got, "acme/from-table-a")
}

// TestResolveClaims_TableEmptyFallsBackToAttributes: an empty table for the
// subject falls through to the custom:repos attribute path so not-yet-migrated
// users keep working.
func TestResolveClaims_TableEmptyFallsBackToAttributes(t *testing.T) {
	reader := &fakeReader{repos: map[string][]string{}} // no grants for anyone
	ev := eventWith("sub-1", map[string]string{"custom:repos": "acme/attr-a acme/attr-b", "custom:default_repo": "acme/attr-a"})

	got := resolveClaims(context.Background(), reader, ev)
	assertReposClaim(t, got, []string{"acme/attr-a", "acme/attr-b"})
	assertDefaultRepoClaim(t, got, "acme/attr-a")
}

// TestResolveClaims_TableErrorFailsClosed: a table read ERROR emits NO claims —
// it does NOT fall back to the attributes and does NOT fabricate a grant. The
// user ends up with no repos claim (=> 403 downstream), the safe outcome.
func TestResolveClaims_TableErrorFailsClosed(t *testing.T) {
	reader := &fakeReader{
		repos:   map[string][]string{"sub-1": {"acme/would-be"}},
		failErr: errors.New("dynamodb throttled"),
	}
	// Attributes DO name a repo — proving fail-closed does not fall back to them.
	ev := eventWith("sub-1", map[string]string{"custom:repos": "acme/attr-a"})

	got := resolveClaims(context.Background(), reader, ev)
	if len(got) != 0 {
		t.Fatalf("fail-closed must emit no claims, got %v", got)
	}
	if _, has := got[claimRepos]; has {
		t.Fatal("fail-closed emitted a repos claim")
	}
}

// TestResolveClaims_NilReaderUsesAttributes: with no table configured (nil
// reader), the resolver is exactly the attribute path.
func TestResolveClaims_NilReaderUsesAttributes(t *testing.T) {
	ev := eventWith("sub-1", map[string]string{"custom:repos": "acme/attr-a"})
	got := resolveClaims(context.Background(), nil, ev)
	assertReposClaim(t, got, []string{"acme/attr-a"})
}

// TestResolveClaims_NoSubjectUsesAttributes: an event with no sub (and no
// userName) cannot key the table, so it falls back to attributes.
func TestResolveClaims_NoSubjectUsesAttributes(t *testing.T) {
	reader := &fakeReader{repos: map[string][]string{}}
	ev := eventWith("", map[string]string{"custom:repos": "acme/attr-a"})
	got := resolveClaims(context.Background(), reader, ev)
	assertReposClaim(t, got, []string{"acme/attr-a"})
	if len(reader.listedSubs) != 0 {
		t.Fatalf("expected no table lookup with empty subject, listed %v", reader.listedSubs)
	}
}

// TestResolveClaims_DefaultRepoErrorOmitsDefaultButKeepsRepos: a default_repo
// read error must not fail closed on the whole token (repos claim is sound) and
// must not fabricate — default_repo is simply omitted.
func TestResolveClaims_DefaultRepoErrorOmitsDefaultButKeepsRepos(t *testing.T) {
	reader := &fakeReader{
		repos:      map[string][]string{"sub-1": {"acme/a"}},
		defaultErr: errors.New("dynamodb throttled on META"),
	}
	ev := eventWith("sub-1", nil)
	got := resolveClaims(context.Background(), reader, ev)
	assertReposClaim(t, got, []string{"acme/a"})
	if _, has := got[claimDefaultRepo]; has {
		t.Fatal("default_repo should be omitted on read error")
	}
}

// TestEventSubject_PrefersSubOverUserName proves the table is keyed by the `sub`
// standard attribute (the token's subject), with userName only as a fallback.
func TestEventSubject_PrefersSubOverUserName(t *testing.T) {
	ev := eventWith("the-sub", nil)
	ev.UserName = "the-username"
	if got := eventSubject(ev); got != "the-sub" {
		t.Fatalf("eventSubject = %q, want the-sub", got)
	}

	ev2 := eventWith("", nil)
	ev2.UserName = "the-username"
	if got := eventSubject(ev2); got != "the-username" {
		t.Fatalf("eventSubject fallback = %q, want the-username", got)
	}
}

// TestHandler_SetsOverrides proves the full event handler (with a table reader)
// wires the resolved claims onto the response's ClaimsToAddOrOverride.
func TestHandler_SetsOverrides(t *testing.T) {
	reader := &fakeReader{
		repos:    map[string][]string{"sub-1": {"acme/repo-a"}},
		defaults: map[string]string{"sub-1": "acme/repo-a"},
	}
	h := makeHandler(reader)
	ev := eventWith("sub-1", map[string]string{"email": "user@example.com"})

	out, err := h(context.Background(), ev)
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

// TestHandler_NoGrantsNoOverrides proves a user with no table grants AND no
// attribute grants gets NO claim overrides at all.
func TestHandler_NoGrantsNoOverrides(t *testing.T) {
	reader := &fakeReader{repos: map[string][]string{}}
	h := makeHandler(reader)
	ev := eventWith("sub-1", map[string]string{"email": "user@example.com"})

	out, err := h(context.Background(), ev)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(out.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride) != 0 {
		t.Errorf("expected no claim overrides, got %v", out.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride)
	}
}

// TestHandler_NilReaderFallsBackToAttributes proves the handler works with no
// table configured (attribute-only), preserving the pre-P4.1 behavior.
func TestHandler_NilReaderFallsBackToAttributes(t *testing.T) {
	h := makeHandler(nil)
	ev := eventWith("sub-1", map[string]string{"custom:repos": `["acme/repo-a"]`, "custom:default_repo": "acme/repo-a"})

	out, err := h(context.Background(), ev)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	claims := out.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride
	if claims["repos"] != `["acme/repo-a"]` {
		t.Errorf("repos override = %q, want [\"acme/repo-a\"]", claims["repos"])
	}
}

// --- helpers ---------------------------------------------------------------

func assertReposClaim(t *testing.T, got map[string]string, wantRepos []string) {
	t.Helper()
	raw, hasRepos := got[claimRepos]
	if wantRepos == nil {
		if hasRepos {
			t.Errorf("repos claim present (%q) but want absent", raw)
		}
		return
	}
	if !hasRepos {
		t.Fatalf("repos claim absent, want %v", wantRepos)
	}
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("repos claim %q is not a JSON array string: %v", raw, err)
	}
	if !reflect.DeepEqual(decoded, wantRepos) {
		t.Errorf("repos = %v, want %v (raw claim %q)", decoded, wantRepos, raw)
	}
}

func assertDefaultRepoClaim(t *testing.T, got map[string]string, wantDefaultRepo string) {
	t.Helper()
	def, hasDef := got[claimDefaultRepo]
	if wantDefaultRepo == "" {
		if hasDef {
			t.Errorf("default_repo claim present (%q) but want absent", def)
		}
		return
	}
	if def != wantDefaultRepo {
		t.Errorf("default_repo = %q, want %q", def, wantDefaultRepo)
	}
}
