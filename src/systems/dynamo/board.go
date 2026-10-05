package dynamo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/hmain/cainban/src/systems/task"
)

// Board rows reuse the reserved single-table key shape:
//
//	PK = <partitionPrefix>BOARD#<id>
//	SK = "META"      -> {board_id, title (= board name)}
//
// Each board is its own partition (the id is in the PK), which keeps a board's
// counter + tasks + links co-located. A DynamoDB Query addresses exactly one
// partition, so it cannot enumerate BOARD#* across ids without a GSI or a Scan.
// Today exactly one board (id 1) exists per tenant, so ListBoards reads the
// known BOARD#1#META row by key. Real multi-board enumeration is a follow-up
// that adds a GSI; it is out of scope here (see the spec non-goals).
const (
	boardMetaSK      = "META"
	defaultBoardID   = 1
	defaultBoardName = "default"
)

// metaItem is the on-DynamoDB representation of a BOARD#<id>#META row. It reuses
// board_id and title (the board name) from the shared item attribute space.
type metaItem struct {
	PK      string `dynamodbav:"PK"`
	SK      string `dynamodbav:"SK"`
	BoardID int    `dynamodbav:"board_id"`
	Title   string `dynamodbav:"title"`
}

// getBoardMeta fetches one BOARD#<id>#META row, or (nil, nil) when it is absent.
func (s *Store) getBoardMeta(ctx context.Context, boardID int) (*task.BoardSummary, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(boardID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: boardMetaSK},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get board %d meta: %w", boardID, err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var mi metaItem
	if err := attributevalue.UnmarshalMap(out.Item, &mi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal board meta: %w", err)
	}
	name := mi.Title
	if name == "" {
		name = defaultBoardName
	}
	return &task.BoardSummary{ID: mi.BoardID, Name: name}, nil
}

// ListBoards returns the boards in the current tenant scope. Scoping is
// structural: every key is built from s.partitionPrefix via boardPK, so a store
// built for repo A can only ever read repo A's board partition.
func (s *Store) ListBoards() ([]task.BoardSummary, error) {
	ctx := context.TODO()
	var out []task.BoardSummary
	meta, err := s.getBoardMeta(ctx, defaultBoardID)
	if err != nil {
		return nil, err
	}
	if meta != nil {
		out = append(out, *meta)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ResolveBoard maps a selector (numeric id or board name) to a board in the
// current scope, or task.ErrBoardNotFound. The repo was authorized upstream;
// the selector is a board, never a repo.
func (s *Store) ResolveBoard(selector string) (task.BoardSummary, error) {
	ctx := context.TODO()
	if n, err := strconv.Atoi(selector); err == nil {
		meta, err := s.getBoardMeta(ctx, n)
		if err != nil {
			return task.BoardSummary{}, err
		}
		if meta == nil {
			return task.BoardSummary{}, task.ErrBoardNotFound
		}
		return *meta, nil
	}
	boards, err := s.ListBoards()
	if err != nil {
		return task.BoardSummary{}, err
	}
	for _, b := range boards {
		if b.Name == selector {
			return b, nil
		}
	}
	return task.BoardSummary{}, task.ErrBoardNotFound
}

// ensureBoardMeta lazily writes a BOARD#<id>#META row if absent, so a tenant
// that already has tasks but no explicit board row still lists the board. It is
// a conditional PutItem (attribute_not_exists(PK)) so it is idempotent and never
// overwrites an existing row. Best-effort: callers in the write path ignore its
// error — a failed meta put must not fail the task create.
func (s *Store) ensureBoardMeta(ctx context.Context, boardID int, name string) error {
	if name == "" {
		name = defaultBoardName
	}
	av, err := attributevalue.MarshalMap(metaItem{
		PK:      s.boardPK(boardID),
		SK:      boardMetaSK,
		BoardID: boardID,
		Title:   name,
	})
	if err != nil {
		return err
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		// A conditional-check failure means the row already exists — expected
		// and not an error for this best-effort upsert.
		var cond *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cond) {
			return nil
		}
		return err
	}
	return nil
}
