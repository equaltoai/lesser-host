package provisionworker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeAPIError struct{ code string }

func (e fakeAPIError) Error() string                 { return "api: " + e.code }
func (e fakeAPIError) ErrorCode() string             { return e.code }
func (e fakeAPIError) ErrorMessage() string          { return e.Error() }
func (e fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

type provisionWaitFn func(*Server, context.Context, *models.ProvisionJob, string, time.Time) (time.Duration, bool, error)

func newProvisionServerWithStore(t *testing.T) *Server {
	t.Helper()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	return &Server{store: st}
}

func runProvisionWait(t *testing.T, fn provisionWaitFn, step string, status cbtypes.StatusType, deepLink string, createdAt time.Time, now time.Time) (*models.ProvisionJob, time.Duration, bool, error) {
	t.Helper()

	var logs *cbtypes.LogsLocation
	if strings.TrimSpace(deepLink) != "" {
		logs = &cbtypes.LogsLocation{DeepLink: aws.String(deepLink)}
	}

	s := newProvisionServerWithStore(t)
	s.cb = &fakeCodebuild{
		batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: status, Logs: logs}}},
	}

	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         step,
		MaxAttempts:  3,
		RunID:        "run1",
		CreatedAt:    createdAt,
	}

	delay, done, err := fn(s, context.Background(), job, "req", now)
	return job, delay, done, err
}

func TestAdvanceProvisionReceiptIngest_LoadError_RetriesThenFails(t *testing.T) {
	t.Parallel()

	s := newProvisionServerWithStore(t)
	s.cfg = config.Config{ArtifactBucketName: "bucket"}
	s.s3 = &fakeS3{err: errors.New("nope")}

	now := time.Unix(100, 0).UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepReceiptIngest,
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
	}

	delay, done, err := s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	if err != nil || done || delay <= 0 || job.Attempts != 1 || !strings.Contains(job.Note, "retrying") {
		t.Fatalf("expected retry, got delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	job.Attempts = job.MaxAttempts - 1
	delay, done, err = s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	if err != nil || done || delay != 0 {
		t.Fatalf("expected fail path without done, got delay=%v done=%v err=%v", delay, done, err)
	}
	if job.Step != provisionStepFailed || job.ErrorCode != "receipt_load_failed" {
		t.Fatalf("expected receipt_load_failed, got job=%#v", job)
	}
}

func TestAdvanceProvisionReceiptIngest_SetsDoneWhenNoBodyNoSoul(t *testing.T) {
	t.Parallel()

	s := newProvisionServerWithStore(t)
	s.cfg = config.Config{ArtifactBucketName: "bucket"}
	s.s3 = &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"app":"x","base_domain":"d","account_id":"123456789012","region":"us-east-1","hosted_zone":{"id":"/hostedzone/Z1","name":"d."}}`))}}

	now := time.Unix(110, 0).UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepReceiptIngest,
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
		BodyEnabled:  false,
		SoulEnabled:  false,
	}

	delay, done, err := s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	if err != nil || delay != 0 || !done {
		t.Fatalf("expected done, got delay=%v done=%v err=%v", delay, done, err)
	}
	if job.Step != provisionStepDone || job.Status != models.ProvisionJobStatusOK || job.Note != noteProvisioned {
		t.Fatalf("unexpected job state: %#v", job)
	}
}

func TestAdvanceProvisionReceiptIngest_ContinuesToSoulWhenEnabled(t *testing.T) {
	t.Parallel()

	s := newProvisionServerWithStore(t)
	s.cfg = config.Config{ArtifactBucketName: "bucket"}
	s.s3 = &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"app":"x","base_domain":"d","account_id":"123456789012","region":"us-east-1","hosted_zone":{"id":"/hostedzone/Z1","name":"d."}}`))}}

	now := time.Unix(120, 0).UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepReceiptIngest,
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
		BodyEnabled:  false,
		SoulEnabled:  true,
	}

	delay, done, err := s.advanceProvisionReceiptIngest(context.Background(), job, "req", now)
	if err != nil || delay != 0 || done {
		t.Fatalf("expected continue to soul, got delay=%v done=%v err=%v", delay, done, err)
	}
	if job.Step != provisionStepSoulDeployStart || !strings.Contains(job.Note, noteStartingSoulDeployRunner) || strings.TrimSpace(job.RunID) != "" {
		t.Fatalf("unexpected job state: %#v", job)
	}
}

func TestAdvanceProvisionBodyDeployStart_SkipsWhenBodyDisabled(t *testing.T) {
	t.Parallel()

	now := time.Unix(200, 0).UTC()
	s := newProvisionServerWithStore(t)

	cases := []struct {
		name        string
		soulEnabled bool
		wantStep    string
		wantDone    bool
	}{
		{name: "no_soul", soulEnabled: false, wantStep: provisionStepDone, wantDone: true},
		{name: "continue_to_soul", soulEnabled: true, wantStep: provisionStepSoulDeployStart, wantDone: false},
	}

	for _, c := range cases {
		job := &models.ProvisionJob{
			ID:                "j1",
			InstanceSlug:      "demo",
			Status:            models.ProvisionJobStatusRunning,
			Step:              provisionStepBodyDeployStart,
			MaxAttempts:       3,
			BodyEnabled:       false,
			SoulEnabled:       c.soulEnabled,
			SoulProvisionedAt: time.Time{},
		}
		delay, done, err := s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
		if err != nil || delay != 0 || done != c.wantDone || job.Step != c.wantStep {
			t.Fatalf("%s: unexpected: delay=%v done=%v job=%#v err=%v", c.name, delay, done, job, err)
		}
		if !c.soulEnabled {
			if job.Status != models.ProvisionJobStatusOK || job.Note != noteProvisioned {
				t.Fatalf("%s: expected ok+provisioned, got %#v", c.name, job)
			}
		}
	}
}

func TestAdvanceProvisionBodyDeployStart_BodyAlreadyProvisioned_GoesToMcpStart(t *testing.T) {
	t.Parallel()

	now := time.Unix(200, 0).UTC()

	s := newProvisionServerWithStore(t)
	job := &models.ProvisionJob{
		ID:                "j1",
		InstanceSlug:      "demo",
		Status:            models.ProvisionJobStatusRunning,
		Step:              provisionStepBodyDeployStart,
		MaxAttempts:       3,
		BodyEnabled:       true,
		BodyProvisionedAt: now.Add(-1 * time.Minute),
		RunID:             "run-should-clear",
	}
	delay, done, err := s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
	if err != nil || delay != 0 || done || job.Step != provisionStepDeployMcpStart || strings.TrimSpace(job.RunID) != "" {
		t.Fatalf("unexpected: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}
}

func TestAdvanceProvisionBodyDeployStart_RunAlreadySet_AdvancesToWait(t *testing.T) {
	t.Parallel()

	now := time.Unix(200, 0).UTC()

	s := newProvisionServerWithStore(t)
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepBodyDeployStart,
		MaxAttempts:  3,
		BodyEnabled:  true,
		RunID:        "run1",
	}
	delay, done, err := s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
	if err != nil || done || delay != provisionDefaultPollDelay || job.Step != provisionStepBodyDeployWait {
		t.Fatalf("unexpected: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}
}

func TestAdvanceProvisionBodyDeployStart_StartRunnerError_RetriesThenFails(t *testing.T) {
	t.Parallel()

	now := time.Unix(200, 0).UTC()

	s := newProvisionServerWithStore(t)
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepBodyDeployStart,
		MaxAttempts:  3,
		BodyEnabled:  true,
		CreatedAt:    now.Add(-1 * time.Minute),
	}
	delay, done, err := s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
	if err != nil || done || delay <= 0 || job.Attempts != 1 {
		t.Fatalf("expected retry, got delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	job.Attempts = job.MaxAttempts - 1
	delay, done, err = s.advanceProvisionBodyDeployStart(context.Background(), job, "req", now)
	if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "body_deploy_start_failed" {
		t.Fatalf("expected fail, got delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}
}

func TestAdvanceProvisionDeployMcpStart_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(400, 0).UTC()
	s := newProvisionServerWithStore(t)

	cases := []struct {
		name           string
		job            models.ProvisionJob
		wantDelay      time.Duration
		wantDone       bool
		wantStep       string
		wantStatusOK   bool
		wantNoteSubstr string
	}{
		{
			name: "skip_when_body_disabled_no_soul",
			job: models.ProvisionJob{
				ID:           "j1",
				InstanceSlug: "demo",
				Status:       models.ProvisionJobStatusRunning,
				Step:         provisionStepDeployMcpStart,
				MaxAttempts:  3,
				BodyEnabled:  false,
				SoulEnabled:  false,
			},
			wantDelay:      0,
			wantDone:       true,
			wantStep:       provisionStepDone,
			wantStatusOK:   true,
			wantNoteSubstr: noteProvisioned,
		},
		{
			name: "rewinds_to_body_deploy_when_body_not_provisioned",
			job: models.ProvisionJob{
				ID:           "j1",
				InstanceSlug: "demo",
				Status:       models.ProvisionJobStatusRunning,
				Step:         provisionStepDeployMcpStart,
				MaxAttempts:  3,
				BodyEnabled:  true,
			},
			wantDelay:      0,
			wantDone:       false,
			wantStep:       provisionStepBodyDeployStart,
			wantNoteSubstr: "starting lesser-body deploy runner",
		},
		{
			name: "mcp_already_wired_completes_when_no_soul",
			job: models.ProvisionJob{
				ID:                "j1",
				InstanceSlug:      "demo",
				Status:            models.ProvisionJobStatusRunning,
				Step:              provisionStepDeployMcpStart,
				MaxAttempts:       3,
				BodyEnabled:       true,
				BodyProvisionedAt: now.Add(-2 * time.Minute),
				McpWiredAt:        now.Add(-1 * time.Minute),
				SoulEnabled:       false,
			},
			wantDelay:      0,
			wantDone:       true,
			wantStep:       provisionStepDone,
			wantStatusOK:   true,
			wantNoteSubstr: noteProvisioned,
		},
		{
			name: "mcp_already_wired_continues_to_soul",
			job: models.ProvisionJob{
				ID:                "j1",
				InstanceSlug:      "demo",
				Status:            models.ProvisionJobStatusRunning,
				Step:              provisionStepDeployMcpStart,
				MaxAttempts:       3,
				BodyEnabled:       true,
				BodyProvisionedAt: now.Add(-2 * time.Minute),
				McpWiredAt:        now.Add(-1 * time.Minute),
				SoulEnabled:       true,
			},
			wantDelay:      0,
			wantDone:       false,
			wantStep:       provisionStepSoulDeployStart,
			wantNoteSubstr: "starting soul deploy runner",
		},
		{
			name: "run_already_set_advances_to_wait",
			job: models.ProvisionJob{
				ID:                "j1",
				InstanceSlug:      "demo",
				Status:            models.ProvisionJobStatusRunning,
				Step:              provisionStepDeployMcpStart,
				MaxAttempts:       3,
				BodyEnabled:       true,
				BodyProvisionedAt: now.Add(-2 * time.Minute),
				RunID:             "run1",
			},
			wantDelay:      provisionDefaultPollDelay,
			wantDone:       false,
			wantStep:       provisionStepDeployMcpWait,
			wantNoteSubstr: "already started",
		},
	}

	for _, c := range cases {
		job := c.job
		delay, done, err := s.advanceProvisionDeployMcpStart(context.Background(), &job, "req", now)
		if err != nil || delay != c.wantDelay || done != c.wantDone || job.Step != c.wantStep || (c.wantStatusOK && job.Status != models.ProvisionJobStatusOK) || !strings.Contains(job.Note, c.wantNoteSubstr) {
			t.Fatalf("%s: unexpected: delay=%v done=%v job=%#v err=%v", c.name, delay, done, job, err)
		}
	}
}

func TestAdvanceProvisionDeployMcpWait_SucceededContinuesToSoul(t *testing.T) {
	t.Parallel()

	s := newProvisionServerWithStore(t)
	s.cb = &fakeCodebuild{
		batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusTypeSucceeded}}},
	}

	now := time.Unix(410, 0).UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepDeployMcpWait,
		MaxAttempts:  3,
		RunID:        "run1",
		SoulEnabled:  true,
	}

	delay, done, err := s.advanceProvisionDeployMcpWait(context.Background(), job, "req", now)
	if err != nil || delay != 0 || done || job.Step != provisionStepSoulDeployStart || job.McpWiredAt.IsZero() || strings.TrimSpace(job.RunID) != "" {
		t.Fatalf("expected continue to soul, got delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}
}

func TestAdvanceProvisionSoulDeployStart_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(500, 0).UTC()
	s := newProvisionServerWithStore(t)

	t.Run("disabled_marks_done", func(t *testing.T) {
		t.Parallel()

		job := &models.ProvisionJob{
			ID:           "j1",
			InstanceSlug: "demo",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepSoulDeployStart,
			MaxAttempts:  3,
			SoulEnabled:  false,
		}
		delay, done, err := s.advanceProvisionSoulDeployStart(context.Background(), job, "req", now)
		if err != nil || delay != 0 || !done || job.Step != provisionStepDone || job.Status != models.ProvisionJobStatusOK || job.Note != noteProvisioned {
			t.Fatalf("unexpected: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})

	t.Run("run_already_set_advances_to_wait", func(t *testing.T) {
		t.Parallel()

		job := &models.ProvisionJob{
			ID:           "j1",
			InstanceSlug: "demo",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepSoulDeployStart,
			MaxAttempts:  3,
			SoulEnabled:  true,
			RunID:        "run1",
		}
		delay, done, err := s.advanceProvisionSoulDeployStart(context.Background(), job, "req", now)
		if err != nil || done || delay != provisionDefaultPollDelay || job.Step != provisionStepSoulDeployWait {
			t.Fatalf("unexpected: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})
}

func TestAdvanceProvisionSoulDeployWait_SucceededAdvancesToInit(t *testing.T) {
	t.Parallel()

	s := newProvisionServerWithStore(t)
	s.cb = &fakeCodebuild{
		batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusTypeSucceeded}}},
	}

	now := time.Unix(520, 0).UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepSoulDeployWait,
		MaxAttempts:  3,
		RunID:        "run1",
		SoulEnabled:  true,
	}

	delay, done, err := s.advanceProvisionSoulDeployWait(context.Background(), job, "req", now)
	if err != nil || delay != 0 || done || job.Step != provisionStepSoulInitStart || strings.TrimSpace(job.RunID) != "" {
		t.Fatalf("expected advance to soul init, got delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}
}

func TestProvisionWorkerSecurityHelpers(t *testing.T) {
	t.Parallel()

	if isOrgAccessDenied(nil) {
		t.Fatalf("expected false for nil")
	}
	if isOrgAccessDenied(errors.New("boom")) {
		t.Fatalf("expected false for non-api error")
	}
	if !isOrgAccessDenied(fakeAPIError{code: "AccessDenied"}) {
		t.Fatalf("expected access denied true")
	}
	if isOrgAccessDenied(fakeAPIError{code: "ThrottlingException"}) {
		t.Fatalf("expected false for other codes")
	}

	if isSecretsManagerNotFound(nil) || isSecretsManagerExists(nil) {
		t.Fatalf("expected false for nil errors")
	}
	if !isSecretsManagerNotFound(&smtypes.ResourceNotFoundException{}) {
		t.Fatalf("expected not found true")
	}
	if !isSecretsManagerExists(&smtypes.ResourceExistsException{}) {
		t.Fatalf("expected exists true")
	}
}

func TestFailOrgPermissions_FailsJob(t *testing.T) {
	t.Parallel()

	s := newProvisionServerWithStore(t)

	now := time.Unix(600, 0).UTC()
	job := &models.ProvisionJob{
		ID:           "j1",
		InstanceSlug: "demo",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepAccountCreatePoll,
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
	}

	if err := s.failOrgPermissions(context.Background(), job, "req", now, "DescribeCreateAccountStatus", fakeAPIError{code: "AccessDenied"}); err != nil {
		t.Fatalf("failOrgPermissions: %v", err)
	}
	if job.Step != provisionStepFailed || job.ErrorCode != "org_permissions_missing" || !strings.Contains(job.ErrorMessage, "access denied") {
		t.Fatalf("unexpected job: %#v", job)
	}
}

func TestAdvanceProvisionStartRunnerErrors_RetryThenFail(t *testing.T) {
	t.Parallel()

	type startFn func(*Server, context.Context, *models.ProvisionJob, string, time.Time) (time.Duration, bool, error)

	now := time.Unix(700, 0).UTC()
	s := newProvisionServerWithStore(t)

	cases := []struct {
		name        string
		fn          startFn
		job         models.ProvisionJob
		wantErrCode string
	}{
		{
			name: "mcp_start",
			fn:   (*Server).advanceProvisionDeployMcpStart,
			job: models.ProvisionJob{
				ID:                "j1",
				InstanceSlug:      "demo",
				Status:            models.ProvisionJobStatusRunning,
				Step:              provisionStepDeployMcpStart,
				MaxAttempts:       3,
				CreatedAt:         now.Add(-1 * time.Minute),
				BodyEnabled:       true,
				BodyProvisionedAt: now.Add(-2 * time.Minute),
				McpWiredAt:        time.Time{},
			},
			wantErrCode: "mcp_deploy_start_failed",
		},
		{
			name: "soul_deploy_start",
			fn:   (*Server).advanceProvisionSoulDeployStart,
			job: models.ProvisionJob{
				ID:           "j1",
				InstanceSlug: "demo",
				Status:       models.ProvisionJobStatusRunning,
				Step:         provisionStepSoulDeployStart,
				MaxAttempts:  3,
				CreatedAt:    now.Add(-1 * time.Minute),
				SoulEnabled:  true,
			},
			wantErrCode: "soul_deploy_start_failed",
		},
		{
			name: "soul_init_start",
			fn:   (*Server).advanceProvisionSoulInitStart,
			job: models.ProvisionJob{
				ID:           "j1",
				InstanceSlug: "demo",
				Status:       models.ProvisionJobStatusRunning,
				Step:         provisionStepSoulInitStart,
				MaxAttempts:  3,
				CreatedAt:    now.Add(-1 * time.Minute),
			},
			wantErrCode: "soul_init_start_failed",
		},
	}

	for _, c := range cases {
		job := c.job
		delay, done, err := c.fn(s, context.Background(), &job, "req", now)
		if err != nil || done || delay <= 0 || job.Attempts != 1 {
			t.Fatalf("%s: expected retry, got delay=%v done=%v job=%#v err=%v", c.name, delay, done, job, err)
		}

		job.Attempts = job.MaxAttempts - 1
		delay, done, err = c.fn(s, context.Background(), &job, "req", now)
		if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != c.wantErrCode {
			t.Fatalf("%s: expected fail %q, got delay=%v done=%v job=%#v err=%v", c.name, c.wantErrCode, delay, done, job, err)
		}
	}
}

func TestAdvanceProvisionWaitTimeoutsAndFailures(t *testing.T) {
	t.Parallel()

	now := time.Unix(800, 0).UTC()

	t.Run("in_progress_timeout_fails", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name        string
			fn          provisionWaitFn
			step        string
			wantErrCode string
		}{
			{name: "body_wait", fn: (*Server).advanceProvisionBodyDeployWait, step: provisionStepBodyDeployWait, wantErrCode: "body_deploy_timeout"},
			{name: "mcp_wait", fn: (*Server).advanceProvisionDeployMcpWait, step: provisionStepDeployMcpWait, wantErrCode: "mcp_deploy_timeout"},
			{name: "soul_deploy_wait", fn: (*Server).advanceProvisionSoulDeployWait, step: provisionStepSoulDeployWait, wantErrCode: "soul_deploy_timeout"},
			{name: "soul_init_wait", fn: (*Server).advanceProvisionSoulInitWait, step: provisionStepSoulInitWait, wantErrCode: "soul_init_timeout"},
		}

		for _, c := range cases {
			createdAt := now.Add(-(provisionMaxDeployAge + 1*time.Minute))
			job, delay, done, err := runProvisionWait(t, c.fn, c.step, cbtypes.StatusTypeInProgress, "", createdAt, now)
			if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != c.wantErrCode {
				t.Fatalf("%s: expected timeout %q, got delay=%v done=%v job=%#v err=%v", c.name, c.wantErrCode, delay, done, job, err)
			}
		}
	})

	t.Run("failed_includes_deep_link", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name        string
			fn          provisionWaitFn
			step        string
			wantErrCode string
		}{
			{name: "body_wait", fn: (*Server).advanceProvisionBodyDeployWait, step: provisionStepBodyDeployWait, wantErrCode: "body_deploy_failed"},
			{name: "mcp_wait", fn: (*Server).advanceProvisionDeployMcpWait, step: provisionStepDeployMcpWait, wantErrCode: "mcp_deploy_failed"},
			{name: "soul_deploy_wait", fn: (*Server).advanceProvisionSoulDeployWait, step: provisionStepSoulDeployWait, wantErrCode: "soul_deploy_failed"},
			{name: "soul_init_wait", fn: (*Server).advanceProvisionSoulInitWait, step: provisionStepSoulInitWait, wantErrCode: "soul_init_failed"},
		}

		for _, c := range cases {
			createdAt := now.Add(-1 * time.Minute)
			job, delay, done, err := runProvisionWait(t, c.fn, c.step, cbtypes.StatusTypeFailed, "link", createdAt, now)
			if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != c.wantErrCode || !strings.Contains(job.ErrorMessage, "CodeBuild: link") {
				t.Fatalf("%s: expected fail %q with deep link, got delay=%v done=%v job=%#v err=%v", c.name, c.wantErrCode, delay, done, job, err)
			}
		}
	})
}
