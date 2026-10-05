package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/hmain/cainban/src/systems/auth"
)

// TestResolveBoardStore_UnscopedTenantFailsClosed is the board-path counterpart
// to TestResolveTaskSystem_UnscopedTenantFailsClosed. It proves the single most
// important rule in this feature: resolveBoardStore refuses an UNSCOPED tenant
// (authenticated but naming no repo) BEFORE opening any store. If it did not,
// list_boards on an empty partition prefix would enumerate EVERY tenant's
// boards — a cross-tenant data leak. The guard returns before
// OpenBoardForTenant, so this needs no DB.
func TestResolveBoardStore_UnscopedTenantFailsClosed(t *testing.T) {
	s := NewStateless()
	ctx := withTenant(context.Background(), &auth.Tenant{
		Subject:  "user-1",
		Unscoped: true,
	})

	bs, closer, err := s.resolveBoardStore(ctx)
	if err == nil {
		if closer != nil {
			closer()
		}
		t.Fatal("resolveBoardStore accepted an unscoped tenant; want a fail-closed error")
	}
	if bs != nil || closer != nil {
		t.Errorf("on fail-closed, store and closer must be nil (got bs=%v closer!=nil=%v)", bs, closer != nil)
	}
	if !strings.Contains(err.Error(), "no target repo") {
		t.Errorf("error = %q, want it to mention the missing target repo", err.Error())
	}
}
