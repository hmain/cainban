package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hmain/cainban/src/systems/board"
)

// writeFile creates an empty file, making parent dirs as needed.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSQLiteBoardStore_ListResolveSetCurrent(t *testing.T) {
	dir := t.TempDir()
	// board.System.ListBoards keys off the default db file and boards/*.db.
	touch(t, filepath.Join(dir, "cainban.db"))        // default board
	touch(t, filepath.Join(dir, "boards", "work.db")) // one extra board

	bs := newSQLiteBoardStore(board.NewWithConfigDir(dir))

	boards, err := bs.ListBoards()
	if err != nil {
		t.Fatalf("ListBoards: %v", err)
	}
	if len(boards) != 2 {
		t.Fatalf("want 2 boards, got %+v", boards)
	}
	// default is first with id 1 (convention).
	if boards[0].ID != 1 || boards[0].Name != "default" {
		t.Fatalf("first board = %+v, want {1 default}", boards[0])
	}
	// Names present for both.
	names := map[string]bool{}
	for _, b := range boards {
		names[b.Name] = true
	}
	if !names["default"] || !names["work"] {
		t.Fatalf("missing expected board names: %+v", boards)
	}

	if got, err := bs.ResolveBoard("default"); err != nil || got.ID != 1 {
		t.Fatalf("ResolveBoard(default) = %+v, err=%v", got, err)
	}
	if got, err := bs.ResolveBoard("1"); err != nil || got.Name != "default" {
		t.Fatalf("ResolveBoard(1) = %+v, err=%v", got, err)
	}
	if got, err := bs.ResolveBoard("work"); err != nil || got.Name != "work" {
		t.Fatalf("ResolveBoard(work) = %+v, err=%v", got, err)
	}
	if _, err := bs.ResolveBoard("missing"); !errors.Is(err, ErrBoardNotFound) {
		t.Fatalf("ResolveBoard(missing) err = %v, want ErrBoardNotFound", err)
	}

	// SetCurrent writes the current-board file.
	if err := bs.SetCurrent("work"); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "current-board"))
	if err != nil {
		t.Fatalf("read current-board: %v", err)
	}
	if string(data) != "work" {
		t.Fatalf("current-board = %q, want %q", string(data), "work")
	}
}
