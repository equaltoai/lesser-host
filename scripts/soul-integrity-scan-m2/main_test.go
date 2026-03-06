package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type integrityTestDB struct {
	db     *ttmocks.MockExtendedDB
	qIdent *ttmocks.MockQuery
	qVer   *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

const integrityTestAgentID = "0xagent"

func newIntegrityTestDB() integrityTestDB {
	db := ttmocks.NewMockExtendedDB()
	qIdent := new(ttmocks.MockQuery)
	qVer := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentVersion")).Return(qVer).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qIdent, qVer, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	}
	qAudit.On("Create").Return(nil).Maybe()

	return integrityTestDB{db: db, qIdent: qIdent, qVer: qVer, qAudit: qAudit}
}

type integrityMemS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *integrityMemS3) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	bucket, key, ok := integrityParsePathStyle(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	fullKey := bucket + "/" + key
	m.mu.Lock()
	body, ok := m.objects[fullKey]
	m.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code></Error>`)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func integrityParsePathStyle(path string) (string, string, bool) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", "", false
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func newIntegrityArtifactStore(t *testing.T, bucket string, objects map[string][]byte) (*artifacts.Store, func()) {
	t.Helper()

	mem := &integrityMemS3{objects: objects}
	ts := httptest.NewServer(http.HandlerFunc(mem.handler))

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
		o.HTTPClient = ts.Client()
	})

	return artifacts.NewWithClient(bucket, client), ts.Close
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func TestIntegrityKeyHelpers(t *testing.T) {
	t.Parallel()

	if got := currentS3Key(" 0xAGENT "); got != "registry/v1/agents/0xagent/registration.json" {
		t.Fatalf("unexpected current key: %q", got)
	}
	if got := versionedS3Key("0xAGENT", 3); got != "registry/v1/agents/0xagent/versions/3/registration.json" {
		t.Fatalf("unexpected versioned key: %q", got)
	}
	if got := deriveS3KeyFromRegistrationURI("bucket", "s3://bucket/path/to.json"); got != "path/to.json" {
		t.Fatalf("unexpected derived key: %q", got)
	}
	if got := deriveS3KeyFromRegistrationURI("bucket", "https://example.com/nope"); got != "" {
		t.Fatalf("expected empty derived key for non-s3 uri, got %q", got)
	}
	if version, ok := parseVersionFromKey(versionedS3Key(integrityTestAgentID, 4)); !ok || version != 4 {
		t.Fatalf("unexpected parsed version: version=%d ok=%v", version, ok)
	}
	if _, ok := parseVersionFromKey("not/a/version"); ok {
		t.Fatalf("expected invalid key parse to fail")
	}
}

func TestIntegrityStoreHelpersAndAudit(t *testing.T) {
	ctx := context.Background()

	t.Run("load and filter helpers", func(t *testing.T) {
		tdb := newIntegrityTestDB()
		tdb.qIdent.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			dest.AgentID = integrityTestAgentID
		}).Once()
		tdb.qIdent.On("All", mock.AnythingOfType("*[]*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentIdentity](t, args, 0)
			*dest = []*models.SoulAgentIdentity{
				{AgentID: "0xactive", Status: models.SoulAgentStatusActive},
				{AgentID: "0xinactive", Status: models.SoulAgentStatusSuspended},
				nil,
			}
		}).Once()
		tdb.qVer.On("All", mock.AnythingOfType("*[]*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
			*dest = []*models.SoulAgentVersion{{VersionNumber: 1}}
		}).Once()

		st := store.New(tdb.db)
		if _, err := loadIdentity(ctx, st, " "); err == nil {
			t.Fatalf("expected validation error for empty agent id")
		}
		identity, err := loadIdentity(ctx, st, " 0xAGENT ")
		if err != nil || identity == nil || identity.AgentID != integrityTestAgentID {
			t.Fatalf("unexpected identity result: identity=%#v err=%v", identity, err)
		}

		items, err := listActiveIdentities(ctx, st)
		if err != nil || len(items) != 1 || items[0].AgentID != "0xactive" {
			t.Fatalf("unexpected active identities: items=%#v err=%v", items, err)
		}

		versions, err := listVersionRecords(ctx, st, integrityTestAgentID)
		if err != nil || len(versions) != 1 || versions[0].VersionNumber != 1 {
			t.Fatalf("unexpected versions result: versions=%#v err=%v", versions, err)
		}
	})

	t.Run("audit create succeeds and condition failure is ignored", func(t *testing.T) {
		tdb := newIntegrityTestDB()
		st := store.New(tdb.db)

		if err := writeReattestationAuditFlag(ctx, st, " ", []string{"reason"}); err == nil {
			t.Fatalf("expected validation error for empty agent id")
		}
		if err := writeReattestationAuditFlag(ctx, st, integrityTestAgentID, []string{strings.Repeat("x", 250)}); err != nil {
			t.Fatalf("write audit flag: %v", err)
		}

		tdb2 := newIntegrityTestDB()
		tdb2.qAudit.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		if err := writeReattestationAuditFlag(ctx, store.New(tdb2.db), integrityTestAgentID, []string{"duplicate"}); err != nil {
			t.Fatalf("expected condition failure to be ignored, got %v", err)
		}
	})
}

func TestScanAgentRegistrationIntegrity_NoIssuesAndMissingCurrent(t *testing.T) {
	ctx := context.Background()
	bucket := "packs"
	agentID := integrityTestAgentID
	v1Body := []byte(`{"version":1}`)
	v2Body := []byte(`{"version":2}`)
	v1SHA := sha256Hex(v1Body)
	v2SHA := sha256Hex(v2Body)

	t.Run("matching history and current registration yields no issues", func(t *testing.T) {
		tdb := newIntegrityTestDB()
		tdb.qVer.On("All", mock.AnythingOfType("*[]*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
			*dest = []*models.SoulAgentVersion{
				{
					VersionNumber:      1,
					RegistrationSHA256: v1SHA,
					RegistrationURI:    fmt.Sprintf("s3://%s/%s", bucket, versionedS3Key(agentID, 1)),
					CreatedAt:          time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					VersionNumber:              2,
					RegistrationSHA256:         v2SHA,
					PreviousRegistrationSHA256: v1SHA,
					CreatedAt:                  time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
				},
			}
		}).Once()

		packs, cleanup := newIntegrityArtifactStore(t, bucket, map[string][]byte{
			bucket + "/" + versionedS3Key(agentID, 1): v1Body,
			bucket + "/" + versionedS3Key(agentID, 2): v2Body,
			bucket + "/" + currentS3Key(agentID):      v2Body,
		})
		defer cleanup()

		issues, err := scanAgentRegistrationIntegrity(ctx, store.New(tdb.db), packs, bucket, &models.SoulAgentIdentity{
			AgentID:                agentID,
			SelfDescriptionVersion: 2,
		}, 1024)
		if err != nil || len(issues) != 0 {
			t.Fatalf("unexpected integrity result: issues=%#v err=%v", issues, err)
		}
	})

	t.Run("missing current registration is reported", func(t *testing.T) {
		tdb := newIntegrityTestDB()
		tdb.qVer.On("All", mock.AnythingOfType("*[]*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
			*dest = []*models.SoulAgentVersion{{
				VersionNumber:      1,
				RegistrationSHA256: v1SHA,
				CreatedAt:          time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			}}
		}).Once()

		packs, cleanup := newIntegrityArtifactStore(t, bucket, map[string][]byte{
			bucket + "/" + versionedS3Key(agentID, 1): v1Body,
		})
		defer cleanup()

		issues, err := scanAgentRegistrationIntegrity(ctx, store.New(tdb.db), packs, bucket, &models.SoulAgentIdentity{
			AgentID:                agentID,
			SelfDescriptionVersion: 1,
		}, 1024)
		if err != nil {
			t.Fatalf("scanAgentRegistrationIntegrity: %v", err)
		}
		if len(issues) == 0 || !strings.Contains(strings.Join(issues, "\n"), "current registration") {
			t.Fatalf("expected missing current registration issue, got %#v", issues)
		}
	})

	t.Run("invalid inputs fail fast", func(t *testing.T) {
		issues, err := scanAgentRegistrationIntegrity(ctx, nil, nil, "", nil, 0)
		if err == nil || len(issues) != 0 {
			t.Fatalf("expected fast failure for invalid inputs, got issues=%#v err=%v", issues, err)
		}
	})

	t.Run("multiple integrity mismatches are surfaced", func(t *testing.T) {
		tdb := newIntegrityTestDB()
		tdb.qVer.On("All", mock.AnythingOfType("*[]*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
			*dest = []*models.SoulAgentVersion{
				{VersionNumber: 1, RegistrationSHA256: "", CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
				{VersionNumber: 3, RegistrationSHA256: strings.Repeat("a", 64), PreviousRegistrationSHA256: strings.Repeat("b", 64), CreatedAt: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)},
			}
		}).Once()

		currentBody := []byte(`{"current":"mismatch"}`)
		packs, cleanup := newIntegrityArtifactStore(t, bucket, map[string][]byte{
			bucket + "/" + currentS3Key(agentID): currentBody,
		})
		defer cleanup()

		issues, err := scanAgentRegistrationIntegrity(ctx, store.New(tdb.db), packs, bucket, &models.SoulAgentIdentity{
			AgentID:                agentID,
			SelfDescriptionVersion: 2,
		}, 1024)
		if err != nil {
			t.Fatalf("scanAgentRegistrationIntegrity: %v", err)
		}
		joined := strings.Join(issues, "\n")
		for _, want := range []string{
			"identity self_description_version=2 does not match latest version=3",
			"version 1 missing registration_sha256",
			"missing version record for version 2",
			"current registration sha not found in version history",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("expected issue %q in %#v", want, issues)
			}
		}
	})
}
