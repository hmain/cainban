package wsconn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeMgmt records posts and can fail specific connection ids with a 410 Gone.
type fakeMgmt struct {
	posts map[string][]byte
	gone  map[string]bool
	other map[string]bool
}

func newFakeMgmt() *fakeMgmt {
	return &fakeMgmt{posts: map[string][]byte{}, gone: map[string]bool{}, other: map[string]bool{}}
}

func (f *fakeMgmt) PostToConnection(_ context.Context, in *apigatewaymanagementapi.PostToConnectionInput, _ ...func(*apigatewaymanagementapi.Options)) (*apigatewaymanagementapi.PostToConnectionOutput, error) {
	id := *in.ConnectionId
	if f.gone[id] {
		return nil, &apitypes.GoneException{}
	}
	if f.other[id] {
		return nil, errors.New("transient")
	}
	f.posts[id] = in.Data
	return &apigatewaymanagementapi.PostToConnectionOutput{}, nil
}

// repoStore wires a store whose Query returns the given connection ids as
// authenticated, so Broadcast can enumerate them.
func repoStore(ddb DDBAPI) *Store { return New(ddb, "conns") }

func queryReturning(ids ...string) *fakeDDB {
	items := make([]map[string]ddbtypes.AttributeValue, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]ddbtypes.AttributeValue{
			AttrConnectionID: &ddbtypes.AttributeValueMemberS{Value: id},
		})
	}
	return &fakeDDB{queryPages: []*dynamodb.QueryOutput{{Items: items}}}
}

func TestBroadcastPostsEventFrameToEachConnection(t *testing.T) {
	ddb := queryReturning("C1", "C2")
	mgmt := newFakeMgmt()
	b := NewBroadcaster(repoStore(ddb), mgmt)

	event := map[string]any{"Action": "created", "BoardTaskID": 7}
	sent, err := b.Broadcast(context.Background(), "acme/repo-a", event)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 2 {
		t.Fatalf("sent = %d, want 2", sent)
	}
	var frame Frame
	if err := json.Unmarshal(mgmt.posts["C1"], &frame); err != nil {
		t.Fatalf("unmarshal posted frame: %v", err)
	}
	if frame.Type != "event" {
		t.Errorf("frame.Type = %q, want event", frame.Type)
	}
}

func TestBroadcastPrunesGoneConnection(t *testing.T) {
	ddb := queryReturning("C1", "C2")
	mgmt := newFakeMgmt()
	mgmt.gone["C1"] = true // C1 has closed
	b := NewBroadcaster(repoStore(ddb), mgmt)

	sent, err := b.Broadcast(context.Background(), "acme/repo-a", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1 (C1 gone, C2 delivered)", sent)
	}
	// The gone connection must have been pruned (a DeleteItem on C1).
	if len(ddb.deleteItems) != 1 {
		t.Fatalf("expected 1 prune DeleteItem, got %d", len(ddb.deleteItems))
	}
	key := ddb.deleteItems[0].Key[AttrConnectionID].(*ddbtypes.AttributeValueMemberS)
	if key.Value != "C1" {
		t.Errorf("pruned %q, want C1", key.Value)
	}
}

func TestBroadcastSkipsOtherErrorsWithoutPruning(t *testing.T) {
	ddb := queryReturning("C1", "C2")
	mgmt := newFakeMgmt()
	mgmt.other["C1"] = true // transient failure, not gone
	b := NewBroadcaster(repoStore(ddb), mgmt)

	sent, err := b.Broadcast(context.Background(), "acme/repo-a", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(ddb.deleteItems) != 0 {
		t.Errorf("transient error must NOT prune, got %d deletes", len(ddb.deleteItems))
	}
}

func TestBroadcastNoConnectionsIsNoop(t *testing.T) {
	ddb := queryReturning()
	mgmt := newFakeMgmt()
	b := NewBroadcaster(repoStore(ddb), mgmt)
	sent, err := b.Broadcast(context.Background(), "acme/repo-a", map[string]any{"x": 1})
	if err != nil || sent != 0 {
		t.Fatalf("sent=%d err=%v, want 0 nil", sent, err)
	}
}

func TestBroadcastEmptyRepoIsNoop(t *testing.T) {
	ddb := queryReturning("C1")
	b := NewBroadcaster(repoStore(ddb), newFakeMgmt())
	sent, err := b.Broadcast(context.Background(), "", map[string]any{"x": 1})
	if err != nil || sent != 0 {
		t.Fatalf("sent=%d err=%v, want 0 nil", sent, err)
	}
	if len(ddb.queries) != 0 {
		t.Error("empty repo must not query")
	}
}
