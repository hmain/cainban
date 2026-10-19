package wsconn

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"

	"github.com/hmain/cainban/src/systems/store"
)

// fakePost records PostToConnection calls and can be told which connection ids
// should return 410 Gone (a disconnected client whose $disconnect never
// arrived).
type fakePost struct {
	mu   sync.Mutex
	sent map[string][]byte // connectionId -> last data posted
	gone map[string]bool   // connectionId -> return GoneException
}

func newFakePost() *fakePost {
	return &fakePost{sent: map[string][]byte{}, gone: map[string]bool{}}
}

func (f *fakePost) PostToConnection(_ context.Context, in *apigatewaymanagementapi.PostToConnectionInput, _ ...func(*apigatewaymanagementapi.Options)) (*apigatewaymanagementapi.PostToConnectionOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := *in.ConnectionId
	if f.gone[id] {
		return nil, &apitypes.GoneException{}
	}
	f.sent[id] = in.Data
	return &apigatewaymanagementapi.PostToConnectionOutput{}, nil
}

// fakeConnStore is an in-memory WSConnectionStore keyed by partition prefix,
// shared across opens so the broadcaster's prune is observable.
type fakeConnStore struct {
	mu       sync.Mutex
	byPrefix map[string]map[string]bool // prefix -> set of connection ids
}

func newFakeConnStore() *fakeConnStore {
	return &fakeConnStore{byPrefix: map[string]map[string]bool{}}
}

func (f *fakeConnStore) put(prefix, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byPrefix[prefix] == nil {
		f.byPrefix[prefix] = map[string]bool{}
	}
	f.byPrefix[prefix][id] = true
}

// opener returns a connStoreOpener bound to this fake.
func (f *fakeConnStore) opener() connStoreOpener {
	return func(_ context.Context, prefix string) (store.WSConnectionStore, func() error, error) {
		return &fakeConnScope{parent: f, prefix: prefix}, func() error { return nil }, nil
	}
}

type fakeConnScope struct {
	parent *fakeConnStore
	prefix string
}

func (s *fakeConnScope) PutWSConnection(_ context.Context, id, _ string) error {
	s.parent.put(s.prefix, id)
	return nil
}

func (s *fakeConnScope) ListWSConnectionIDs(_ context.Context) ([]string, error) {
	s.parent.mu.Lock()
	defer s.parent.mu.Unlock()
	out := []string{}
	for id := range s.parent.byPrefix[s.prefix] {
		out = append(out, id)
	}
	return out, nil
}

func (s *fakeConnScope) DeleteWSConnection(_ context.Context, id string) error {
	return s.DeleteWSConnectionInScope(context.Background(), id)
}

func (s *fakeConnScope) DeleteWSConnectionInScope(_ context.Context, id string) error {
	s.parent.mu.Lock()
	defer s.parent.mu.Unlock()
	if b := s.parent.byPrefix[s.prefix]; b != nil {
		delete(b, id)
	}
	return nil
}

func newTestBroadcaster(post postAPI, open connStoreOpener) *Broadcaster {
	return &Broadcaster{post: post, open: open}
}

func TestBroadcast_PostsToEveryConnection(t *testing.T) {
	ctx := context.Background()
	fp := newFakePost()
	fc := newFakeConnStore()
	prefix := "REPO#acme/widgets#"
	fc.put(prefix, "conn-1")
	fc.put(prefix, "conn-2")

	b := newTestBroadcaster(fp, fc.opener())
	ev := map[string]interface{}{"BoardTaskID": 7, "Action": "created", "Actor": "x@y", "Detail": "New", "Timestamp": "2026-01-01T00:00:00Z"}

	sent, err := b.Broadcast(ctx, "acme/widgets", ev)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 2 {
		t.Fatalf("want sent=2, got %d", sent)
	}

	// Every posted frame must be the { "type":"event", "event":... } envelope
	// the frontend adapter expects.
	for id, data := range fp.sent {
		var env struct {
			Type  string                 `json:"type"`
			Event map[string]interface{} `json:"event"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("conn %s: frame not valid JSON: %v", id, err)
		}
		if env.Type != "event" {
			t.Fatalf("conn %s: want type=event, got %q", id, env.Type)
		}
		if env.Event["Action"] != "created" {
			t.Fatalf("conn %s: event not carried through, got %v", id, env.Event)
		}
	}
}

func TestBroadcast_PrunesGoneConnections(t *testing.T) {
	ctx := context.Background()
	fp := newFakePost()
	fc := newFakeConnStore()
	prefix := "REPO#acme/widgets#"
	fc.put(prefix, "live")
	fc.put(prefix, "dead")
	fp.gone["dead"] = true

	b := newTestBroadcaster(fp, fc.opener())
	sent, err := b.Broadcast(ctx, "acme/widgets", map[string]interface{}{"Action": "updated"})
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 1 {
		t.Fatalf("want sent=1 (live only), got %d", sent)
	}

	// The 410-Gone connection must be pruned from the store.
	ids, _ := (&fakeConnScope{parent: fc, prefix: prefix}).ListWSConnectionIDs(ctx)
	if len(ids) != 1 || ids[0] != "live" {
		t.Fatalf("want only [live] after prune, got %v", ids)
	}
}

func TestBroadcast_NoConnectionsIsNoOp(t *testing.T) {
	ctx := context.Background()
	fp := newFakePost()
	fc := newFakeConnStore() // empty
	b := newTestBroadcaster(fp, fc.opener())

	sent, err := b.Broadcast(ctx, "acme/widgets", map[string]interface{}{"Action": "created"})
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 0 {
		t.Fatalf("want sent=0 with no connections, got %d", sent)
	}
}

func TestBroadcast_InvalidRepoErrors(t *testing.T) {
	b := newTestBroadcaster(newFakePost(), newFakeConnStore().opener())
	if _, err := b.Broadcast(context.Background(), "not-a-repo", map[string]interface{}{}); err == nil {
		t.Fatal("want error for a structurally invalid repo, got nil")
	}
}
