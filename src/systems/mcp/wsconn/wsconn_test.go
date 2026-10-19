package wsconn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDDB records the inputs it receives and returns canned outputs, so the
// store's request shaping (keys, conditions, TTL, GSI query) is asserted
// without a live table.
type fakeDDB struct {
	putItems    []*dynamodb.PutItemInput
	updateItems []*dynamodb.UpdateItemInput
	deleteItems []*dynamodb.DeleteItemInput
	queries     []*dynamodb.QueryInput

	queryPages []*dynamodb.QueryOutput // returned in order, one per Query call
	queryErr   error
	updateErr  error
}

func (f *fakeDDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putItems = append(f.putItems, in)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateItems = append(f.updateItems, in)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeDDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.deleteItems = append(f.deleteItems, in)
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *fakeDDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.queries = append(f.queries, in)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	idx := len(f.queries) - 1
	if idx < len(f.queryPages) {
		return f.queryPages[idx], nil
	}
	return &dynamodb.QueryOutput{}, nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestAddStoresUnauthenticatedWithTTL(t *testing.T) {
	f := &fakeDDB{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(f, "conns").WithClock(fixedClock(now))

	if err := s.Add(context.Background(), "C1", "acme/repo-a"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(f.putItems) != 1 {
		t.Fatalf("expected 1 PutItem, got %d", len(f.putItems))
	}
	item := f.putItems[0].Item
	if v, ok := item[AttrConnectionID].(*ddbtypes.AttributeValueMemberS); !ok || v.Value != "C1" {
		t.Errorf("connectionId = %+v, want C1", item[AttrConnectionID])
	}
	if v, ok := item[AttrRepo].(*ddbtypes.AttributeValueMemberS); !ok || v.Value != "acme/repo-a" {
		t.Errorf("repo = %+v, want acme/repo-a", item[AttrRepo])
	}
	if v, ok := item[AttrAuthenticated].(*ddbtypes.AttributeValueMemberBOOL); !ok || v.Value {
		t.Errorf("authenticated = %+v, want false", item[AttrAuthenticated])
	}
	if _, ok := item[AttrExpiresAt].(*ddbtypes.AttributeValueMemberN); !ok {
		t.Errorf("expiresAt missing/not numeric: %+v", item[AttrExpiresAt])
	}
}

func TestAddEmptyConnectionIDErrors(t *testing.T) {
	s := New(&fakeDDB{}, "conns")
	if err := s.Add(context.Background(), "", "acme/repo"); err == nil {
		t.Fatal("expected error for empty connectionId")
	}
}

func TestAuthenticateSetsFlagRepoSub(t *testing.T) {
	f := &fakeDDB{}
	s := New(f, "conns")
	if err := s.Authenticate(context.Background(), "C1", "acme/repo-a", "sub-123"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if len(f.updateItems) != 1 {
		t.Fatalf("expected 1 UpdateItem, got %d", len(f.updateItems))
	}
	in := f.updateItems[0]
	if in.ConditionExpression == nil || *in.ConditionExpression == "" {
		t.Error("Authenticate must guard with attribute_exists condition")
	}
	v := in.ExpressionAttributeValues
	if b, ok := v[":t"].(*ddbtypes.AttributeValueMemberBOOL); !ok || !b.Value {
		t.Errorf(":t = %+v, want true", v[":t"])
	}
	if r, ok := v[":r"].(*ddbtypes.AttributeValueMemberS); !ok || r.Value != "acme/repo-a" {
		t.Errorf(":r = %+v, want acme/repo-a", v[":r"])
	}
	if sub, ok := v[":s"].(*ddbtypes.AttributeValueMemberS); !ok || sub.Value != "sub-123" {
		t.Errorf(":s = %+v, want sub-123", v[":s"])
	}
}

func TestAuthenticateConnectionGoneIsError(t *testing.T) {
	f := &fakeDDB{updateErr: &ddbtypes.ConditionalCheckFailedException{}}
	s := New(f, "conns")
	err := s.Authenticate(context.Background(), "C1", "acme/repo", "sub")
	if err == nil {
		t.Fatal("expected error when connection gone")
	}
}

func TestRemoveDeletesByConnectionID(t *testing.T) {
	f := &fakeDDB{}
	s := New(f, "conns")
	if err := s.Remove(context.Background(), "C1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(f.deleteItems) != 1 {
		t.Fatalf("expected 1 DeleteItem, got %d", len(f.deleteItems))
	}
	key := f.deleteItems[0].Key
	if v, ok := key[AttrConnectionID].(*ddbtypes.AttributeValueMemberS); !ok || v.Value != "C1" {
		t.Errorf("delete key = %+v, want C1", key[AttrConnectionID])
	}
}

func TestListAuthenticatedForRepoUsesIndexAndPaginates(t *testing.T) {
	f := &fakeDDB{
		queryPages: []*dynamodb.QueryOutput{
			{
				Items: []map[string]ddbtypes.AttributeValue{
					{AttrConnectionID: &ddbtypes.AttributeValueMemberS{Value: "C1"}},
				},
				LastEvaluatedKey: map[string]ddbtypes.AttributeValue{
					AttrConnectionID: &ddbtypes.AttributeValueMemberS{Value: "C1"},
				},
			},
			{
				Items: []map[string]ddbtypes.AttributeValue{
					{AttrConnectionID: &ddbtypes.AttributeValueMemberS{Value: "C2"}},
				},
			},
		},
	}
	s := New(f, "conns")
	ids, err := s.ListAuthenticatedForRepo(context.Background(), "acme/repo-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 2 || ids[0] != "C1" || ids[1] != "C2" {
		t.Fatalf("ids = %v, want [C1 C2]", ids)
	}
	if len(f.queries) != 2 {
		t.Fatalf("expected 2 Query calls (pagination), got %d", len(f.queries))
	}
	q := f.queries[0]
	if q.IndexName == nil || *q.IndexName != RepoIndexName {
		t.Errorf("IndexName = %v, want %s", q.IndexName, RepoIndexName)
	}
	if q.FilterExpression == nil || *q.FilterExpression == "" {
		t.Error("list must filter to authenticated connections only")
	}
}

func TestListEmptyRepoReturnsNil(t *testing.T) {
	f := &fakeDDB{}
	s := New(f, "conns")
	ids, err := s.ListAuthenticatedForRepo(context.Background(), "")
	if err != nil || ids != nil {
		t.Fatalf("empty repo: ids=%v err=%v, want nil nil", ids, err)
	}
	if len(f.queries) != 0 {
		t.Error("empty repo must not query")
	}
}

func TestListPropagatesQueryError(t *testing.T) {
	f := &fakeDDB{queryErr: errors.New("boom")}
	s := New(f, "conns")
	if _, err := s.ListAuthenticatedForRepo(context.Background(), "acme/repo"); err == nil {
		t.Fatal("expected query error to propagate")
	}
}
