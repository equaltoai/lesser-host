package trust

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	aiEvidenceTextModule        = "evidence_text_comprehend"
	aiEvidenceTextPolicyVersion = "v1"
	aiEvidenceTextModelSet      = "aws:comprehend"
	aiEvidenceTextBaseCredits   = int64(1)

	aiEvidenceImageModule        = "evidence_image_rekognition"
	aiEvidenceImagePolicyVersion = "v1"
	aiEvidenceImageModelSet      = "aws:rekognition"
	aiEvidenceImageBaseCredits   = int64(3)
)

type aiEvidenceTextInputsV1 struct {
	Text string `json:"text"`
}

type aiEvidenceImageInputsV1 struct {
	ObjectKey   string `json:"object_key"`
	ObjectETag  string `json:"object_etag,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type aiEvidenceTextRequest struct {
	Text string `json:"text"`
}

type aiEvidenceImageRequest struct {
	ObjectKey string `json:"object_key"`
}

type aiEvidenceResponse struct {
	Status string `json:"status"` // ok|queued|not_checked_budget|error
	Cached bool   `json:"cached"`
	JobID  string `json:"job_id,omitempty"`

	Budget ai.BudgetDecision `json:"budget"`

	Contract ai.ModuleContract `json:"contract"`

	Result any              `json:"result,omitempty"`
	Usage  models.AIUsage   `json:"usage,omitempty"`
	Errors []models.AIError `json:"errors,omitempty"`
}

var aiJobIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) handleGetAIJob(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	jobID := strings.TrimSpace(ctx.Param("jobId"))
	if !aiJobIDRE.MatchString(jobID) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid job id"}
	}

	job, err := s.store.GetAIJob(ctx.Context(), jobID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	// Instance-scoped jobs must not be visible cross-instance.
	if strings.TrimSpace(job.InstanceSlug) != instanceSlug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}

	out := map[string]any{
		"job": job,
	}

	if res, err := s.store.GetAIResult(ctx.Context(), jobID); err == nil && res != nil {
		var parsed any
		if strings.TrimSpace(res.ResultJSON) != "" {
			_ = json.Unmarshal([]byte(res.ResultJSON), &parsed)
		}
		out["result"] = map[string]any{
			"contract": ai.ModuleContract{
				Module:        strings.TrimSpace(res.Module),
				PolicyVersion: strings.TrimSpace(res.PolicyVersion),
				ModelSet:      strings.TrimSpace(res.ModelSet),
				InputsHash:    strings.TrimSpace(res.InputsHash),
				CreatedAt:     res.CreatedAt,
				ExpiresAt:     res.ExpiresAt,
			},
			"result": parsed,
			"usage":  res.Usage,
			"errors": res.Errors,
		}
	}

	return apptheory.JSON(http.StatusOK, out)
}

func (s *Server) handleAIEvidenceText(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req aiEvidenceTextRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "text is required"}
	}
	// Comprehend input size guardrail.
	if len([]byte(text)) > 5000 {
		b := []byte(text)
		text = string(b[:5000])
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == "allow"

	inputs := aiEvidenceTextInputsV1{Text: text}
	inputsHash, _ := ai.InputsHash(inputs)

	resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
		InstanceSlug:         instanceSlug,
		RequestID:            strings.TrimSpace(ctx.RequestID),
		Module:               aiEvidenceTextModule,
		PolicyVersion:        aiEvidenceTextPolicyVersion,
		ModelSet:             aiEvidenceTextModelSet,
		CacheScope:           ai.CacheScopeInstance,
		Inputs:               inputs,
		BaseCredits:          aiEvidenceTextBaseCredits,
		PricingMultiplierBps: instCfg.AIPricingMultiplierBps,
		AllowOverage:         allowOverage,
		JobTTL:               30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	if resp.Status == ai.JobStatusQueued {
		if s.queues == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "safety queue not configured"}
		}
		if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue job"}
		}
	}

	out := aiEvidenceResponse{
		Status: string(resp.Status),
		Cached: resp.Cached,
		JobID:  strings.TrimSpace(resp.JobID),
		Budget: resp.Budget,
		Contract: ai.ModuleContract{
			Module:        aiEvidenceTextModule,
			PolicyVersion: aiEvidenceTextPolicyVersion,
			ModelSet:      aiEvidenceTextModelSet,
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

func (s *Server) handleAIEvidenceImage(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req aiEvidenceImageRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	key := strings.TrimSpace(req.ObjectKey)
	if key == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object_key is required"}
	}

	// Small ref + ETag for stable caching without reading the full object.
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

	inputs := aiEvidenceImageInputsV1{
		ObjectKey:   key,
		ObjectETag:  strings.TrimSpace(etag),
		Bytes:       size,
		ContentType: strings.TrimSpace(contentType),
	}
	inputsHash, _ := ai.InputsHash(inputs)

	resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
		InstanceSlug:  instanceSlug,
		RequestID:     strings.TrimSpace(ctx.RequestID),
		Module:        aiEvidenceImageModule,
		PolicyVersion: aiEvidenceImagePolicyVersion,
		ModelSet:      aiEvidenceImageModelSet,
		CacheScope:    ai.CacheScopeInstance,
		Inputs:        inputs,
		Evidence: []models.AIEvidenceRef{
			{
				Kind:        "s3_object",
				Ref:         key,
				Hash:        strings.TrimSpace(etag),
				Bytes:       size,
				ContentType: strings.TrimSpace(contentType),
			},
		},
		BaseCredits:          aiEvidenceImageBaseCredits,
		PricingMultiplierBps: instCfg.AIPricingMultiplierBps,
		AllowOverage:         allowOverage,
		JobTTL:               30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	if resp.Status == ai.JobStatusQueued {
		if s.queues == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "safety queue not configured"}
		}
		if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue job"}
		}
	}

	out := aiEvidenceResponse{
		Status: string(resp.Status),
		Cached: resp.Cached,
		JobID:  strings.TrimSpace(resp.JobID),
		Budget: resp.Budget,
		Contract: ai.ModuleContract{
			Module:        aiEvidenceImageModule,
			PolicyVersion: aiEvidenceImagePolicyVersion,
			ModelSet:      aiEvidenceImageModelSet,
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
