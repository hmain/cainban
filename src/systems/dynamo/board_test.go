package dynamo

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/task"
)

// seedBoardMeta writes a BOARD#<id>#META row directly into the fake, as a
// tenant with an explicit board row would have.
func seedBoardMeta(t *testing.T, fake *fakeDDB, store *Store, id int, name string) {
	t.Helper()
	av, err := attributevalue.MarshalMap(metaItem{
		PK:      store.boardPK(id),
		SK:      boardMetaSK,
		BoardID: id,
		Title:   name,
	})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if _, err := fake.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String("cainban-test"),
		Item:      av,
	}); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
}

func TestDynamoBoard_ListAndResolve(t *testing.T) {
	fake := newFakeDDB()
	st := NewWithPrefix(fake, "cainban-test", "")
	seedBoardMeta(t, fake, st, 1, "default")

	boards, err := st.ListBoards()
	if err != nil {
		t.Fatalf("ListBoards: %v", err)
	}
	if len(boards) != 1 || boards[0].ID != 1 || boards[0].Name != "default" {
		t.Fatalf("ListBoards = %+v, want [{1 default}]", boards)
	}

	byName, err := st.ResolveBoard("default")
	if err != nil {
		t.Fatalf("ResolveBoard(default): %v", err)
	}
	byID, err := st.ResolveBoard("1")
	if err != nil {
		t.Fatalf("ResolveBoard(1): %v", err)
	}
	if byName != byID || byName.ID != 1 {
		t.Fatalf("name and id selectors disagree: %+v vs %+v", byName, byID)
	}

	if _, err := st.ResolveBoard("nope"); !errors.Is(err, task.ErrBoardNotFound) {
		t.Fatalf("ResolveBoard(nope) err = %v, want ErrBoardNotFound", err)
	}
	if _, err := st.ResolveBoard("999"); !errors.Is(err, task.ErrBoardNotFound) {
		t.Fatalf("ResolveBoard(999) err = %v, want ErrBoardNotFound", err)
	}
}

// A tenant with tasks but no board row still lists board 1 after the next
// create triggers the lazy META upsert; a second create does not overwrite it.
func TestDynamoBoard_LazyMetaUpsertOnCreate(t *testing.T) {
	fake := newFakeDDB()
	st := NewWithPrefix(fake, "cainban-test", "")

	// Before any create: no board row.
	before, err := st.ListBoards()
	if err != nil {
		t.Fatalf("ListBoards before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("want no boards before create, got %+v", before)
	}

	if _, err := st.Create(1, "first task", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	after, err := st.ListBoards()
	if err != nil {
		t.Fatalf("ListBoards after: %v", err)
	}
	if len(after) != 1 || after[0].ID != 1 || after[0].Name != "default" {
		t.Fatalf("after create ListBoards = %+v, want [{1 default}]", after)
	}

	// A second create must not fail and must not overwrite the META row.
	if _, err := st.Create(1, "second task", ""); err != nil {
		t.Fatalf("second Create: %v", err)
	}
	again, err := st.ListBoards()
	if err != nil {
		t.Fatalf("ListBoards again: %v", err)
	}
	if len(again) != 1 || again[0].Name != "default" {
		t.Fatalf("second create changed board list: %+v", again)
	}
}

// ensureBoardMeta failure must not propagate: the create path ignores it.
func TestDynamoBoard_UpsertErrorDoesNotFailCreate(t *testing.T) {
	fake := &putFailOnMeta{fakeDDB: newFakeDDB()}
	st := NewWithPrefix(fake, "cainban-test", "")
	if _, err := st.Create(1, "task", ""); err != nil {
		t.Fatalf("Create must succeed even when the META put fails: %v", err)
	}
}

// putFailOnMeta makes only the BOARD#...#META PutItem fail, leaving the task
// PutItem (SK = TASK#...) working, to prove the best-effort upsert is isolated.
type putFailOnMeta struct {
	*fakeDDB
}

func (p *putFailOnMeta) PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if sk, ok := in.Item["SK"].(*ddbtypes.AttributeValueMemberS); ok && sk.Value == boardMetaSK {
		return nil, errors.New("simulated meta put failure")
	}
	return p.fakeDDB.PutItem(ctx, in, optFns...)
}

// Board rows are tenant-isolated: a store scoped to repo A never resolves repo
// B's board, because every key is built from the partition prefix.
func TestDynamoBoard_TenantIsolation(t *testing.T) {
	fake := newFakeDDB()
	repoA := NewWithPrefix(fake, "cainban-test", auth.PartitionPrefixFor("acme/a"))
	repoB := NewWithPrefix(fake, "cainban-test", auth.PartitionPrefixFor("acme/b"))

	seedBoardMeta(t, fake, repoA, 1, "default")
	// repoB has NO board row seeded.

	aBoards, err := repoA.ListBoards()
	if err != nil || len(aBoards) != 1 {
		t.Fatalf("repoA.ListBoards = %+v, err=%v; want one board", aBoards, err)
	}
	bBoards, err := repoB.ListBoards()
	if err != nil {
		t.Fatalf("repoB.ListBoards: %v", err)
	}
	if len(bBoards) != 0 {
		t.Fatalf("ISOLATION BREACH: repoB saw boards it did not own: %+v", bBoards)
	}
	if _, err := repoB.ResolveBoard("1"); !errors.Is(err, task.ErrBoardNotFound) {
		t.Fatalf("repoB.ResolveBoard(1) err = %v, want ErrBoardNotFound (A's row must be invisible)", err)
	}
}
