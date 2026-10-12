package mcp

import (
	"context"
	"testing"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/githubref"
	"github.com/hmain/cainban/src/systems/task"
)

// enrichGitHubRefs attaches a derived, non-persisted GitHub reference to each
// task at the read/serialize boundary. These tests pin that behavior: a scoped
// tenant resolves a bare "#N" against its board repo; an unscoped tenant leaves
// a bare reference unresolved (nothing to resolve against) but a full URL still
// parses; a card with no reference gets a nil ref so omitempty drops the field.

func TestEnrichGitHubRefs_ScopedResolvesBareRef(t *testing.T) {
	ctx := withTenant(context.Background(), &auth.Tenant{
		Repo:            "acme/a",
		PartitionPrefix: auth.PartitionPrefixFor("acme/a"),
		AuthorizedRepos: []string{"acme/a"},
	})

	tasks := []*task.Task{
		{BoardTaskID: 1, Title: "Fix login #42", Description: ""},
		{BoardTaskID: 2, Title: "No reference here", Description: "nothing"},
		{BoardTaskID: 3, Title: "See the PR", Description: "https://github.com/acme/a/pull/9"},
	}

	enrichGitHubRefs(ctx, tasks)

	if tasks[0].GitHubRef == nil {
		t.Fatal("task #1 should have a derived ref")
	}
	if got := tasks[0].GitHubRef; got.Kind != githubref.KindIssue || got.Number != 42 ||
		got.Repo != "acme/a" || got.URL != "https://github.com/acme/a/issues/42" {
		t.Errorf("task #1 ref = %+v, want issue #42 on acme/a", got)
	}
	if tasks[1].GitHubRef != nil {
		t.Errorf("task #2 should have no ref, got %+v", tasks[1].GitHubRef)
	}
	if tasks[2].GitHubRef == nil || tasks[2].GitHubRef.Kind != githubref.KindPR ||
		tasks[2].GitHubRef.Number != 9 {
		t.Errorf("task #3 ref = %+v, want PR #9", tasks[2].GitHubRef)
	}
}

func TestEnrichGitHubRefs_UnscopedLeavesBareUnresolved(t *testing.T) {
	// Unscoped tenant: repoFromCtx returns "", so a bare "#42" cannot resolve,
	// but an absolute github.com URL still parses on its own.
	ctx := withTenant(context.Background(), &auth.Tenant{
		Subject:         "u1",
		Unscoped:        true,
		AuthorizedRepos: []string{"acme/a"},
	})

	tasks := []*task.Task{
		{BoardTaskID: 1, Title: "Fix login #42"},
		{BoardTaskID: 2, Title: "Full URL", Description: "https://github.com/x/y/issues/3"},
	}

	enrichGitHubRefs(ctx, tasks)

	if tasks[0].GitHubRef != nil {
		t.Errorf("bare ref must stay unresolved for an unscoped tenant, got %+v", tasks[0].GitHubRef)
	}
	if tasks[1].GitHubRef == nil || tasks[1].GitHubRef.Repo != "x/y" || tasks[1].GitHubRef.Number != 3 {
		t.Errorf("full URL should parse regardless of scope, got %+v", tasks[1].GitHubRef)
	}
}

func TestEnrichGitHubRef_SingleNilSafe(t *testing.T) {
	ctx := context.Background()
	if got := enrichGitHubRef(ctx, nil); got != nil {
		t.Errorf("enrichGitHubRef(nil) = %+v, want nil", got)
	}
}
