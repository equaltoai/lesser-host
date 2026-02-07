package trust

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	aiModerationTextBaseCredits  = int64(2)
	aiModerationImageBaseCredits = int64(4)
)

type aiModerationTextRequest struct {
	Text    string                 `json:"text"`
	Context *aiModerationScanCtxV1 `json:"context,omitempty"`
}

type aiModerationImageRequest struct {
	ObjectKey string                 `json:"object_key,omitempty"`
	URL       string                 `json:"url,omitempty"`
	Context   *aiModerationScanCtxV1 `json:"context,omitempty"`
}

type aiModerationScanCtxV1 struct {
	HasLinks      bool  `json:"has_links,omitempty"`
	HasMedia      bool  `json:"has_media,omitempty"`
	ViralityScore int64 `json:"virality_score,omitempty"`
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

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func (s *Server) handleAIModerationText(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationTextTriggered(ctx, "moderation.scan.request")
}

func (s *Server) handleAIModerationTextReport(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationTextTriggered(ctx, "moderation.scan.report")
}

func clampModerationText(raw string) (string, *apptheory.AppError) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "text is required"}
	}
	if len([]byte(text)) <= 10_000 {
		return text, nil
	}
	return string([]byte(text)[:10_000]), nil
}

func moderationModelSet(instCfg instanceTrustConfig) string {
	modelSet := modelSetDeterministic
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}
	return modelSet
}

func (s *Server) writeAIJobAuditEntryBestEffort(ctx *apptheory.Context, instanceSlug string, action string, resp ai.Response) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return
	}
	if strings.TrimSpace(action) == "" || strings.TrimSpace(resp.JobID) == "" {
		return
	}

	entry := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(instanceSlug),
		Action:    strings.TrimSpace(action),
		Target:    "ai_job:" + strings.TrimSpace(resp.JobID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: time.Now().UTC(),
	}
	_ = entry.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(entry).Create()
}

func buildAIModerationResponse(resp ai.Response, module string, policyVersion string, modelSet string, inputsHash string) aiModerationResponse {
	out := aiModerationResponse{
		Status: string(resp.Status),
		Cached: resp.Cached,
		JobID:  strings.TrimSpace(resp.JobID),
		Budget: resp.Budget,
		Contract: ai.ModuleContract{
			Module:        strings.TrimSpace(module),
			PolicyVersion: strings.TrimSpace(policyVersion),
			ModelSet:      strings.TrimSpace(modelSet),
			InputsHash:    strings.TrimSpace(inputsHash),
		},
	}
	if resp.Result == nil {
		return out
	}

	var parsed any
	if strings.TrimSpace(resp.Result.ResultJSON) != "" {
		_ = json.Unmarshal([]byte(resp.Result.ResultJSON), &parsed)
	}
	out.Contract.CreatedAt = resp.Result.CreatedAt
	out.Contract.ExpiresAt = resp.Result.ExpiresAt
	out.Result = parsed
	out.Usage = resp.Result.Usage
	out.Errors = resp.Result.Errors
	return out
}

func (s *Server) handleAIModerationTextTriggered(ctx *apptheory.Context, action string) (*apptheory.Response, error) {
	instanceSlug, err := s.requireAIHandler(ctx)
	if err != nil {
		return nil, err
	}

	var req aiModerationTextRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	text, appErr := clampModerationText(req.Text)
	if appErr != nil {
		return nil, appErr
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow

	modelSet := moderationModelSet(instCfg)

	inputs := ai.ModerationTextInputsV1{Text: text}
	inputsHash, _ := ai.InputsHash(inputs)

	creditsRequested := billing.PricedCredits(aiModerationTextBaseCredits, instCfg.AIPricingMultiplierBps)
	if !instCfg.ModerationEnabled {
		return apptheory.JSON(
			http.StatusOK,
			moderationDisabledResponse(
				ai.ModerationTextLLMModule,
				ai.ModerationTextLLMPolicyVersion,
				modelSet,
				inputsHash,
				creditsRequested,
				"moderation scanning disabled for instance",
			),
		)
	}

	if strings.EqualFold(action, "moderation.scan.request") {
		trigger := strings.ToLower(strings.TrimSpace(instCfg.ModerationTrigger))
		ok, msg := moderationRequestAllowed(trigger, req.Context, instCfg.ModerationViralityMin, "text")
		if !ok {
			return apptheory.JSON(
				http.StatusOK,
				moderationDisabledResponse(
					ai.ModerationTextLLMModule,
					ai.ModerationTextLLMPolicyVersion,
					modelSet,
					inputsHash,
					creditsRequested,
					msg,
				),
			)
		}
	}

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
		MaxInflightJobs:      instCfg.AIMaxInflightJobs,
	})
	if err != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ModerationTextLLMModule, ai.Response{Status: ai.JobStatusError}, err)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	s.writeAIJobAuditEntryBestEffort(ctx, instanceSlug, action, resp)

	if enqueueErr := s.enqueueAIJobIfQueued(ctx, resp); enqueueErr != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ModerationTextLLMModule, ai.Response{Status: ai.JobStatusError, Budget: resp.Budget}, enqueueErr)
		return nil, enqueueErr
	}
	s.emitAIRequestMetrics(instanceSlug, ai.ModerationTextLLMModule, resp, nil)

	return apptheory.JSON(http.StatusOK, buildAIModerationResponse(resp, ai.ModerationTextLLMModule, ai.ModerationTextLLMPolicyVersion, modelSet, inputsHash))
}

func (s *Server) handleAIModerationImage(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationImageTriggered(ctx, "moderation.scan.request")
}

func (s *Server) handleAIModerationImageReport(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleAIModerationImageTriggered(ctx, "moderation.scan.report")
}

func (s *Server) handleAIModerationImageTriggered(ctx *apptheory.Context, action string) (*apptheory.Response, error) {
	if s.artifacts == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "artifact store not configured"}
	}

	instanceSlug, err := s.requireAIHandler(ctx)
	if err != nil {
		return nil, err
	}

	var req aiModerationImageRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	key, contentType, etag, size, err := s.prepareModerationImageInput(ctx.Context(), instanceSlug, req)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object_key or url is required"}
	}

	if size <= 0 || size > 5*1024*1024 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object too large"}
	}
	if ct := strings.ToLower(strings.TrimSpace(contentType)); ct != "" && !strings.HasPrefix(ct, "image/") {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "object must be an image"}
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow

	modelSet := moderationModelSet(instCfg)

	inputs := ai.ModerationImageInputsV1{
		ObjectKey:   key,
		ObjectETag:  strings.TrimSpace(etag),
		Bytes:       size,
		ContentType: strings.TrimSpace(contentType),
	}
	inputsHash, _ := ai.InputsHash(inputs)

	creditsRequested := billing.PricedCredits(aiModerationImageBaseCredits, instCfg.AIPricingMultiplierBps)
	if !instCfg.ModerationEnabled {
		return apptheory.JSON(
			http.StatusOK,
			moderationDisabledResponse(
				ai.ModerationImageLLMModule,
				ai.ModerationImageLLMPolicyVersion,
				modelSet,
				inputsHash,
				creditsRequested,
				"moderation scanning disabled for instance",
			),
		)
	}

	if strings.EqualFold(action, "moderation.scan.request") {
		trigger := strings.ToLower(strings.TrimSpace(instCfg.ModerationTrigger))
		ok, msg := moderationRequestAllowed(trigger, req.Context, instCfg.ModerationViralityMin, "image")
		if !ok {
			return apptheory.JSON(
				http.StatusOK,
				moderationDisabledResponse(
					ai.ModerationImageLLMModule,
					ai.ModerationImageLLMPolicyVersion,
					modelSet,
					inputsHash,
					creditsRequested,
					msg,
				),
			)
		}
	}

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
		MaxInflightJobs:      instCfg.AIMaxInflightJobs,
	})
	if err != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ModerationImageLLMModule, ai.Response{Status: ai.JobStatusError}, err)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	s.writeAIJobAuditEntryBestEffort(ctx, instanceSlug, action, resp)

	if enqueueErr := s.enqueueAIJobIfQueued(ctx, resp); enqueueErr != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ModerationImageLLMModule, ai.Response{Status: ai.JobStatusError, Budget: resp.Budget}, enqueueErr)
		return nil, enqueueErr
	}
	s.emitAIRequestMetrics(instanceSlug, ai.ModerationImageLLMModule, resp, nil)

	return apptheory.JSON(http.StatusOK, buildAIModerationResponse(resp, ai.ModerationImageLLMModule, ai.ModerationImageLLMPolicyVersion, modelSet, inputsHash))
}

func (s *Server) prepareModerationImageInput(ctx context.Context, instanceSlug string, req aiModerationImageRequest) (key string, contentType string, etag string, size int64, err error) {
	if s == nil || s.artifacts == nil {
		return "", "", "", 0, &apptheory.AppError{Code: "app.internal", Message: "artifact store not configured"}
	}

	instanceSlug = strings.TrimSpace(instanceSlug)
	key = strings.TrimSpace(req.ObjectKey)
	rawURL := strings.TrimSpace(req.URL)
	if rawURL != "" {
		start, err2 := normalizeModerationImageURL(rawURL)
		if err2 != nil {
			return "", "", "", 0, &apptheory.AppError{Code: "app.bad_request", Message: "invalid url"}
		}

		client := newPreviewHTTPClient(8 * time.Second)
		_, _, body, ct, fetchErr := fetchWithRedirects(ctx, nil, client, start, 3, 5*1024*1024)
		if fetchErr != nil {
			return "", "", "", 0, &apptheory.AppError{Code: "app.bad_request", Message: "failed to fetch url"}
		}
		ct = strings.TrimSpace(ct)
		if ct == "" {
			ct = http.DetectContentType(body)
		}
		if strings.TrimSpace(ct) == "" || !strings.HasPrefix(strings.ToLower(ct), "image/") {
			return "", "", "", 0, &apptheory.AppError{Code: "app.bad_request", Message: "url must be an image"}
		}

		sum := sha256.Sum256(body)
		etag = fmt.Sprintf("%x", sum[:])
		key = fmt.Sprintf("moderation/%s/%s", instanceSlug, etag)
		if putErr := s.artifacts.PutObject(ctx, key, body, ct, "no-store"); putErr != nil {
			return "", "", "", 0, &apptheory.AppError{Code: "app.internal", Message: "failed to store object"}
		}
		return key, ct, etag, int64(len(body)), nil
	}

	if key == "" {
		return "", "", "", 0, nil
	}

	prefix := fmt.Sprintf("moderation/%s/", instanceSlug)
	if !strings.HasPrefix(key, prefix) {
		return "", "", "", 0, &apptheory.AppError{Code: "app.bad_request", Message: "object_key must be under " + prefix}
	}

	ct, e, sz, headErr := s.artifacts.HeadObject(ctx, key)
	if headErr != nil {
		return "", "", "", 0, &apptheory.AppError{Code: "app.bad_request", Message: "object not found"}
	}
	return key, ct, e, sz, nil
}

func normalizeModerationImageURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	_, normalized, err := normalizeLinkURL(raw)
	if err != nil || normalized == nil {
		return nil, fmt.Errorf("invalid url")
	}
	return normalized, nil
}

func moderationDisabledResponse(module string, policyVersion string, modelSet string, inputsHash string, creditsRequested int64, message string) aiModerationResponse {
	return aiModerationResponse{
		Status: statusDisabled,
		Cached: false,
		Budget: ai.BudgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           statusDisabled,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		},
		Contract: ai.ModuleContract{
			Module:        strings.TrimSpace(module),
			PolicyVersion: strings.TrimSpace(policyVersion),
			ModelSet:      strings.TrimSpace(modelSet),
			InputsHash:    strings.TrimSpace(inputsHash),
		},
		ErrorCode:    statusDisabled,
		ErrorMessage: strings.TrimSpace(message),
	}
}

func moderationRequestAllowed(trigger string, scanCtx *aiModerationScanCtxV1, viralityMin int64, kind string) (bool, string) {
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	switch trigger {
	case moderationTriggerOnReports:
		return false, fmt.Sprintf("moderation trigger is on_reports; use /ai/moderation/%s/report", strings.TrimSpace(kind))
	case moderationTriggerLinksMediaOnly:
		// For image scans, the item is inherently "media".
		if strings.EqualFold(kind, "image") {
			return true, ""
		}

		hasLinks := scanCtx != nil && scanCtx.HasLinks
		hasMedia := scanCtx != nil && scanCtx.HasMedia
		if !hasLinks && !hasMedia {
			return false, "moderation trigger is links_media_only and context indicates no links or media"
		}
		return true, ""
	case moderationTriggerVirality:
		if viralityMin <= 0 {
			return false, "moderation trigger is virality but moderation_virality_min is not configured"
		}
		score := int64(0)
		if scanCtx != nil {
			score = scanCtx.ViralityScore
		}
		if score < viralityMin {
			return false, "moderation trigger is virality and virality_score is below threshold"
		}
		return true, ""
	default:
		return true, ""
	}
}
