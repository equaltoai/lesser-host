// Command soul-agent-identity-gsi3-backfill is the operator tool for the
// SoulAgentIdentity gsi3 status enumeration index (issue #1061 part C1).
//
// The stack update that creates gsi3 deploys as its own stack update (DynamoDB
// creates one GSI per UpdateTable). After the operator deploys it, this tool
// backfills gsi3PK/gsi3SK onto the existing identity items that predate the
// index. It is dry-run by default; pass --apply to write. It never clobbers
// concurrent live writes (conditional updates only), throttles between pages,
// persists a LastEvaluatedKey checkpoint so interrupted runs resume, refuses to
// run until gsi3 exists and is ACTIVE, and writes a completeness marker only
// after a complete error-free apply pass — the request-path identity
// enumerations fail closed until that marker exists.
//
// Usage:
//
//	go run ./scripts/soul-agent-identity-gsi3-backfill --profile <aws-profile> --stage <lab|live> [--apply]
//
// AWS_PROFILE is honored when --profile is omitted. The table resolves to
// lesser-host-<stage>-state unless overridden with --table.
//
// Part C2 (issue #1067) extends this tool to the SoulAgentMintConversation gsi3
// backfill by adding a second modelPlan; the scan/checkpoint/throttle machinery
// is model-agnostic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const (
	defaultRegion = "us-east-1"
	stateTableFmt = "lesser-host-%s-state"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opt, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "soul-agent-identity-gsi3-backfill: %v\n", err)
		os.Exit(2)
	}

	client, err := newDDBClient(opt.profile, opt.region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soul-agent-identity-gsi3-backfill: %v\n", err)
		os.Exit(1)
	}

	mode := "dry-run"
	if opt.apply {
		mode = "apply"
	}
	fmt.Printf("soul-agent-identity-gsi3-backfill mode=%s table=%s page_size=%d sleep_ms=%d\n",
		mode, opt.table, opt.pageSize, opt.sleepMS)

	rep, err := run(ctx, opt, client, os.Stdout, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soul-agent-identity-gsi3-backfill: %v\n", err)
		os.Exit(1)
	}
	if rep == nil {
		fmt.Fprintf(os.Stderr, "soul-agent-identity-gsi3-backfill: no report\n")
		os.Exit(1)
	}
	state := "complete"
	if rep.Interrupted {
		state = "interrupted"
	}
	fmt.Printf("soul-agent-identity-gsi3-backfill %s %s\n", state, rep.String())
}

// parseArgs resolves flags into options, honoring AWS_PROFILE for the profile.
func parseArgs(args []string) (options, error) {
	fs := flag.NewFlagSet("soul-agent-identity-gsi3-backfill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	stage := fs.String("stage", "", "deploy stage (lab|live); resolves the table as lesser-host-<stage>-state")
	profile := fs.String("profile", "", "AWS profile (default: AWS_PROFILE env)")
	region := fs.String("region", "", fmt.Sprintf("AWS region (default: %s or AWS_REGION)", defaultRegion))
	table := fs.String("table", "", "override table name (default: lesser-host-<stage>-state)")
	checkpoint := fs.String("checkpoint", "", "checkpoint file path (default: soul-agent-identity-gsi3-backfill.<stage>.checkpoint.json)")
	apply := fs.Bool("apply", false, "apply updates (default: dry-run)")
	pageSize := fs.Int("page-size", defaultPageSize, "scan page size (max 200)")
	sleepMS := fs.Int("sleep-ms", defaultSleepMS, "base sleep between scan pages in ms (jitter up to +50%)")
	resume := fs.Bool("resume", false, "resume from the checkpoint file (must exist)")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	stage = strPtr(strings.ToLower(strings.TrimSpace(*stage)))
	if *stage != "lab" && *stage != "live" {
		return options{}, errors.New("--stage must be lab or live")
	}

	regionVal := strings.TrimSpace(*region)
	if regionVal == "" {
		regionVal = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if regionVal == "" {
		regionVal = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if regionVal == "" {
		regionVal = defaultRegion
	}

	profileVal := strings.TrimSpace(*profile)
	if profileVal == "" {
		profileVal = strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	}

	tableVal := strings.TrimSpace(*table)
	if tableVal == "" {
		tableVal = fmt.Sprintf(stateTableFmt, *stage)
	}

	checkpointVal := strings.TrimSpace(*checkpoint)
	if checkpointVal == "" {
		checkpointVal = fmt.Sprintf("soul-agent-identity-gsi3-backfill.%s.checkpoint.json", *stage)
	}

	pageSizeVal := *pageSize
	if pageSizeVal <= 0 {
		pageSizeVal = defaultPageSize
	}
	if pageSizeVal > maxPageSize {
		pageSizeVal = maxPageSize
	}
	sleepMSVal := *sleepMS
	if sleepMSVal < 0 {
		sleepMSVal = 0
	}

	return options{
		stage:      *stage,
		profile:    profileVal,
		region:     regionVal,
		table:      tableVal,
		checkpoint: checkpointVal,
		apply:      *apply,
		pageSize:   pageSizeVal,
		sleepMS:    sleepMSVal,
		resume:     *resume,
	}, nil
}

// newDDBClient loads the AWS config (honoring the resolved profile) and returns
// a DynamoDB client.
func newDDBClient(profile, region string) (ddbAPI, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return dynamodb.NewFromConfig(cfg), nil
}

func strPtr(s string) *string { return &s }

// --- checkpoint file IO ---

func loadCheckpoint(path string) (*checkpoint, error) {
	// #nosec G703 -- checkpoint path is an operator-supplied CLI flag, not network input
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ckpt checkpoint
	if err := json.Unmarshal(body, &ckpt); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &ckpt, nil
}

func saveCheckpoint(path string, ckpt checkpoint) error {
	body, err := json.Marshal(&ckpt)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		// #nosec G703 -- checkpoint path is an operator-supplied CLI flag, not network input
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	// #nosec G703 -- checkpoint path is an operator-supplied CLI flag, not network input
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	// #nosec G703 -- checkpoint path is an operator-supplied CLI flag, not network input
	return os.Rename(tmp, path)
}

func removeCheckpoint(path string) error {
	// #nosec G703 -- checkpoint path is an operator-supplied CLI flag, not network input
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// #nosec G703 -- checkpoint path is an operator-supplied CLI flag, not network input
func statPath(path string) (os.FileInfo, error) { return os.Stat(path) }
