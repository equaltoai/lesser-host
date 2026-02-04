package trust

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	aiModerationTextBaseCredits  = int64(2)
	aiModerationImageBaseCredits = int64(4)
)

type aiModerationTextRequest struct {
	Text string `json:"text"`
}

type aiModerationImageRequest struct {
	ObjectKey string `json:"object_key"`
}

type aiModerationResponse struct {
	Status string `json:"status"` // ok|queued|not_checked_budget|error
	Cached bool   `json:"cached"`
	JobID  string `json:"job_id,omitempty"`

	Budget ai.BudgetDecision `json:"budget"`

	Contract ai.ModuleContract `json:"contract"`

	Result any              `json:"result,omitempty"`
	Usage  models.AIUsage   `json:"usage,omitempty"`
	Errors []models.AIError `json:"errors,omitempty"`
}

func (s *Server) handleAIModerationText(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationTextTriggered(ctx, "moderation.scan.request")
}

func (s *Server) handleAIModerationTextReport(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationTextTriggered(ctx, "moderation.scan.report")
}

func (s *Server) handleAIModerationTextTriggered(ctx *apptheory.Context, action string) (*apptheory.Response, error) {
	if s == nil || s.ai == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var req aiModerationTextRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "text is required"}
	}
	// Guardrail: keep moderation input bounded.
	if len([]byte(text)) > 10_000 {
		b := []byte(text)
		text = string(b[:10_000])
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == "allow"

	modelSet := "deterministic"
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}

	inputs := ai.ModerationTextInputsV1{Text: text}
	inputsHash, _ := ai.InputsHash(inputs)

	resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
		InstanceSlug:         instanceSlug,
		RequestID:            strings.TrimSpace(ctx.RequestID),
		Module:               ai.ModerationTextLLMModule,
		PolicyVersion:        ai.ModerationTextLLMPolicyVersion,
		ModelSet:             modelSet,
		CacheScope:           ai.CacheScopeInstance,
		Inputs:               inputs,
		BaseCredits:          aiModerationTextBaseCredits,
		PricingMultiplierBps: instCfg.AIPricingMultiplierBps,
		AllowOverage:         allowOverage,
		JobTTL:               30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	if strings.TrimSpace(action) != "" && s.store.DB != nil && strings.TrimSpace(resp.JobID) != "" {
		entry := &models.AuditLogEntry{
			Actor:     instanceSlug,
			Action:    strings.TrimSpace(action),
			Target:    "ai_job:" + strings.TrimSpace(resp.JobID),
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: time.Now().UTC(),
		}
		_ = entry.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(entry).Create()
	}

	if resp.Status == ai.JobStatusQueued {
		if s.queues == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "safety queue not configured"}
		}
		if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue job"}
		}
	}

	out := aiModerationResponse{
		Status: string(resp.Status),
		Cached: resp.Cached,
		JobID:  strings.TrimSpace(resp.JobID),
		Budget: resp.Budget,
		Contract: ai.ModuleContract{
			Module:        ai.ModerationTextLLMModule,
			PolicyVersion: ai.ModerationTextLLMPolicyVersion,
			ModelSet:      modelSet,
			InputsHash:    strings.TrimSpace(inputsHash),
		},
	}
	if resp.Result != nil {
		var parsed any
		if strings.TrimSpace(resp.Result.ResultJSON) != "" {
			_ = json.Unmarshal([]byte(resp.Result.ResultJSON), &parsed)
		}
		out.Contract.CreatedAt = resp.Result.CreatedAt
		out.Contract.ExpiresAt = resp.Result.ExpiresAt
		out.Result = parsed
		out.Usage = resp.Result.Usage
		out.Errors = resp.Result.Errors
	}

	return apptheory.JSON(http.StatusOK, out)
}

func (s *Server) handleAIModerationImage(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationImageTriggered(ctx, "moderation.scan.request")
}

func (s *Server) handleAIModerationImageReport(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationImageTriggered(ctx, "moderation.scan.report")
}

func (s *Server) handleAIModerationImageTriggered(ctx *apptheory.Context, action string) (*apptheory.Response, error) {
	if s == nil || s.ai == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.artifacts == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "artifact store not configured"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var req aiModerationImageRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	key := strings.TrimSpace(req.ObjectKey)
	if key == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object_key is required"}
	}

	contentType, etag, size, err := s.artifacts.HeadObject(ctx.Context(), key)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object not found"}
	}
	if size <= 0 || size > 5*1024*1024 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object too large"}
	}
	if ct := strings.ToLower(strings.TrimSpace(contentType)); ct != "" && !strings.HasPrefix(ct, "image/") {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object must be an image"}
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == "allow"

	modelSet := "deterministic"
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}

	inputs := ai.ModerationImageInputsV1{
		ObjectKey:   key,
		ObjectETag:  strings.TrimSpace(etag),
		Bytes:       size,
		ContentType: strings.TrimSpace(contentType),
	}
	inputsHash, _ := ai.InputsHash(inputs)

	resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
		InstanceSlug:         instanceSlug,
		RequestID:            strings.TrimSpace(ctx.RequestID),
		Module:               ai.ModerationImageLLMModule,
		PolicyVersion:        ai.ModerationImageLLMPolicyVersion,
		ModelSet:             modelSet,
		CacheScope:           ai.CacheScopeInstance,
		Inputs:               inputs,
		BaseCredits:          aiModerationImageBaseCredits,
		PricingMultiplierBps: instCfg.AIPricingMultiplierBps,
		AllowOverage:         allowOverage,
		JobTTL:               30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	if strings.TrimSpace(action) != "" && s.store.DB != nil && strings.TrimSpace(resp.JobID) != "" {
		entry := &models.AuditLogEntry{
			Actor:     instanceSlug,
			Action:    strings.TrimSpace(action),
			Target:    "ai_job:" + strings.TrimSpace(resp.JobID),
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: time.Now().UTC(),
		}
		_ = entry.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(entry).Create()
	}

	if resp.Status == ai.JobStatusQueued {
		if s.queues == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "safety queue not configured"}
		}
		if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue job"}
		}
	}

	out := aiModerationResponse{
		Status: string(resp.Status),
		Cached: resp.Cached,
		JobID:  strings.TrimSpace(resp.JobID),
		Budget: resp.Budget,
		Contract: ai.ModuleContract{
			Module:        ai.ModerationImageLLMModule,
			PolicyVersion: ai.ModerationImageLLMPolicyVersion,
			ModelSet:      modelSet,
			InputsHash:    strings.TrimSpace(inputsHash),
		},
	}
	if resp.Result != nil {
		var parsed any
		if strings.TrimSpace(resp.Result.ResultJSON) != "" {
			_ = json.Unmarshal([]byte(resp.Result.ResultJSON), &parsed)
		}
		out.Contract.CreatedAt = resp.Result.CreatedAt
		out.Contract.ExpiresAt = resp.Result.ExpiresAt
		out.Result = parsed
		out.Usage = resp.Result.Usage
		out.Errors = resp.Result.Errors
	}

	return apptheory.JSON(http.StatusOK, out)
}
