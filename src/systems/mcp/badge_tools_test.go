package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
)

// scopedBadgeCtx returns a context carrying a tenant scoped to repo, mimicking
// what the resolver produces after a successful (live) GitHub-App authorization.
// AuthorizedRepos is intentionally EMPTY — exactly as it is for a GitHub token —
// to prove the handler no longer depends on that claim.
func scopedBadgeCtx(repo string) context.Context {
	return withTenant(context.Background(), &auth.Tenant{
		Repo:            repo,
		PartitionPrefix: auth.PartitionPrefixFor(repo),
		Subject:         "github:123",
		Actor:           "octocat",
		AuthorizedRepos: nil,
	})
}

// TestCreateBadgeURL_ScopedRepo proves a GitHub-token tenant (empty claim) can
// mint a badge for the repo it is scoped to — the regression the auth migration
// introduced. Both the implicit (no arg) and explicit-same-repo paths work, and
// badge signing is active via the test secret.
func TestCreateBadgeURL_ScopedRepo(t *testing.T) {
	t.Setenv("CAINBAN_BADGE_SECRET", "test-secret-abcdefghijklmnop")
	s := NewStateless()
	ctx := scopedBadgeCtx("hmain/cainban")

	for _, name := range []string{"implicit", "explicit-same"} {
		args := CreateBadgeURLArgs{}
		if name == "explicit-same" {
			args.Repo = "hmain/cainban"
		}
		res, out, err := s.handleCreateBadgeURL(ctx, nil, args)
		if err != nil {
			t.Fatalf("%s: unexpected transport error: %v", name, err)
		}
		if res != nil && res.IsError {
			t.Fatalf("%s: unexpected tool error: %+v", name, res.Content)
		}
		if out == nil || out.URL == "" {
			t.Fatalf("%s: expected a signed URL, got %+v", name, out)
		}
		if !strings.Contains(out.URL, "/badge/hmain/cainban.svg?sig=") {
			t.Errorf("%s: URL %q missing scoped path/signature", name, out.URL)
		}
	}
}

// TestCreateBadgeURL_RejectsOtherRepo proves a caller cannot mint a badge for a
// repo the connection is NOT scoped to. Scoping is anchored to the resolver's
// decision (the tenant's Repo), not to the untrusted argument.
func TestCreateBadgeURL_RejectsOtherRepo(t *testing.T) {
	t.Setenv("CAINBAN_BADGE_SECRET", "test-secret-abcdefghijklmnop")
	s := NewStateless()
	ctx := scopedBadgeCtx("hmain/cainban")

	res, out, err := s.handleCreateBadgeURL(ctx, nil, CreateBadgeURLArgs{Repo: "acme/other"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected a tool error rejecting the other repo, got %+v", res)
	}
	if out != nil {
		t.Errorf("expected no result on rejection, got %+v", out)
	}
}

// TestCreateBadgeURL_InvalidRepoArg proves a structurally invalid repo arg is a
// tool error, not a silently-signed malformed URL.
func TestCreateBadgeURL_InvalidRepoArg(t *testing.T) {
	t.Setenv("CAINBAN_BADGE_SECRET", "test-secret-abcdefghijklmnop")
	s := NewStateless()
	ctx := scopedBadgeCtx("hmain/cainban")

	res, _, err := s.handleCreateBadgeURL(ctx, nil, CreateBadgeURLArgs{Repo: "not-a-repo"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected a tool error for malformed repo, got %+v", res)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"4w", 28 * 24 * time.Hour, false},
		{"1y", 365 * 24 * time.Hour, false},
		{"", 0, true},
		{"d", 0, true},
		{"0d", 0, true},
		{"-1d", 0, true},
		{"1x", 0, true},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := parseDuration(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q) expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
