package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func main() {
	var (
		agentID   string
		allActive bool
		apply     bool
		maxAgents int
		maxBytes  int64
	)

	flag.StringVar(&agentID, "agent-id", "", "Target agent id (0x... 32-byte hex)")
	flag.BoolVar(&allActive, "all-active", false, "Scan all active agents (warning: may be expensive)")
	flag.BoolVar(&apply, "apply", false, "Write audit log entries for agents that need re-attestation")
	flag.IntVar(&maxAgents, "max-agents", 0, "Max agents to scan (0 = unlimited)")
	flag.Int64Var(&maxBytes, "max-bytes", 512*1024, "Max bytes to download per S3 object")
	flag.Parse()

	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" && !allActive {
		die("must provide --agent-id or --all-active")
	}
	if strings.TrimSpace(os.Getenv("STATE_TABLE_NAME")) == "" {
		die("STATE_TABLE_NAME is required")
	}

	bucket := strings.TrimSpace(os.Getenv("SOUL_PACK_BUCKET_NAME"))
	if bucket == "" {
		die("SOUL_PACK_BUCKET_NAME is required")
	}

	mode := "dry-run"
	if apply {
		mode = "apply"
	}
	fmt.Printf("soul-integrity-scan-m2 mode=%s table=%s bucket=%s\n", mode, models.MainTableName(), bucket)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	db, err := store.LambdaInit()
	if err != nil {
		die("init store: %v", err)
	}
	st := store.New(db)
	packs := artifacts.New(bucket)

	idents := make([]*models.SoulAgentIdentity, 0, 1)
	if agentID != "" {
		id, err := loadIdentity(ctx, st, agentID)
		if err != nil {
			die("load identity: %v", err)
		}
		idents = append(idents, id)
	} else if allActive {
		items, err := listActiveIdentities(ctx, st)
		if err != nil {
			die("list identities: %v", err)
		}
		idents = items
	}

	scannedAgents := 0
	issueAgents := 0
	totalIssues := 0

	for _, id := range idents {
		if id == nil {
			continue
		}
		scannedAgents++
		if maxAgents > 0 && scannedAgents > maxAgents {
			break
		}

		issues, err := scanAgentRegistrationIntegrity(ctx, st, packs, bucket, id, maxBytes)
		if err != nil {
			die("scan agent %s: %v", strings.TrimSpace(id.AgentID), err)
		}

		if len(issues) == 0 {
			fmt.Printf("agent=%s ok\n", strings.TrimSpace(id.AgentID))
			continue
		}

		issueAgents++
		totalIssues += len(issues)
		fmt.Printf("agent=%s needs_reattestation=true issues=%d\n", strings.TrimSpace(id.AgentID), len(issues))
		for _, issue := range issues {
			fmt.Printf("  - %s\n", issue)
		}

		if apply {
			if err := writeReattestationAuditFlag(ctx, st, strings.TrimSpace(id.AgentID), issues); err != nil {
				die("write audit flag: %v", err)
			}
		}
	}

	fmt.Printf("summary scanned_agents=%d issue_agents=%d issues=%d\n", scannedAgents, issueAgents, totalIssues)
	if issueAgents > 0 {
		os.Exit(2)
	}
}

func scanAgentRegistrationIntegrity(ctx context.Context, st *store.Store, packs *artifacts.Store, bucket string, identity *models.SoulAgentIdentity, maxBytes int64) ([]string, error) {
	if st == nil || st.DB == nil || packs == nil || identity == nil {
		return nil, errors.New("internal error")
	}
	agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, errors.New("bucket is required")
	}
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}

	issues := make([]string, 0, 8)

	versions, err := listVersionRecords(ctx, st, agentID)
	if err != nil {
		return nil, err
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].VersionNumber == versions[j].VersionNumber {
			return versions[i].CreatedAt.Before(versions[j].CreatedAt)
		}
		return versions[i].VersionNumber < versions[j].VersionNumber
	})

	maxVersion := 0
	shaSet := map[string]struct{}{}
	byVersion := map[int]*models.SoulAgentVersion{}
	for _, v := range versions {
		if v == nil {
			continue
		}
		if v.VersionNumber > maxVersion {
			maxVersion = v.VersionNumber
		}
		if strings.TrimSpace(v.RegistrationSHA256) != "" {
			shaSet[strings.ToLower(strings.TrimSpace(v.RegistrationSHA256))] = struct{}{}
		}
		byVersion[v.VersionNumber] = v
	}

	// Check identity-version alignment.
	if maxVersion > 0 && identity.SelfDescriptionVersion != maxVersion {
		issues = append(issues, fmt.Sprintf("identity self_description_version=%d does not match latest version=%d", identity.SelfDescriptionVersion, maxVersion))
	}

	// Verify S3 sha for each version record.
	for _, v := range versions {
		if v == nil {
			continue
		}
		sha := strings.ToLower(strings.TrimSpace(v.RegistrationSHA256))
		if sha == "" {
			issues = append(issues, fmt.Sprintf("version %d missing registration_sha256", v.VersionNumber))
			continue
		}
		if len(sha) != 64 {
			issues = append(issues, fmt.Sprintf("version %d invalid registration_sha256=%q", v.VersionNumber, sha))
			continue
		}

		key := deriveS3KeyFromRegistrationURI(bucket, v.RegistrationUri)
		if key == "" {
			key = versionedS3Key(agentID, v.VersionNumber)
		}

		body, _, _, getErr := packs.GetObject(ctx, key, maxBytes)
		if getErr != nil {
			var nsk *s3types.NoSuchKey
			if errors.As(getErr, &nsk) {
				issues = append(issues, fmt.Sprintf("s3 missing versioned object for version %d key=%s", v.VersionNumber, key))
				continue
			}
			return nil, fmt.Errorf("s3 get version %d: %w", v.VersionNumber, getErr)
		}

		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != sha {
			issues = append(issues, fmt.Sprintf("sha mismatch version %d key=%s record=%s s3=%s", v.VersionNumber, key, sha, got))
		}
	}

	// Verify previous hash chain (best-effort).
	for _, v := range versions {
		if v == nil {
			continue
		}
		if v.VersionNumber <= 1 {
			continue
		}
		prev := byVersion[v.VersionNumber-1]
		if prev == nil {
			issues = append(issues, fmt.Sprintf("missing version record for version %d (needed by %d)", v.VersionNumber-1, v.VersionNumber))
			continue
		}
		wantPrev := strings.ToLower(strings.TrimSpace(prev.RegistrationSHA256))
		gotPrev := strings.ToLower(strings.TrimSpace(v.PreviousRegistrationSHA256))
		if wantPrev != "" && gotPrev != "" && wantPrev != gotPrev {
			issues = append(issues, fmt.Sprintf("hash chain mismatch at version %d prev_record=%s prev_field=%s", v.VersionNumber, wantPrev, gotPrev))
		}
	}

	// Verify current registration matches some version sha (or report it as orphaned).
	currentKey := currentS3Key(agentID)
	currentBody, _, _, curErr := packs.GetObject(ctx, currentKey, maxBytes)
	if curErr != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(curErr, &nsk) {
			if maxVersion > 0 {
				issues = append(issues, "s3 missing current registration.json")
			}
		} else {
			return nil, fmt.Errorf("s3 get current: %w", curErr)
		}
	} else {
		sum := sha256.Sum256(currentBody)
		curSHA := hex.EncodeToString(sum[:])
		if _, ok := shaSet[curSHA]; !ok && maxVersion > 0 {
			issues = append(issues, fmt.Sprintf("current registration sha not found in version history sha=%s", curSHA))
		}

		// If identity claims a latest version, ensure current matches that version's sha.
		if identity.SelfDescriptionVersion > 0 {
			if rec := byVersion[identity.SelfDescriptionVersion]; rec == nil {
				issues = append(issues, fmt.Sprintf("missing version record for identity self_description_version=%d", identity.SelfDescriptionVersion))
			} else if strings.TrimSpace(rec.RegistrationSHA256) != "" {
				want := strings.ToLower(strings.TrimSpace(rec.RegistrationSHA256))
				if curSHA != want {
					issues = append(issues, fmt.Sprintf("current registration sha does not match identity version=%d record=%s current=%s", identity.SelfDescriptionVersion, want, curSHA))
				}
			}
		}
	}

	return issues, nil
}

func loadIdentity(ctx context.Context, st *store.Store, agentID string) (*models.SoulAgentIdentity, error) {
	if st == nil || st.DB == nil {
		return nil, errors.New("store not configured")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}

	var out models.SoulAgentIdentity
	err := st.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "IDENTITY").
		First(&out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func listActiveIdentities(ctx context.Context, st *store.Store) ([]*models.SoulAgentIdentity, error) {
	if st == nil || st.DB == nil {
		return nil, errors.New("store not configured")
	}
	var items []*models.SoulAgentIdentity
	if err := st.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("SK", "=", "IDENTITY").
		All(&items); err != nil {
		return nil, err
	}
	out := make([]*models.SoulAgentIdentity, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		if strings.TrimSpace(it.Status) != models.SoulAgentStatusActive {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func listVersionRecords(ctx context.Context, st *store.Store, agentID string) ([]*models.SoulAgentVersion, error) {
	if st == nil || st.DB == nil {
		return nil, errors.New("store not configured")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}

	var items []*models.SoulAgentVersion
	err := st.DB.WithContext(ctx).
		Model(&models.SoulAgentVersion{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "VERSION#").
		All(&items)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func currentS3Key(agentID string) string {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	return fmt.Sprintf("registry/v1/agents/%s/registration.json", agentID)
}

func versionedS3Key(agentID string, version int) string {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	return fmt.Sprintf("registry/v1/agents/%s/versions/%d/registration.json", agentID, version)
}

func deriveS3KeyFromRegistrationURI(bucket string, uri string) string {
	bucket = strings.TrimSpace(bucket)
	uri = strings.TrimSpace(uri)
	if bucket == "" || uri == "" {
		return ""
	}

	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "s3" {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(u.Host), bucket) {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(u.Path), "/")
}

func writeReattestationAuditFlag(ctx context.Context, st *store.Store, agentID string, issues []string) error {
	if st == nil || st.DB == nil {
		return errors.New("store not configured")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return errors.New("agent id is required")
	}

	reason := ""
	if len(issues) > 0 {
		reason = issues[0]
		if len(reason) > 200 {
			reason = reason[:200]
		}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     "integrity_scan_m2",
		Action:    "soul.integrity.needs_reattestation",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentID),
		RequestID: fmt.Sprintf("scan_%d", now.Unix()),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	// Best-effort: store the first issue as the action suffix.
	if reason != "" {
		audit.Action = audit.Action + ":" + reason
	}

	if err := st.DB.WithContext(ctx).Model(audit).Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			// Duplicate audit entry: ignore.
			return nil
		}
		return err
	}
	return nil
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// parseVersionFromKey is used for diagnostics only.
func parseVersionFromKey(key string) (int, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, false
	}
	// registry/v1/agents/<id>/versions/<n>/registration.json
	parts := strings.Split(key, "/")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "versions" && i+1 < len(parts) {
			n, err := strconv.Atoi(parts[i+1])
			return n, err == nil && n > 0
		}
	}
	return 0, false
}
