package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type memS3Object struct {
	body         []byte
	contentType  string
	cacheControl string
	etag         string
}

type memS3Server struct {
	mu   sync.Mutex
	objs map[string]memS3Object // bucket/key -> object
}

func (m *memS3Server) handler(w http.ResponseWriter, r *http.Request) {
	bucket, key, ok := parsePathStyle(r.URL.Path)
	if !ok || bucket == "" {
		http.NotFound(w, r)
		return
	}
	objKey := bucket + "/" + key

	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`

		contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
		if contentType == "application/octet-stream" {
			// S3/SDK defaults; treat as unset to exercise content-type detection branches in handlers.
			contentType = ""
		}

		m.mu.Lock()
		if m.objs == nil {
			m.objs = map[string]memS3Object{}
		}
		m.objs[objKey] = memS3Object{
			body:         body,
			contentType:  contentType,
			cacheControl: strings.TrimSpace(r.Header.Get("Cache-Control")),
			etag:         etag,
		}
		m.mu.Unlock()

		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		return

	case http.MethodGet:
		m.mu.Lock()
		obj, ok := m.objs[objKey]
		m.mu.Unlock()
		if !ok {
			writeNoSuchKey(w)
			return
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		if obj.etag != "" {
			w.Header().Set("ETag", obj.etag)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.body)
		return

	case http.MethodHead:
		m.mu.Lock()
		obj, ok := m.objs[objKey]
		m.mu.Unlock()
		if !ok {
			writeNoSuchKey(w)
			return
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		if obj.etag != "" {
			w.Header().Set("ETag", obj.etag)
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.body)))
		w.WriteHeader(http.StatusOK)
		return

	default:
		http.NotFound(w, r)
		return
	}
}

func parsePathStyle(path string) (bucket string, key string, ok bool) {
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

func writeNoSuchKey(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>NoSuchKey</Code>
  <Message>The specified key does not exist.</Message>
  <Key>missing</Key>
</Error>`)
}

func newTestArtifactsStore(t *testing.T, bucket string) (*artifacts.Store, func()) {
	t.Helper()

	mem := &memS3Server{}
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

func TestHandleAIEvidenceImage_BudgetNotConfigured(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	art, cleanup := newTestArtifactsStore(t, "bucket")
	t.Cleanup(cleanup)

	if err := art.PutObject(ctx, "img1", []byte("abc"), "image/png", ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	tdb := newAIEvidenceTestDB()
	st := store.New(tdb.db)
	s := &Server{
		store:     st,
		ai:        ai.NewService(st),
		artifacts: art,
	}

	// loadInstanceTrustConfig falls back to defaults when instance not found.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
	// No cached result, no job exists.
	tdb.qResult.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	// Concurrency check queries queued jobs by instance.
	tdb.qJob.On("All", mock.AnythingOfType("*[]*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AIJob](t, args, 0)
		*dest = nil
	}).Once()
	// Budget month missing => not_checked_budget response.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(aiEvidenceImageRequest{ObjectKey: "img1"})
	resp, err := s.handleAIEvidenceImage(&apptheory.Context{
		AuthIdentity: "demo",
		Request:      apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out aiEvidenceResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != string(ai.JobStatusNotCheckedBudget) || out.Budget.Allowed {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHeadAndValidateEvidenceImageObject_RejectsNonImages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	art, cleanup := newTestArtifactsStore(t, "bucket")
	t.Cleanup(cleanup)

	if err := art.PutObject(ctx, "txt1", []byte("hi"), "text/plain", ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	s := &Server{artifacts: art}
	if _, _, _, err := s.headAndValidateEvidenceImageObject(ctx, "txt1"); err == nil {
		t.Fatalf("expected non-image rejection")
	}
}

func TestHandleAIModerationTextAndImageReport_BudgetNotConfigured(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	art, cleanup := newTestArtifactsStore(t, "bucket")
	t.Cleanup(cleanup)

	// Image must exist and be under moderation/{instance}/.
	if err := art.PutObject(ctx, "moderation/inst/obj", []byte("abc"), "image/png", ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	tdb := newAIEvidenceTestDB()

	// Moderation handlers write audit entries best-effort.
	qAudit := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	qAudit.On("Create").Return(nil).Maybe()

	st := store.New(tdb.db)
	s := &Server{
		store:     st,
		ai:        ai.NewService(st),
		artifacts: art,
	}

	// Instance overrides: moderation enabled, deterministic model set.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		v := true
		dest.ModerationEnabled = &v
	}).Twice()

	// No cached result, no job exists.
	tdb.qResult.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Twice()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Twice()
	// Concurrency check queries queued jobs by instance.
	tdb.qJob.On("All", mock.AnythingOfType("*[]*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AIJob](t, args, 0)
		*dest = nil
	}).Twice()
	// Budget month missing => not_checked_budget response.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Twice()

	body, _ := json.Marshal(aiModerationTextRequest{Text: "hello"})
	resp, err := s.handleAIModerationTextReport(&apptheory.Context{
		AuthIdentity: "inst",
		RequestID:    "rid",
		Request:      apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out aiModerationResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &out); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if out.Status != string(ai.JobStatusNotCheckedBudget) || out.Budget.Allowed {
		t.Fatalf("unexpected text moderation response: %#v", out)
	}

	body, _ = json.Marshal(aiModerationImageRequest{ObjectKey: "moderation/inst/obj"})
	resp, err = s.handleAIModerationImageReport(&apptheory.Context{
		AuthIdentity: "inst",
		RequestID:    "rid2",
		Request:      apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	if unmarshalErr := json.Unmarshal(resp.Body, &out); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if out.Status != string(ai.JobStatusNotCheckedBudget) || out.Budget.Allowed {
		t.Fatalf("unexpected image moderation response: %#v", out)
	}
}
