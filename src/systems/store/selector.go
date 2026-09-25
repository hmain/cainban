package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/hmain/cainban/src/systems/dynamo"
	"github.com/hmain/cainban/src/systems/storage"
	"github.com/hmain/cainban/src/systems/task"
)

// Backend names for the CAINBAN_BACKEND selector.
const (
	BackendSQLite   = "sqlite"
	BackendDynamoDB = "dynamodb"

	// EnvBackend selects the storage backend. Default is sqlite so local
	// `cainban tui` / CLI work with no AWS involvement. Set to "dynamodb" for
	// the Lambda/serverless path.
	EnvBackend = "CAINBAN_BACKEND"
	// EnvDDBTable overrides the DynamoDB table name (default "cainban").
	EnvDDBTable = "CAINBAN_DDB_TABLE"
	// EnvDDBRegion overrides the AWS region for the DynamoDB client.
	EnvDDBRegion = "CAINBAN_DDB_REGION"
)

// Selected reports the configured backend name (defaulting to sqlite).
func Selected() string {
	if b := strings.ToLower(strings.TrimSpace(os.Getenv(EnvBackend))); b != "" {
		return b
	}
	return BackendSQLite
}

// OpenTask returns a TaskStore for the configured backend plus a close func the
// caller MUST call when done.
//
//   - sqlite   (default): opens the SQLite DB at sqlitePath (CGO required) and
//     returns a *task.System. Used by the local CLI/TUI.
//   - dynamodb: builds a DynamoDB-backed store using the default AWS config
//     chain (env/instance/role creds). sqlitePath is ignored. Pure Go.
//
// The DynamoDB path is what the Lambda entrypoint uses; there, credentials come
// from the Lambda execution role and the region/table from env vars set by the
// CDK stack.
func OpenTask(ctx context.Context, sqlitePath string) (TaskStore, func() error, error) {
	switch Selected() {
	case BackendDynamoDB:
		return openDynamo(ctx)
	case BackendSQLite:
		return openSQLite(sqlitePath)
	default:
		return nil, nil, fmt.Errorf("unknown %s=%q (want %q or %q)",
			EnvBackend, Selected(), BackendSQLite, BackendDynamoDB)
	}
}

func openSQLite(path string) (TaskStore, func() error, error) {
	db, err := storage.New(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open sqlite backend: %w", err)
	}
	return task.New(db.Conn()), db.Close, nil
}

func openDynamo(ctx context.Context) (TaskStore, func() error, error) {
	return openDynamoWithPrefix(ctx, "")
}

// openDynamoWithPrefix builds a DynamoDB-backed store scoped to partitionPrefix
// (Phase 3: "REPO#<owner>/<repo>#"). Empty prefix is the single-tenant Phase 2
// behavior. The AWS config + DynamoDB client are cached process-wide (see
// dynamoClient) and reused across requests; only the prefix varies per tenant,
// which is the cheap per-request tenant switch the Lambda path relies on.
func openDynamoWithPrefix(ctx context.Context, partitionPrefix string) (TaskStore, func() error, error) {
	client, err := dynamoClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	table := strings.TrimSpace(os.Getenv(EnvDDBTable))
	if table == "" {
		table = dynamo.DefaultTableName
	}
	// No handle to close for the DynamoDB client (it is shared/cached).
	return dynamo.NewWithPrefix(client, table, partitionPrefix), func() error { return nil }, nil
}

// dynamoClientCache memoizes the AWS config + DynamoDB client so per-request,
// per-tenant store construction does not reload credentials or rebuild the HTTP
// client on every call. Safe for concurrent use.
var (
	dynamoClientMu    sync.Mutex
	dynamoClientCache *dynamodb.Client
)

func dynamoClient(ctx context.Context) (*dynamodb.Client, error) {
	dynamoClientMu.Lock()
	defer dynamoClientMu.Unlock()
	if dynamoClientCache != nil {
		return dynamoClientCache, nil
	}
	var opts []func(*awsconfig.LoadOptions) error
	if region := strings.TrimSpace(os.Getenv(EnvDDBRegion)); region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	dynamoClientCache = dynamodb.NewFromConfig(cfg)
	return dynamoClientCache, nil
}

// OpenTaskForTenant returns a TaskStore scoped to a tenant's DynamoDB partition
// prefix (Phase 3). It is only meaningful for the DynamoDB backend; for SQLite
// (local single-tenant dev) the prefix is ignored and it behaves like OpenTask.
// The caller MUST call the returned close func.
func OpenTaskForTenant(ctx context.Context, sqlitePath, partitionPrefix string) (TaskStore, func() error, error) {
	switch Selected() {
	case BackendDynamoDB:
		return openDynamoWithPrefix(ctx, partitionPrefix)
	case BackendSQLite:
		// Local dev is single-tenant; the prefix has no meaning for SQLite.
		return openSQLite(sqlitePath)
	default:
		return nil, nil, fmt.Errorf("unknown %s=%q (want %q or %q)",
			EnvBackend, Selected(), BackendSQLite, BackendDynamoDB)
	}
}

// Compile-time assertions that both backends satisfy TaskStore.
var (
	_ TaskStore = (*task.System)(nil)
	_ TaskStore = (*dynamo.Store)(nil)
)
