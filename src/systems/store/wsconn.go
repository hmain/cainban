package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/hmain/cainban/src/systems/dynamo"
)

// WSConnectionStore persists the set of live API Gateway WebSocket connections
// so a serverless deployment (which has no long-running process to hold the
// in-process Hub's connection set) can fan activity events out across Lambda
// invocations. It is repo-scoped by construction exactly like TaskStore: the
// DynamoDB implementation is built with the request tenant's partition prefix,
// so a broadcast for repo A can only ever read repo A's connections.
//
// The $connect/$disconnect Lambda uses it to register/remove a connection; the
// MCP Lambda's broadcaster lists a repo's connections and posts to each.
type WSConnectionStore interface {
	// PutWSConnection registers a connection for this scope's repo.
	PutWSConnection(ctx context.Context, connectionID, repo string) error
	// ListWSConnectionIDs returns the connection ids watching this scope's repo.
	ListWSConnectionIDs(ctx context.Context) ([]string, error)
	// DeleteWSConnection removes a connection by id, resolving its repo scope
	// from the reverse-lookup row (the $disconnect path, which has only the
	// connectionId). A missing connection is not an error.
	DeleteWSConnection(ctx context.Context, connectionID string) error
	// DeleteWSConnectionInScope removes a connection from THIS scope's repo
	// (the broadcaster's 410-Gone prune path, which already knows the repo). A
	// missing connection is not an error.
	DeleteWSConnectionInScope(ctx context.Context, connectionID string) error
}

// OpenWSConnectionStoreForTenant returns a WSConnectionStore for the configured
// backend, scoped to a tenant's DynamoDB partition prefix, plus a close func the
// caller MUST call. It mirrors OpenWidgetTokenStoreForTenant: the DynamoDB
// backend honors the prefix; the SQLite/local backend uses a process-wide
// in-memory registry (local dev has no API Gateway WebSocket API).
//
// For the $disconnect path, which has no tenant/repo, open with an EMPTY prefix
// and call DeleteWSConnection — it resolves the repo from the stored lookup row.
func OpenWSConnectionStoreForTenant(ctx context.Context, partitionPrefix string) (WSConnectionStore, func() error, error) {
	switch Selected() {
	case BackendDynamoDB:
		client, err := dynamoClient(ctx)
		if err != nil {
			return nil, nil, err
		}
		table := strings.TrimSpace(os.Getenv(EnvDDBTable))
		if table == "" {
			table = dynamo.DefaultTableName
		}
		return dynamo.NewWithPrefix(client, table, partitionPrefix), func() error { return nil }, nil
	case BackendSQLite:
		return memWSConnections.scoped(partitionPrefix), func() error { return nil }, nil
	default:
		return nil, nil, fmt.Errorf("unknown %s=%q (want %q or %q)",
			EnvBackend, Selected(), BackendSQLite, BackendDynamoDB)
	}
}

// --- in-memory backend (SQLite/local dev + tests) --------------------------

// memWSConnections is a process-wide in-memory connection registry for the
// local/SQLite backend, which has no API Gateway WebSocket API. It mirrors the
// dynamo design: a per-prefix fan-out map plus a global connectionId -> prefix
// lookup so DeleteWSConnection (no scope) can find the fan-out entry.
var memWSConnections = &memWSConnStore{
	byPrefix: map[string]map[string]string{},
	lookup:   map[string]string{},
}

type memWSConnStore struct {
	mu sync.Mutex
	// byPrefix[prefix][connectionID] = repo
	byPrefix map[string]map[string]string
	// lookup[connectionID] = prefix
	lookup map[string]string
}

func (m *memWSConnStore) scoped(prefix string) WSConnectionStore {
	return &memWSConnScope{parent: m, prefix: prefix}
}

type memWSConnScope struct {
	parent *memWSConnStore
	prefix string
}

func (m *memWSConnScope) bucket() map[string]string {
	b, ok := m.parent.byPrefix[m.prefix]
	if !ok {
		b = map[string]string{}
		m.parent.byPrefix[m.prefix] = b
	}
	return b
}

func (m *memWSConnScope) PutWSConnection(_ context.Context, connectionID, repo string) error {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	m.bucket()[connectionID] = repo
	m.parent.lookup[connectionID] = m.prefix
	return nil
}

func (m *memWSConnScope) ListWSConnectionIDs(_ context.Context) ([]string, error) {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	b := m.bucket()
	out := make([]string, 0, len(b))
	for id := range b {
		out = append(out, id)
	}
	return out, nil
}

func (m *memWSConnScope) DeleteWSConnection(_ context.Context, connectionID string) error {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	prefix, ok := m.parent.lookup[connectionID]
	delete(m.parent.lookup, connectionID)
	if ok {
		if b := m.parent.byPrefix[prefix]; b != nil {
			delete(b, connectionID)
		}
	}
	return nil
}

func (m *memWSConnScope) DeleteWSConnectionInScope(_ context.Context, connectionID string) error {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	delete(m.bucket(), connectionID)
	delete(m.parent.lookup, connectionID)
	return nil
}

// Compile-time assertion that the dynamo store satisfies the interface.
var _ WSConnectionStore = (*dynamo.Store)(nil)
