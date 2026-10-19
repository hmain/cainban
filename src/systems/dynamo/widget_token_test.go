package dynamo

import (
	"context"
	"testing"
	"time"
)

// These tests exercise the DynamoDB widget-token metadata store against the
// in-memory fake client (the dev/CI host has no DynamoDB Local), covering the
// put/get/list/revoke round-trip and the structural per-prefix isolation that
// keeps one repo's tokens invisible to another.

func newWidgetStore(t *testing.T, prefix string) *Store {
	t.Helper()
	return NewWithPrefix(newFakeDDB(), DefaultTableName, prefix)
}

func TestWidgetToken_PutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newWidgetStore(t, "REPO#acme/a#")

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	expires := created.Add(30 * 24 * time.Hour)
	rec := WidgetTokenRecord{
		TokenID:   "tok-1",
		Repo:      "acme/a",
		CreatedBy: "dev@example.com",
		CreatedAt: created,
		ExpiresAt: expires,
		Revoked:   false,
	}
	if err := s.PutWidgetToken(ctx, rec); err != nil {
		t.Fatalf("PutWidgetToken: %v", err)
	}

	got, err := s.GetWidgetToken(ctx, "tok-1")
	if err != nil {
		t.Fatalf("GetWidgetToken: %v", err)
	}
	if got == nil {
		t.Fatal("expected token, got nil")
	}
	if got.Repo != "acme/a" || got.CreatedBy != "dev@example.com" || got.Revoked {
		t.Errorf("unexpected record: %+v", got)
	}
	if !got.CreatedAt.Equal(created) || !got.ExpiresAt.Equal(expires) {
		t.Errorf("timestamps did not round-trip: created=%v expires=%v", got.CreatedAt, got.ExpiresAt)
	}
}

func TestWidgetToken_GetMissingReturnsNil(t *testing.T) {
	s := newWidgetStore(t, "REPO#acme/a#")
	got, err := s.GetWidgetToken(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetWidgetToken: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing token, got %+v", got)
	}
}

func TestWidgetToken_ListNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := newWidgetStore(t, "REPO#acme/a#")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"old", "mid", "new"} {
		rec := WidgetTokenRecord{
			TokenID:   id,
			Repo:      "acme/a",
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
			ExpiresAt: base.Add(time.Duration(i)*time.Hour + 24*time.Hour),
		}
		if err := s.PutWidgetToken(ctx, rec); err != nil {
			t.Fatalf("PutWidgetToken %s: %v", id, err)
		}
	}
	recs, err := s.ListWidgetTokens(ctx)
	if err != nil {
		t.Fatalf("ListWidgetTokens: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("len = %d, want 3", len(recs))
	}
	if recs[0].TokenID != "new" || recs[2].TokenID != "old" {
		t.Errorf("order = [%s ... %s], want newest-first", recs[0].TokenID, recs[2].TokenID)
	}
}

func TestWidgetToken_Revoke(t *testing.T) {
	ctx := context.Background()
	s := newWidgetStore(t, "REPO#acme/a#")
	rec := WidgetTokenRecord{TokenID: "tok-1", Repo: "acme/a", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.PutWidgetToken(ctx, rec); err != nil {
		t.Fatalf("PutWidgetToken: %v", err)
	}

	ok, err := s.RevokeWidgetToken(ctx, "tok-1")
	if err != nil {
		t.Fatalf("RevokeWidgetToken: %v", err)
	}
	if !ok {
		t.Fatal("expected revoke to report success")
	}
	got, err := s.GetWidgetToken(ctx, "tok-1")
	if err != nil {
		t.Fatalf("GetWidgetToken: %v", err)
	}
	if got == nil || !got.Revoked {
		t.Errorf("token should be revoked, got %+v", got)
	}
}

func TestWidgetToken_RevokeMissingReportsFalse(t *testing.T) {
	s := newWidgetStore(t, "REPO#acme/a#")
	ok, err := s.RevokeWidgetToken(context.Background(), "nope")
	if err != nil {
		t.Fatalf("RevokeWidgetToken: %v", err)
	}
	if ok {
		t.Error("expected false for a missing token")
	}
}

func TestWidgetToken_PrefixIsolation(t *testing.T) {
	ctx := context.Background()
	client := newFakeDDB()
	a := NewWithPrefix(client, DefaultTableName, "REPO#acme/a#")
	b := NewWithPrefix(client, DefaultTableName, "REPO#acme/b#")

	if err := a.PutWidgetToken(ctx, WidgetTokenRecord{TokenID: "a-tok", Repo: "acme/a", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Put a: %v", err)
	}

	// B must not see A's token by id or in a listing.
	got, err := b.GetWidgetToken(ctx, "a-tok")
	if err != nil {
		t.Fatalf("Get b: %v", err)
	}
	if got != nil {
		t.Error("repo B must not read repo A's token")
	}
	recs, err := b.ListWidgetTokens(ctx)
	if err != nil {
		t.Fatalf("List b: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("repo B listing should be empty, got %d", len(recs))
	}
	// And B revoking A's id is a no-op (not found in B's scope).
	ok, err := b.RevokeWidgetToken(ctx, "a-tok")
	if err != nil {
		t.Fatalf("Revoke b: %v", err)
	}
	if ok {
		t.Error("repo B must not revoke repo A's token")
	}
}
