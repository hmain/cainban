// Command cainban-pretoken is the AWS Cognito PRE-TOKEN-GENERATION Lambda
// trigger for cainban's auth. It exists to bridge one specific gap:
//
//	The auth validator (src/systems/auth) authorizes a request from a TOP-LEVEL
//	`repos` claim (the set of "owner/repo" the caller may touch) plus an optional
//	top-level `default_repo`. Those top-level claims are populated here, at token
//	generation, from the user's GRANTS — and this trigger returns
//	claimsToAddOrOverride with:
//
//	repos:        a JSON-array-ENCODED STRING of the user's "owner/repo" grants
//	              (Cognito claim-override values are always strings; the
//	              validator's reposClaim decoder accepts this string shape).
//	default_repo: the user's default repo (string), when set.
//
// # Phase 4 (P4.1): the DynamoDB grants table is the grant home
//
// Grants now live in a DynamoDB GRANTS TABLE (src/systems/grants), keyed
// PK=USER#<cognitoSub> / SK=GRANT#<owner>/<repo>. At token generation this
// trigger looks the subject's grants up in that table and builds the `repos`
// claim from them. The Cognito custom:repos / custom:default_repo attributes
// remain as a MIGRATION FALLBACK so nothing breaks for users not yet migrated.
//
// Resolution order (per event):
//
//  1. Read grants from the table for the event subject (the `sub` standard
//     attribute; falls back to userName only if sub is absent).
//  2. TABLE HAS GRANTS  -> build the repos claim from the table (+ table default_repo).
//  3. TABLE EMPTY       -> fall back to the custom:repos / custom:default_repo
//     attribute path (the pre-P4.1 buildClaims logic), so existing users keep working.
//  4. TABLE ERROR       -> FAIL CLOSED: emit NO repos claim (no fallback, no
//     fabricated grant). A transient DynamoDB error must never silently drop a
//     user to the attribute path (which could widen or narrow their true grant
//     set unpredictably) nor invent access. No claim => the user has no grants
//     for this token => 403 downstream, which is the safe outcome.
//
// It NEVER invents a grant: an empty table AND empty attributes yield no `repos`
// claim, so the caller is unauthorized (403) — the safe default. The exact
// emitted shape is preserved: `repos` is a JSON-array-encoded string and
// `default_repo` is a string.
//
// The Lambda needs a DynamoDB client and the grants table name from env
// (CAINBAN_GRANTS_TABLE; region from CAINBAN_GRANTS_REGION / AWS_REGION). The
// CDK stack grants it least-privilege read (GetItem + Query) on the grants
// table ARN only. If the table env is UNSET, the trigger degrades to the
// attribute-only path (buildClaims) — it never fails token issuance on missing
// config, it simply has no table to read.
//
// Build (arm64, provided.al2023, pure Go — no CGO, no SQLite):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-pretoken
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"unicode"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/hmain/cainban/src/systems/grants"
)

// attrRepos / attrDefaultRepo are the Cognito custom-attribute names holding a
// user's repo grants (the migration FALLBACK source). Cognito prefixes custom
// attributes with "custom:".
const (
	attrRepos       = "custom:repos"
	attrDefaultRepo = "custom:default_repo"

	// attrSub is the standard Cognito attribute carrying the user's immutable
	// subject id — the SAME value the JWT validator reads as `sub` and the key
	// the grants table is partitioned by (PK=USER#<sub>).
	attrSub = "sub"

	// claimRepos / claimDefaultRepo are the TOP-LEVEL claim names the validator
	// reads. The trigger maps grants onto these.
	claimRepos       = "repos"
	claimDefaultRepo = "default_repo"

	// envGrantsTable / envGrantsRegion configure the grants-table client. When
	// the table env is unset the trigger degrades to the attribute-only path.
	envGrantsTable  = "CAINBAN_GRANTS_TABLE"
	envGrantsRegion = "CAINBAN_GRANTS_REGION"
)

// grantsReader is the subset of grants.Store the handler needs. Declaring it as
// an interface lets the handler be unit-tested with an in-memory fake (no live
// DynamoDB), covering: table-has-grants, table-empty (fallback), table-error
// (fail closed).
type grantsReader interface {
	ListReposForSubject(ctx context.Context, subject string) ([]string, error)
	GetDefaultRepo(ctx context.Context, subject string) (string, error)
}

func main() {
	// Build the grants-table client from env once at cold start. A configuration
	// problem is logged but is NOT fatal: with no reader the trigger degrades to
	// the attribute-only fallback path rather than failing every token issuance.
	reader := buildGrantsReader(context.Background())
	lambda.Start(makeHandler(reader))
}

// buildGrantsReader constructs a grants.Store from env, or returns nil when the
// grants table is not configured (CAINBAN_GRANTS_TABLE unset) or the AWS config
// cannot be loaded. A nil reader means "no table" — the handler falls back to
// the custom-attribute path.
func buildGrantsReader(ctx context.Context) grantsReader {
	table := strings.TrimSpace(os.Getenv(envGrantsTable))
	if table == "" {
		log.Printf("cainban-pretoken: %s unset — using custom:repos attribute path only", envGrantsTable)
		return nil
	}
	var optFns []func(*awsconfig.LoadOptions) error
	if region := strings.TrimSpace(os.Getenv(envGrantsRegion)); region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		// No client => attribute-only fallback. Do not fail token issuance.
		log.Printf("cainban-pretoken: AWS config load failed (%v) — using custom:repos attribute path only", err)
		return nil
	}
	return grants.New(dynamodb.NewFromConfig(cfg), table)
}

// makeHandler returns the Lambda handler bound to a grants reader (which may be
// nil when the table is not configured). Split out from main so tests can
// inject a fake reader.
func makeHandler(reader grantsReader) func(context.Context, events.CognitoEventUserPoolsPreTokenGen) (events.CognitoEventUserPoolsPreTokenGen, error) {
	return func(ctx context.Context, event events.CognitoEventUserPoolsPreTokenGen) (events.CognitoEventUserPoolsPreTokenGen, error) {
		overrides := resolveClaims(ctx, reader, event)
		if len(overrides) > 0 {
			event.Response.ClaimsOverrideDetails.ClaimsToAddOrOverride = overrides
		}
		return event, nil
	}
}

// eventSubject returns the subject the grants table is keyed by: the standard
// `sub` attribute (matches the token's `sub` claim), falling back to the header
// userName only when `sub` is absent from the event.
func eventSubject(event events.CognitoEventUserPoolsPreTokenGen) string {
	if sub := strings.TrimSpace(event.Request.UserAttributes[attrSub]); sub != "" {
		return sub
	}
	return strings.TrimSpace(event.UserName)
}

// resolveClaims is the Phase 4 claim resolver. It reads grants from the table
// (when a reader is configured) and falls back to the custom-attribute path,
// failing CLOSED on a table error. It returns the top-level claim overrides to
// emit — never fabricating a grant.
//
// Table-vs-fallback precedence:
//   - reader nil (no table configured)           -> attribute path (buildClaims)
//   - table read ERROR                            -> fail closed: NO claims
//   - table has >=1 grant                         -> claims from the table
//   - table empty                                 -> attribute path (buildClaims)
func resolveClaims(ctx context.Context, reader grantsReader, event events.CognitoEventUserPoolsPreTokenGen) map[string]string {
	attrs := event.Request.UserAttributes

	// No table configured: pure attribute fallback (pre-P4.1 behavior).
	if reader == nil {
		return buildClaims(attrs)
	}

	subject := eventSubject(event)
	if subject == "" {
		// No subject to key the table by — fall back to attributes.
		return buildClaims(attrs)
	}

	repos, err := reader.ListReposForSubject(ctx, subject)
	if err != nil {
		// FAIL CLOSED. A table error must not fall through to the attribute path
		// or fabricate a grant. Emit no claims -> no grants on this token -> 403.
		log.Printf("cainban-pretoken: grants table read failed for subject — failing closed (no repos claim): %v", err)
		return map[string]string{}
	}

	if len(repos) == 0 {
		// Table has nothing for this subject: fall back to the custom:repos
		// attribute path so not-yet-migrated users keep working.
		return buildClaims(attrs)
	}

	// Table has grants: build the claim from the table. The repos are already
	// normalized by the grants store on write; encode as the JSON-array string
	// shape the validator consumes.
	out := make(map[string]string, 2)
	encoded, _ := json.Marshal(repos)
	out[claimRepos] = string(encoded)

	// default_repo from the table (best-effort): a read error here must not
	// fail closed on the whole token (the repos claim is already sound) and must
	// not fabricate — on error we simply omit default_repo.
	if def, derr := reader.GetDefaultRepo(ctx, subject); derr == nil && def != "" {
		out[claimDefaultRepo] = def
	} else if derr != nil {
		log.Printf("cainban-pretoken: grants table default_repo read failed — omitting default_repo: %v", derr)
	}
	return out
}

// buildClaims is the pure, unit-testable FALLBACK core: given the user's Cognito
// attributes, it returns the top-level claims to add/override. It emits:
//
//   - `repos`        only when custom:repos parses to at least one grant, as a
//     JSON-array-encoded string (e.g. `["acme/a","acme/b"]`).
//   - `default_repo` only when custom:default_repo is non-empty.
//
// It NEVER fabricates a grant: empty/absent attributes yield an empty map, so
// the token carries no repos claim and the caller is unauthorized (403) — the
// safe default. custom:repos is parsed defensively, accepting the same shapes
// an operator might set it to: a JSON array string, or a space/comma-delimited
// list.
func buildClaims(userAttributes map[string]string) map[string]string {
	out := make(map[string]string, 2)

	repos := parseRepoList(userAttributes[attrRepos])
	if len(repos) > 0 {
		// Emit as a JSON-array-encoded string. json.Marshal of a []string never
		// fails, so the error is discarded deliberately.
		encoded, _ := json.Marshal(repos)
		out[claimRepos] = string(encoded)
	}

	if def := strings.TrimSpace(userAttributes[attrDefaultRepo]); def != "" {
		out[claimDefaultRepo] = def
	}

	return out
}

// parseRepoList tokenizes the custom:repos attribute into a de-duplicated,
// order-preserving list of "owner/repo" tokens. It accepts:
//
//	JSON array:       ["acme/a","acme/b"]
//	space-delimited:  acme/a acme/b
//	comma-delimited:  acme/a,acme/b
//	empty/absent:     no tokens
//
// It does not validate owner/repo form here (the validator canonicalizes and
// the resolver authorizes); it only splits and trims, dropping empties.
func parseRepoList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var tokens []string
	if raw[0] == '[' {
		// JSON array shape.
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			tokens = arr
		} else {
			// Malformed JSON array: fall back to delimiter splitting on the raw
			// text rather than dropping everything.
			tokens = splitDelimited(raw)
		}
	} else {
		tokens = splitDelimited(raw)
	}

	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		for _, t := range splitDelimited(tok) {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitDelimited splits a string on whitespace and commas, trimming and
// dropping empty tokens.
func splitDelimited(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
