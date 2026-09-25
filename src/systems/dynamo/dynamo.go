// Package dynamo is the DynamoDB-backed implementation of store.TaskStore.
//
// It is PURE GO (no CGO): the Lambda build path uses CGO_ENABLED=0, so this
// backend must not pull in cgo-linked drivers. All persistence goes through
// aws-sdk-go-v2.
//
// # Single-table design
//
// One table (default name "cainban") holds boards, the atomic per-board task
// counter, tasks and task links. A single table is chosen over three tables
// because:
//   - every access is already board-scoped, so one partition per board keeps a
//     board's counter + tasks + links co-located (single-partition reads/writes,
//     cheaper and strongly consistent per board);
//   - it is the smallest IAM/infra surface (one table, one set of actions);
//   - it maps cleanly onto the Phase 3 tenancy design in
//     docs/serverless-multiuser-plan.md, which already specifies a
//     REPO#<owner>/<repo> partition key with sort keys namespacing
//     boards/tasks/links/counter.
//
// Keys (Phase 2, single-tenant):
//
//	PK = partitionPrefix + "BOARD#<boardID>"     (partitionPrefix is "" in Phase 2)
//	SK = "META"                                   -> board metadata
//	SK = "COUNTER"                                -> atomic board_task_id counter
//	SK = "TASK#<zero-padded boardTaskID>"         -> a task
//	SK = "LINK#<from>#<to>#<type>"                -> a task link
//
// Phase 3 sets partitionPrefix to "REPO#<owner>/<repo>#" (see NewWithPrefix)
// without any change to sort-key layout or item shape — that is the whole point
// of keeping the prefix a construction-time parameter rather than baking a
// single-tenant key in.
//
// # ID model
//
// SQLite distinguishes an internal global auto-increment id from the
// board-scoped 1..N board_task_id. DynamoDB has no global auto-increment. In
// the single-board, single-tenant Phase 2 world the two collapse safely, so
// this backend sets Task.ID == Task.BoardTaskID. Task links therefore reference
// the same 1..N number the user sees (which is what the CLI link commands pass),
// and the atomic counter is the single source of new ids per board.
package dynamo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/hmain/cainban/src/systems/task"
)

// DefaultTableName is used when the CAINBAN_DDB_TABLE env var is unset.
const DefaultTableName = "cainban"

// API is the subset of the DynamoDB client this backend uses. Declaring it as
// an interface lets tests inject a fake client without DynamoDB Local.
type API interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// Store is the DynamoDB implementation of store.TaskStore.
type Store struct {
	client          API
	table           string
	partitionPrefix string
	now             func() time.Time
}

// item is the on-DynamoDB representation of a task (and, with a different SK,
// board/counter/link records). Only the fields relevant to a given SK are set.
type item struct {
	PK          string     `dynamodbav:"PK"`
	SK          string     `dynamodbav:"SK"`
	BoardID     int        `dynamodbav:"board_id,omitempty"`
	BoardTaskID int        `dynamodbav:"board_task_id,omitempty"`
	Title       string     `dynamodbav:"title,omitempty"`
	Description string     `dynamodbav:"description,omitempty"`
	Status      string     `dynamodbav:"status,omitempty"`
	Priority    int        `dynamodbav:"priority"`
	DeletedAt   *time.Time `dynamodbav:"deleted_at,omitempty"`
	CreatedAt   time.Time  `dynamodbav:"created_at,omitempty"`
	UpdatedAt   time.Time  `dynamodbav:"updated_at,omitempty"`
	// Link fields (SK = LINK#...).
	FromTaskID int    `dynamodbav:"from_task_id,omitempty"`
	ToTaskID   int    `dynamodbav:"to_task_id,omitempty"`
	LinkType   string `dynamodbav:"link_type,omitempty"`
}

// New builds a Store from a live DynamoDB client (single-tenant, empty prefix).
func New(client API, table string) *Store {
	return NewWithPrefix(client, table, "")
}

// NewWithPrefix builds a Store with an explicit partition prefix. Phase 3 will
// pass "REPO#<owner>/<repo>#"; Phase 2 passes "".
func NewWithPrefix(client API, table, partitionPrefix string) *Store {
	if table == "" {
		table = DefaultTableName
	}
	return &Store{
		client:          client,
		table:           table,
		partitionPrefix: partitionPrefix,
		now:             time.Now,
	}
}

// --- key helpers -----------------------------------------------------------

func (s *Store) boardPK(boardID int) string {
	return fmt.Sprintf("%sBOARD#%d", s.partitionPrefix, boardID)
}

// taskSK zero-pads the board task id so lexical Query ordering matches numeric
// ordering up to 1e9-1 tasks per board.
func taskSK(boardTaskID int) string { return fmt.Sprintf("TASK#%09d", boardTaskID) }

const counterSK = "COUNTER"

func linkSK(from, to int, lt task.LinkType) string {
	return fmt.Sprintf("LINK#%09d#%09d#%s", from, to, lt)
}

// --- store.TaskStore -------------------------------------------------------

// Create adds a task with default priority.
func (s *Store) Create(boardID int, title, description string) (*task.Task, error) {
	return s.CreateWithPriority(boardID, title, description, task.PriorityNone)
}

// CreateWithPriority allocates the next board task id atomically and writes the
// task. The counter increment (UpdateItem ADD) is the atomic replacement for
// SQLite AUTOINCREMENT and guarantees a unique, monotonic 1..N id per board even
// under concurrent writers.
func (s *Store) CreateWithPriority(boardID int, title, description string, priority interface{}) (*task.Task, error) {
	if err := task.ValidateTitle(title); err != nil {
		return nil, err
	}
	if !task.IsValidPriority(priority) {
		return nil, fmt.Errorf("invalid priority level")
	}
	priorityLevel, _ := task.ParsePriority(priority)

	ctx := context.TODO()
	nextID, err := s.nextBoardTaskID(ctx, boardID)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	t := &task.Task{
		ID:          nextID,
		BoardID:     boardID,
		BoardTaskID: nextID,
		Title:       title,
		Description: description,
		Status:      task.StatusTodo,
		Priority:    priorityLevel,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	av, err := attributevalue.MarshalMap(s.taskItem(t))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal task: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      av,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}
	return t, nil
}

// nextBoardTaskID atomically increments the per-board counter and returns the
// new value. ADD on a missing number attribute starts from 0, so the first
// task gets id 1.
func (s *Store) nextBoardTaskID(ctx context.Context, boardID int) (int, error) {
	out, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(boardID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: counterSK},
		},
		UpdateExpression: aws.String("ADD seq :one"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":one": &ddbtypes.AttributeValueMemberN{Value: "1"},
		},
		ReturnValues: ddbtypes.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to allocate board task id: %w", err)
	}
	seqAV, ok := out.Attributes["seq"].(*ddbtypes.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("counter returned no seq value")
	}
	n, err := strconv.Atoi(seqAV.Value)
	if err != nil {
		return 0, fmt.Errorf("invalid counter value %q: %w", seqAV.Value, err)
	}
	return n, nil
}

func (s *Store) taskItem(t *task.Task) item {
	return item{
		PK:          s.boardPK(t.BoardID),
		SK:          taskSK(t.BoardTaskID),
		BoardID:     t.BoardID,
		BoardTaskID: t.BoardTaskID,
		Title:       t.Title,
		Description: t.Description,
		Status:      string(t.Status),
		Priority:    t.Priority,
		DeletedAt:   t.DeletedAt,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

func (it item) toTask() *task.Task {
	return &task.Task{
		ID:          it.BoardTaskID,
		BoardID:     it.BoardID,
		BoardTaskID: it.BoardTaskID,
		Title:       it.Title,
		Description: it.Description,
		Status:      task.Status(it.Status),
		Priority:    it.Priority,
		DeletedAt:   it.DeletedAt,
		CreatedAt:   it.CreatedAt,
		UpdatedAt:   it.UpdatedAt,
	}
}

// GetByID is GetByBoardTaskID for the default board (ID == BoardTaskID here).
func (s *Store) GetByID(id int) (*task.Task, error) {
	return s.GetByBoardTaskID(1, id)
}

// GetByBoardTaskID fetches one task item and rejects soft-deleted rows.
func (s *Store) GetByBoardTaskID(boardID, boardTaskID int) (*task.Task, error) {
	out, err := s.client.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(boardID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: taskSK(boardTaskID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, fmt.Errorf("task #%d not found in board %d", boardTaskID, boardID)
	}
	var it item
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}
	if it.DeletedAt != nil {
		return nil, fmt.Errorf("task #%d not found in board %d", boardTaskID, boardID)
	}
	return it.toTask(), nil
}

// queryTasks returns all task items (SK begins_with TASK#) for a board.
func (s *Store) queryTasks(ctx context.Context, boardID int) ([]*task.Task, error) {
	var tasks []*task.Task
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk":     &ddbtypes.AttributeValueMemberS{Value: s.boardPK(boardID)},
				":prefix": &ddbtypes.AttributeValueMemberS{Value: "TASK#"},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list tasks: %w", err)
		}
		for _, raw := range out.Items {
			var it item
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("failed to unmarshal task: %w", err)
			}
			if it.DeletedAt != nil {
				continue // soft-deleted
			}
			tasks = append(tasks, it.toTask())
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	// Match SQLite ordering: priority DESC, board_task_id ASC.
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		return tasks[i].BoardTaskID < tasks[j].BoardTaskID
	})
	return tasks, nil
}

// List returns all non-deleted tasks for a board.
func (s *Store) List(boardID int) ([]*task.Task, error) {
	return s.queryTasks(context.TODO(), boardID)
}

// ListByStatus filters List by status.
func (s *Store) ListByStatus(boardID int, status task.Status) ([]*task.Task, error) {
	all, err := s.queryTasks(context.TODO(), boardID)
	if err != nil {
		return nil, err
	}
	var out []*task.Task
	for _, t := range all {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, nil
}

// updateTaskFields applies a set of field updates to a task item (default
// board) and always bumps updated_at. Returns an error if the task is missing.
func (s *Store) updateTaskFields(id int, expr string, names map[string]string, values map[string]ddbtypes.AttributeValue) error {
	now := s.now().UTC()
	values[":updated"] = &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}
	fullExpr := expr + ", updated_at = :updated"
	_, err := s.client.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: taskSK(id)},
		},
		UpdateExpression:          aws.String(fullExpr),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ConditionExpression:       aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cf) {
			return fmt.Errorf("task with id %d not found", id)
		}
		return fmt.Errorf("failed to update task %d: %w", id, err)
	}
	return nil
}

// UpdateStatus updates a task's status.
func (s *Store) UpdateStatus(id int, status task.Status) error {
	if !task.IsValidStatus(string(status)) {
		return fmt.Errorf("invalid status: %s", status)
	}
	return s.updateTaskFields(id,
		"SET #s = :status",
		map[string]string{"#s": "status"},
		map[string]ddbtypes.AttributeValue{":status": &ddbtypes.AttributeValueMemberS{Value: string(status)}},
	)
}

// Update updates a task's title and description.
func (s *Store) Update(id int, title, description string) error {
	if err := task.ValidateTitle(title); err != nil {
		return err
	}
	return s.updateTaskFields(id,
		"SET title = :title, description = :desc",
		nil,
		map[string]ddbtypes.AttributeValue{
			":title": &ddbtypes.AttributeValueMemberS{Value: title},
			":desc":  &ddbtypes.AttributeValueMemberS{Value: description},
		},
	)
}

// UpdatePriority updates a task's priority.
func (s *Store) UpdatePriority(id int, priority interface{}) error {
	priorityLevel, err := task.ParsePriority(priority)
	if err != nil {
		return err
	}
	return s.updateTaskFields(id,
		"SET priority = :priority",
		nil,
		map[string]ddbtypes.AttributeValue{
			":priority": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(priorityLevel)},
		},
	)
}

// Delete soft-deletes a task (SQLite default behavior).
func (s *Store) Delete(id int) error { return s.SoftDelete(id) }

// SoftDelete marks a task deleted without removing the item.
func (s *Store) SoftDelete(id int) error {
	now := s.now().UTC()
	_, err := s.client.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: taskSK(id)},
		},
		UpdateExpression: aws.String("SET deleted_at = :now, updated_at = :now"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":now": &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		},
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_not_exists(deleted_at)"),
	})
	if err != nil {
		var cf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cf) {
			return fmt.Errorf("task %d not found or already deleted", id)
		}
		return fmt.Errorf("failed to soft delete task: %w", err)
	}
	return nil
}

// RestoreTask clears deleted_at on a soft-deleted task.
func (s *Store) RestoreTask(id int) error {
	now := s.now().UTC()
	_, err := s.client.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: taskSK(id)},
		},
		UpdateExpression: aws.String("REMOVE deleted_at SET updated_at = :now"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":now": &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		},
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_exists(deleted_at)"),
	})
	if err != nil {
		var cf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cf) {
			return fmt.Errorf("task %d not found or not deleted", id)
		}
		return fmt.Errorf("failed to restore task: %w", err)
	}
	return nil
}

// HardDelete permanently removes a task and all its links.
func (s *Store) HardDelete(id int) error {
	ctx := context.TODO()
	// Delete the task's links first (both directions).
	links, err := s.GetTaskLinks(id)
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := s.deleteLinkItem(ctx, l.FromTaskID, l.ToTaskID, l.LinkType); err != nil {
			return fmt.Errorf("failed to delete task links: %w", err)
		}
	}
	_, err = s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: taskSK(id)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &cf) {
			return fmt.Errorf("task %d not found", id)
		}
		return fmt.Errorf("failed to delete task: %w", err)
	}
	return nil
}

// SearchTasks fuzzy-matches titles within a board, reusing task.System's
// scoring via a shared exported helper is not possible (unexported), so this
// mirrors the same substring-first ranking the SQLite path uses.
func (s *Store) SearchTasks(boardID int, query string) ([]*task.Task, error) {
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}
	tasks, err := s.List(boardID)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		t     *task.Task
		score int
	}
	var hits []scored
	for _, t := range tasks {
		if sc := task.FuzzyMatchScore(strings.ToLower(t.Title), q); sc > 0 {
			hits = append(hits, scored{t: t, score: sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]*task.Task, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.t)
	}
	return out, nil
}

// FindTaskByFuzzyID resolves a board-scoped ID or a fuzzy title to one task,
// mirroring task.System.FindTaskByFuzzyID.
func (s *Store) FindTaskByFuzzyID(boardID int, idOrQuery string) (*task.Task, error) {
	if boardTaskID, err := strconv.Atoi(idOrQuery); err == nil {
		if t, err := s.GetByBoardTaskID(boardID, boardTaskID); err == nil {
			return t, nil
		}
	}
	matches, err := s.SearchTasks(boardID, idOrQuery)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		if _, numErr := strconv.Atoi(idOrQuery); numErr == nil {
			return nil, fmt.Errorf("no task found with ID #%s and no tasks found matching '%s'", idOrQuery, idOrQuery)
		}
		return nil, fmt.Errorf("no tasks found matching '%s'", idOrQuery)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	var suggestions []string
	for i, m := range matches {
		if i >= 5 {
			break
		}
		suggestions = append(suggestions, fmt.Sprintf("#%d %s", m.BoardTaskID, m.Title))
	}
	return nil, fmt.Errorf("multiple tasks match '%s':\n%s\nPlease be more specific or use the task ID",
		idOrQuery, strings.Join(suggestions, "\n"))
}

// --- links -----------------------------------------------------------------

// LinkTasks creates a link between two tasks (both must exist, no self-link).
func (s *Store) LinkTasks(fromTaskID, toTaskID int, linkType task.LinkType) error {
	if _, err := s.GetByID(fromTaskID); err != nil {
		return fmt.Errorf("from task not found: %w", err)
	}
	if _, err := s.GetByID(toTaskID); err != nil {
		return fmt.Errorf("to task not found: %w", err)
	}
	if fromTaskID == toTaskID {
		return fmt.Errorf("cannot link task to itself")
	}
	it := item{
		PK:         s.boardPK(1),
		SK:         linkSK(fromTaskID, toTaskID, linkType),
		FromTaskID: fromTaskID,
		ToTaskID:   toTaskID,
		LinkType:   string(linkType),
		CreatedAt:  s.now().UTC(),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("failed to marshal link: %w", err)
	}
	_, err = s.client.PutItem(context.TODO(), &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("failed to create task link: %w", err)
	}
	return nil
}

func (s *Store) deleteLinkItem(ctx context.Context, from, to int, lt task.LinkType) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: linkSK(from, to, lt)},
		},
	})
	return err
}

// UnlinkTasks removes a specific link.
func (s *Store) UnlinkTasks(fromTaskID, toTaskID int, linkType task.LinkType) error {
	out, err := s.client.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: linkSK(fromTaskID, toTaskID, linkType)},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to remove task link: %w", err)
	}
	if len(out.Item) == 0 {
		return fmt.Errorf("no link found between tasks %d and %d with type %s", fromTaskID, toTaskID, linkType)
	}
	if err := s.deleteLinkItem(context.TODO(), fromTaskID, toTaskID, linkType); err != nil {
		return fmt.Errorf("failed to remove task link: %w", err)
	}
	return nil
}

// GetTaskLinks returns all links referencing a task (either direction).
func (s *Store) GetTaskLinks(taskID int) ([]task.TaskLink, error) {
	ctx := context.TODO()
	var links []task.TaskLink
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk":     &ddbtypes.AttributeValueMemberS{Value: s.boardPK(1)},
				":prefix": &ddbtypes.AttributeValueMemberS{Value: "LINK#"},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query task links: %w", err)
		}
		for _, raw := range out.Items {
			var it item
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("failed to scan task link: %w", err)
			}
			if it.FromTaskID == taskID || it.ToTaskID == taskID {
				links = append(links, task.TaskLink{
					FromTaskID: it.FromTaskID,
					ToTaskID:   it.ToTaskID,
					LinkType:   task.LinkType(it.LinkType),
					CreatedAt:  it.CreatedAt,
				})
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	// Newest first, matching the SQLite ORDER BY created_at DESC.
	sort.SliceStable(links, func(i, j int) bool { return links[i].CreatedAt.After(links[j].CreatedAt) })
	return links, nil
}
