package grants

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDDB is an in-memory DynamoDB API fake: no DynamoDB Local, no live AWS. It
// stores items keyed by "PK\x00SK" and implements just the operations the
// grants Store issues (GetItem, PutItem, DeleteItem, Query with a
// begins_with(SK, :prefix) key condition). Set failErr to make every call
// return that error, which is how the fail-closed-on-error tests are driven.
type fakeDDB struct {
	items   map[string]map[string]ddbtypes.AttributeValue
	failErr error
	// calls records operation names for assertions (e.g. "no DDB call on bad repo").
	calls []string
}

func newFakeDDB() *fakeDDB {
	return &fakeDDB{items: map[string]map[string]ddbtypes.AttributeValue{}}
}

func keyOf(item map[string]ddbtypes.AttributeValue) string {
	pk, _ := item["PK"].(*ddbtypes.AttributeValueMemberS)
	sk, _ := item["SK"].(*ddbtypes.AttributeValueMemberS)
	var pkv, skv string
	if pk != nil {
		pkv = pk.Value
	}
	if sk != nil {
		skv = sk.Value
	}
	return pkv + "\x00" + skv
}

func (f *fakeDDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.calls = append(f.calls, "GetItem")
	if f.failErr != nil {
		return nil, f.failErr
	}
	it := f.items[keyOf(in.Key)]
	return &dynamodb.GetItemOutput{Item: it}, nil
}

func (f *fakeDDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.calls = append(f.calls, "PutItem")
	if f.failErr != nil {
		return nil, f.failErr
	}
	f.items[keyOf(in.Item)] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.calls = append(f.calls, "DeleteItem")
	if f.failErr != nil {
		return nil, f.failErr
	}
	delete(f.items, keyOf(in.Key))
	return &dynamodb.DeleteItemOutput{}, nil
}

// Query implements the single "PK = :pk AND begins_with(SK, :prefix)" shape the
// Store uses. It ignores pagination (returns everything in one page), which is
// sufficient for the fake.
func (f *fakeDDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.calls = append(f.calls, "Query")
	if f.failErr != nil {
		return nil, f.failErr
	}
	pkAV, _ := in.ExpressionAttributeValues[":pk"].(*ddbtypes.AttributeValueMemberS)
	prefixAV, _ := in.ExpressionAttributeValues[":prefix"].(*ddbtypes.AttributeValueMemberS)
	var pk, prefix string
	if pkAV != nil {
		pk = pkAV.Value
	}
	if prefixAV != nil {
		prefix = prefixAV.Value
	}
	var out []map[string]ddbtypes.AttributeValue
	for _, itm := range f.items {
		p, _ := itm["PK"].(*ddbtypes.AttributeValueMemberS)
		s, _ := itm["SK"].(*ddbtypes.AttributeValueMemberS)
		if p == nil || s == nil {
			continue
		}
		if p.Value == pk && len(s.Value) >= len(prefix) && s.Value[:len(prefix)] == prefix {
			out = append(out, itm)
		}
	}
	return &dynamodb.QueryOutput{Items: out}, nil
}

const testSubject = "sub-abc-123"

func TestPutListDeleteGrant(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	s := New(f, "cainban-grants")

	// Empty subject -> empty list, no error, no DDB call.
	if repos, err := s.ListReposForSubject(ctx, ""); err != nil || repos != nil {
		t.Fatalf("empty subject: got (%v,%v), want (nil,nil)", repos, err)
	}

	// No grants yet -> empty.
	repos, err := s.ListReposForSubject(ctx, testSubject)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected no grants, got %v", repos)
	}

	// Put two grants (out of sorted order) and one more.
	for _, r := range []string{"acme/repo-b", "acme/repo-a", "acme/repo-c"} {
		if err := s.PutGrant(ctx, testSubject, r); err != nil {
			t.Fatalf("put %s: %v", r, err)
		}
	}

	repos, err = s.ListReposForSubject(ctx, testSubject)
	if err != nil {
		t.Fatalf("list after put: %v", err)
	}
	want := []string{"acme/repo-a", "acme/repo-b", "acme/repo-c"}
	if !reflect.DeepEqual(repos, want) {
		t.Fatalf("list = %v, want %v (sorted)", repos, want)
	}

	// Get present + absent.
	if ok, err := s.Get(ctx, testSubject, "acme/repo-a"); err != nil || !ok {
		t.Fatalf("Get present: got (%v,%v), want (true,nil)", ok, err)
	}
	if ok, err := s.Get(ctx, testSubject, "acme/repo-x"); err != nil || ok {
		t.Fatalf("Get absent: got (%v,%v), want (false,nil)", ok, err)
	}

	// Delete one; it disappears, the rest remain.
	if err := s.DeleteGrant(ctx, testSubject, "acme/repo-b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	repos, err = s.ListReposForSubject(ctx, testSubject)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if !reflect.DeepEqual(repos, []string{"acme/repo-a", "acme/repo-c"}) {
		t.Fatalf("after delete list = %v, want [acme/repo-a acme/repo-c]", repos)
	}

	// Deleting a non-existent grant is a no-op (no error).
	if err := s.DeleteGrant(ctx, testSubject, "acme/repo-b"); err != nil {
		t.Fatalf("delete non-existent should be no-op, got %v", err)
	}
}

func TestPutGrant_NormalizesAndRejectsBadRepo(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	s := New(f, "cainban-grants")

	// A grant put with surrounding whitespace is stored under the normalized
	// (trimmed) repo, so the list reflects the canonical form.
	if err := s.PutGrant(ctx, testSubject, "  acme/repo-a  "); err != nil {
		t.Fatalf("put with whitespace: %v", err)
	}
	repos, _ := s.ListReposForSubject(ctx, testSubject)
	if !reflect.DeepEqual(repos, []string{"acme/repo-a"}) {
		t.Fatalf("normalized list = %v, want [acme/repo-a]", repos)
	}

	// A structurally invalid repo is rejected BEFORE any DynamoDB write.
	f2 := newFakeDDB()
	s2 := New(f2, "cainban-grants")
	for _, bad := range []string{"", "no-slash", "a/b/c", "owner/", "/repo"} {
		if err := s2.PutGrant(ctx, testSubject, bad); err == nil {
			t.Fatalf("PutGrant(%q) should have errored", bad)
		}
	}
	if len(f2.calls) != 0 {
		t.Fatalf("bad repo made DynamoDB calls %v, want none", f2.calls)
	}
}

func TestDefaultRepo(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	s := New(f, "cainban-grants")

	// Unset -> "".
	if def, err := s.GetDefaultRepo(ctx, testSubject); err != nil || def != "" {
		t.Fatalf("unset default: got (%q,%v), want (\"\",nil)", def, err)
	}

	// Set, read back.
	if err := s.SetDefaultRepo(ctx, testSubject, "acme/repo-a"); err != nil {
		t.Fatalf("set default: %v", err)
	}
	if def, err := s.GetDefaultRepo(ctx, testSubject); err != nil || def != "acme/repo-a" {
		t.Fatalf("get default: got (%q,%v), want (acme/repo-a,nil)", def, err)
	}

	// Clear (empty repo) -> "".
	if err := s.SetDefaultRepo(ctx, testSubject, ""); err != nil {
		t.Fatalf("clear default: %v", err)
	}
	if def, err := s.GetDefaultRepo(ctx, testSubject); err != nil || def != "" {
		t.Fatalf("after clear: got (%q,%v), want (\"\",nil)", def, err)
	}

	// A malformed non-empty default is rejected.
	if err := s.SetDefaultRepo(ctx, testSubject, "no-slash"); err == nil {
		t.Fatalf("SetDefaultRepo with bad repo should error")
	}
}

// TestErrorsAreReturnedUnmasked proves the fail-closed contract at the store
// layer: a DynamoDB error is propagated, NEVER swallowed into an empty/false
// success. The pre-token trigger relies on this to fail closed.
func TestErrorsAreReturnedUnmasked(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("dynamodb unavailable")
	f := &fakeDDB{items: map[string]map[string]ddbtypes.AttributeValue{}, failErr: boom}
	s := New(f, "cainban-grants")

	if _, err := s.ListReposForSubject(ctx, testSubject); err == nil {
		t.Fatal("ListReposForSubject swallowed a DynamoDB error")
	}
	if _, err := s.Get(ctx, testSubject, "acme/repo-a"); err == nil {
		t.Fatal("Get swallowed a DynamoDB error")
	}
	if _, err := s.GetDefaultRepo(ctx, testSubject); err == nil {
		t.Fatal("GetDefaultRepo swallowed a DynamoDB error")
	}
	if err := s.PutGrant(ctx, testSubject, "acme/repo-a"); err == nil {
		t.Fatal("PutGrant swallowed a DynamoDB error")
	}
	if err := s.DeleteGrant(ctx, testSubject, "acme/repo-a"); err == nil {
		t.Fatal("DeleteGrant swallowed a DynamoDB error")
	}
}

// TestNewDefaultsTableName proves an empty table name falls back to the default.
func TestNewDefaultsTableName(t *testing.T) {
	s := New(newFakeDDB(), "")
	if s.table != DefaultTableName {
		t.Fatalf("table = %q, want %q", s.table, DefaultTableName)
	}
}

// TestIdentity covers the linked-GitHub-identity item the P4.3 connect callback
// writes and the connect API reads.
func TestIdentity(t *testing.T) {
	ctx := context.Background()
	f := newFakeDDB()
	s := New(f, "cainban-grants")

	// Unset -> "".
	if login, err := s.GetIdentity(ctx, testSubject); err != nil || login != "" {
		t.Fatalf("unset identity: got (%q,%v), want (\"\",nil)", login, err)
	}
	// Empty subject -> ("",nil), no call.
	if login, err := s.GetIdentity(ctx, ""); err != nil || login != "" {
		t.Fatalf("empty subject identity: got (%q,%v), want (\"\",nil)", login, err)
	}

	// Put, read back.
	if err := s.PutIdentity(ctx, testSubject, "octocat"); err != nil {
		t.Fatalf("put identity: %v", err)
	}
	if login, err := s.GetIdentity(ctx, testSubject); err != nil || login != "octocat" {
		t.Fatalf("get identity: got (%q,%v), want (octocat,nil)", login, err)
	}

	// Re-link overwrites.
	if err := s.PutIdentity(ctx, testSubject, "  hubber  "); err != nil {
		t.Fatalf("re-link: %v", err)
	}
	if login, _ := s.GetIdentity(ctx, testSubject); login != "hubber" {
		t.Fatalf("re-linked identity = %q, want hubber (trimmed)", login)
	}

	// Empty subject / empty login are rejected before any write.
	if err := s.PutIdentity(ctx, "", "x"); err == nil {
		t.Fatal("PutIdentity with empty subject should error")
	}
	if err := s.PutIdentity(ctx, testSubject, "   "); err == nil {
		t.Fatal("PutIdentity with empty login should error")
	}
}

// TestIdentityErrorsUnmasked proves the fail-closed contract for identity I/O.
func TestIdentityErrorsUnmasked(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("dynamodb unavailable")
	f := &fakeDDB{items: map[string]map[string]ddbtypes.AttributeValue{}, failErr: boom}
	s := New(f, "cainban-grants")

	if _, err := s.GetIdentity(ctx, testSubject); err == nil {
		t.Fatal("GetIdentity swallowed a DynamoDB error")
	}
	if err := s.PutIdentity(ctx, testSubject, "octocat"); err == nil {
		t.Fatal("PutIdentity swallowed a DynamoDB error")
	}
}
