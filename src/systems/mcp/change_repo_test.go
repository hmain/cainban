package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/hmain/cainban/src/systems/auth"
)

// TestPartitionPrefixForCall covers the stateless per-call repo switch: a tool's
// optional `repo` arg selects which authorized repo this one call acts on, and
// is authorized against the tenant's signed claim before any store opens.
func TestPartitionPrefixForCall(t *testing.T) {
	scoped := withTenant(context.Background(), &auth.Tenant{
		Repo:            "acme/a",
		PartitionPrefix: auth.PartitionPrefixFor("acme/a"),
		AuthorizedRepos: []string{"acme/a", "acme/b"},
	})
	unscoped := withTenant(context.Background(), &auth.Tenant{
		Subject:         "u1",
		Unscoped:        true,
		AuthorizedRepos: []string{"acme/a", "acme/b"},
	})

	cases := []struct {
		name       string
		ctx        context.Context
		argRepo    string
		wantPrefix string
		wantErr    string // substring; "" means no error
	}{
		{
			name:       "no tenant (local CLI) → empty prefix, override ignored",
			ctx:        context.Background(),
			argRepo:    "acme/b",
			wantPrefix: "",
		},
		{
			name:       "scoped, no override → tenant's edge repo",
			ctx:        scoped,
			argRepo:    "",
			wantPrefix: auth.PartitionPrefixFor("acme/a"),
		},
		{
			name:       "scoped, override to another authorized repo → that repo's prefix",
			ctx:        scoped,
			argRepo:    "acme/b",
			wantPrefix: auth.PartitionPrefixFor("acme/b"),
		},
		{
			name:    "scoped, override to an UNauthorized repo → forbidden",
			ctx:     scoped,
			argRepo: "acme/secret",
			wantErr: "not authorized for repo",
		},
		{
			name:    "scoped, structurally invalid override → error",
			ctx:     scoped,
			argRepo: "not-a-repo",
			wantErr: "invalid repo",
		},
		{
			name:       "unscoped, override to an authorized repo → that repo's prefix (runtime pick with no default_repo)",
			ctx:        unscoped,
			argRepo:    "acme/a",
			wantPrefix: auth.PartitionPrefixFor("acme/a"),
		},
		{
			name:    "unscoped, no override → fail closed",
			ctx:     unscoped,
			argRepo: "",
			wantErr: "no target repo",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := partitionPrefixForCall(c.ctx, c.argRepo)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (prefix %q)", c.wantErr, got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.wantPrefix {
				t.Errorf("prefix = %q, want %q", got, c.wantPrefix)
			}
		})
	}
}

// TestChangeRepo_ListsAuthorized proves change_repo with no arg lists exactly
// the token's authorized repos and marks the current default.
func TestChangeRepo_ListsAuthorized(t *testing.T) {
	s := NewStateless()
	ctx := withTenant(context.Background(), &auth.Tenant{
		Repo:            "acme/a",
		PartitionPrefix: auth.PartitionPrefixFor("acme/a"),
		AuthorizedRepos: []string{"acme/a", "acme/b"},
	})

	res, out, err := s.handleChangeRepo(ctx, nil, ChangeRepoArgs{})
	if err != nil {
		t.Fatalf("handleChangeRepo error: %v", err)
	}
	if res.IsError {
		t.Fatal("listing authorized repos must not be an error")
	}
	if out == nil || len(out.AuthorizedRepos) != 2 {
		t.Fatalf("structured result = %+v, want 2 authorized repos", out)
	}
	if out.CurrentRepo != "acme/a" {
		t.Errorf("current repo = %q, want acme/a", out.CurrentRepo)
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "acme/a") || !strings.Contains(text, "acme/b") {
		t.Errorf("listing text = %q, want both repos", text)
	}
	if !strings.Contains(text, "current default") {
		t.Errorf("listing text = %q, want the current default marked", text)
	}
}

// TestChangeRepo_ValidatesTarget proves change_repo authorizes a named target
// against the signed claim: an authorized repo is OK, an unauthorized one is a
// tool error that lists what IS authorized.
func TestChangeRepo_ValidatesTarget(t *testing.T) {
	s := NewStateless()
	ctx := withTenant(context.Background(), &auth.Tenant{
		Repo:            "acme/a",
		PartitionPrefix: auth.PartitionPrefixFor("acme/a"),
		AuthorizedRepos: []string{"acme/a", "acme/b"},
	})

	// Authorized target.
	res, out, err := s.handleChangeRepo(ctx, nil, ChangeRepoArgs{Repo: "acme/b"})
	if err != nil {
		t.Fatalf("handleChangeRepo error: %v", err)
	}
	if res.IsError {
		t.Fatal("an authorized target must not be a tool error")
	}
	if !out.Authorized || out.Requested != "acme/b" {
		t.Errorf("result = %+v, want Requested=acme/b Authorized=true", out)
	}

	// Unauthorized target.
	res2, out2, err := s.handleChangeRepo(ctx, nil, ChangeRepoArgs{Repo: "acme/secret"})
	if err != nil {
		t.Fatalf("handleChangeRepo error: %v", err)
	}
	if !res2.IsError {
		t.Fatal("an unauthorized target must be a tool error")
	}
	if out2 == nil || out2.Authorized {
		t.Errorf("result = %+v, want Authorized=false", out2)
	}
	text := textOf(t, res2.Content[0])
	if !strings.Contains(text, "not authorized") {
		t.Errorf("error text = %q, want it to say not authorized", text)
	}
}

// TestChangeRepo_NoGrants proves a token with no grants gets the remedy message
// as normal content (not an error).
func TestChangeRepo_NoGrants(t *testing.T) {
	s := NewStateless()
	ctx := withTenant(context.Background(), &auth.Tenant{
		Subject:         "u1",
		Unscoped:        true,
		AuthorizedRepos: nil,
	})

	res, out, err := s.handleChangeRepo(ctx, nil, ChangeRepoArgs{})
	if err != nil {
		t.Fatalf("handleChangeRepo error: %v", err)
	}
	if res.IsError {
		t.Fatal("no-grants change_repo must be normal content, not an error")
	}
	if out == nil || len(out.AuthorizedRepos) != 0 {
		t.Fatalf("result = %+v, want empty authorized set", out)
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "no repos") && !strings.Contains(text, "authorizes no") {
		t.Errorf("text = %q, want it to say the token authorizes no repos", text)
	}
}

// TestChangeRepo_LocalCLI proves the no-tenant path reports a single local
// scope rather than erroring.
func TestChangeRepo_LocalCLI(t *testing.T) {
	s := NewStateless()
	res, out, err := s.handleChangeRepo(context.Background(), nil, ChangeRepoArgs{})
	if err != nil {
		t.Fatalf("handleChangeRepo error: %v", err)
	}
	if res.IsError {
		t.Fatal("local CLI change_repo must not be an error")
	}
	if out.CurrentRepo != "(local)" {
		t.Errorf("current repo = %q, want (local)", out.CurrentRepo)
	}
}
