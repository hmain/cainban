package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/hmain/cainban/src/systems/auth"
)

// TestResolveTaskSystem_UnscopedTenantFailsClosed proves the store-opening path
// refuses an UNSCOPED tenant (authenticated but no repo) BEFORE it opens any
// store. This is the isolation guarantee behind the repo-agnostic handshake
// fix: the handshake may proceed unscoped, but a data operation must never run
// with an empty partition prefix (which would collapse every tenant into one
// partition). The guard returns before OpenTaskForTenant, so this needs no DB.
func TestResolveTaskSystem_UnscopedTenantFailsClosed(t *testing.T) {
	s := NewStateless()
	ctx := withTenant(context.Background(), &auth.Tenant{
		Subject:  "user-1",
		Unscoped: true,
	})

	ts, closer, err := s.resolveTaskSystem(ctx, "default")
	if err == nil {
		if closer != nil {
			closer()
		}
		t.Fatal("resolveTaskSystem accepted an unscoped tenant; want a fail-closed error")
	}
	if ts != nil || closer != nil {
		t.Errorf("on fail-closed, store and closer must be nil (got ts=%v closer!=nil=%v)", ts, closer != nil)
	}
	if !strings.Contains(err.Error(), "no target repo") {
		t.Errorf("error = %q, want it to mention the missing target repo", err.Error())
	}
}
