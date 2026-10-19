package main

import (
	"context"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/hmain/cainban/src/systems/mcp"
	"github.com/hmain/cainban/src/systems/mcp/wsconn"
)

// WebSocket real-time fan-out env vars the CDK stack sets on the MCP function
// when the WebSocket API is deployed. All three must be present to enable the
// broadcaster; any missing one disables real-time push (best-effort feature).
const (
	envWSConnTable   = "CAINBAN_WS_CONN_TABLE"
	envWSConnRegion  = "CAINBAN_WS_CONN_REGION"
	envWSCallbackURL = "CAINBAN_WS_CALLBACK_URL"
)

// buildBroadcaster constructs the API Gateway WebSocket broadcaster from env.
// It returns (nil, nil) when the WebSocket env is unset — the common case for a
// deployment without the WebSocket API — so the caller treats "not configured"
// differently from "misconfigured". It returns a non-nil error only when the
// env is partially set (a real misconfiguration worth logging).
func buildBroadcaster() (mcp.ActivityBroadcaster, error) {
	table := strings.TrimSpace(os.Getenv(envWSConnTable))
	callbackURL := strings.TrimSpace(os.Getenv(envWSCallbackURL))
	if table == "" && callbackURL == "" {
		return nil, nil // WebSocket API not deployed; feature off.
	}

	ctx := context.Background()
	region := strings.TrimSpace(os.Getenv(envWSConnRegion))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	var optFns []func(*awsconfig.LoadOptions) error
	if region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, err
	}

	store := wsconn.New(dynamodb.NewFromConfig(cfg), table)
	mgmt := apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
		o.BaseEndpoint = &callbackURL
	})
	return wsconn.NewBroadcaster(store, mgmt), nil
}
