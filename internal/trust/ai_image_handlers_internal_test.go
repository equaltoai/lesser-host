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
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

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

func TestValidateAIEvidenceImageObjectKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"evidence/inst/img",
		"moderation/inst/img",
	} {
		if err := validateAIEvidenceImageObjectKey("inst", key); err != nil {
			t.Fatalf("expected %q to be accepted: %v", key, err)
		}
	}

	for _, key := range []string{
		"evidence/other/img",
		"moderation/other/img",
		"renders/render-id/snapshot.txt",
		"evidence/inst",
		"",
	} {
		if err := validateAIEvidenceImageObjectKey("inst", key); err == nil || err.Code != appErrCodeBadRequest {
			t.Fatalf("expected bad_request for %q, got %T: %v", key, err, err)
		}
	}

	if err := validateAIEvidenceImageObjectKey("", "evidence/inst/img"); err == nil || err.Code != "app.unauthorized" {
		t.Fatalf("expected unauthorized for missing instance, got %T: %v", err, err)
	}
}

func TestHandleAIEvidenceImage_RejectsCrossInstanceObjectKeyBeforeStorage(t *testing.T) {
	t.Parallel()

	st := &store.Store{}
	s := &Server{store: st, ai: ai.NewService(st), artifacts: artifacts.New("bucket")}
	body, _ := json.Marshal(aiEvidenceImageRequest{ObjectKey: "evidence/other/img"})
	_, err := s.handleAIEvidenceImage(&apptheory.Context{
		AuthIdentity: testBudgetInstanceSlug,
		Request:      apptheory.Request{Body: body},
	})
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Code != appErrCodeBadRequest || !strings.Contains(appErr.Message, "evidence/inst/") {
		t.Fatalf("expected owned-prefix bad_request, got %T: %v", err, err)
	}
}

func TestHandleAIEvidenceImage_DisabledShortCircuitsStorage(t *testing.T) {
	t.Parallel()

	st := &store.Store{}
	s := &Server{
		store:     st,
		ai:        ai.NewService(st),
		artifacts: artifacts.New("bucket"),
	}

	body, _ := json.Marshal(aiEvidenceImageRequest{ObjectKey: "evidence/inst/missing"})
	resp, err := s.handleAIEvidenceImage(&apptheory.Context{
		AuthIdentity: testBudgetInstanceSlug,
		Request:      apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out aiEvidenceResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != statusDisabled || out.Budget.Reason != aiDisabledForInstanceReason {
		t.Fatalf("expected disabled response before storage use, got %#v", out)
	}
	if out.Contract.Module != aiEvidenceImageModule || out.Contract.InputsHash == "" {
		t.Fatalf("expected image contract with precheck hash, got %#v", out.Contract)
	}
	if out.Budget.RequestedCredits != aiEvidenceImageBaseCredits || out.Budget.DebitedCredits != 0 || out.Budget.Allowed {
		t.Fatalf("unexpected budget decision: %#v", out.Budget)
	}
}

func TestAIEvidenceDisabledResponses(t *testing.T) {
	t.Parallel()

	if got := aiEvidenceTextDisabledResponse(instanceTrustConfig{AIEnabled: true}, "hash"); got != nil {
		t.Fatalf("expected enabled config to return nil, got %#v", got)
	}
	textResp := aiEvidenceTextDisabledResponse(instanceTrustConfig{AIEnabled: false, AIPricingMultiplierBps: 25000}, " hash ")
	if textResp == nil || textResp.Status != statusDisabled || textResp.Budget.RequestedCredits != 3 {
		t.Fatalf("unexpected text disabled response: %#v", textResp)
	}

	resp := aiEvidenceDisabledResponse(" module ", " policy ", " model ", " input ", 7, " disabled ")
	if resp.Contract.Module != "module" || resp.Contract.PolicyVersion != "policy" || resp.Contract.ModelSet != "model" || resp.Contract.InputsHash != "input" {
		t.Fatalf("expected trimmed contract, got %#v", resp.Contract)
	}
	if resp.Budget.Reason != statusDisabled || resp.Budget.RequestedCredits != 7 || resp.Budget.Allowed {
		t.Fatalf("unexpected budget: %#v", resp.Budget)
	}
}

func TestHandleAIEvidenceImage_BudgetNotConfigured(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	art, cleanup := newTestArtifactsStore(t, "bucket")
	t.Cleanup(cleanup)

	if err := art.PutObject(ctx, "evidence/demo/img1", []byte("abc"), "image/png", ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	tdb := newAIEvidenceTestDB()
	st := store.New(tdb.db)
	s := &Server{
		store:     st,
		ai:        ai.NewService(st),
		artifacts: art,
	}

	enabled := true
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", AIEnabled: &enabled}
	}).Once()
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

	body, _ := json.Marshal(aiEvidenceImageRequest{ObjectKey: "evidence/demo/img1"})
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
		dest.AIEnabled = &v
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
