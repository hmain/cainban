package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestWhoami_UnscopedReportsNoScope proves whoami on an UNSCOPED tenant
// (authenticated, no repo named, no default_repo) reports the no-scope message
// as NORMAL content — not an error, and without opening any store. Asking
// "where am I?" with no scope is a valid question whose answer is "nowhere yet",
// so unlike the data tools this must not fail closed with an error.
func TestWhoami_UnscopedReportsNoScope(t *testing.T) {
	s := NewStateless()
	ctx := withTenant(context.Background(), &auth.Tenant{
		Subject:  "user-1",
		Actor:    "user-1@example.com",
		Unscoped: true,
	})

	res, _, err := s.handleWhoami(ctx, nil, WhoamiArgs{})
	if err != nil {
		t.Fatalf("handleWhoami returned a transport error on unscoped tenant: %v", err)
	}
	if res.IsError {
		t.Error("unscoped whoami must be normal content, not isError")
	}
	if len(res.Content) == 0 {
		t.Fatal("unscoped whoami returned no content")
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "No repo in scope") {
		t.Errorf("whoami unscoped text = %q, want it to say there is no repo in scope", text)
	}
	if !strings.Contains(text, auth.HeaderTargetRepo) {
		t.Errorf("whoami unscoped text = %q, want it to name the %s remedy", text, auth.HeaderTargetRepo)
	}
}

// TestScopeLine_FormatsRepoBoard covers the one-line scope header prefixed to
// list_tasks / list_boards output in each tenant state. The board is always id
// 1 today (single board per tenant).
func TestScopeLine_FormatsRepoBoard(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "scoped multi-user",
			ctx: withTenant(context.Background(), &auth.Tenant{
				Repo:            "acme/widgets",
				PartitionPrefix: auth.PartitionPrefixFor("acme/widgets"),
			}),
			want: "Scope — repo: acme/widgets · board: default (id 1)",
		},
		{
			name: "unscoped tenant reads (local) — nothing authorized to show",
			ctx: withTenant(context.Background(), &auth.Tenant{
				Subject:  "user-1",
				Unscoped: true,
			}),
			want: "Scope — repo: (local) · board: default (id 1)",
		},
		{
			name: "local CLI (no tenant at all)",
			ctx:  context.Background(),
			want: "Scope — repo: (local) · board: default (id 1)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scopeLine(c.ctx); got != c.want {
				t.Errorf("scopeLine = %q, want %q", got, c.want)
			}
		})
	}
}

// textOf extracts the text from a TextContent, failing the test otherwise.
func textOf(t *testing.T, c any) string {
	t.Helper()
	tc, ok := c.(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", c)
	}
	return tc.Text
}
