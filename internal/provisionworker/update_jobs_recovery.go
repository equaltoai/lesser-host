package provisionworker

import (
	"context"
	"fmt"
	"strings"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func updateRunnerMissingExpired(job *models.UpdateJob, now time.Time) bool {
	if job == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	lastSeen := job.UpdatedAt
	if lastSeen.IsZero() {
		lastSeen = job.CreatedAt
	}
	if lastSeen.IsZero() {
		return false
	}
	return now.Sub(lastSeen) >= updateRunnerMissingMaxAge
}

func (s *Server) failMissingUpdateRunnerWait(
	ctx context.Context,
	job *models.UpdateJob,
	requestID string,
	now time.Time,
	spec updateRunnerWaitSpec,
) (time.Duration, bool, error) {
	msg := strings.TrimSpace(spec.missingMessage)
	if msg == "" {
		msg = "deploy runner disappeared from CodeBuild before the update could reconcile its terminal state"
	}

	runURL := strings.TrimSpace(job.RunURL)
	if runURL == "" {
		switch strings.TrimSpace(spec.phase) {
		case updatePhaseDeploy:
			runURL = strings.TrimSpace(job.DeployRunURL)
		case updatePhaseBody:
			runURL = strings.TrimSpace(job.BodyRunURL)
		case updatePhaseMCP:
			runURL = strings.TrimSpace(job.MCPRunURL)
		}
	}
	if runURL != "" {
		job.RunURL = runURL
		setUpdateJobPhaseRunURL(job, spec.phase, runURL)
		msg += " (CodeBuild: " + runURL + ")"
	}

	setUpdateJobPhaseFailed(job, spec.phase, msg)
	code := strings.TrimSpace(spec.missingCode)
	if code == "" {
		code = strings.TrimSpace(spec.failedCode)
	}
	return 0, false, s.failUpdateJob(ctx, job, requestID, now, code, msg)
}

func (s *Server) processActiveUpdateSweep(ctx context.Context, requestID string, now time.Time) (map[string]any, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	items, err := s.store.ListActiveUpdateJobs(ctx, updateSweepLimit)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, err
	}

	activeJobs := len(items)
	processed := 0
	errorCount := 0
	var firstErr error

	for _, item := range items {
		if item == nil || !updateJobProcessable(item) {
			continue
		}
		if err := s.processUpdateJob(ctx, requestID, strings.TrimSpace(item.ID)); err != nil {
			errorCount++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		processed++
	}

	result := map[string]any{
		"active_jobs": activeJobs,
		"processed":   processed,
		"errors":      errorCount,
		"swept_at":    now.UTC().Format(time.RFC3339),
	}
	if firstErr != nil {
		return result, fmt.Errorf("update sweep encountered %d errors: %w", errorCount, firstErr)
	}
	return result, nil
}
