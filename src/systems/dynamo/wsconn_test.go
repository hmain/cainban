package dynamo

import (
	"context"
	"sort"
	"testing"
)

// newWSStore builds a Store over the shared fakeDDB with a repo partition
// prefix, so its connection rows land under REPO#<repo>#WSCONN exactly like
// production.
func newWSStore(f *fakeDDB, prefix string) *Store {
	return NewWithPrefix(f, "cainban", prefix)
}

func TestWSConnection_RegisterListDelete(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	s := newWSStore(f, "REPO#acme/widgets#")

	if err := s.PutWSConnection(ctx, "conn-1", "acme/widgets"); err != nil {
		t.Fatalf("PutWSConnection conn-1: %v", err)
	}
	if err := s.PutWSConnection(ctx, "conn-2", "acme/widgets"); err != nil {
		t.Fatalf("PutWSConnection conn-2: %v", err)
	}

	ids, err := s.ListWSConnectionIDs(ctx)
	if err != nil {
		t.Fatalf("ListWSConnectionIDs: %v", err)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "conn-1" || ids[1] != "conn-2" {
		t.Fatalf("want [conn-1 conn-2], got %v", ids)
	}

	// DeleteWSConnection (the $disconnect path) has no prefix: it resolves the
	// repo from the reverse-lookup row. Use a prefix-less store to mimic the
	// disconnect handler.
	disc := newWSStore(f, "")
	if err := disc.DeleteWSConnection(ctx, "conn-1"); err != nil {
		t.Fatalf("DeleteWSConnection conn-1: %v", err)
	}

	ids, err = s.ListWSConnectionIDs(ctx)
	if err != nil {
		t.Fatalf("ListWSConnectionIDs after delete: %v", err)
	}
	if len(ids) != 1 || ids[0] != "conn-2" {
		t.Fatalf("want [conn-2] after delete, got %v", ids)
	}
}

func TestWSConnection_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	a := newWSStore(f, "REPO#acme/a#")
	b := newWSStore(f, "REPO#acme/b#")

	if err := a.PutWSConnection(ctx, "conn-a", "acme/a"); err != nil {
		t.Fatalf("put a: %v", err)
	}
	if err := b.PutWSConnection(ctx, "conn-b", "acme/b"); err != nil {
		t.Fatalf("put b: %v", err)
	}

	aIDs, _ := a.ListWSConnectionIDs(ctx)
	bIDs, _ := b.ListWSConnectionIDs(ctx)
	if len(aIDs) != 1 || aIDs[0] != "conn-a" {
		t.Fatalf("repo a must see only its own connection, got %v", aIDs)
	}
	if len(bIDs) != 1 || bIDs[0] != "conn-b" {
		t.Fatalf("repo b must see only its own connection, got %v", bIDs)
	}
}

func TestWSConnection_DeleteInScope(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	s := newWSStore(f, "REPO#acme/widgets#")

	if err := s.PutWSConnection(ctx, "conn-x", "acme/widgets"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.DeleteWSConnectionInScope(ctx, "conn-x"); err != nil {
		t.Fatalf("DeleteWSConnectionInScope: %v", err)
	}
	ids, _ := s.ListWSConnectionIDs(ctx)
	if len(ids) != 0 {
		t.Fatalf("want empty after in-scope delete, got %v", ids)
	}
}

func TestWSConnection_DeleteMissingIsNoError(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	disc := newWSStore(f, "")
	// $disconnect for a connection that was never registered (lost $connect or
	// double $disconnect) must be idempotent, not an error.
	if err := disc.DeleteWSConnection(ctx, "ghost"); err != nil {
		t.Fatalf("DeleteWSConnection ghost: want nil, got %v", err)
	}
}
