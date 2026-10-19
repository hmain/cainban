// Package wsconn implements the mcp.ActivityBroadcaster interface over an AWS
// API Gateway WebSocket API, for the serverless (Lambda) deployment.
//
// On a long-running server the in-process mcp.Hub fans activity events out to
// the live *websocket.Conn set it holds. Lambda has no such process: the set of
// connected browsers is persisted in DynamoDB (store.WSConnectionStore, written
// by the $connect/$disconnect Lambda), and a mutation Lambda pushes a frame to
// each connection through the API Gateway Management API (PostToConnection).
//
// The wire frame is IDENTICAL to the in-process Hub's: { "type":"event",
// "event": <wsActivity> }. The Hub builds that envelope from the unexported
// wsActivity type; this package is a sibling package that receives the already-
// serialized activity value as an interface{} (mcp.ActivityBroadcaster.Broadcast's
// `event` argument is exactly mcp.activityToWS(ev)) and wraps it in the same
// envelope shape via an exported struct, so the two producers stay wire-compatible
// with the frontend adapter (web/src/board/wsAdapter.ts).
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

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

// postAPI is the subset of the API Gateway Management API client this package
// uses. Declaring it as an interface lets tests inject a fake without a live
// endpoint.
type postAPI interface {
	PostToConnection(ctx context.Context, in *apigatewaymanagementapi.PostToConnectionInput, optFns ...func(*apigatewaymanagementapi.Options)) (*apigatewaymanagementapi.PostToConnectionOutput, error)
}

// connStoreOpener opens a repo-scoped WSConnectionStore. It matches
// store.OpenWSConnectionStoreForTenant so production wiring passes that function
// directly and tests pass a fake.
type connStoreOpener func(ctx context.Context, partitionPrefix string) (store.WSConnectionStore, func() error, error)

// envelope is the wire frame pushed to a client. It mirrors the in-process
// Hub's { "type":"event", "event":... } exactly; Event is the already-
// serialized activity (mcp.activityToWS output), carried as a generic value so
// this package need not import the mcp package's unexported wire type.
type envelope struct {
	Type  string      `json:"type"`
	Event interface{} `json:"event"`
}

// Broadcaster fans a single activity event out to every API Gateway WebSocket
// connection watching a repo. It satisfies mcp.ActivityBroadcaster.
type Broadcaster struct {
	post postAPI
	open connStoreOpener
}

// New builds a Broadcaster posting to the given API Gateway WebSocket callback
// endpoint (the per-stage URL, e.g.
// https://<apiId>.execute-api.<region>.amazonaws.com/<stage>). cfg supplies
// credentials/region from the Lambda execution role.
func New(cfg aws.Config, endpoint string) *Broadcaster {
	client := apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	return &Broadcaster{
		post: client,
		open: store.OpenWSConnectionStoreForTenant,
	}
}

// Broadcast delivers one activity event to every connection watching repo. It
// returns the number of connections it successfully posted to. A connection
// that returns 410 Gone (the client disconnected without a delivered
// $disconnect) is pruned from the store so it is not retried. Any other
// per-connection error is logged and skipped — a single bad connection never
// fails the whole broadcast, and the broadcast never fails the tool call (it is
// best-effort observability, as the ActivityBroadcaster contract requires).
func (b *Broadcaster) Broadcast(ctx context.Context, repo string, event interface{}) (int, error) {
	norm, err := auth.NormalizeRepo(repo)
	if err != nil {
		return 0, fmt.Errorf("broadcast: %w", err)
	}
	prefix := auth.PartitionPrefixFor(norm)

	connStore, closeFn, err := b.open(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("broadcast: open connection store: %w", err)
	}
	defer func() { _ = closeFn() }()

	ids, err := connStore.ListWSConnectionIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("broadcast: list connections: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	data, err := json.Marshal(envelope{Type: "event", Event: event})
	if err != nil {
		return 0, fmt.Errorf("broadcast: marshal frame: %w", err)
	}

	sent := 0
	for _, id := range ids {
		_, perr := b.post.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
			ConnectionId: aws.String(id),
			Data:         data,
		})
		if perr == nil {
			sent++
			continue
		}
		var gone *apitypes.GoneException
		if errors.As(perr, &gone) {
			// The client is gone but its $disconnect never arrived. Prune the
			// stale fan-out row so future broadcasts skip it. Best-effort.
			if derr := connStore.DeleteWSConnectionInScope(ctx, id); derr != nil {
				log.Printf("cainban: prune stale ws connection %s for %s: %v", id, norm, derr)
			}
			continue
		}
		log.Printf("cainban: post to ws connection %s for %s: %v", id, norm, perr)
	}
	return sent, nil
}
