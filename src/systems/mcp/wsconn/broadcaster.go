package wsconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"
)

// MgmtAPI is the subset of the API Gateway Management API client the broadcaster
// uses. Narrow so tests can fake it and the IAM grant stays least-privilege
// (execute-api:ManageConnections only).
type MgmtAPI interface {
	PostToConnection(ctx context.Context, in *apigatewaymanagementapi.PostToConnectionInput, optFns ...func(*apigatewaymanagementapi.Options)) (*apigatewaymanagementapi.PostToConnectionOutput, error)
}

// Broadcaster posts activity frames to the WebSocket connections watching a
// repo, using the API Gateway Management API. It looks up the live connections
// in the Store (by repo, authenticated only) and PostToConnection to each. A
// connection that is GONE (410) is pruned from the store so a stale row never
// accumulates — exactly the "handle stale/gone connections gracefully"
// requirement. Any other post error is logged and skipped; one dead client
// never fails the whole broadcast.
//
// The frame wire shape matches the in-process hub's event frame
// ({ "type": "event", "event": {...} }) so the frontend adapter
// (web/src/board/wsAdapter.ts) handles an API-Gateway-delivered event
// identically to a hub-delivered one.
type Broadcaster struct {
	store *Store
	mgmt  MgmtAPI
}

// NewBroadcaster wires a connection store to a management-API client.
func NewBroadcaster(store *Store, mgmt MgmtAPI) *Broadcaster {
	return &Broadcaster{store: store, mgmt: mgmt}
}

// Frame is the JSON envelope posted to a connection. Field names mirror the
// in-process hub's wsMessage so the browser adapter needs no branch for the
// transport.
type Frame struct {
	Type  string      `json:"type"`
	Event interface{} `json:"event,omitempty"`
}

// EventFrame builds the standard { "type":"event", "event":<payload> } frame.
func EventFrame(event interface{}) Frame { return Frame{Type: "event", Event: event} }

// Broadcast delivers one activity frame to every authenticated connection
// watching repo. It is best-effort: a delivery failure to one connection is
// logged (and, for a 410 Gone, the connection is pruned) but never aborts the
// fan-out or surfaces to the caller as a hard error unless the connection
// LOOKUP itself fails. Returns the number of connections a frame was
// successfully posted to.
func (b *Broadcaster) Broadcast(ctx context.Context, repo string, event interface{}) (int, error) {
	if repo == "" {
		return 0, nil
	}
	ids, err := b.store.ListAuthenticatedForRepo(ctx, repo)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	data, err := json.Marshal(EventFrame(event))
	if err != nil {
		return 0, fmt.Errorf("wsconn: marshal frame: %w", err)
	}

	sent := 0
	for _, id := range ids {
		if postErr := b.post(ctx, id, data); postErr != nil {
			if isGone(postErr) {
				// The socket closed without a clean $disconnect; prune the row
				// so the next broadcast does not try it again.
				if rmErr := b.store.Remove(ctx, id); rmErr != nil {
					log.Printf("wsconn: prune gone connection %s: %v", id, rmErr)
				}
				continue
			}
			log.Printf("wsconn: post to %s: %v", id, postErr)
			continue
		}
		sent++
	}
	return sent, nil
}

// Send posts a single pre-marshaled frame to one connection (used by the Lambda
// for direct replies like pong / snapshot / error). A 410 Gone prunes the row.
func (b *Broadcaster) Send(ctx context.Context, connectionID string, frame interface{}) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("wsconn: marshal: %w", err)
	}
	if err := b.post(ctx, connectionID, data); err != nil {
		if isGone(err) {
			_ = b.store.Remove(ctx, connectionID)
		}
		return err
	}
	return nil
}

func (b *Broadcaster) post(ctx context.Context, connectionID string, data []byte) error {
	_, err := b.mgmt.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
		ConnectionId: aws.String(connectionID),
		Data:         data,
	})
	return err
}

// isGone reports whether a PostToConnection error is the 410 GoneException the
// management API returns for a connection that has already closed.
func isGone(err error) bool {
	var gone *apitypes.GoneException
	return errors.As(err, &gone)
}
