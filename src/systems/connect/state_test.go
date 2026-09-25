package connect

import (
	"strings"
	"testing"
	"time"
)

func newTestSigner(t *testing.T) *StateSigner {
	t.Helper()
	s, err := NewStateSigner([]byte("test-hmac-key-0123456789"))
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	return s
}

func TestState_RoundTrip(t *testing.T) {
	s := newTestSigner(t)
	tok, err := s.Issue("sub-123")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Verify(tok, "sub-123"); err != nil {
		t.Fatalf("Verify of a fresh state failed: %v", err)
	}
}

func TestState_SubBinding(t *testing.T) {
	s := newTestSigner(t)
	tok, _ := s.Issue("sub-123")
	// A state minted for sub-123 must NOT verify for a different sub.
	if err := s.Verify(tok, "sub-999"); err == nil {
		t.Fatal("state bound to sub-123 must not verify for sub-999 (anti-CSRF)")
	}
}

func TestState_Tampered(t *testing.T) {
	s := newTestSigner(t)
	tok, _ := s.Issue("sub-123")
	// Flip the last character of the payload segment.
	parts := strings.SplitN(tok, ".", 2)
	mangled := parts[0][:len(parts[0])-1] + "X" + "." + parts[1]
	if err := s.Verify(mangled, "sub-123"); err == nil {
		t.Fatal("a tampered state must not verify")
	}
	// A totally bogus token.
	if err := s.Verify("not-a-state", "sub-123"); err == nil {
		t.Fatal("a malformed state must not verify")
	}
}

func TestState_WrongKey(t *testing.T) {
	s1 := newTestSigner(t)
	s2, _ := NewStateSigner([]byte("a-completely-different-key"))
	tok, _ := s1.Issue("sub-123")
	if err := s2.Verify(tok, "sub-123"); err == nil {
		t.Fatal("a state signed with a different key must not verify")
	}
}

func TestState_Expired(t *testing.T) {
	s := newTestSigner(t)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }
	tok, _ := s.Issue("sub-123")
	// Advance past the TTL.
	s.now = func() time.Time { return base.Add(stateTTL + time.Second) }
	if err := s.Verify(tok, "sub-123"); err == nil {
		t.Fatal("an expired state must not verify")
	}
}

func TestState_EmptyKeyRejected(t *testing.T) {
	if _, err := NewStateSigner(nil); err == nil {
		t.Fatal("empty key must be rejected (no fail-open state)")
	}
}

func TestState_EmptySubject(t *testing.T) {
	s := newTestSigner(t)
	if _, err := s.Issue("  "); err == nil {
		t.Fatal("Issue for empty subject must error")
	}
}
