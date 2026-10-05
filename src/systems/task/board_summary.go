package task

import "errors"

// BoardSummary is the backend-neutral view of a board as the board tools
// (list_boards, change_board) expose it. It lives in package task — the shared
// leaf both store and dynamo already import — so the DynamoDB backend can
// return it without importing store (which would create an import cycle, since
// store imports dynamo). store re-exports it as store.BoardSummary.
type BoardSummary struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ErrBoardNotFound is returned by a BoardStore's ResolveBoard when the selector
// matches no board in the current scope. Callers use errors.Is to format a
// scope-aware "not found" message. store re-exports it as store.ErrBoardNotFound.
var ErrBoardNotFound = errors.New("board not found in scope")
