package provisionworker

import (
	"context"
	"strings"
	"time"

	"github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func provisionStatusUpdate(jobID string, continuing bool) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		ub.Set("ProvisionJobID", strings.TrimSpace(jobID))
		if continuing {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusRunning)
		} else {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusOK)
		}
		return nil
	}
}

func (s *Server) advanceProvisionContinueToSoulOrDone(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	continuing := job.SoulEnabled && job.SoulProvisionedAt.IsZero()
	if continuing {
		job.Step = provisionStepSoulDeployStart
		job.Note = noteStartingSoulDeployRunner
		job.RunID = ""
	} else {
		job.Step = provisionStepDone
		job.Status = models.ProvisionJobStatusOK
		job.Note = noteProvisioned
	}

	if err := s.persistJobAndInstance(ctx, job, requestID, now, provisionStatusUpdate(job.ID, continuing)); err != nil {
		return 0, false, err
	}
	return 0, !continuing, nil
}

func (s *Server) advanceProvisionBodyDeployStartRunnerAlreadyStarted(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	job.Step = provisionStepBodyDeployWait
	job.Note = "lesser-body deploy runner already started"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionBodyDeployStartBodyAlreadyProvisioned(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	job.Step = provisionStepDeployMcpStart
	job.Note = noteStartingMcpWiringDeployRunner
	job.RunID = ""
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionStartDeployRunner(
	ctx context.Context,
	job *models.ProvisionJob,
	requestID string,
	now time.Time,
	mode string,
	receiptKey string,
	waitStep string,
	failCode string,
	failMessagePrefix string,
	retryNotePrefix string,
	inProgressNote string,
) (time.Duration, bool, error) {
	runID, err := s.startDeployRunnerWithMode(ctx, job, strings.TrimSpace(mode), strings.TrimSpace(receiptKey))
	if err != nil {
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			return 0, false, s.failJob(ctx, job, requestID, now, strings.TrimSpace(failCode), failMessagePrefix+err.Error())
		}
		job.Note = retryNotePrefix + compactErr(err)
		_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 10*time.Minute), false, nil
	}

	job.RunID = strings.TrimSpace(runID)
	job.Step = strings.TrimSpace(waitStep)
	job.Note = strings.TrimSpace(inProgressNote)
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionBodyDeployStartStartRunner(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceProvisionStartDeployRunner(
		ctx,
		job,
		requestID,
		now,
		"lesser-body",
		s.bodyReceiptS3Key(job),
		provisionStepBodyDeployWait,
		"body_deploy_start_failed",
		"failed to start lesser-body deploy runner: ",
		"failed to start lesser-body deploy runner; retrying: ",
		"lesser-body deploy runner in progress",
	)
}

func (s *Server) advanceProvisionDeployMcpStartRewindToBody(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	job.Step = provisionStepBodyDeployStart
	job.Note = "starting lesser-body deploy runner"
	job.RunID = ""
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func (s *Server) advanceProvisionDeployMcpStartRunnerAlreadyStarted(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	job.Step = provisionStepDeployMcpWait
	job.Note = "MCP wiring deploy runner already started"
	if err := s.persistJobAndInstance(ctx, job, requestID, now, nil); err != nil {
		return 0, false, err
	}
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionDeployMcpStartStartRunner(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	return s.advanceProvisionStartDeployRunner(
		ctx,
		job,
		requestID,
		now,
		"lesser-mcp",
		s.mcpReceiptS3Key(job),
		provisionStepDeployMcpWait,
		"mcp_deploy_start_failed",
		"failed to start MCP wiring deploy runner: ",
		"failed to start MCP wiring deploy runner; retrying: ",
		"MCP wiring deploy runner in progress",
	)
}

func (s *Server) advanceProvisionDeployMcpWaitRetryPollError(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, err error) (time.Duration, bool, error) {
	job.Attempts++
	if job.Attempts >= job.MaxAttempts {
		return 0, false, s.failJob(ctx, job, requestID, now, "mcp_deploy_status_failed", "failed to poll MCP wiring deploy runner: "+err.Error())
	}
	job.Note = "failed to poll MCP wiring deploy runner; retrying: " + compactErr(err)
	_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
	return jitteredBackoff(job.Attempts, provisionDefaultPollDelay, 10*time.Minute), false, nil
}

func (s *Server) advanceProvisionDeployMcpWaitInProgress(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
		return 0, false, s.failJob(ctx, job, requestID, now, "mcp_deploy_timeout", "MCP wiring deploy runner timed out")
	}
	job.Note = "MCP wiring deploy runner in progress"
	_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
	return provisionDefaultPollDelay, false, nil
}

func (s *Server) advanceProvisionDeployMcpWaitFailed(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, deepLink string) (time.Duration, bool, error) {
	msg := "MCP wiring deploy runner failed"
	if deepLink != "" {
		msg = msg + " (CodeBuild: " + deepLink + ")"
	}
	return 0, false, s.failJob(ctx, job, requestID, now, "mcp_deploy_failed", msg)
}

func (s *Server) advanceProvisionDeployMcpWaitUnknownStatus(ctx context.Context, job *models.ProvisionJob, requestID string, now time.Time, status string) (time.Duration, bool, error) {
	if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) > provisionMaxDeployAge {
		return 0, false, s.failJob(ctx, job, requestID, now, "mcp_deploy_timeout", "MCP wiring deploy runner timed out")
	}
	job.Note = "MCP wiring deploy runner status: " + status
	_ = s.persistJobAndInstance(ctx, job, requestID, now, nil)
	return provisionDefaultPollDelay, false, nil
}
