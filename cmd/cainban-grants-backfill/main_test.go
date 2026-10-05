package main

import "testing"

// a single-grant/no-default subject gets exactly one write to that repo.
func TestDecideBackfill_SingleGrantNoDefault_Writes(t *testing.T) {
	writes, st := decideBackfill([]subjectState{
		{subject: "alice", grants: []string{"acme/widgets"}},
	})
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
	if writes[0].subject != "alice" || writes[0].repo != "acme/widgets" {
		t.Fatalf("write = %+v, want {alice acme/widgets}", writes[0])
	}
	if st.writes != 1 || st.subjectsScanned != 1 {
		t.Fatalf("stats = %+v, want writes=1 scanned=1", st)
	}
}

// a multi-grant subject is skipped (multi-repo users choose their own default).
func TestDecideBackfill_MultiGrant_Skipped(t *testing.T) {
	writes, st := decideBackfill([]subjectState{
		{subject: "bob", grants: []string{"acme/a", "acme/b"}},
	})
	if len(writes) != 0 {
		t.Fatalf("writes = %d, want 0 for a multi-grant subject", len(writes))
	}
	if st.skippedMulti != 1 {
		t.Fatalf("skippedMulti = %d, want 1", st.skippedMulti)
	}
}

// a subject that already has a default is never clobbered.
func TestDecideBackfill_AlreadyDefault_Skipped(t *testing.T) {
	writes, st := decideBackfill([]subjectState{
		{subject: "carol", grants: []string{"acme/widgets"}, hasDefault: true},
	})
	if len(writes) != 0 {
		t.Fatalf("writes = %d, want 0 when a default already exists", len(writes))
	}
	if st.skippedHasDef != 1 {
		t.Fatalf("skippedHasDef = %d, want 1", st.skippedHasDef)
	}
}

// a subject with no grants (e.g. an identity-only row) is counted but not written.
func TestDecideBackfill_NoGrant_Skipped(t *testing.T) {
	writes, st := decideBackfill([]subjectState{
		{subject: "dave"}, // no grants, no default
	})
	if len(writes) != 0 {
		t.Fatalf("writes = %d, want 0 for a no-grant subject", len(writes))
	}
	if st.skippedNoGrant != 1 {
		t.Fatalf("skippedNoGrant = %d, want 1", st.skippedNoGrant)
	}
}

// a mixed table yields writes only for the eligible subjects, deterministically
// ordered by subject.
func TestDecideBackfill_MixedTable_OnlyEligible(t *testing.T) {
	writes, st := decideBackfill([]subjectState{
		{subject: "bob", grants: []string{"acme/a", "acme/b"}},           // multi -> skip
		{subject: "alice", grants: []string{"acme/widgets"}},             // eligible
		{subject: "carol", grants: []string{"acme/x"}, hasDefault: true}, // has default -> skip
		{subject: "erin", grants: []string{"acme/only"}},                 // eligible
		{subject: "dave"}, // no grant -> skip
	})
	if len(writes) != 2 {
		t.Fatalf("writes = %d, want 2 (alice, erin)", len(writes))
	}
	// sorted by subject
	if writes[0].subject != "alice" || writes[1].subject != "erin" {
		t.Fatalf("writes not sorted by subject: %+v", writes)
	}
	if st.subjectsScanned != 5 || st.writes != 2 || st.skippedMulti != 1 || st.skippedHasDef != 1 || st.skippedNoGrant != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// idempotence: feeding back the SAME table after the writes have landed (the
// eligible subjects now carry hasDefault) yields zero further writes.
func TestDecideBackfill_Idempotent(t *testing.T) {
	first := []subjectState{
		{subject: "alice", grants: []string{"acme/widgets"}},
		{subject: "erin", grants: []string{"acme/only"}},
	}
	writes, _ := decideBackfill(first)
	if len(writes) != 2 {
		t.Fatalf("first pass writes = %d, want 2", len(writes))
	}
	// Simulate the applied state: the written subjects now have a default.
	applied := make([]subjectState, len(first))
	copy(applied, first)
	for i := range applied {
		applied[i].hasDefault = true
	}
	writes2, st2 := decideBackfill(applied)
	if len(writes2) != 0 {
		t.Fatalf("second pass writes = %d, want 0 (idempotent)", len(writes2))
	}
	if st2.skippedHasDef != 2 {
		t.Fatalf("second pass skippedHasDef = %d, want 2", st2.skippedHasDef)
	}
}
