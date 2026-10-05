package store

import (
	"strconv"

	"github.com/hmain/cainban/src/systems/board"
)

// sqliteBoardStore is the single-user (SQLite) implementation of BoardStore. It
// is a thin adapter over the existing board.System, which scans the local
// ~/.cainban tree. This preserves single-user behaviour exactly: the list and
// resolution logic is board.System's, unchanged.
//
// board.System has no persistent numeric board id (the filesystem scan leaves
// Board.ID == 0), so this adapter assigns ids by convention: the "default"
// board is id 1 and any additional local board files are numbered in the order
// board.System returns them. list_boards historically printed only names, so
// these ids are additive metadata and contradict no stored identity.
type sqliteBoardStore struct {
	sys *board.System
}

// newSQLiteBoardStore wraps a board.System as a BoardStore.
func newSQLiteBoardStore(sys *board.System) *sqliteBoardStore {
	return &sqliteBoardStore{sys: sys}
}

// ListBoards maps board.System's local boards to BoardSummary with convention
// ids (default => 1, extras numbered by scan order).
func (s *sqliteBoardStore) ListBoards() ([]BoardSummary, error) {
	boards, err := s.sys.ListBoards()
	if err != nil {
		return nil, err
	}
	summaries := make([]BoardSummary, 0, len(boards))
	for i, b := range boards {
		summaries = append(summaries, BoardSummary{ID: i + 1, Name: b.Name})
	}
	return summaries, nil
}

// ResolveBoard resolves a numeric selector against the convention ids and a
// name selector against the local board files, returning ErrBoardNotFound on no
// match.
func (s *sqliteBoardStore) ResolveBoard(selector string) (BoardSummary, error) {
	summaries, err := s.ListBoards()
	if err != nil {
		return BoardSummary{}, err
	}
	if n, convErr := strconv.Atoi(selector); convErr == nil {
		for _, sum := range summaries {
			if sum.ID == n {
				return sum, nil
			}
		}
		return BoardSummary{}, ErrBoardNotFound
	}
	for _, sum := range summaries {
		if sum.Name == selector {
			return sum, nil
		}
	}
	return BoardSummary{}, ErrBoardNotFound
}

// SetCurrent sets the local current board for CLI/TUI continuity. Only the
// SQLite adapter implements this; the handler reaches it via a type assertion,
// so the DynamoDB store never mutates shared state. Best-effort by contract: a
// caller may ignore the error.
func (s *sqliteBoardStore) SetCurrent(name string) error {
	return s.sys.SetCurrentBoard(name)
}

var _ BoardStore = (*sqliteBoardStore)(nil)
