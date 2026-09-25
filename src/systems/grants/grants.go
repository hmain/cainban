// Package grants is the DynamoDB-backed home for a subject's per-repo access
// grants — the "real" grant store introduced in Phase 4 that supersedes the
// Cognito custom:repos attribute (which the pre-token trigger keeps as a
// migration fallback; see cmd/cainban-pretoken).
//
// # Why a SEPARATE table (not the single "cainban" table)
//
// The Phase 2/3 "cainban" table is TENANT DATA: boards, the per-board counter,
// tasks and links, every item keyed under a REPO#<owner>/<repo># partition
// prefix. Grants are a DIFFERENT axis — they are keyed by SUBJECT (USER#<sub>),
// answer a different question ("which repos may this subject touch?"), and are
// read on a different path (the Cognito pre-token Lambda, which must never see
// task data). Keeping grants in their own table:
//
//   - lets the pre-token Lambda's IAM be scoped to the grants table ARN alone,
//     so it structurally cannot read or write any board/task item (least
//     privilege — the tightest possible surface for a token-minting trigger);
//   - keeps PITR / backup / retention boundaries clean per concern;
//   - avoids overloading one partition keyspace with two unrelated key shapes.
//
// Cost is identical to co-tenanting: both tables are PAY_PER_REQUEST (on-demand)
// and cost nothing at rest. The frugal argument that once favored a single
// table (one board's counter+tasks+links co-located) does not apply here —
// grants share no partition with board data and are never read together with
// it. So a separate "cainban-grants" table is both cleaner and tighter.
//
// # Key design
//
// One item per grant; presence of the item IS the grant (no boolean to keep in
// sync). A subject's grants are one partition, so ListReposForSubject is a
// single Query.
//
//	PK = USER#<subject>          SK = GRANT#<owner>/<repo>   -> a granted repo
//	PK = USER#<subject>          SK = META                   -> default_repo marker
//	PK = USER#<subject>          SK = IDENTITY#github        -> linked GitHub identity/install (P4.2/P4.3 populate; minimal now)
//
// <subject> is the Cognito sub (human) or, in a later phase, an agent
// principal's client-id — keyed identically. owner/repo is normalized EXACTLY
// as the auth resolver normalizes it (auth.NormalizeRepo: trim, validate
// owner/repo form, case-PRESERVING because repo names are case-sensitive on the
// forge) so a grant maps to exactly one partition prefix and matches the same
// string the validator authorizes against.
//
// # Fail-closed contract
//
// This package never fabricates a grant. A read that finds nothing returns an
// empty result and a nil error; an underlying DynamoDB error is returned to the
// caller UNMASKED so the caller (the pre-token trigger) can fail CLOSED — an
// error must never be swallowed into "no grants" in a way that could later be
// confused with "grant exists". The pre-token trigger's own logic decides that
// an error => emit no claim (never fall through to a fabricated grant).
//
// It is PURE GO (no CGO): the pre-token Lambda builds with CGO_ENABLED=0.
package grants

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/hmain/cainban/src/systems/auth"
)

// DefaultTableName is used when no table name is supplied (env CAINBAN_GRANTS_TABLE).
const DefaultTableName = "cainban-grants"

// Key prefixes / sort keys. Kept unexported constants so the layout has a
// single source of truth.
const (
	pkPrefix        = "USER#"
	grantSKPrefix   = "GRANT#"
	metaSK          = "META"
	identitySKGH    = "IDENTITY#github"
	defaultRepoAttr = "default_repo"
)

// API is the subset of the DynamoDB client this package uses. Declaring it as
// an interface (mirroring src/systems/dynamo.API) lets tests inject an
// in-memory fake with no DynamoDB Local and no live AWS.
type API interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// Store reads and writes a subject's grants in the grants table.
type Store struct {
	client API
	table  string
}

// New builds a Store from a live (or fake) DynamoDB client and a table name.
// An empty table name falls back to DefaultTableName.
func New(client API, table string) *Store {
	if table == "" {
		table = DefaultTableName
	}
	return &Store{client: client, table: table}
}

// --- key helpers -----------------------------------------------------------

func subjectPK(subject string) string { return pkPrefix + subject }

func grantSK(repo string) string { return grantSKPrefix + repo }

// --- reads -----------------------------------------------------------------

// ListReposForSubject returns the normalized "owner/repo" grants for a subject,
// sorted for stable output. It is a single Query over the subject's partition,
// filtered to the GRANT# sort-key range.
//
// An empty subject returns (nil, nil) — no partition, no grants. A DynamoDB
// error is returned UNMASKED (never collapsed into an empty list), so the
// caller can fail closed. A subject with no grant items returns an empty slice
// and nil error.
func (s *Store) ListReposForSubject(ctx context.Context, subject string) ([]string, error) {
	if subject == "" {
		return nil, nil
	}
	var repos []string
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := s.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk":     &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
				":prefix": &ddbtypes.AttributeValueMemberS{Value: grantSKPrefix},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("grants: list repos for subject: %w", err)
		}
		for _, itm := range out.Items {
			skAV, ok := itm["SK"].(*ddbtypes.AttributeValueMemberS)
			if !ok {
				continue
			}
			repo := skAV.Value[len(grantSKPrefix):]
			if repo != "" {
				repos = append(repos, repo)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	sort.Strings(repos)
	return repos, nil
}

// Get reports whether a subject has a grant for a repo. The repo is normalized
// first; a malformed repo returns (false, error) without touching DynamoDB.
func (s *Store) Get(ctx context.Context, subject, repo string) (bool, error) {
	norm, err := auth.NormalizeRepo(repo)
	if err != nil {
		return false, fmt.Errorf("grants: get: %w", err)
	}
	if subject == "" {
		return false, errors.New("grants: get: empty subject")
	}
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: grantSK(norm)},
		},
	})
	if err != nil {
		return false, fmt.Errorf("grants: get: %w", err)
	}
	return len(out.Item) > 0, nil
}

// GetDefaultRepo returns the subject's default repo (normalized) or "" when
// none is set. A DynamoDB error is returned unmasked.
func (s *Store) GetDefaultRepo(ctx context.Context, subject string) (string, error) {
	if subject == "" {
		return "", nil
	}
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: metaSK},
		},
	})
	if err != nil {
		return "", fmt.Errorf("grants: get default repo: %w", err)
	}
	if len(out.Item) == 0 {
		return "", nil
	}
	av, ok := out.Item[defaultRepoAttr].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return "", nil
	}
	return av.Value, nil
}

// --- writes ----------------------------------------------------------------
//
// PutGrant / DeleteGrant / SetDefaultRepo are not used by the P4.1 pre-token
// read path; they are implemented now (cheap) for the P4.3 connect API to write
// verified grants. They are deliberately simple item put/deletes — the connect
// API owns verification and authorization; this package only persists.

// PutGrant records a grant for subject -> repo. The repo is normalized first;
// a malformed repo is rejected before any write. Idempotent (a repeated put
// overwrites the same item).
func (s *Store) PutGrant(ctx context.Context, subject, repo string) error {
	norm, err := auth.NormalizeRepo(repo)
	if err != nil {
		return fmt.Errorf("grants: put grant: %w", err)
	}
	if subject == "" {
		return errors.New("grants: put grant: empty subject")
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item: map[string]ddbtypes.AttributeValue{
			"PK":   &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
			"SK":   &ddbtypes.AttributeValueMemberS{Value: grantSK(norm)},
			"repo": &ddbtypes.AttributeValueMemberS{Value: norm},
		},
	})
	if err != nil {
		return fmt.Errorf("grants: put grant: %w", err)
	}
	return nil
}

// DeleteGrant revokes a grant for subject -> repo. The repo is normalized
// first. Deleting a non-existent grant is a no-op (no error).
func (s *Store) DeleteGrant(ctx context.Context, subject, repo string) error {
	norm, err := auth.NormalizeRepo(repo)
	if err != nil {
		return fmt.Errorf("grants: delete grant: %w", err)
	}
	if subject == "" {
		return errors.New("grants: delete grant: empty subject")
	}
	_, err = s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: grantSK(norm)},
		},
	})
	if err != nil {
		return fmt.Errorf("grants: delete grant: %w", err)
	}
	return nil
}

// SetDefaultRepo sets (or, with an empty repo, clears) the subject's default
// repo on the META item. A non-empty repo is normalized first.
func (s *Store) SetDefaultRepo(ctx context.Context, subject, repo string) error {
	if subject == "" {
		return errors.New("grants: set default repo: empty subject")
	}
	if repo == "" {
		// Clear: delete the META item.
		_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(s.table),
			Key: map[string]ddbtypes.AttributeValue{
				"PK": &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
				"SK": &ddbtypes.AttributeValueMemberS{Value: metaSK},
			},
		})
		if err != nil {
			return fmt.Errorf("grants: clear default repo: %w", err)
		}
		return nil
	}
	norm, err := auth.NormalizeRepo(repo)
	if err != nil {
		return fmt.Errorf("grants: set default repo: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item: map[string]ddbtypes.AttributeValue{
			"PK":            &ddbtypes.AttributeValueMemberS{Value: subjectPK(subject)},
			"SK":            &ddbtypes.AttributeValueMemberS{Value: metaSK},
			defaultRepoAttr: &ddbtypes.AttributeValueMemberS{Value: norm},
		},
	})
	if err != nil {
		return fmt.Errorf("grants: set default repo: %w", err)
	}
	return nil
}
