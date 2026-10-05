// Command cainban-grants-backfill is a ONE-SHOT operator migration that sets a
// default repo for existing single-grant users who connected a repo before the
// "default-repo on first grant" feature shipped.
//
// # Why it exists
//
// The connect API now sets a subject's default_repo (the grants-table META item)
// to the FIRST repo they grant, so a single-repo user's MCP config can omit the
// X-Cainban-Repo header — the token's default_repo claim names the repo. Users
// who granted a repo BEFORE that change have a GRANT# item but no META default,
// so a header-free config would resolve to the UNSCOPED tenant and fail. This
// tool backfills the META default for exactly those users.
//
// # What it does (and does not) touch
//
//	For each subject (PK=USER#<sub>) in the grants table:
//	  - exactly ONE GRANT# item AND no META default -> write META default = that repo
//	  - more than one GRANT# item                   -> SKIP (multi-repo users choose their own)
//	  - already has a META default                  -> SKIP (never clobber)
//
// It ONLY ever calls SetDefaultRepo. It never creates, modifies, or deletes a
// GRANT# or IDENTITY# item. Writing the same default twice is an idempotent
// overwrite of the identical META item, so the tool is safe to re-run.
//
// # Safety
//
// It DEFAULTS to -dry-run=true: the first run only reports what it WOULD write.
// Re-run with -dry-run=false to apply. It operates on the cainban-grants table
// alone and needs no IAM beyond Scan + PutItem on that table (operator creds).
//
// Build (pure Go):
//
//	CGO_ENABLED=0 go build -o cainban-grants-backfill ./cmd/cainban-grants-backfill
//
// Run:
//
//	CAINBAN_GRANTS_TABLE=cainban-grants AWS_REGION=eu-central-1 \
//	    ./cainban-grants-backfill            # dry-run (default)
//	CAINBAN_GRANTS_TABLE=cainban-grants AWS_REGION=eu-central-1 \
//	    ./cainban-grants-backfill -dry-run=false   # apply
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/hmain/cainban/src/systems/grants"
)

// Key prefixes / sort keys, mirroring src/systems/grants (kept local so this
// one-shot does not force the grants package to export its internal layout).
const (
	pkPrefix        = "USER#"
	grantSKPrefix   = "GRANT#"
	metaSK          = "META"
	defaultRepoAttr = "default_repo"
)

// subjectState accumulates, per subject, the grants seen and whether a non-empty
// META default already exists.
type subjectState struct {
	subject    string
	grants     []string
	hasDefault bool
}

// plannedWrite is one backfill action: set subject's default to repo.
type plannedWrite struct {
	subject string
	repo    string
}

// backfillStats is the summary a run prints.
type backfillStats struct {
	subjectsScanned int
	writes          int
	skippedMulti    int
	skippedHasDef   int
	skippedNoGrant  int
}

// decideBackfill is the PURE decision core: given the per-subject state, it
// returns the writes to perform (sorted for stable output) and the summary
// counts. It is unit-tested without any DynamoDB.
//
// A write is planned ONLY for a subject with exactly one grant and no existing
// default. Multi-grant and already-defaulted subjects are skipped. A subject
// with zero grants (e.g. an IDENTITY#-only row) is counted but never written.
func decideBackfill(subjects []subjectState) ([]plannedWrite, backfillStats) {
	var writes []plannedWrite
	var st backfillStats
	for _, s := range subjects {
		st.subjectsScanned++
		switch {
		case len(s.grants) == 0:
			st.skippedNoGrant++
		case s.hasDefault:
			st.skippedHasDef++
		case len(s.grants) > 1:
			st.skippedMulti++
		default: // exactly one grant, no default
			writes = append(writes, plannedWrite{subject: s.subject, repo: s.grants[0]})
			st.writes++
		}
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].subject < writes[j].subject })
	return writes, st
}

func main() {
	dryRun := flag.Bool("dry-run", true, "report what would be written without changing anything (default true)")
	tableFlag := flag.String("table", "", "grants table name (overrides CAINBAN_GRANTS_TABLE)")
	regionFlag := flag.String("region", "", "AWS region (overrides CAINBAN_GRANTS_REGION / AWS_REGION)")
	flag.Parse()

	table := firstNonEmpty(*tableFlag, os.Getenv("CAINBAN_GRANTS_TABLE"))
	if table == "" {
		log.Fatal("grants table name required: set -table or CAINBAN_GRANTS_TABLE")
	}
	region := firstNonEmpty(*regionFlag, os.Getenv("CAINBAN_GRANTS_REGION"), os.Getenv("AWS_REGION"))

	ctx := context.Background()
	var optFns []func(*awsconfig.LoadOptions) error
	if region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	ddb := dynamodb.NewFromConfig(cfg)
	store := grants.New(ddb, table)

	subjects, err := scanSubjects(ctx, ddb, table)
	if err != nil {
		log.Fatalf("scan grants table %q: %v", table, err)
	}

	writes, st := decideBackfill(subjects)

	mode := "DRY-RUN (no changes)"
	if !*dryRun {
		mode = "APPLY"
	}
	fmt.Printf("cainban-grants-backfill: table=%s region=%s mode=%s\n", table, orDefault(region, "<default>"), mode)

	for _, wr := range writes {
		if *dryRun {
			fmt.Printf("  would set default: sub=%s -> %s\n", wr.subject, wr.repo)
			continue
		}
		if err := store.SetDefaultRepo(ctx, wr.subject, wr.repo); err != nil {
			log.Printf("  ERROR setting default sub=%s -> %s: %v", wr.subject, wr.repo, err)
			continue
		}
		fmt.Printf("  set default: sub=%s -> %s\n", wr.subject, wr.repo)
	}

	verb := "would write"
	if !*dryRun {
		verb = "wrote"
	}
	fmt.Printf("summary: scanned=%d %s=%d skipped(multi-grant)=%d skipped(already-default)=%d skipped(no-grant)=%d\n",
		st.subjectsScanned, verb, st.writes, st.skippedMulti, st.skippedHasDef, st.skippedNoGrant)
}

// scanSubjects reads the whole grants table (paginated) and groups items by
// subject into subjectState records. Only GRANT# and META items are inspected;
// everything else (IDENTITY#github) is ignored for the grant/default decision
// but its subject is still tracked (so a subject with ONLY an identity row is
// counted as zero-grant and skipped).
func scanSubjects(ctx context.Context, ddb *dynamodb.Client, table string) ([]subjectState, error) {
	bySubject := map[string]*subjectState{}
	get := func(sub string) *subjectState {
		s, ok := bySubject[sub]
		if !ok {
			s = &subjectState{subject: sub}
			bySubject[sub] = s
		}
		return s
	}

	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := ddb.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(table),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, err
		}
		for _, itm := range out.Items {
			pkAV, ok := itm["PK"].(*ddbtypes.AttributeValueMemberS)
			if !ok || !strings.HasPrefix(pkAV.Value, pkPrefix) {
				continue
			}
			subject := pkAV.Value[len(pkPrefix):]
			if subject == "" {
				continue
			}
			skAV, ok := itm["SK"].(*ddbtypes.AttributeValueMemberS)
			if !ok {
				continue
			}
			s := get(subject)
			switch {
			case strings.HasPrefix(skAV.Value, grantSKPrefix):
				repo := skAV.Value[len(grantSKPrefix):]
				if repo != "" {
					s.grants = append(s.grants, repo)
				}
			case skAV.Value == metaSK:
				if dv, ok := itm[defaultRepoAttr].(*ddbtypes.AttributeValueMemberS); ok && strings.TrimSpace(dv.Value) != "" {
					s.hasDefault = true
				}
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}

	subjects := make([]subjectState, 0, len(bySubject))
	for _, s := range bySubject {
		subjects = append(subjects, *s)
	}
	return subjects, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
