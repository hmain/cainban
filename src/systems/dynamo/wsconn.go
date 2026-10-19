package dynamo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// WebSocket connection registry (API Gateway WebSocket API).
//
// On a serverless deployment there is no long-running process to hold the live
// *websocket.Conn set the in-process Hub keeps, so the set of connected clients
// is persisted in DynamoDB instead. The $connect/$disconnect Lambda writes/
// removes a connection row; the MCP Lambda's broadcaster (src/systems/mcp/wsconn)
// Queries the connections watching a repo and posts the activity frame to each
// via the API Gateway Management API.
//
// # Two rows per connection
//
// A single connection is written as TWO items so both access paths are a
// point/single-partition operation and never a table Scan:
//
//	1. FAN-OUT row  (broadcast path, keyed by REPO):
//	     PK = <partitionPrefix>WSCONN            SK = CONN#<connectionId>
//	   Listing a repo's connections is one Query on that repo-scoped partition —
//	   the same structural tenant isolation the task/board items use: a broadcast
//	   for repo A can only ever read repo A's connection rows.
//
//	2. LOOKUP row   ($disconnect path, keyed by CONNECTION ID):
//	     PK = CONN#<connectionId>                SK = META
//	   API Gateway's $disconnect event carries ONLY the connectionId (not the
//	   repo), so this reverse-lookup row records which repo (partition prefix) a
//	   connection belongs to, letting $disconnect delete the fan-out row without
//	   a Scan. It is NOT repo-partition-scoped (the connectionId is globally
//	   unique and the disconnect handler has no tenant), which is safe: it holds
//	   only {repo, connectedAt}, never task data.
//
// A connection row carries a TTL (`expires_at`, epoch seconds) so a connection
// whose $disconnect was never delivered (Lambda error, client crash) is reaped
// by DynamoDB rather than lingering as a dead fan-out target forever.

// wsConnFanoutPKSuffix is the per-tenant fan-out partition holding a repo's
// live connection rows (SK = CONN#<id>).
const wsConnFanoutPKSuffix = "WSCONN"

func (s *Store) wsConnFanoutPK() string { return s.partitionPrefix + wsConnFanoutPKSuffix }

func wsConnSK(connectionID string) string { return "CONN#" + connectionID }

// wsConnLookupPK is the connection-id-keyed reverse-lookup partition. It is
// deliberately NOT prefixed by the tenant: $disconnect has only the globally
// unique connectionId and no repo/tenant context.
func wsConnLookupPK(connectionID string) string { return "CONN#" + connectionID }

const wsConnLookupSK = "META"

// WSConnectionTTL is how long a connection row survives without an explicit
// $disconnect. API Gateway idle/hard timeouts are well under this, so the TTL
// only reaps rows whose $disconnect was lost; a healthy connection is removed
// promptly by $disconnect.
const WSConnectionTTL = 24 * time.Hour

// wsConnItem is the on-DynamoDB representation of a connection (both the fan-out
// and the lookup row use it; only the relevant keys/fields are populated).
type wsConnItem struct {
	PK           string `dynamodbav:"PK"`
	SK           string `dynamodbav:"SK"`
	ConnectionID string `dynamodbav:"connection_id,omitempty"`
	Repo         string `dynamodbav:"repo,omitempty"`
	ConnectedAt  string `dynamodbav:"connected_at,omitempty"`
	ExpiresAt    int64  `dynamodbav:"expires_at,omitempty"`
}

// PutWSConnection registers a connection for this store's repo scope. It writes
// both the fan-out row (repo-partitioned) and the connection-id lookup row so
// $disconnect can later find and delete the fan-out row. repo is the normalized
// "owner/repo" the connection authenticated for (stored for the lookup row and
// for diagnostics).
func (s *Store) PutWSConnection(ctx context.Context, connectionID, repo string) error {
	now := s.now().UTC()
	connectedAt := now.Format(time.RFC3339Nano)
	expires := now.Add(WSConnectionTTL).Unix()

	fanout, err := attributevalue.MarshalMap(wsConnItem{
		PK:           s.wsConnFanoutPK(),
		SK:           wsConnSK(connectionID),
		ConnectionID: connectionID,
		Repo:         repo,
		ConnectedAt:  connectedAt,
		ExpiresAt:    expires,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal ws connection: %w", err)
	}
	if _, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      fanout,
	}); err != nil {
		return fmt.Errorf("failed to store ws connection: %w", err)
	}

	lookup, err := attributevalue.MarshalMap(wsConnItem{
		PK:           wsConnLookupPK(connectionID),
		SK:           wsConnLookupSK,
		ConnectionID: connectionID,
		Repo:         repo,
		ConnectedAt:  connectedAt,
		ExpiresAt:    expires,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal ws connection lookup: %w", err)
	}
	if _, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      lookup,
	}); err != nil {
		return fmt.Errorf("failed to store ws connection lookup: %w", err)
	}
	return nil
}

// ListWSConnectionIDs returns the connection ids watching this store's repo
// scope. It is a single Query on the repo-scoped fan-out partition, so a
// broadcast for repo A can only read repo A's connections.
func (s *Store) ListWSConnectionIDs(ctx context.Context) ([]string, error) {
	var ids []string
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk":     &ddbtypes.AttributeValueMemberS{Value: s.wsConnFanoutPK()},
				":prefix": &ddbtypes.AttributeValueMemberS{Value: "CONN#"},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list ws connections: %w", err)
		}
		for _, raw := range out.Items {
			var it wsConnItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("failed to unmarshal ws connection: %w", err)
			}
			if it.ConnectionID != "" {
				ids = append(ids, it.ConnectionID)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return ids, nil
}

// DeleteWSConnection removes a connection by id, resolving its repo scope from
// the lookup row so the repo-partitioned fan-out row is also removed. It is the
// $disconnect path. A missing connection is not an error (idempotent): a lost
// $connect or a double $disconnect converges to "not present".
func (s *Store) DeleteWSConnection(ctx context.Context, connectionID string) error {
	// Resolve the repo (partition prefix) this connection belongs to.
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: wsConnLookupPK(connectionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: wsConnLookupSK},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to look up ws connection: %w", err)
	}

	// Delete the lookup row regardless (best-effort cleanup).
	if delErr := s.deleteWSConnLookup(ctx, connectionID); delErr != nil {
		return delErr
	}
	if len(out.Item) == 0 {
		// No lookup row: either already gone or the fan-out row lives under
		// THIS store's own prefix (DeleteWSConnectionInScope path). Nothing
		// more to do here.
		return nil
	}
	var it wsConnItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return fmt.Errorf("failed to unmarshal ws connection lookup: %w", err)
	}

	// The fan-out row lives under REPO#<repo>#WSCONN. Build that PK from the
	// stored repo WITHOUT depending on s.partitionPrefix (the disconnect store
	// has none).
	fanoutPK := wsConnFanoutPKSuffix
	if it.Repo != "" {
		fanoutPK = fmt.Sprintf("REPO#%s#%s", it.Repo, wsConnFanoutPKSuffix)
	}
	if _, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: fanoutPK},
			"SK": &ddbtypes.AttributeValueMemberS{Value: wsConnSK(connectionID)},
		},
	}); err != nil {
		return fmt.Errorf("failed to delete ws connection fan-out row: %w", err)
	}
	return nil
}

// deleteWSConnLookup removes the connection-id reverse-lookup row.
func (s *Store) deleteWSConnLookup(ctx context.Context, connectionID string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: wsConnLookupPK(connectionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: wsConnLookupSK},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete ws connection lookup: %w", err)
	}
	return nil
}

// DeleteWSConnectionInScope removes a single connection's fan-out row from THIS
// store's repo scope (and its lookup row). It is the path the broadcaster uses
// when a post returns 410 Gone: the broadcaster already knows the repo (it is
// scoped to it), so it prunes the stale connection without the lookup round
// trip. A missing row is not an error.
func (s *Store) DeleteWSConnectionInScope(ctx context.Context, connectionID string) error {
	if _, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.wsConnFanoutPK()},
			"SK": &ddbtypes.AttributeValueMemberS{Value: wsConnSK(connectionID)},
		},
	}); err != nil {
		return fmt.Errorf("failed to delete ws connection in scope: %w", err)
	}
	// Best-effort lookup-row cleanup; a missing row is fine.
	var cf *ddbtypes.ConditionalCheckFailedException
	if err := s.deleteWSConnLookup(ctx, connectionID); err != nil && !errors.As(err, &cf) {
		return err
	}
	return nil
}
