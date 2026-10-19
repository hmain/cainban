package dynamo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Widget-token metadata reuses the single-table design, co-located in the
// tenant's partition so listing a repo's tokens is a single-partition Query and
// scoping is structural (every key is built from s.partitionPrefix):
//
//	PK = <partitionPrefix>WIDGET_TOKENS
//	SK = "TOKEN#<id>"   -> {repo, created_by, created_at, expires_at, revoked}
//
// The token STRING and the signing secret are never stored — only the metadata
// needed to list, audit and revoke. Revocation flips the `revoked` attribute;
// the auth path reads it on every widget-token request.
const widgetTokensPKSuffix = "WIDGET_TOKENS"

func (s *Store) widgetTokensPK() string {
	return s.partitionPrefix + widgetTokensPKSuffix
}

func widgetTokenSK(id string) string { return "TOKEN#" + id }

// widgetTokenItem is the on-DynamoDB representation of a widget-token metadata
// row. Timestamps are RFC3339Nano strings so they round-trip exactly.
type widgetTokenItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	TokenID   string `dynamodbav:"token_id"`
	Repo      string `dynamodbav:"repo"`
	CreatedBy string `dynamodbav:"created_by,omitempty"`
	CreatedAt string `dynamodbav:"created_at,omitempty"`
	ExpiresAt string `dynamodbav:"expires_at,omitempty"`
	Revoked   bool   `dynamodbav:"revoked"`
}

// WidgetTokenRecord is the backend-neutral metadata the dynamo store returns.
// It mirrors the mcp.WidgetTokenMeta shape without importing the mcp package
// (which would be a cycle); the mcp store adapter converts between the two.
type WidgetTokenRecord struct {
	TokenID   string
	Repo      string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	Revoked   bool
}

func (it widgetTokenItem) toRecord() WidgetTokenRecord {
	createdAt, _ := time.Parse(time.RFC3339Nano, it.CreatedAt)
	expiresAt, _ := time.Parse(time.RFC3339Nano, it.ExpiresAt)
	return WidgetTokenRecord{
		TokenID:   it.TokenID,
		Repo:      it.Repo,
		CreatedBy: it.CreatedBy,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Revoked:   it.Revoked,
	}
}

// PutWidgetToken stores (or overwrites) a widget token's metadata.
func (s *Store) PutWidgetToken(ctx context.Context, rec WidgetTokenRecord) error {
	av, err := attributevalue.MarshalMap(widgetTokenItem{
		PK:        s.widgetTokensPK(),
		SK:        widgetTokenSK(rec.TokenID),
		TokenID:   rec.TokenID,
		Repo:      rec.Repo,
		CreatedBy: rec.CreatedBy,
		CreatedAt: rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt: rec.ExpiresAt.UTC().Format(time.RFC3339Nano),
		Revoked:   rec.Revoked,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal widget token: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("failed to store widget token: %w", err)
	}
	return nil
}

// GetWidgetToken returns one token's metadata, or (nil, nil) when absent in
// this scope.
func (s *Store) GetWidgetToken(ctx context.Context, tokenID string) (*WidgetTokenRecord, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.widgetTokensPK()},
			"SK": &ddbtypes.AttributeValueMemberS{Value: widgetTokenSK(tokenID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get widget token: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var it widgetTokenItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return nil, fmt.Errorf("failed to unmarshal widget token: %w", err)
	}
	rec := it.toRecord()
	return &rec, nil
}

// ListWidgetTokens returns every token's metadata in this scope, newest first.
func (s *Store) ListWidgetTokens(ctx context.Context) ([]WidgetTokenRecord, error) {
	var recs []WidgetTokenRecord
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk":     &ddbtypes.AttributeValueMemberS{Value: s.widgetTokensPK()},
				":prefix": &ddbtypes.AttributeValueMemberS{Value: "TOKEN#"},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list widget tokens: %w", err)
		}
		for _, raw := range out.Items {
			var it widgetTokenItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("failed to unmarshal widget token: %w", err)
			}
			recs = append(recs, it.toRecord())
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].CreatedAt.After(recs[j].CreatedAt) })
	return recs, nil
}

// RevokeWidgetToken flips a token's `revoked` flag. It returns (false, nil)
// when no such token exists in this scope, (true, nil) on success. The
// conditional (attribute_exists(PK)) distinguishes a missing token from a
// successful revoke without a prior read.
func (s *Store) RevokeWidgetToken(ctx context.Context, tokenID string) (bool, error) {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.widgetTokensPK()},
			"SK": &ddbtypes.AttributeValueMemberS{Value: widgetTokenSK(tokenID)},
		},
		UpdateExpression: aws.String("SET revoked = :true"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":true": &ddbtypes.AttributeValueMemberBOOL{Value: true},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cf) {
			return false, nil
		}
		return false, fmt.Errorf("failed to revoke widget token: %w", err)
	}
	return true, nil
}
