package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hmain/cainban/src/systems/dynamo"
)

// WidgetTokenMeta is the stored, human-inspectable record of a minted widget
// token, used for listing and revocation. It never contains the token string or
// the signing secret — only enough to identify, audit and revoke a token.
type WidgetTokenMeta struct {
	TokenID   string    `json:"token_id"`
	Repo      string    `json:"repo"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
}

// WidgetTokenStore persists widget-token metadata so a token can be listed and
// revoked after it is minted. It is repo-scoped by construction (the DynamoDB
// implementation is built with the request tenant's partition prefix, exactly
// like TaskStore/BoardStore), so a caller authorized for repo A can never see
// or revoke repo B's tokens.
type WidgetTokenStore interface {
	// PutWidgetToken stores (or overwrites) a token's metadata.
	PutWidgetToken(ctx context.Context, meta WidgetTokenMeta) error
	// GetWidgetToken returns the metadata for a token id, or (nil, nil) when no
	// such token exists in this scope.
	GetWidgetToken(ctx context.Context, tokenID string) (*WidgetTokenMeta, error)
	// ListWidgetTokens returns every token's metadata in this scope, newest
	// first.
	ListWidgetTokens(ctx context.Context) ([]WidgetTokenMeta, error)
	// RevokeWidgetToken marks a token revoked. It returns (false, nil) when no
	// such token exists in this scope, (true, nil) on success.
	RevokeWidgetToken(ctx context.Context, tokenID string) (bool, error)
}

// OpenWidgetTokenStoreForTenant returns a WidgetTokenStore for the configured
// backend, scoped to a tenant's DynamoDB partition prefix, plus a close func the
// caller MUST call. It mirrors OpenTaskForTenant: the DynamoDB backend honors
// the prefix (so repo A never sees repo B's tokens); the SQLite/local backend
// uses a process-wide in-memory store keyed by the prefix (local single-tenant
// dev has no durable token table, and widget tokens are a serverless feature).
func OpenWidgetTokenStoreForTenant(ctx context.Context, partitionPrefix string) (WidgetTokenStore, func() error, error) {
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
		return &dynamoWidgetTokenStore{s: dynamo.NewWithPrefix(client, table, partitionPrefix)}, func() error { return nil }, nil
	case BackendSQLite:
		return memWidgetTokens.scoped(partitionPrefix), func() error { return nil }, nil
	default:
		return nil, nil, fmt.Errorf("unknown %s=%q (want %q or %q)",
			EnvBackend, Selected(), BackendSQLite, BackendDynamoDB)
	}
}

// dynamoWidgetTokenStore adapts the dynamo.Store widget-token methods (which
// use dynamo.WidgetTokenRecord to avoid importing this package) to the
// WidgetTokenStore interface this package exposes.
type dynamoWidgetTokenStore struct{ s *dynamo.Store }

func (d *dynamoWidgetTokenStore) PutWidgetToken(ctx context.Context, meta WidgetTokenMeta) error {
	return d.s.PutWidgetToken(ctx, toDynamoRecord(meta))
}

func (d *dynamoWidgetTokenStore) GetWidgetToken(ctx context.Context, tokenID string) (*WidgetTokenMeta, error) {
	rec, err := d.s.GetWidgetToken(ctx, tokenID)
	if err != nil || rec == nil {
		return nil, err
	}
	m := fromDynamoRecord(*rec)
	return &m, nil
}

func (d *dynamoWidgetTokenStore) ListWidgetTokens(ctx context.Context) ([]WidgetTokenMeta, error) {
	recs, err := d.s.ListWidgetTokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WidgetTokenMeta, 0, len(recs))
	for _, r := range recs {
		out = append(out, fromDynamoRecord(r))
	}
	return out, nil
}

func (d *dynamoWidgetTokenStore) RevokeWidgetToken(ctx context.Context, tokenID string) (bool, error) {
	return d.s.RevokeWidgetToken(ctx, tokenID)
}

func toDynamoRecord(m WidgetTokenMeta) dynamo.WidgetTokenRecord {
	return dynamo.WidgetTokenRecord{
		TokenID:   m.TokenID,
		Repo:      m.Repo,
		CreatedBy: m.CreatedBy,
		CreatedAt: m.CreatedAt,
		ExpiresAt: m.ExpiresAt,
		Revoked:   m.Revoked,
	}
}

func fromDynamoRecord(r dynamo.WidgetTokenRecord) WidgetTokenMeta {
	return WidgetTokenMeta{
		TokenID:   r.TokenID,
		Repo:      r.Repo,
		CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
		Revoked:   r.Revoked,
	}
}

// --- in-memory backend (SQLite/local dev path) -----------------------------

// memWidgetTokens is a process-wide, prefix-keyed in-memory widget-token store
// for the local/SQLite backend, which has no durable token table. It is safe
// for concurrent use. Widget tokens are primarily a serverless (DynamoDB)
// feature; this keeps the tools functional in local dev and tests.
var memWidgetTokens = &memWidgetTokenStore{byPrefix: map[string]map[string]WidgetTokenMeta{}}

type memWidgetTokenStore struct {
	mu       sync.Mutex
	byPrefix map[string]map[string]WidgetTokenMeta
}

// scoped returns a view bound to one partition prefix.
func (m *memWidgetTokenStore) scoped(prefix string) WidgetTokenStore {
	return &memWidgetTokenScope{parent: m, prefix: prefix}
}

type memWidgetTokenScope struct {
	parent *memWidgetTokenStore
	prefix string
}

func (m *memWidgetTokenScope) bucket() map[string]WidgetTokenMeta {
	b, ok := m.parent.byPrefix[m.prefix]
	if !ok {
		b = map[string]WidgetTokenMeta{}
		m.parent.byPrefix[m.prefix] = b
	}
	return b
}

func (m *memWidgetTokenScope) PutWidgetToken(_ context.Context, meta WidgetTokenMeta) error {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	m.bucket()[meta.TokenID] = meta
	return nil
}

func (m *memWidgetTokenScope) GetWidgetToken(_ context.Context, tokenID string) (*WidgetTokenMeta, error) {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	meta, ok := m.bucket()[tokenID]
	if !ok {
		return nil, nil
	}
	return &meta, nil
}

func (m *memWidgetTokenScope) ListWidgetTokens(_ context.Context) ([]WidgetTokenMeta, error) {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	b := m.bucket()
	out := make([]WidgetTokenMeta, 0, len(b))
	for _, meta := range b {
		out = append(out, meta)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *memWidgetTokenScope) RevokeWidgetToken(_ context.Context, tokenID string) (bool, error) {
	m.parent.mu.Lock()
	defer m.parent.mu.Unlock()
	b := m.bucket()
	meta, ok := b[tokenID]
	if !ok {
		return false, nil
	}
	meta.Revoked = true
	b[tokenID] = meta
	return true, nil
}

// Compile-time assertion that the dynamo adapter satisfies the interface.
var _ WidgetTokenStore = (*dynamoWidgetTokenStore)(nil)
