package mcp

import (
	"strings"
	"testing"
)

// TestResolveDeviceScope covers the scope-selection logic for the GitHub device
// flow. The critical invariant (RFC: narrowing cainban's GitHub OAuth scope):
// the DEFAULT scope must NOT contain the broad classic `repo` scope — cainban
// only performs a read-only permission check, so the default is identity +
// org read. An explicit caller-supplied scope still flows through verbatim.
func TestResolveDeviceScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       string
		want     string
		wantRepo bool // whether the broad `repo` scope is expected in the result
	}{
		{
			name:     "empty defaults to narrowed identity+org read",
			in:       "",
			want:     "read:user user:email read:org",
			wantRepo: false,
		},
		{
			name:     "whitespace-only is treated as empty and narrowed",
			in:       "   ",
			want:     "read:user user:email read:org",
			wantRepo: false,
		},
		{
			name:     "explicit scope flows through verbatim",
			in:       "read:user",
			want:     "read:user",
			wantRepo: false,
		},
		{
			name:     "explicit repo scope is honored if the caller insists",
			in:       "read:user user:email repo",
			want:     "read:user user:email repo",
			wantRepo: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveDeviceScope(tc.in)
			if got != tc.want {
				t.Errorf("resolveDeviceScope(%q) = %q, want %q", tc.in, got, tc.want)
			}
			hasRepo := scopeHasRepo(got)
			if hasRepo != tc.wantRepo {
				t.Errorf("resolveDeviceScope(%q) repo-present = %v, want %v (got %q)", tc.in, hasRepo, tc.wantRepo, got)
			}
		})
	}
}

// TestDefaultDeviceScopeNeverHasRepo is the regression guard: the DEFAULT scope
// must never silently reacquire the broad `repo` scope. This is the test-side
// counterpart to the pre-commit grep gate.
func TestDefaultDeviceScopeNeverHasRepo(t *testing.T) {
	t.Parallel()
	def := resolveDeviceScope("")
	if scopeHasRepo(def) {
		t.Fatalf("default device scope %q must not contain the broad `repo` scope", def)
	}
	// Identity scopes we still require.
	for _, must := range []string{"read:user", "user:email", "read:org"} {
		if !strings.Contains(def, must) {
			t.Errorf("default device scope %q missing required scope %q", def, must)
		}
	}
}

// scopeHasRepo reports whether a space-delimited scope string contains the
// broad classic `repo` scope as a whole token (not a prefix like `read:repo`
// or a suffix like `public_repo`).
func scopeHasRepo(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == "repo" {
			return true
		}
	}
	return false
}
