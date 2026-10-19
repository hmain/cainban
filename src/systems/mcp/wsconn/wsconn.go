// Package wsconn is the DynamoDB-backed connection registry and API Gateway
// broadcaster for cainban's real-time WebSocket endpoint WHEN it runs on an AWS
// API Gateway WebSocket API + Lambda (as opposed to the in-process hub in the
// mcp package, which only works on a long-running server).
//
// # Why a separate store exists
//
// API Gateway WebSocket is a different runtime model from a persistent Go
// server: there is no process that holds live *net.Conn objects. Instead API
// Gateway owns the sockets, invokes a Lambda once per event ($connect,
// $disconnect, a client message), and the server pushes a frame to a client by
// calling the API Gateway Management API with that client's connectionId. So
// "who is watching repo X" must live in durable storage shared across
// invocations — a DynamoDB table keyed by connectionId — not in process memory.
//
// # Table shape (one item per live connection)
//
//	PK            connectionId          (the API Gateway connection id)
//	repo          "owner/repo"          (the repo this socket watches)
//	connectedAt   RFC3339 UTC           (when $connect ran)
//	authenticated bool                  (true once a valid auth frame arrived)
//	tenantSub     cognito subject       (the authenticated caller; "" until auth)
//	expiresAt     unix epoch seconds    (DynamoDB TTL — reaps stale connections)
//
// A GSI on `repo` lets Broadcast enumerate the connections for one repo without
// scanning the whole table. Tenant isolation is structural: a broadcast for
// repo A queries only repo A's partition in the index, exactly as the in-process
// hub buckets clients by repoKey.
package wsconn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Attribute / index names. Kept here as the single source of truth shared by the
// store, the Lambda handler, and the CDK stack (which must create a GSII with the
// SAME name/key). Changing one without the other silently breaks Broadcast.
const (
	// AttrConnectionID is the partition key: the API Gateway connection id.
	AttrConnectionID = "connectionId"
	// AttrRepo is the normalized "owner/repo" this socket watches, and the GSI
	// partition key used by Broadcast to enumerate a repo's connections.
	AttrRepo = "repo"
	// AttrConnectedAt is the RFC3339 UTC time $connect ran (diagnostics).
	AttrConnectedAt = "connectedAt"
	// AttrAuthenticated is true once a valid auth frame has been processed.
	AttrAuthenticated = "authenticated"
	// AttrTenantSub is the authenticated Cognito subject ("" until auth).
	AttrTenantSub = "tenantSub"
	// AttrExpiresAt is the DynamoDB TTL attribute (unix epoch seconds). A stale
	// connection the $disconnect event never cleaned up is reaped automatically.
	AttrExpiresAt = "expiresAt"

	// RepoIndexName is the GSI on AttrRepo. The CDK stack MUST create a GSI with
	// exactly this name and partition key, or Broadcast's Query fails at runtime.
	RepoIndexName = "repo-index"

	// DefaultTTL is how long after $connect a connection item is kept before
	// DynamoDB TTL may reap it, as a backstop for a missed $disconnect. API
	// Gateway closes idle WebSocket connections at 10 minutes and caps a
	// connection at 2 hours, so a few hours is a safe upper bound.
	DefaultTTL = 3 * time.Hour
)

// DDBAPI is the subset of the DynamoDB client the store uses. Narrow on purpose
// so tests can supply a fake and the production IAM grant stays least-privilege.
type DDBAPI interface {
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// Connection is one live WebSocket connection row.
type Connection struct {
	ConnectionID  string
	Repo          string
	ConnectedAt   time.Time
	Authenticated bool
	TenantSub     string
	ExpiresAt     time.Time
}

// Store persists WebSocket connection rows in DynamoDB.
type Store struct {
	ddb   DDBAPI
	table string
	// now is injectable so tests get deterministic connectedAt/expiresAt. It
	// defaults to time.Now when zero.
	now func() time.Time
	// ttl is the keep-alive window applied to a new connection. Defaults to
	// DefaultTTL when zero.
	ttl time.Duration
}

// New builds a Store over the given DynamoDB client and table name.
func New(ddb DDBAPI, table string) *Store {
	return &Store{ddb: ddb, table: table, now: time.Now, ttl: DefaultTTL}
}

// WithClock overrides the clock (tests). Returns the store for chaining.
func (s *Store) WithClock(now func() time.Time) *Store {
	if now != nil {
		s.now = now
	}
	return s
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *Store) ttlWindow() time.Duration {
	if s.ttl <= 0 {
		return DefaultTTL
	}
	return s.ttl
}

// Add records a new connection at $connect time. It is UNAUTHENTICATED until a
// valid auth frame arrives (authenticated=false, tenantSub=""); the repo comes
// from the connect path params and is only a HINT until the auth frame proves
// the caller may watch it. An expiresAt TTL is set as a backstop for a missed
// $disconnect.
func (s *Store) Add(ctx context.Context, connectionID, repo string) error {
	if connectionID == "" {
		return errors.New("wsconn: empty connectionId")
	}
	now := s.clock().UTC()
	item := map[string]ddbtypes.AttributeValue{
		AttrConnectionID:  &ddbtypes.AttributeValueMemberS{Value: connectionID},
		AttrConnectedAt:   &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
		AttrAuthenticated: &ddbtypes.AttributeValueMemberBOOL{Value: false},
		AttrExpiresAt:     &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(now.Add(s.ttlWindow()).Unix(), 10)},
	}
	// repo is optional at connect time (the auth frame can re-assert it), but
	// when present it feeds the GSI so a broadcast can find this socket.
	if repo != "" {
		item[AttrRepo] = &ddbtypes.AttributeValueMemberS{Value: repo}
	}
	_, err := s.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("wsconn: add %s: %w", connectionID, err)
	}
	return nil
}

// Authenticate marks a connection authenticated for a repo + tenant subject,
// after the Lambda has validated the auth frame's JWT and authorized the repo.
// It uses UpdateItem with a condition that the item still exists, so a race with
// $disconnect (the socket closed before the auth frame was processed) is a
// clean no-op error rather than resurrecting a dead connection.
func (s *Store) Authenticate(ctx context.Context, connectionID, repo, tenantSub string) error {
	if connectionID == "" {
		return errors.New("wsconn: empty connectionId")
	}
	_, err := s.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			AttrConnectionID: &ddbtypes.AttributeValueMemberS{Value: connectionID},
		},
		UpdateExpression:    aws.String("SET #auth = :t, #repo = :r, #sub = :s"),
		ConditionExpression: aws.String("attribute_exists(#cid)"),
		ExpressionAttributeNames: map[string]string{
			"#auth": AttrAuthenticated,
			"#repo": AttrRepo,
			"#sub":  AttrTenantSub,
			"#cid":  AttrConnectionID,
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":t": &ddbtypes.AttributeValueMemberBOOL{Value: true},
			":r": &ddbtypes.AttributeValueMemberS{Value: repo},
			":s": &ddbtypes.AttributeValueMemberS{Value: tenantSub},
		},
	})
	if err != nil {
		var cfe *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return fmt.Errorf("wsconn: authenticate %s: connection gone: %w", connectionID, err)
		}
		return fmt.Errorf("wsconn: authenticate %s: %w", connectionID, err)
	}
	return nil
}

// Remove deletes a connection at $disconnect. It is idempotent: deleting a row
// that no longer exists is not an error (DynamoDB DeleteItem succeeds).
func (s *Store) Remove(ctx context.Context, connectionID string) error {
	if connectionID == "" {
		return errors.New("wsconn: empty connectionId")
	}
	_, err := s.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			AttrConnectionID: &ddbtypes.AttributeValueMemberS{Value: connectionID},
		},
	})
	if err != nil {
		return fmt.Errorf("wsconn: remove %s: %w", connectionID, err)
	}
	return nil
}

// ListAuthenticatedForRepo returns the connection ids of every AUTHENTICATED
// socket watching repo, via the repo GSI. Only authenticated connections are
// returned: an un-authenticated socket has not proven it may see the repo's
// data, so it never receives a broadcast. Pagination is followed to completion.
func (s *Store) ListAuthenticatedForRepo(ctx context.Context, repo string) ([]string, error) {
	if repo == "" {
		return nil, nil
	}
	var ids []string
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := s.ddb.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			IndexName:              aws.String(RepoIndexName),
			KeyConditionExpression: aws.String("#repo = :r"),
			FilterExpression:       aws.String("#auth = :t"),
			ExpressionAttributeNames: map[string]string{
				"#repo": AttrRepo,
				"#auth": AttrAuthenticated,
			},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":r": &ddbtypes.AttributeValueMemberS{Value: repo},
				":t": &ddbtypes.AttributeValueMemberBOOL{Value: true},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("wsconn: list repo %s: %w", repo, err)
		}
		for _, itm := range out.Items {
			if cid, ok := itm[AttrConnectionID].(*ddbtypes.AttributeValueMemberS); ok && cid.Value != "" {
				ids = append(ids, cid.Value)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return ids, nil
}
