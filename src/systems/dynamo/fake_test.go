package dynamo

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDDB is an in-memory stand-in for the DynamoDB client, implementing the
// exact subset of behavior the store relies on: keyed items, the ADD counter
// update, conditional expressions used by the store, REMOVE, and a
// PK = :pk AND begins_with(SK, :prefix) Query. It is NOT a general DynamoDB
// emulator — only the expressions dynamo.go actually issues are supported, and
// anything else panics so a drift in the store surfaces loudly in tests.
//
// This is why the tests use a fake rather than DynamoDB Local: the CI/dev host
// has no Docker and no JVM, so DynamoDB Local cannot run here. The fake covers
// the store's create/list/get/update/status/counter/links/soft-delete paths
// deterministically.
type fakeDDB struct {
	mu    sync.Mutex
	items map[string]map[string]ddbtypes.AttributeValue // key "PK\x00SK" -> item
}

func newFakeDDB() *fakeDDB {
	return &fakeDDB{items: map[string]map[string]ddbtypes.AttributeValue{}}
}

func keyOf(pk, sk string) string { return pk + "\x00" + sk }

func s(av ddbtypes.AttributeValue) string {
	if v, ok := av.(*ddbtypes.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func (f *fakeDDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk, sk := s(in.Key["PK"]), s(in.Key["SK"])
	item, ok := f.items[keyOf(pk, sk)]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: cloneItem(item)}, nil
}

func (f *fakeDDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk, sk := s(in.Item["PK"]), s(in.Item["SK"])
	f.items[keyOf(pk, sk)] = cloneItem(in.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk, sk := s(in.Key["PK"]), s(in.Key["SK"])
	k := keyOf(pk, sk)
	if in.ConditionExpression != nil && strings.Contains(*in.ConditionExpression, "attribute_exists(PK)") {
		if _, ok := f.items[k]; !ok {
			return nil, &ddbtypes.ConditionalCheckFailedException{}
		}
	}
	delete(f.items, k)
	return &dynamodb.DeleteItemOutput{}, nil
}

// UpdateItem supports the three update shapes the store issues:
//   - "ADD seq :one"                    (atomic counter)
//   - "SET ... , updated_at = :updated" (field updates, with attribute_exists condition)
//   - "SET deleted_at = :now, ..."      (soft delete, with not_exists(deleted_at) condition)
//   - "REMOVE deleted_at SET ..."       (restore, with exists(deleted_at) condition)
func (f *fakeDDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk, sk := s(in.Key["PK"]), s(in.Key["SK"])
	k := keyOf(pk, sk)
	expr := ""
	if in.UpdateExpression != nil {
		expr = *in.UpdateExpression
	}

	// Atomic counter: ADD seq :one.
	if strings.HasPrefix(expr, "ADD seq") {
		item := f.items[k]
		if item == nil {
			item = map[string]ddbtypes.AttributeValue{
				"PK": &ddbtypes.AttributeValueMemberS{Value: pk},
				"SK": &ddbtypes.AttributeValueMemberS{Value: sk},
			}
		}
		cur := 0
		if seq, ok := item["seq"].(*ddbtypes.AttributeValueMemberN); ok {
			cur, _ = strconv.Atoi(seq.Value)
		}
		inc := 1
		if v, ok := in.ExpressionAttributeValues[":one"].(*ddbtypes.AttributeValueMemberN); ok {
			inc, _ = strconv.Atoi(v.Value)
		}
		next := cur + inc
		item["seq"] = &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(next)}
		f.items[k] = item
		return &dynamodb.UpdateItemOutput{
			Attributes: map[string]ddbtypes.AttributeValue{
				"seq": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(next)},
			},
		}, nil
	}

	// All other updates require the item to exist / conditions to hold.
	item, exists := f.items[k]
	if in.ConditionExpression != nil {
		cond := *in.ConditionExpression
		if strings.Contains(cond, "attribute_exists(PK)") && !exists {
			return nil, &ddbtypes.ConditionalCheckFailedException{}
		}
		if strings.Contains(cond, "attribute_not_exists(deleted_at)") {
			if exists && item["deleted_at"] != nil {
				return nil, &ddbtypes.ConditionalCheckFailedException{}
			}
			if !exists {
				return nil, &ddbtypes.ConditionalCheckFailedException{}
			}
		}
		if strings.Contains(cond, "attribute_exists(deleted_at)") {
			if !exists || item["deleted_at"] == nil {
				return nil, &ddbtypes.ConditionalCheckFailedException{}
			}
		}
	}
	if !exists {
		return nil, &ddbtypes.ConditionalCheckFailedException{}
	}

	applyNames := func(name string) string {
		if in.ExpressionAttributeNames != nil {
			if real, ok := in.ExpressionAttributeNames[name]; ok {
				return real
			}
		}
		return name
	}

	// REMOVE deleted_at [SET ...]
	if strings.HasPrefix(expr, "REMOVE deleted_at") {
		delete(item, "deleted_at")
	}

	// SET a = :x, b = :y  — parse the comma-separated assignments in the SET
	// clause (the store always uses simple "field = :placeholder" forms).
	if idx := strings.Index(expr, "SET "); idx >= 0 {
		setClause := expr[idx+len("SET "):]
		for _, assign := range strings.Split(setClause, ",") {
			parts := strings.SplitN(assign, "=", 2)
			if len(parts) != 2 {
				continue
			}
			field := applyNames(strings.TrimSpace(parts[0]))
			ph := strings.TrimSpace(parts[1])
			val, ok := in.ExpressionAttributeValues[ph]
			if !ok {
				continue
			}
			item[field] = val
		}
	}
	f.items[k] = item
	return &dynamodb.UpdateItemOutput{}, nil
}

// Query supports PK = :pk AND begins_with(SK, :prefix).
func (f *fakeDDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk := s(in.ExpressionAttributeValues[":pk"])
	prefix := s(in.ExpressionAttributeValues[":prefix"])
	var out []map[string]ddbtypes.AttributeValue
	for _, item := range f.items {
		if s(item["PK"]) != pk {
			continue
		}
		if prefix != "" && !strings.HasPrefix(s(item["SK"]), prefix) {
			continue
		}
		out = append(out, cloneItem(item))
	}
	return &dynamodb.QueryOutput{Items: out}, nil
}

func cloneItem(in map[string]ddbtypes.AttributeValue) map[string]ddbtypes.AttributeValue {
	if in == nil {
		return nil
	}
	out := make(map[string]ddbtypes.AttributeValue, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ensure the fake satisfies the store's API interface.
var _ API = (*fakeDDB)(nil)
