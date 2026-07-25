package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type provisioningTestDB struct {
	db    *ttmocks.MockExtendedDB
	qInst *ttmocks.MockQuery
	qJob  *ttmocks.MockQuery
}

func newProvisioningTestDB() provisioningTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qJob} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	return provisioningTestDB{db: db, qInst: qInst, qJob: qJob}
}

func TestParseStartInstanceProvisionRequest(t *testing.T) {
	t.Parallel()

	if _, err := parseStartInstanceProvisionRequest(nil); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	// Empty body is allowed.
	got, err := parseStartInstanceProvisionRequest(&apptheory.Context{})
	if err != nil || got != (startInstanceProvisionRequest{}) {
		t.Fatalf("unexpected: got=%#v err=%v", got, err)
	}
}

func TestBuildManagedProvisionJob_ConsentFailClosedWithoutKey(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 29, 13, 30, 0, 0, time.UTC)
	consentMessage := buildProvisionConsentMessage(testProvisionConsentStageLab, "demo.lesser.host", testPortalInstanceSlugDemo, testProvisionConsentNonce16, expiresAt)

	// No encryption key configured — consent is supplied but cannot be
	// encrypted. CSR-017 fix: fail closed instead of silently dropping.
	s := &Server{cfg: config.Config{
		Stage:                       "lab",
		ManagedParentDomain:         "lesser.host",
		ManagedDefaultRegion:        "us-east-1",
		ManagedLesserDefaultVersion: "v1.2.6",
	}}

	job, _, _, appErr := s.buildManagedProvisionJob(testPortalInstanceSlugDemo, startInstanceProvisionRequest{
		LesserVersion:      "v1.2.6",
		AdminWalletType:    "ethereum",
		AdminWalletAddress: "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
		AdminUsername:      testPortalInstanceSlugDemo,
		ConsentMessage:     consentMessage,
		ConsentSignature:   "0xsignature",
	}, "req", time.Now().UTC())
	require.NotNil(t, appErr)
	require.Nil(t, job)
	require.Contains(t, appErr.Message, "consent")
}

func TestBuildManagedProvisionJob_EncryptsConsentWithKey(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 29, 13, 30, 0, 0, time.UTC)
	consentMessage := buildProvisionConsentMessage(testProvisionConsentStageLab, "demo.lesser.host", testPortalInstanceSlugDemo, testProvisionConsentNonce16, expiresAt)

	// Use a valid 32-byte hex key for encryption.
	testKeyHex := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	s := &Server{cfg: config.Config{
		Stage:                                   "lab",
		ManagedParentDomain:                     "lesser.host",
		ManagedDefaultRegion:                    "us-east-1",
		ManagedLesserDefaultVersion:             "v1.2.6",
		ManagedProvisionConsentEncryptionKeyHex: testKeyHex,
	}}

	job, baseDomain, region, appErr := s.buildManagedProvisionJob(testPortalInstanceSlugDemo, startInstanceProvisionRequest{
		LesserVersion:      "v1.2.6",
		AdminWalletType:    "ethereum",
		AdminWalletAddress: "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
		AdminUsername:      testPortalInstanceSlugDemo,
		ConsentMessage:     consentMessage,
		ConsentSignature:   "0xsignature",
	}, "req", time.Now().UTC())
	require.Nil(t, appErr)
	require.NotNil(t, job)
	require.Equal(t, "demo.lesser.host", baseDomain)
	require.Equal(t, "us-east-1", region)
	// CSR-017: consent is no longer stored as plaintext — encrypted only.
	require.Empty(t, job.ConsentMessage, "consent message must not be stored as plaintext")
	require.Empty(t, job.ConsentSignature, "consent signature must not be stored as plaintext")
	require.NotEmpty(t, job.ConsentEncrypted, "consent must be stored encrypted")
	// The hash is still preserved for audit reference (computed pre-encryption).
	require.Equal(t, sha256Hex(consentMessage), job.ConsentMessageHash)
	require.Equal(t, expiresAt.UTC(), job.ConsentExpiresAt)
}

func TestHydrateManagedProvisionJobFromInstance_ReusesPartialProvisioningState(t *testing.T) {
	t.Parallel()

	job := &models.ProvisionJob{
		ID:           "job1",
		InstanceSlug: "demo",
		Region:       "us-west-2",
		BaseDomain:   "demo.greater.website",
	}
	inst := &models.Instance{
		Slug:             "demo",
		HostedAccountID:  "123456789012",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: "demo.greater.website",
		HostedZoneID:     "Z123",
	}

	hydrateManagedProvisionJobFromInstance(job, inst)

	require.Equal(t, "123456789012", job.AccountID)
	require.Equal(t, "us-east-1", job.Region)
	require.Equal(t, "demo.greater.website", job.BaseDomain)
	require.Equal(t, "Z123", job.ChildHostedZoneID)
	require.Equal(t, "PROVISION_JOB#job1", job.PK)
	require.Equal(t, models.SKJob, job.SK)
}

func TestStartAndGetInstanceProvisioning(t *testing.T) {
	t.Parallel()

	tdb := newProvisioningTestDB()
	s := &Server{
		cfg: config.Config{
			ManagedParentDomain:         "lesser.host",
			ManagedDefaultRegion:        "us-east-1",
			ManagedLesserDefaultVersion: "v1.2.6",
		},
		store: store.New(tdb.db),
		// queues intentionally nil (offline tests).
	}

	// Instance exists.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		_ = dest.UpdateKeys()
	}).Once()

	body, _ := json.Marshal(startInstanceProvisionRequest{
		Region:             "us-west-2",
		LesserVersion:      "v1.2.6",
		AdminWalletType:    "ethereum",
		AdminWalletAddress: "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
	})
	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo"}
	ctx.Request.Body = body

	resp, err := s.handleStartInstanceProvisioning(ctx)
	if err != nil {
		t.Fatalf("handleStartInstanceProvisioning err: %v", err)
	}
	if resp.Status != 202 {
		t.Fatalf("expected 202, got %d", resp.Status)
	}

	// Existing queued job path: instance has job id + status, job fetch succeeds.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             "demo",
			Status:           models.InstanceStatusActive,
			ProvisionJobID:   "job1",
			ProvisionStatus:  models.ProvisionJobStatusQueued,
			HostedBaseDomain: "demo.lesser.host",
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{ID: "job1", InstanceSlug: "demo", Status: models.ProvisionJobStatusQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()

	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"slug": "demo"}
	resp, err = s.handleStartInstanceProvisioning(ctx2)
	if err != nil || resp.Status != 200 {
		t.Fatalf("expected existing job 200, got resp=%#v err=%v", resp, err)
	}

	// Get provisioning: instance points to job id.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive, ProvisionJobID: "job2"}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{ID: "job2", InstanceSlug: "demo", Status: models.ProvisionJobStatusRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()

	ctx3 := adminCtx()
	ctx3.Params = map[string]string{"slug": "demo"}
	resp, err = s.handleGetInstanceProvisioning(ctx3)
	if err != nil || resp.Status != 200 {
		t.Fatalf("expected 200, got resp=%#v err=%v", resp, err)
	}

	// No provisioning job.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive, ProvisionJobID: ""}
		_ = dest.UpdateKeys()
	}).Once()
	ctxNoJob := adminCtx()
	ctxNoJob.Params = map[string]string{"slug": "demo"}
	if _, err := s.handleGetInstanceProvisioning(ctxNoJob); err == nil {
		t.Fatalf("expected error for missing job id")
	}

	// Job missing.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive, ProvisionJobID: "job404"}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()
	ctxMissing := adminCtx()
	ctxMissing.Params = map[string]string{"slug": "demo"}
	if _, err := s.handleGetInstanceProvisioning(ctxMissing); err == nil {
		t.Fatalf("expected not found for missing job")
	}
}

func TestHandleStartInstanceProvisioning_RejectsMalformedReleaseTag(t *testing.T) {
	t.Parallel()

	tdb := newProvisioningTestDB()
	s := &Server{
		cfg: config.Config{
			ManagedParentDomain:         "lesser.host",
			ManagedDefaultRegion:        "us-east-1",
			ManagedLesserDefaultVersion: "v0.0.0",
		},
		store: store.New(tdb.db),
	}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		_ = dest.UpdateKeys()
	}).Once()

	body, _ := json.Marshal(startInstanceProvisionRequest{
		LesserVersion:      "v.1.2.3",
		AdminWalletType:    "ethereum",
		AdminWalletAddress: "0x0000000000000000000000000000000000000003",
		AdminWalletChainID: 1,
	})
	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo"}
	ctx.Request.Body = body

	_, err := s.handleStartInstanceProvisioning(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.bad_request", appErr.Code)
	require.Contains(t, appErr.Message, "lesser_version must be \"latest\" or a final tag like v1.2.3")
}
