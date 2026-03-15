package provisionworker

import (
	"context"
	"strings"
	"time"

	"github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func updateBodyReceiptIngestInstanceUpdate(job *models.UpdateJob, bodyProvisionedAt time.Time) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		if job == nil {
			return nil
		}
		appliedAt := bodyProvisionedAt
		if appliedAt.IsZero() {
			appliedAt = job.UpdatedAt
		}
		setManagedUpdateInstanceMarker(ub, job, models.UpdateJobStatusOK, appliedAt)
		if strings.TrimSpace(job.LesserBodyVersion) != "" {
			ub.Set("LesserBodyVersion", strings.TrimSpace(job.LesserBodyVersion))
		}
		if !bodyProvisionedAt.IsZero() {
			ub.Set("BodyProvisionedAt", bodyProvisionedAt)
		}
		return nil
	}
}

func updateMCPReceiptIngestInstanceUpdate(job *models.UpdateJob, mcpWiredAt time.Time) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		if job == nil {
			return nil
		}
		appliedAt := mcpWiredAt
		if appliedAt.IsZero() {
			appliedAt = job.UpdatedAt
		}
		setManagedUpdateInstanceMarker(ub, job, models.UpdateJobStatusOK, appliedAt)
		if strings.TrimSpace(job.LesserBodyVersion) != "" {
			ub.Set("LesserBodyVersion", strings.TrimSpace(job.LesserBodyVersion))
		}
		if !mcpWiredAt.IsZero() {
			ub.Set("McpWiredAt", mcpWiredAt)
		}
		return nil
	}
}

type updatePhaseReceiptIngestSpec struct {
	phase              string
	receiptKey         string
	phaseLabel         string
	failureCode        string
	successNote        string
	loadReceiptVersion func(context.Context, string) (string, string, error)
	instanceUpdate     func(*models.UpdateJob, time.Time) func(core.UpdateBuilder) error
}

type updateRunnerReceiptRecoverySpec struct {
	ingestStep  string
	ingestNote  string
	phase       string
	receiptSpec updatePhaseReceiptIngestSpec
	loadReceipt func(context.Context, string) (string, string, error)
}

func (s *Server) recoverUpdateRunnerFromReceipt(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updateRunnerReceiptRecoverySpec,
) (time.Duration, bool, error, bool) {
	if s == nil || job == nil {
		return 0, false, nil, false
	}
	receiptKey := strings.TrimSpace(spec.receiptSpec.receiptKey)
	if receiptKey == "" || spec.loadReceipt == nil {
		return 0, false, nil, false
	}

	receiptJSON, bodyVersion, err := spec.loadReceipt(ctx, receiptKey)
	if err != nil {
		return 0, false, nil, false
	}

	job.RunID = ""
	job.Step = strings.TrimSpace(spec.ingestStep)
	job.Note = strings.TrimSpace(spec.ingestNote)
	setUpdateJobActivePhase(job, spec.phase)
	delay, done, ingestErr := s.advanceUpdatePhaseReceiptLoaded(ctx, job, requestID, now, spec.receiptSpec, receiptJSON, bodyVersion)
	return delay, done, ingestErr, true
}

func (s *Server) advanceUpdateRunnerWaitWithReceiptRecovery(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	recovery updateRunnerReceiptRecoverySpec,
	wait updateRunnerWaitSpec,
) (time.Duration, bool, error) {
	if delay, done, err, recovered := s.recoverUpdateRunnerFromReceipt(ctx, job, requestID, now, recovery); recovered {
		return delay, done, err
	}
	return s.advanceUpdateRunnerWait(ctx, job, requestID, now, wait)
}

func (s *Server) advanceUpdateComponentDeployWait(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	ingestStep string,
	ingestNote string,
	wait updateRunnerWaitSpec,
	receiptSpec updatePhaseReceiptIngestSpec,
	loadReceipt func(context.Context, string) (string, string, error),
) (time.Duration, bool, error) {
	return s.advanceUpdateRunnerWaitWithReceiptRecovery(ctx, job, requestID, now, updateRunnerReceiptRecoverySpec{
		ingestStep:  strings.TrimSpace(ingestStep),
		ingestNote:  strings.TrimSpace(ingestNote),
		phase:       strings.TrimSpace(receiptSpec.phase),
		receiptSpec: receiptSpec,
		loadReceipt: loadReceipt,
	}, wait)
}

func (s *Server) advanceUpdateOptionalPhaseDeployWait(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	phase string,
	receiptKey string,
	phaseLabel string,
	pollFailureCode string,
	pollFailureMessage string,
	ingestStep string,
	ingestNote string,
	inProgressNote string,
	timeoutCode string,
	timeoutMessage string,
	failedCode string,
	failedMessage string,
	statusPrefix string,
	receiptFailureCode string,
	successNote string,
	instanceUpdate func(*models.UpdateJob, time.Time) func(core.UpdateBuilder) error,
	loadReceipt func(context.Context, string) (string, string, error),
) (time.Duration, bool, error) {
	receiptSpec := updatePhaseReceiptIngestSpec{
		phase:          strings.TrimSpace(phase),
		receiptKey:     strings.TrimSpace(receiptKey),
		phaseLabel:     strings.TrimSpace(phaseLabel),
		failureCode:    strings.TrimSpace(receiptFailureCode),
		successNote:    strings.TrimSpace(successNote),
		instanceUpdate: instanceUpdate,
	}
	waitSpec := updateRunnerWaitSpec{
		phase:              strings.TrimSpace(phase),
		pollFailureCode:    strings.TrimSpace(pollFailureCode),
		pollFailureMessage: strings.TrimSpace(pollFailureMessage),
		successStep:        strings.TrimSpace(ingestStep),
		successNote:        strings.TrimSpace(ingestNote),
		inProgressNote:     strings.TrimSpace(inProgressNote),
		timeoutCode:        strings.TrimSpace(timeoutCode),
		timeoutMessage:     strings.TrimSpace(timeoutMessage),
		failedCode:         strings.TrimSpace(failedCode),
		failedMessage:      strings.TrimSpace(failedMessage),
		statusPrefix:       strings.TrimSpace(statusPrefix),
	}
	return s.advanceUpdateComponentDeployWait(ctx, job, requestID, now, ingestStep, ingestNote, waitSpec, receiptSpec, loadReceipt)
}

func (s *Server) advanceUpdatePhaseReceiptLoaded(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updatePhaseReceiptIngestSpec,
	receiptJSON string,
	bodyVersion string,
) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}
	job.RunID = ""
	if strings.TrimSpace(bodyVersion) != "" {
		job.LesserBodyVersion = strings.TrimSpace(bodyVersion)
	}
	job.ReceiptJSON = strings.TrimSpace(receiptJSON)
	job.Step = updateStepDone
	job.Status = models.UpdateJobStatusOK
	job.Note = strings.TrimSpace(spec.successNote)
	job.ErrorCode = ""
	job.ErrorMessage = ""
	setUpdateJobPhaseSucceeded(job, spec.phase)
	if err := s.persistUpdateJobAndInstance(ctx, job, requestID, now, spec.instanceUpdate(job, now)); err != nil {
		return 0, false, err
	}
	return 0, true, nil
}

func (s *Server) advanceUpdatePhaseReceiptIngest(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updatePhaseReceiptIngestSpec,
) (time.Duration, bool, error) {
	if job == nil {
		return 0, true, nil
	}

	receiptJSON, bodyVersion, err := spec.loadReceiptVersion(ctx, strings.TrimSpace(spec.receiptKey))
	if err != nil {
		msg := "failed to load " + strings.TrimSpace(spec.phaseLabel) + " receipt: " + err.Error()
		job.Attempts++
		if job.Attempts >= job.MaxAttempts {
			setUpdateJobPhaseFailed(job, spec.phase, msg)
			return 0, false, s.failUpdateJob(ctx, job, requestID, now, spec.failureCode, msg)
		}
		job.Note = "failed to load " + strings.TrimSpace(spec.phaseLabel) + " receipt; retrying: " + compactErr(err)
		setUpdateJobActivePhase(job, spec.phase)
		_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
		return jitteredBackoff(job.Attempts, provisionDefaultShortRetryDelay, 5*time.Minute), false, nil
	}

	return s.advanceUpdatePhaseReceiptLoaded(ctx, job, requestID, now, spec, receiptJSON, bodyVersion)
}
