package aiworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	comprehendtypes "github.com/aws/aws-sdk-go-v2/service/comprehend/types"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rekognitiontypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/attestations"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	deterministicValue       = "deterministic"
	claimVerdictInconclusive = "inconclusive"

	aiErrorCodeLLMUnavailable   = "llm_unavailable"
	aiErrorCodeLLMFailed        = "llm_failed"
	aiErrorCodeLLMMissingOutput = "llm_missing_output"

	claimVerifyEvidenceMaxBytes = int64(8 * 1024)
	claimVerifyMaxEvidenceItems = 5
)

var hex64RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

type aiStore interface {
	GetAIJob(ctx context.Context, id string) (*models.AIJob, error)
	PutAIJob(ctx context.Context, item *models.AIJob) error
	GetAIResult(ctx context.Context, id string) (*models.AIResult, error)
	PutAIResult(ctx context.Context, item *models.AIResult) error
}

type comprehendAPI interface {
	DetectDominantLanguage(ctx context.Context, params *comprehend.DetectDominantLanguageInput, optFns ...func(*comprehend.Options)) (*comprehend.DetectDominantLanguageOutput, error)
	DetectEntities(ctx context.Context, params *comprehend.DetectEntitiesInput, optFns ...func(*comprehend.Options)) (*comprehend.DetectEntitiesOutput, error)
	DetectPiiEntities(ctx context.Context, params *comprehend.DetectPiiEntitiesInput, optFns ...func(*comprehend.Options)) (*comprehend.DetectPiiEntitiesOutput, error)
}

type rekognitionAPI interface {
	DetectModerationLabels(ctx context.Context, params *rekognition.DetectModerationLabelsInput, optFns ...func(*rekognition.Options)) (*rekognition.DetectModerationLabelsOutput, error)
	DetectText(ctx context.Context, params *rekognition.DetectTextInput, optFns ...func(*rekognition.Options)) (*rekognition.DetectTextOutput, error)
	DetectFaces(ctx context.Context, params *rekognition.DetectFacesInput, optFns ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error)
}

// Server processes AI jobs from the worker queue.
type Server struct {
	cfg config.Config

	store       aiStore
	artifacts   *artifacts.Store
	comprehend  comprehendAPI
	rekognition rekognitionAPI
	attest      *attestations.KMSService
}

// NewServer constructs a Server with AWS service clients and a store.
func NewServer(cfg config.Config, st aiStore, art *artifacts.Store, comp comprehendAPI, rek rekognitionAPI) *Server {
	return &Server{
		cfg:         cfg,
		store:       st,
		artifacts:   art,
		comprehend:  comp,
		rekognition: rek,
		attest:      attestations.NewKMSService(cfg.AttestationSigningKeyID, cfg.AttestationPublicKeyIDs),
	}
}

// Register registers SQS handlers with the provided app.
func (s *Server) Register(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	queueName := sqsQueueNameFromURL(s.cfg.SafetyQueueURL)
	if queueName != "" {
		app.SQS(queueName, s.handleSafetyQueueMessage)
	}
}

func (s *Server) handleSafetyQueueMessage(ctx *apptheory.EventContext, msg events.SQSMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("event context is nil")
	}

	var jm ai.JobMessage
	if err := json.Unmarshal([]byte(msg.Body), &jm); err != nil {
		return nil // drop invalid
	}
	switch strings.TrimSpace(jm.Kind) {
	case "ai_job":
		jobID := strings.TrimSpace(jm.JobID)
		if jobID == "" {
			return nil
		}
		return s.processAIJob(ctx.Context(), ctx.RequestID, jobID)
	case "ai_job_batch":
		if len(jm.JobIDs) == 0 {
			return nil
		}
		return s.processAIBatch(ctx.Context(), ctx.RequestID, jm.JobIDs)
	default:
		return nil
	}
}

func (s *Server) processAIJob(ctx context.Context, requestID string, jobID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	now := time.Now().UTC()
	job := s.getQueuedAIJob(ctx, jobID, now)
	if job == nil {
		return nil // drop missing/expired/not-queued
	}

	if s.markAIJobOKIfResultExists(ctx, job, requestID) {
		return nil
	}

	module, resultJSON, usage, errs, runErr, ok := s.runAIJobModuleV1(ctx, job)
	if !ok {
		return nil // drop unknown module/policy
	}
	if runErr != nil {
		s.recordAIJobError(ctx, job, requestID, now, module, usage, errs, runErr)
		return runErr
	}

	if err := s.persistAIJobResult(ctx, job, requestID, now, module, resultJSON, usage, errs); err != nil {
		return err
	}
	s.emitAIJobMetrics(job.InstanceSlug, module, models.AIJobStatusOK, usage, errs, nil)
	return nil
}

func (s *Server) getQueuedAIJob(ctx context.Context, jobID string, now time.Time) *models.AIJob {
	if s == nil || s.store == nil {
		return nil
	}

	job, err := s.store.GetAIJob(ctx, strings.TrimSpace(jobID))
	if err != nil || job == nil {
		return nil
	}

	if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
		return nil
	}
	if strings.TrimSpace(job.Status) != models.AIJobStatusQueued {
		return nil
	}

	return job
}

func (s *Server) markAIJobOKIfResultExists(ctx context.Context, job *models.AIJob, requestID string) bool {
	if s == nil || s.store == nil || job == nil {
		return false
	}

	jobID := strings.TrimSpace(job.ID)
	if jobID == "" {
		return false
	}

	if existing, getResErr := s.store.GetAIResult(ctx, jobID); getResErr == nil && existing != nil {
		job.Status = models.AIJobStatusOK
		job.ErrorCode = ""
		job.ErrorMessage = ""
		job.RequestID = strings.TrimSpace(requestID)
		_ = job.UpdateKeys()
		_ = s.store.PutAIJob(ctx, job)
		return true
	}

	return false
}

func (s *Server) runAIJobModuleV1(ctx context.Context, job *models.AIJob) (string, string, models.AIUsage, []models.AIError, error, bool) {
	if s == nil || job == nil {
		return "", "", models.AIUsage{}, nil, nil, false
	}

	module := strings.ToLower(strings.TrimSpace(job.Module))
	if module == "" || strings.TrimSpace(job.PolicyVersion) != "v1" {
		return module, "", models.AIUsage{}, nil, nil, false
	}

	switch module {
	case "evidence_text_comprehend":
		resultJSON, usage, errs, err := s.runComprehendTextEvidenceV1(ctx, job)
		return module, resultJSON, usage, errs, err, true
	case "evidence_image_rekognition":
		resultJSON, usage, errs, err := s.runRekognitionImageEvidenceV1(ctx, job)
		return module, resultJSON, usage, errs, err, true
	case "render_summary_llm":
		resultJSON, usage, errs, err := s.runRenderSummaryLLMV1(ctx, job)
		return module, resultJSON, usage, errs, err, true
	case "moderation_text_llm":
		resultJSON, usage, errs, err := s.runModerationTextLLMV1(ctx, job)
		return module, resultJSON, usage, errs, err, true
	case "moderation_image_llm":
		resultJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, job)
		return module, resultJSON, usage, errs, err, true
	case "claim_verify_llm":
		resultJSON, usage, errs, err := s.runClaimVerifyLLMV1(ctx, job)
		return module, resultJSON, usage, errs, err, true
	default:
		return module, "", models.AIUsage{}, nil, nil, false
	}
}

func (s *Server) recordAIJobError(ctx context.Context, job *models.AIJob, requestID string, now time.Time, module string, usage models.AIUsage, errs []models.AIError, runErr error) {
	if s == nil || s.store == nil || job == nil {
		return
	}

	job.Status = models.AIJobStatusError
	job.Attempts++
	job.ErrorCode = "tool_failed"
	job.ErrorMessage = "tool execution failed"
	job.RequestID = strings.TrimSpace(requestID)
	_ = job.UpdateKeys()
	_ = s.store.PutAIJob(ctx, job)
	s.emitAIJobMetrics(job.InstanceSlug, strings.TrimSpace(module), models.AIJobStatusError, usage, errs, runErr)
}

func (s *Server) persistAIJobResult(ctx context.Context, job *models.AIJob, requestID string, now time.Time, module string, resultJSON string, usage models.AIUsage, errs []models.AIError) error {
	if s == nil || s.store == nil || job == nil {
		return fmt.Errorf("store not initialized")
	}

	res := &models.AIResult{
		ID:            strings.TrimSpace(job.ID),
		InstanceSlug:  strings.TrimSpace(job.InstanceSlug),
		Module:        strings.TrimSpace(module),
		PolicyVersion: strings.TrimSpace(job.PolicyVersion),
		ModelSet:      strings.TrimSpace(job.ModelSet),
		CacheScope:    strings.TrimSpace(job.CacheScope),
		ScopeKey:      strings.TrimSpace(job.ScopeKey),
		InputsHash:    strings.TrimSpace(job.InputsHash),
		ResultJSON:    strings.TrimSpace(resultJSON),
		Usage:         usage,
		Errors:        append([]models.AIError(nil), errs...),
		CreatedAt:     now,
		ExpiresAt:     now.Add(30 * 24 * time.Hour),
		JobID:         strings.TrimSpace(job.ID),
		RequestID:     strings.TrimSpace(requestID),
	}
	_ = res.UpdateKeys()

	if err := s.store.PutAIResult(ctx, res); err != nil {
		return err
	}

	job.Status = models.AIJobStatusOK
	job.ErrorCode = ""
	job.ErrorMessage = ""
	job.RequestID = strings.TrimSpace(requestID)
	_ = job.UpdateKeys()
	_ = s.store.PutAIJob(ctx, job)
	return nil
}

func (s *Server) processAIBatch(ctx context.Context, requestID string, jobIDs []string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	now := time.Now().UTC()

	// Collect eligible render summary jobs; fall back to per-job processing for mixed batches.
	var renderSummaryJobs []*models.AIJob
	var otherJobIDs []string

	for _, id := range jobIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		job, err := s.store.GetAIJob(ctx, id)
		if err != nil || job == nil {
			continue
		}

		if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
			continue
		}
		if strings.TrimSpace(job.Status) != models.AIJobStatusQueued {
			continue
		}

		if strings.ToLower(strings.TrimSpace(job.Module)) == "render_summary_llm" && strings.TrimSpace(job.PolicyVersion) == "v1" {
			renderSummaryJobs = append(renderSummaryJobs, job)
			continue
		}

		otherJobIDs = append(otherJobIDs, id)
	}

	// Mixed batches: process non-render-summary jobs individually.
	for _, id := range otherJobIDs {
		_ = s.processAIJob(ctx, requestID, id)
	}

	if len(renderSummaryJobs) == 0 {
		return nil
	}

	return s.processRenderSummaryBatchV1(ctx, requestID, renderSummaryJobs)
}

func (s *Server) processRenderSummaryBatchV1(ctx context.Context, requestID string, jobs []*models.AIJob) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if len(jobs) == 0 {
		return nil
	}

	now := time.Now().UTC()

	byModelSet := s.groupRenderSummaryJobs(ctx, requestID, now, jobs)
	for modelSet, group := range byModelSet {
		if err := s.processRenderSummaryGroup(ctx, requestID, now, modelSet, group); err != nil {
			return err
		}
	}
	return nil
}

type renderSummaryParsedJob struct {
	Job   *models.AIJob
	Input ai.RenderSummaryInputsV1
}

func (s *Server) groupRenderSummaryJobs(ctx context.Context, requestID string, now time.Time, jobs []*models.AIJob) map[string][]renderSummaryParsedJob {
	byModelSet := make(map[string][]renderSummaryParsedJob)
	for _, job := range jobs {
		pj, ok := s.parseRenderSummaryJob(ctx, requestID, now, job)
		if !ok {
			continue
		}
		ms := strings.TrimSpace(pj.Job.ModelSet)
		if ms == "" {
			ms = deterministicValue
		}
		byModelSet[ms] = append(byModelSet[ms], pj)
	}
	return byModelSet
}

func (s *Server) parseRenderSummaryJob(ctx context.Context, requestID string, now time.Time, job *models.AIJob) (renderSummaryParsedJob, bool) {
	if s == nil || s.store == nil || job == nil {
		return renderSummaryParsedJob{}, false
	}
	if strings.TrimSpace(job.Status) != models.AIJobStatusQueued {
		return renderSummaryParsedJob{}, false
	}
	if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
		return renderSummaryParsedJob{}, false
	}

	// Idempotency: if result already exists, mark job OK and skip.
	if existing, err := s.store.GetAIResult(ctx, strings.TrimSpace(job.ID)); err == nil && existing != nil {
		job.Status = models.AIJobStatusOK
		job.ErrorCode = ""
		job.ErrorMessage = ""
		job.RequestID = strings.TrimSpace(requestID)
		_ = job.UpdateKeys()
		_ = s.store.PutAIJob(ctx, job)
		return renderSummaryParsedJob{}, false
	}

	var in ai.RenderSummaryInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return renderSummaryParsedJob{}, false
	}

	return renderSummaryParsedJob{Job: job, Input: in}, true
}

func (s *Server) processRenderSummaryGroup(ctx context.Context, requestID string, now time.Time, modelSet string, group []renderSummaryParsedJob) error {
	if len(group) == 0 {
		return nil
	}

	items := renderSummaryBatchItems(group)
	if len(items) == 0 {
		return nil
	}

	results, usage, commonErrs := s.renderSummaryBatchResults(ctx, modelSet, items)

	for _, pj := range group {
		if err := s.putRenderSummaryResult(ctx, requestID, now, pj, modelSet, results, usage, commonErrs); err != nil {
			return err
		}
	}

	return nil
}

func renderSummaryBatchItems(group []renderSummaryParsedJob) []llm.RenderSummaryBatchItem {
	items := make([]llm.RenderSummaryBatchItem, 0, len(group))
	for _, pj := range group {
		if pj.Job == nil {
			continue
		}
		items = append(items, llm.RenderSummaryBatchItem{
			ItemID: strings.TrimSpace(pj.Job.ID),
			Input:  pj.Input,
		})
	}
	return items
}

func (s *Server) renderSummaryBatchResults(ctx context.Context, modelSet string, items []llm.RenderSummaryBatchItem) (map[string]ai.RenderSummaryResultV1, models.AIUsage, []models.AIError) {
	start := time.Now()
	results := map[string]ai.RenderSummaryResultV1{}
	usage := models.AIUsage{}
	commonErrs := []models.AIError{}

	modelSet = strings.TrimSpace(modelSet)
	useDeterministic := true

	lowerModelSet := strings.ToLower(modelSet)
	switch {
	case strings.HasPrefix(lowerModelSet, "openai:"):
		apiKey, keyErr := openAIAPIKey(ctx)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			commonErrs = append(commonErrs, models.AIError{Code: aiErrorCodeLLMUnavailable, Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		} else {
			outMap, u, err := llm.RenderSummaryBatchOpenAI(ctx, apiKey, modelSet, items)
			if err != nil {
				commonErrs = append(commonErrs, models.AIError{Code: aiErrorCodeLLMFailed, Message: "LLM call failed; used deterministic fallback", Retryable: false})
			} else {
				results = outMap
				usage = u
				useDeterministic = false
			}
		}
	case strings.HasPrefix(lowerModelSet, "anthropic:"):
		apiKey, keyErr := anthropicAPIKey(ctx)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			commonErrs = append(commonErrs, models.AIError{Code: aiErrorCodeLLMUnavailable, Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		} else {
			outMap, u, err := llm.RenderSummaryBatchAnthropic(ctx, apiKey, modelSet, items)
			if err != nil {
				commonErrs = append(commonErrs, models.AIError{Code: aiErrorCodeLLMFailed, Message: "LLM call failed; used deterministic fallback", Retryable: false})
			} else {
				results = outMap
				usage = u
				useDeterministic = false
			}
		}
	}

	if useDeterministic {
		for _, it := range items {
			results[it.ItemID] = ai.RenderSummaryDeterministicV1(it.Input)
		}
		usage = models.AIUsage{
			Provider:   deterministicValue,
			Model:      deterministicValue,
			ToolCalls:  0,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	return results, usage, commonErrs
}

func (s *Server) putRenderSummaryResult(
	ctx context.Context,
	requestID string,
	now time.Time,
	pj renderSummaryParsedJob,
	modelSet string,
	results map[string]ai.RenderSummaryResultV1,
	usage models.AIUsage,
	commonErrs []models.AIError,
) error {
	job := pj.Job
	if s == nil || s.store == nil || job == nil {
		return nil
	}

	id := strings.TrimSpace(job.ID)
	if id == "" {
		return nil
	}

	res, ok := results[id]
	itemErrs := append([]models.AIError(nil), commonErrs...)
	if !ok || strings.TrimSpace(res.ShortSummary) == "" {
		res = ai.RenderSummaryDeterministicV1(pj.Input)
		itemErrs = append(itemErrs, models.AIError{Code: aiErrorCodeLLMMissingOutput, Message: "LLM output missing; used deterministic fallback", Retryable: false})
	}

	b, err := json.Marshal(res)
	if err != nil {
		return err
	}

	item := &models.AIResult{
		ID:            id,
		InstanceSlug:  strings.TrimSpace(job.InstanceSlug),
		Module:        strings.ToLower(strings.TrimSpace(job.Module)),
		PolicyVersion: strings.TrimSpace(job.PolicyVersion),
		ModelSet:      strings.TrimSpace(modelSet),
		CacheScope:    strings.TrimSpace(job.CacheScope),
		ScopeKey:      strings.TrimSpace(job.ScopeKey),
		InputsHash:    strings.TrimSpace(job.InputsHash),
		ResultJSON:    strings.TrimSpace(string(b)),
		Usage:         usage,
		Errors:        itemErrs,
		CreatedAt:     now,
		ExpiresAt:     now.Add(30 * 24 * time.Hour),
		JobID:         id,
		RequestID:     strings.TrimSpace(requestID),
	}
	_ = item.UpdateKeys()

	if err := s.store.PutAIResult(ctx, item); err != nil {
		return err
	}

	job.Status = models.AIJobStatusOK
	job.ErrorCode = ""
	job.ErrorMessage = ""
	job.RequestID = strings.TrimSpace(requestID)
	_ = job.UpdateKeys()
	_ = s.store.PutAIJob(ctx, job)
	s.emitAIJobMetrics(job.InstanceSlug, strings.ToLower(strings.TrimSpace(job.Module)), models.AIJobStatusOK, usage, itemErrs, nil)

	return nil
}

type comprehendTextInputsV1 struct {
	Text string `json:"text"`
}

type comprehendTextLanguage struct {
	Code  string  `json:"code"`
	Score float64 `json:"score"`
}

type comprehendTextEntity struct {
	Text  string  `json:"text"`
	Type  string  `json:"type"`
	Score float64 `json:"score"`
	Begin int32   `json:"begin,omitempty"`
	End   int32   `json:"end,omitempty"`
}

type comprehendTextPIIEntity struct {
	Type  string  `json:"type"`
	Score float64 `json:"score"`
	Begin int32   `json:"begin,omitempty"`
	End   int32   `json:"end,omitempty"`
}

type comprehendTextEvidenceV1 struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`

	Language         []comprehendTextLanguage `json:"language,omitempty"`
	DominantLanguage string                   `json:"dominant_language,omitempty"`

	Entities    []comprehendTextEntity    `json:"entities,omitempty"`
	PIIEntities []comprehendTextPIIEntity `json:"pii_entities,omitempty"`

	Truncated bool     `json:"truncated,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func (s *Server) runComprehendTextEvidenceV1(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError, error) {
	if s == nil || s.comprehend == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("comprehend client not configured")
	}
	if job == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("job is nil")
	}

	text, ok := parseComprehendTextInputs(job)
	if !ok {
		return "", models.AIUsage{}, nil, nil
	}

	start := time.Now()

	out := comprehendTextEvidenceV1{
		Kind:    "comprehend_text",
		Version: "v1",
	}

	languageCode := s.detectComprehendLanguage(ctx, text, &out)
	s.detectComprehendEntities(ctx, text, languageCode, &out)
	if strings.EqualFold(languageCode, "en") {
		s.detectComprehendPII(ctx, text, &out)
	}

	b, err := json.Marshal(out)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}

	usage := models.AIUsage{
		Provider:   "aws",
		Model:      "comprehend",
		ToolCalls:  3,
		DurationMs: time.Since(start).Milliseconds(),
	}

	return string(b), usage, nil, nil
}

func parseComprehendTextInputs(job *models.AIJob) (string, bool) {
	if job == nil {
		return "", false
	}

	var in comprehendTextInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", false
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return "", false
	}

	if len([]byte(text)) > 5000 {
		b := []byte(text)
		text = string(b[:5000])
	}

	return text, true
}

func (s *Server) detectComprehendLanguage(ctx context.Context, text string, out *comprehendTextEvidenceV1) string {
	if s == nil || s.comprehend == nil || out == nil {
		return "en"
	}

	langOut, err := s.comprehend.DetectDominantLanguage(ctx, &comprehend.DetectDominantLanguageInput{
		Text: aws.String(text),
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_dominant_language_failed")
		return "en"
	}

	for _, l := range langOut.Languages {
		code := strings.TrimSpace(aws.ToString(l.LanguageCode))
		if code == "" {
			continue
		}
		out.Language = append(out.Language, comprehendTextLanguage{
			Code:  code,
			Score: roundScore(l.Score),
		})
	}
	sort.Slice(out.Language, func(i, j int) bool {
		if out.Language[i].Score == out.Language[j].Score {
			return out.Language[i].Code < out.Language[j].Code
		}
		return out.Language[i].Score > out.Language[j].Score
	})
	if len(out.Language) > 0 {
		out.DominantLanguage = out.Language[0].Code
	}

	code := strings.TrimSpace(out.DominantLanguage)
	if code == "" {
		code = "en"
	}
	return code
}

func (s *Server) detectComprehendEntities(ctx context.Context, text string, languageCode string, out *comprehendTextEvidenceV1) {
	if s == nil || s.comprehend == nil || out == nil {
		return
	}

	lang := strings.TrimSpace(languageCode)
	if lang == "" {
		lang = "en"
	}

	entOut, err := s.comprehend.DetectEntities(ctx, &comprehend.DetectEntitiesInput{
		Text:         aws.String(text),
		LanguageCode: comprehendtypes.LanguageCode(lang),
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_entities_failed")
		return
	}

	const maxEntities = 50
	for _, e := range entOut.Entities {
		t := strings.TrimSpace(aws.ToString(e.Text))
		if t == "" {
			continue
		}
		if len(t) > 64 {
			t = t[:64]
		}
		out.Entities = append(out.Entities, comprehendTextEntity{
			Text:  t,
			Type:  strings.TrimSpace(string(e.Type)),
			Score: roundScore(e.Score),
			Begin: aws.ToInt32(e.BeginOffset),
			End:   aws.ToInt32(e.EndOffset),
		})
		if len(out.Entities) >= maxEntities {
			out.Truncated = true
			break
		}
	}
	sort.Slice(out.Entities, func(i, j int) bool {
		if out.Entities[i].Begin == out.Entities[j].Begin {
			return out.Entities[i].Type < out.Entities[j].Type
		}
		return out.Entities[i].Begin < out.Entities[j].Begin
	})
}

func (s *Server) detectComprehendPII(ctx context.Context, text string, out *comprehendTextEvidenceV1) {
	if s == nil || s.comprehend == nil || out == nil {
		return
	}

	piiOut, err := s.comprehend.DetectPiiEntities(ctx, &comprehend.DetectPiiEntitiesInput{
		Text:         aws.String(text),
		LanguageCode: comprehendtypes.LanguageCodeEn,
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_pii_failed")
		return
	}

	const maxPII = 50
	for _, p := range piiOut.Entities {
		out.PIIEntities = append(out.PIIEntities, comprehendTextPIIEntity{
			Type:  strings.TrimSpace(string(p.Type)),
			Score: roundScore(p.Score),
			Begin: aws.ToInt32(p.BeginOffset),
			End:   aws.ToInt32(p.EndOffset),
		})
		if len(out.PIIEntities) >= maxPII {
			out.Truncated = true
			break
		}
	}
	sort.Slice(out.PIIEntities, func(i, j int) bool {
		if out.PIIEntities[i].Begin == out.PIIEntities[j].Begin {
			return out.PIIEntities[i].Type < out.PIIEntities[j].Type
		}
		return out.PIIEntities[i].Begin < out.PIIEntities[j].Begin
	})
}

type rekognitionImageInputsV1 struct {
	ObjectKey string `json:"object_key"`
}

type rekognitionLabel struct {
	Name       string  `json:"name"`
	ParentName string  `json:"parent_name,omitempty"`
	Confidence float64 `json:"confidence"`
}

type rekognitionTextDetection struct {
	Text       string  `json:"text"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

type rekognitionImageEvidenceV1 struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`

	ModerationLabels []rekognitionLabel         `json:"moderation_labels,omitempty"`
	TextDetections   []rekognitionTextDetection `json:"text_detections,omitempty"`

	FaceCount int `json:"face_count,omitempty"`

	Truncated bool     `json:"truncated,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func (s *Server) addRekognitionModerationLabels(ctx context.Context, img *rekognitiontypes.Image, out *rekognitionImageEvidenceV1) {
	if s == nil || s.rekognition == nil || img == nil || out == nil {
		return
	}

	mlOut, err := s.rekognition.DetectModerationLabels(ctx, &rekognition.DetectModerationLabelsInput{
		Image:         img,
		MinConfidence: aws.Float32(60),
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_moderation_failed")
		return
	}

	const maxLabels = 50
	for _, l := range mlOut.ModerationLabels {
		name := strings.TrimSpace(aws.ToString(l.Name))
		if name == "" {
			continue
		}
		out.ModerationLabels = append(out.ModerationLabels, rekognitionLabel{
			Name:       name,
			ParentName: strings.TrimSpace(aws.ToString(l.ParentName)),
			Confidence: roundScore(l.Confidence),
		})
		if len(out.ModerationLabels) >= maxLabels {
			out.Truncated = true
			break
		}
	}
	sort.Slice(out.ModerationLabels, func(i, j int) bool {
		return out.ModerationLabels[i].Name < out.ModerationLabels[j].Name
	})
}

func (s *Server) addRekognitionTextDetections(ctx context.Context, img *rekognitiontypes.Image, out *rekognitionImageEvidenceV1) {
	if s == nil || s.rekognition == nil || img == nil || out == nil {
		return
	}

	txtOut, err := s.rekognition.DetectText(ctx, &rekognition.DetectTextInput{Image: img})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_text_failed")
		return
	}

	const maxText = 50
	for _, d := range txtOut.TextDetections {
		t := strings.TrimSpace(aws.ToString(d.DetectedText))
		if t == "" {
			continue
		}
		if len(t) > 64 {
			t = t[:64]
		}
		out.TextDetections = append(out.TextDetections, rekognitionTextDetection{
			Text:       t,
			Type:       strings.TrimSpace(string(d.Type)),
			Confidence: roundScore(d.Confidence),
		})
		if len(out.TextDetections) >= maxText {
			out.Truncated = true
			break
		}
	}
	sort.Slice(out.TextDetections, func(i, j int) bool {
		if out.TextDetections[i].Confidence == out.TextDetections[j].Confidence {
			return out.TextDetections[i].Text < out.TextDetections[j].Text
		}
		return out.TextDetections[i].Confidence > out.TextDetections[j].Confidence
	})
}

func (s *Server) addRekognitionFaceCount(ctx context.Context, img *rekognitiontypes.Image, out *rekognitionImageEvidenceV1) {
	if s == nil || s.rekognition == nil || img == nil || out == nil {
		return
	}

	fOut, err := s.rekognition.DetectFaces(ctx, &rekognition.DetectFacesInput{
		Image:      img,
		Attributes: []rekognitiontypes.Attribute{rekognitiontypes.AttributeDefault},
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_faces_failed")
		return
	}

	out.FaceCount = len(fOut.FaceDetails)
}

func (s *Server) runRekognitionImageEvidenceV1(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError, error) {
	if s == nil || s.rekognition == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("rekognition client not configured")
	}
	if job == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("job is nil")
	}

	var in rekognitionImageInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", models.AIUsage{}, nil, nil // drop invalid
	}

	key := strings.TrimSpace(in.ObjectKey)
	if key == "" {
		return "", models.AIUsage{}, nil, nil
	}

	bucket := strings.TrimSpace(s.cfg.ArtifactBucketName)
	if bucket == "" {
		return "", models.AIUsage{}, nil, fmt.Errorf("artifact bucket not configured")
	}

	start := time.Now()

	img := &rekognitiontypes.Image{
		S3Object: &rekognitiontypes.S3Object{
			Bucket: aws.String(bucket),
			Name:   aws.String(key),
		},
	}

	out := rekognitionImageEvidenceV1{
		Kind:    "rekognition_image",
		Version: "v1",
	}

	s.addRekognitionModerationLabels(ctx, img, &out)
	s.addRekognitionTextDetections(ctx, img, &out)
	s.addRekognitionFaceCount(ctx, img, &out)

	b, err := json.Marshal(out)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}

	usage := models.AIUsage{
		Provider:   "aws",
		Model:      "rekognition",
		ToolCalls:  3,
		DurationMs: time.Since(start).Milliseconds(),
	}

	return string(b), usage, nil, nil
}

func (s *Server) runRenderSummaryLLMV1(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError, error) {
	if job == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("job is nil")
	}

	var in ai.RenderSummaryInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", models.AIUsage{}, nil, nil // drop invalid
	}

	modelSet := strings.TrimSpace(job.ModelSet)
	if modelSet == "" {
		modelSet = deterministicValue
	}

	start := time.Now()
	var res ai.RenderSummaryResultV1
	var usage models.AIUsage
	var errs []models.AIError

	jobID := strings.TrimSpace(job.ID)
	deterministicFallback := func() {
		res = ai.RenderSummaryDeterministicV1(in)
		usage = models.AIUsage{
			Provider:   deterministicValue,
			Model:      deterministicValue,
			ToolCalls:  0,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	type batchFn func(context.Context, string, string, []llm.RenderSummaryBatchItem) (map[string]ai.RenderSummaryResultV1, models.AIUsage, error)
	var keyFn func(context.Context) (string, error)
	var callFn batchFn

	lowerModelSet := strings.ToLower(modelSet)
	switch {
	case strings.HasPrefix(lowerModelSet, "openai:"):
		keyFn = openAIAPIKey
		callFn = llm.RenderSummaryBatchOpenAI
	case strings.HasPrefix(lowerModelSet, "anthropic:"):
		keyFn = anthropicAPIKey
		callFn = llm.RenderSummaryBatchAnthropic
	default:
		deterministicFallback()
	}

	if callFn != nil {
		apiKey, keyErr := keyFn(ctx)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			errs = append(errs, models.AIError{Code: aiErrorCodeLLMUnavailable, Message: "LLM unavailable; used deterministic fallback", Retryable: false})
			deterministicFallback()
		} else {
			out, u, err := callFn(ctx, apiKey, modelSet, []llm.RenderSummaryBatchItem{
				{ItemID: jobID, Input: in},
			})
			if err != nil {
				errs = append(errs, models.AIError{Code: aiErrorCodeLLMFailed, Message: "LLM call failed; used deterministic fallback", Retryable: false})
				deterministicFallback()
			} else if item, ok := out[jobID]; ok && strings.TrimSpace(item.ShortSummary) != "" {
				res = item
				usage = u
			} else {
				errs = append(errs, models.AIError{Code: aiErrorCodeLLMMissingOutput, Message: "LLM output missing; used deterministic fallback", Retryable: false})
				deterministicFallback()
			}
		}
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}

	return string(b), usage, errs, nil
}

func (s *Server) runModerationTextLLMV1(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError, error) {
	if job == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("job is nil")
	}

	var in ai.ModerationTextInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", models.AIUsage{}, nil, nil // drop invalid
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return "", models.AIUsage{}, nil, nil
	}
	if len([]byte(text)) > 10_000 {
		b := []byte(text)
		text = string(b[:10_000])
		in.Text = text
	}

	modelSet := strings.TrimSpace(job.ModelSet)
	if modelSet == "" {
		modelSet = deterministicValue
	}

	start := time.Now()
	var errs []models.AIError

	evidenceJSON, evidenceUsage, evidenceErrs := s.moderationTextEvidence(ctx, job)
	errs = append(errs, evidenceErrs...)

	var res ai.ModerationResultV1
	var usage models.AIUsage

	jobID := strings.TrimSpace(job.ID)
	res, usage, llmErrs := s.callModerationLLM(
		ctx,
		modelSet,
		jobID,
		func(ctx context.Context, apiKey string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
			return llm.ModerationTextBatchOpenAI(ctx, apiKey, modelSet, []llm.ModerationTextBatchItem{
				{ItemID: jobID, Input: in, Evidence: json.RawMessage(evidenceJSON)},
			})
		},
		func(ctx context.Context, apiKey string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
			return llm.ModerationTextBatchAnthropic(ctx, apiKey, modelSet, []llm.ModerationTextBatchItem{
				{ItemID: jobID, Input: in, Evidence: json.RawMessage(evidenceJSON)},
			})
		},
	)
	errs = append(errs, llmErrs...)
	if strings.TrimSpace(res.Decision) != "" {
		usage.ToolCalls += evidenceUsage.ToolCalls
		usage.DurationMs = time.Since(start).Milliseconds()
	}

	if strings.TrimSpace(res.Decision) == "" {
		res = ai.ModerationTextDeterministicV1(text)
		res = bumpModerationTextWithPII(res, evidenceJSON)

		usage = evidenceUsage
		if strings.TrimSpace(usage.Provider) == "" {
			usage = models.AIUsage{Provider: deterministicValue, Model: deterministicValue}
		}
		usage.DurationMs = time.Since(start).Milliseconds()
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}
	return string(b), usage, errs, nil
}

func (s *Server) moderationTextEvidence(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError) {
	if s == nil {
		return "", models.AIUsage{}, nil
	}

	evidenceJSON, evidenceUsage, _, evidenceErr := s.runComprehendTextEvidenceV1(ctx, job)
	if evidenceErr != nil {
		return "", models.AIUsage{}, []models.AIError{{
			Code:      "tool_failed",
			Message:   "Tool evidence failed; continuing with limited signals",
			Retryable: false,
		}}
	}

	return evidenceJSON, evidenceUsage, nil
}

func bumpModerationTextWithPII(res ai.ModerationResultV1, evidenceJSON string) ai.ModerationResultV1 {
	// Evidence-driven bump: if Comprehend PII signals exist, force at least review + category.
	var ev comprehendTextEvidenceV1
	if strings.TrimSpace(evidenceJSON) == "" || json.Unmarshal([]byte(evidenceJSON), &ev) != nil {
		return res
	}
	if len(ev.PIIEntities) == 0 {
		return res
	}

	if strings.TrimSpace(res.Decision) == "" || res.Decision == "allow" {
		res.Decision = "review"
	}

	for _, c := range res.Categories {
		if strings.TrimSpace(c.Code) == "pii" {
			return res
		}
	}

	res.Categories = append(res.Categories, ai.ModerationCategoryV1{
		Code:       "pii",
		Confidence: 0.8,
		Severity:   "medium",
		Summary:    "Tooling detected potential PII in the text.",
	})
	return res
}

func (s *Server) runModerationImageLLMV1(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError, error) {
	if job == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("job is nil")
	}

	var in ai.ModerationImageInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", models.AIUsage{}, nil, nil // drop invalid
	}

	key := strings.TrimSpace(in.ObjectKey)
	if key == "" {
		return "", models.AIUsage{}, nil, nil
	}

	modelSet := strings.TrimSpace(job.ModelSet)
	if modelSet == "" {
		modelSet = deterministicValue
	}

	start := time.Now()
	var errs []models.AIError

	evidenceJSON, evidenceUsage, _, evidenceErr := s.runRekognitionImageEvidenceV1(ctx, job)
	if evidenceErr != nil {
		errs = append(errs, models.AIError{Code: "tool_failed", Message: "Tool evidence failed; continuing with limited signals", Retryable: false})
		evidenceJSON = ""
		evidenceUsage = models.AIUsage{}
	}

	var res ai.ModerationResultV1
	var usage models.AIUsage

	jobID := strings.TrimSpace(job.ID)
	res, usage, llmErrs := s.callModerationLLM(
		ctx,
		modelSet,
		jobID,
		func(ctx context.Context, apiKey string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
			return llm.ModerationImageBatchOpenAI(ctx, apiKey, modelSet, []llm.ModerationImageBatchItem{
				{ItemID: jobID, Input: in, Evidence: json.RawMessage(evidenceJSON)},
			})
		},
		func(ctx context.Context, apiKey string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
			return llm.ModerationImageBatchAnthropic(ctx, apiKey, modelSet, []llm.ModerationImageBatchItem{
				{ItemID: jobID, Input: in, Evidence: json.RawMessage(evidenceJSON)},
			})
		},
	)
	errs = append(errs, llmErrs...)
	if strings.TrimSpace(res.Decision) != "" {
		usage.ToolCalls += evidenceUsage.ToolCalls
		usage.DurationMs = time.Since(start).Milliseconds()
	}

	if strings.TrimSpace(res.Decision) == "" {
		var ev rekognitionImageEvidenceV1
		if strings.TrimSpace(evidenceJSON) != "" {
			_ = json.Unmarshal([]byte(evidenceJSON), &ev)
		}
		res = moderationImageDeterministicFromRekognition(ev)
		usage = evidenceUsage
		if strings.TrimSpace(usage.Provider) == "" {
			usage = models.AIUsage{Provider: deterministicValue, Model: deterministicValue}
		}
		usage.DurationMs = time.Since(start).Milliseconds()
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}
	return string(b), usage, errs, nil
}

func (s *Server) callModerationLLM(
	ctx context.Context,
	modelSet string,
	jobID string,
	callOpenAI func(context.Context, string) (map[string]ai.ModerationResultV1, models.AIUsage, error),
	callAnthropic func(context.Context, string) (map[string]ai.ModerationResultV1, models.AIUsage, error),
) (ai.ModerationResultV1, models.AIUsage, []models.AIError) {
	modelSetLower := strings.ToLower(strings.TrimSpace(modelSet))
	if !strings.HasPrefix(modelSetLower, "openai:") && !strings.HasPrefix(modelSetLower, "anthropic:") {
		return ai.ModerationResultV1{}, models.AIUsage{}, nil
	}

	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ai.ModerationResultV1{}, models.AIUsage{}, []models.AIError{{
			Code:      "invalid_inputs",
			Message:   "job id is required",
			Retryable: false,
		}}
	}

	call := callOpenAI
	keyFn := openAIAPIKey
	if strings.HasPrefix(modelSetLower, "anthropic:") {
		call = callAnthropic
		keyFn = anthropicAPIKey
	}

	if call == nil {
		return ai.ModerationResultV1{}, models.AIUsage{}, []models.AIError{{
			Code:      "internal_error",
			Message:   "moderation LLM call is not configured",
			Retryable: false,
		}}
	}

	apiKey, keyErr := keyFn(ctx)
	if keyErr != nil || strings.TrimSpace(apiKey) == "" {
		return ai.ModerationResultV1{}, models.AIUsage{}, []models.AIError{{
			Code:      aiErrorCodeLLMUnavailable,
			Message:   "LLM unavailable; used deterministic fallback",
			Retryable: false,
		}}
	}

	out, usage, err := call(ctx, apiKey)
	if err != nil {
		return ai.ModerationResultV1{}, models.AIUsage{}, []models.AIError{{
			Code:      aiErrorCodeLLMFailed,
			Message:   "LLM call failed; used deterministic fallback",
			Retryable: false,
		}}
	}

	item, ok := out[jobID]
	if !ok || strings.TrimSpace(item.Decision) == "" {
		return ai.ModerationResultV1{}, models.AIUsage{}, []models.AIError{{
			Code:      aiErrorCodeLLMMissingOutput,
			Message:   "LLM output missing; used deterministic fallback",
			Retryable: false,
		}}
	}

	return item, usage, nil
}

func (s *Server) runClaimVerifyLLMV1(ctx context.Context, job *models.AIJob) (string, models.AIUsage, []models.AIError, error) {
	if job == nil {
		return "", models.AIUsage{}, nil, fmt.Errorf("job is nil")
	}

	var in ai.ClaimVerifyInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", models.AIUsage{}, nil, nil // drop invalid
	}

	modelSet := strings.TrimSpace(job.ModelSet)
	if modelSet == "" {
		modelSet = deterministicValue
	}

	start := time.Now()
	hydrationErrs := s.hydrateClaimVerifyEvidenceFromRenders(ctx, &in)
	in.Retrieval = normalizeClaimVerifyRetrievalV1(in.Retrieval)
	retrievalUsed, retrievalDisclaimer, retrievalUsage, retrievalErrs := s.maybeAddClaimVerifyWebSearchEvidence(ctx, modelSet, &in)

	evidenceIDs, evidenceText := claimVerifyEvidenceMaps(in.Evidence)
	if len(evidenceIDs) == 0 || len(evidenceText) == 0 {
		out, usage, errs, err := claimVerifyMissingEvidenceResponse(in, start)
		errs = append(hydrationErrs, errs...)
		errs = append(retrievalErrs, errs...)
		return out, usage, errs, err
	}

	var res ai.ClaimVerifyResultV1
	var usage models.AIUsage

	res, usage, errs := s.claimVerifyWithLLM(ctx, modelSet, strings.TrimSpace(job.ID), in, start)
	errs = append(retrievalErrs, errs...)
	errs = append(hydrationErrs, errs...)

	if len(res.Claims) == 0 {
		res = ai.ClaimVerifyDeterministicV1(in)
		if strings.TrimSpace(usage.Provider) == "" {
			usage = models.AIUsage{
				Provider:   deterministicValue,
				Model:      deterministicValue,
				ToolCalls:  0,
				DurationMs: time.Since(start).Milliseconds(),
			}
		}
	}

	res.Sources = trimClaimVerifySourcesForOutput(in.Evidence)
	usage = applyClaimVerifyRetrievalEffects(&res, usage, start, retrievalUsed, retrievalDisclaimer, retrievalUsage)

	res.Claims = sanitizeClaimVerifyClaims(res.Claims, evidenceIDs, evidenceText)

	errs = append(errs, s.issueClaimVerifyAttestationV1(ctx, job, in, res)...)

	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}
	return string(b), usage, errs, nil
}

func applyClaimVerifyRetrievalEffects(
	res *ai.ClaimVerifyResultV1,
	usage models.AIUsage,
	start time.Time,
	used bool,
	disclaimer string,
	retrievalUsage models.AIUsage,
) models.AIUsage {
	if res == nil || !used {
		return usage
	}

	res.Disclaimer = strings.TrimSpace(disclaimer)
	res.Warnings = append(res.Warnings, "web_search_used")
	return mergeAIUsage(usage, retrievalUsage, start)
}

func (s *Server) maybeAddClaimVerifyWebSearchEvidence(
	ctx context.Context,
	modelSet string,
	in *ai.ClaimVerifyInputsV1,
) (used bool, disclaimer string, usage models.AIUsage, errs []models.AIError) {
	if in == nil || in.Retrieval == nil {
		return false, "", models.AIUsage{}, nil
	}
	if !strings.EqualFold(strings.TrimSpace(in.Retrieval.Mode), ai.ClaimVerifyRetrievalModeOpenAIWebSearch) {
		return false, "", models.AIUsage{}, nil
	}

	maxSources := in.Retrieval.MaxSources
	if maxSources <= 0 {
		maxSources = 3
	}
	if maxSources > claimVerifyMaxEvidenceItems {
		maxSources = claimVerifyMaxEvidenceItems
	}

	remaining := claimVerifyMaxEvidenceItems - len(in.Evidence)
	if remaining <= 0 {
		return false, "", models.AIUsage{}, []models.AIError{{Code: "retrieval_skipped", Message: "Evidence limit reached; skipping web search retrieval", Retryable: false}}
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelSet)), "openai:") {
		return false, "", models.AIUsage{}, []models.AIError{{Code: "retrieval_unsupported", Message: "Web search retrieval requires an openai:* model set; continuing without retrieval", Retryable: false}}
	}

	if maxSources > remaining {
		maxSources = remaining
	}

	apiKey, keyErr := openAIAPIKey(ctx)
	if keyErr != nil || strings.TrimSpace(apiKey) == "" {
		return false, "", models.AIUsage{}, []models.AIError{{Code: aiErrorCodeLLMUnavailable, Message: "LLM unavailable; skipped web search retrieval", Retryable: false}}
	}

	ev, disc, u, evErr := llm.ClaimVerifyWebSearchEvidenceOpenAI(ctx, apiKey, modelSet, in.Claims, in.Text, maxSources, strings.TrimSpace(in.Retrieval.SearchContextSize))
	if evErr != nil {
		return false, "", models.AIUsage{}, []models.AIError{{Code: "retrieval_failed", Message: "Web search retrieval failed; continuing with provided evidence", Retryable: true}}
	}
	if len(ev) == 0 {
		return false, "", models.AIUsage{}, nil
	}

	in.Evidence = append(in.Evidence, uniquifyClaimVerifyEvidenceIDs(in.Evidence, ev)...)
	return true, strings.TrimSpace(disc), u, nil
}

func (s *Server) hydrateClaimVerifyEvidenceFromRenders(ctx context.Context, in *ai.ClaimVerifyInputsV1) []models.AIError {
	if s == nil || s.artifacts == nil || in == nil {
		return nil
	}

	errs := []models.AIError{}

	for i := range in.Evidence {
		if strings.TrimSpace(in.Evidence[i].Text) != "" {
			continue
		}

		renderID := strings.TrimSpace(in.Evidence[i].RenderID)
		if renderID == "" {
			continue
		}
		if !hex64RE.MatchString(renderID) {
			errs = append(errs, models.AIError{Code: "invalid_inputs", Message: "Invalid evidence render_id", Retryable: false})
			continue
		}

		key := rendering.SnapshotObjectKey(renderID)
		body, _, _, err := s.artifacts.GetObject(ctx, key, claimVerifyEvidenceMaxBytes)
		if err != nil {
			errs = append(errs, models.AIError{Code: "evidence_unavailable", Message: "Evidence snapshot unavailable; continuing with limited evidence", Retryable: true})
			continue
		}

		text := strings.TrimSpace(string(body))
		if text == "" {
			continue
		}
		if int64(len([]byte(text))) > claimVerifyEvidenceMaxBytes {
			text = strings.TrimSpace(string([]byte(text)[:claimVerifyEvidenceMaxBytes]))
		}
		in.Evidence[i].Text = text
	}

	return errs
}

func normalizeClaimVerifyRetrievalV1(in *ai.ClaimVerifyRetrievalV1) *ai.ClaimVerifyRetrievalV1 {
	if in == nil {
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	switch mode {
	case "":
		mode = ai.ClaimVerifyRetrievalModeProvidedOnly
	case ai.ClaimVerifyRetrievalModeProvidedOnly, ai.ClaimVerifyRetrievalModeOpenAIWebSearch:
		// ok
	default:
		mode = ai.ClaimVerifyRetrievalModeProvidedOnly
	}

	maxSources := in.MaxSources
	if maxSources < 0 {
		maxSources = 0
	}
	if maxSources > claimVerifyMaxEvidenceItems {
		maxSources = claimVerifyMaxEvidenceItems
	}

	ctxSize := strings.ToLower(strings.TrimSpace(in.SearchContextSize))
	switch ctxSize {
	case "", ai.ClaimVerifySearchContextLow, ai.ClaimVerifySearchContextMedium, ai.ClaimVerifySearchContextHigh:
		// ok
	default:
		ctxSize = ""
	}

	return &ai.ClaimVerifyRetrievalV1{
		Mode:              mode,
		MaxSources:        maxSources,
		SearchContextSize: ctxSize,
	}
}

func uniquifyClaimVerifyEvidenceIDs(existing []ai.ClaimVerifyEvidenceV1, add []ai.ClaimVerifyEvidenceV1) []ai.ClaimVerifyEvidenceV1 {
	seen := map[string]struct{}{}
	for _, e := range existing {
		id := strings.TrimSpace(e.SourceID)
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
	}

	out := make([]ai.ClaimVerifyEvidenceV1, 0, len(add))
	next := 1
	for _, e := range add {
		if len(out) >= claimVerifyMaxEvidenceItems {
			break
		}

		id := strings.TrimSpace(e.SourceID)
		if id == "" {
			id = fmt.Sprintf("web_%d", next)
		}
		for {
			if _, ok := seen[id]; !ok {
				break
			}
			next++
			id = fmt.Sprintf("web_%d", next)
		}
		next++

		e.SourceID = id
		seen[id] = struct{}{}
		out = append(out, e)
	}

	return out
}

func trimClaimVerifySourcesForOutput(in []ai.ClaimVerifyEvidenceV1) []ai.ClaimVerifyEvidenceV1 {
	out := make([]ai.ClaimVerifyEvidenceV1, 0, len(in))
	for _, e := range in {
		e.SourceID = strings.TrimSpace(e.SourceID)
		e.URL = strings.TrimSpace(e.URL)
		e.Title = strings.TrimSpace(e.Title)
		e.RenderID = strings.TrimSpace(e.RenderID)
		e.Text = strings.TrimSpace(e.Text)
		if e.SourceID == "" || e.Text == "" {
			continue
		}
		if int64(len([]byte(e.Text))) > claimVerifyEvidenceMaxBytes {
			e.Text = strings.TrimSpace(string([]byte(e.Text)[:claimVerifyEvidenceMaxBytes]))
		}
		out = append(out, e)
		if len(out) >= claimVerifyMaxEvidenceItems {
			break
		}
	}
	return out
}

func mergeAIUsage(primary models.AIUsage, extra models.AIUsage, start time.Time) models.AIUsage {
	primary.DurationMs = time.Since(start).Milliseconds()
	if strings.TrimSpace(extra.Provider) == "" {
		return primary
	}

	if strings.TrimSpace(primary.Provider) == "" || strings.EqualFold(primary.Provider, deterministicValue) {
		primary.Provider = strings.TrimSpace(extra.Provider)
		if strings.TrimSpace(primary.Model) == "" {
			primary.Model = strings.TrimSpace(extra.Model)
		}
	}

	if strings.EqualFold(strings.TrimSpace(primary.Provider), strings.TrimSpace(extra.Provider)) {
		primary.InputTokens += extra.InputTokens
		primary.OutputTokens += extra.OutputTokens
		primary.TotalTokens += extra.TotalTokens
		primary.ToolCalls += extra.ToolCalls
	}
	return primary
}

type claimVerifyAttestationEvidenceSourceV1 struct {
	SourceID string `json:"source_id"`
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	RenderID string `json:"render_id,omitempty"`

	TextSHA256 string `json:"text_sha256,omitempty"`
}

type claimVerifyAttestationEvidenceV1 struct {
	Sources       []claimVerifyAttestationEvidenceSourceV1 `json:"sources,omitempty"`
	RetrievalMode string                                   `json:"retrieval_mode,omitempty"`
	Disclaimer    string                                   `json:"disclaimer,omitempty"`
}

func (s *Server) issueClaimVerifyAttestationV1(ctx context.Context, job *models.AIJob, in ai.ClaimVerifyInputsV1, res ai.ClaimVerifyResultV1) []models.AIError {
	if s == nil || s.attest == nil || !s.attest.Enabled() || job == nil {
		return nil
	}

	actorURI, objectURI, contentHash, ok := claimVerifyAttestationSubject(in)
	if !ok {
		return nil
	}

	st, err := s.attestationStoreForAIJob()
	if err != nil || st == nil {
		return []models.AIError{{Code: "attestation_unavailable", Message: "Attestation store unavailable", Retryable: true}}
	}

	module, policyVersion, ok := aiJobModulePolicy(job)
	if !ok {
		return nil
	}

	id := attestations.AttestationID(actorURI, objectURI, contentHash, module, policyVersion)

	exists, errs := s.attestationAlreadyExists(ctx, st, id)
	if len(errs) > 0 {
		return errs
	}
	if exists {
		return nil
	}

	now := time.Now().UTC()
	expiresAt := now.Add(30 * 24 * time.Hour)

	// Do not include full evidence texts in the public attestation.
	evidenceRefs := claimVerifyAttestationEvidenceRefs(in.Evidence)
	retrievalMode := claimVerifyAttestationRetrievalMode(in.Retrieval)
	resForAttest := claimVerifyResultForAttestation(res)

	payload := attestations.PayloadV1{
		Type: attestations.PayloadTypeV1,

		ActorURI:    actorURI,
		ObjectURI:   objectURI,
		ContentHash: contentHash,

		Module:        module,
		PolicyVersion: policyVersion,
		ModelSet:      strings.TrimSpace(job.ModelSet),

		CreatedAt: now,
		ExpiresAt: expiresAt,

		Evidence: claimVerifyAttestationEvidenceV1{
			Sources:       evidenceRefs,
			RetrievalMode: retrievalMode,
			Disclaimer:    strings.TrimSpace(res.Disclaimer),
		},
		Result: resForAttest,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return []models.AIError{{Code: "attestation_failed", Message: "Failed to encode attestation payload", Retryable: false}}
	}

	jws, _, err := s.attest.SignPayloadJWS(ctx, payloadBytes)
	if err != nil {
		return []models.AIError{{Code: "attestation_failed", Message: "Failed to sign attestation", Retryable: true}}
	}

	item := &models.Attestation{
		ID:          id,
		ActorURI:    actorURI,
		ObjectURI:   objectURI,
		ContentHash: contentHash,

		Module:        module,
		PolicyVersion: policyVersion,
		ModelSet:      strings.TrimSpace(job.ModelSet),
		JWS:           jws,

		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	_ = item.UpdateKeys()

	err = st.DB.WithContext(ctx).Model(item).Create()
	if theoryErrors.IsConditionFailed(err) {
		return nil
	}
	if err != nil {
		return []models.AIError{{Code: "attestation_failed", Message: "Failed to write attestation", Retryable: true}}
	}
	return nil
}

func claimVerifyAttestationSubject(in ai.ClaimVerifyInputsV1) (string, string, string, bool) {
	actorURI := strings.TrimSpace(in.ActorURI)
	objectURI := strings.TrimSpace(in.ObjectURI)
	contentHash := strings.TrimSpace(in.ContentHash)
	if actorURI == "" || objectURI == "" || contentHash == "" {
		return "", "", "", false
	}
	return actorURI, objectURI, contentHash, true
}

func (s *Server) attestationStoreForAIJob() (*store.Store, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	st, ok := s.store.(*store.Store)
	if !ok || st == nil || st.DB == nil {
		return nil, fmt.Errorf("attestation store unavailable")
	}
	return st, nil
}

func aiJobModulePolicy(job *models.AIJob) (string, string, bool) {
	if job == nil {
		return "", "", false
	}
	module := strings.ToLower(strings.TrimSpace(job.Module))
	policyVersion := strings.TrimSpace(job.PolicyVersion)
	if module == "" || policyVersion == "" {
		return "", "", false
	}
	return module, policyVersion, true
}

func (s *Server) attestationAlreadyExists(ctx context.Context, st *store.Store, id string) (bool, []models.AIError) {
	if s == nil || st == nil || st.DB == nil {
		return false, []models.AIError{{Code: "attestation_unavailable", Message: "Attestation store unavailable", Retryable: true}}
	}

	existing, err := st.GetAttestation(ctx, id)
	if err == nil && existing != nil {
		return true, nil
	}
	if err != nil && !store.IsNotFound(err) {
		return false, []models.AIError{{Code: "attestation_failed", Message: "Failed to check existing attestation", Retryable: true}}
	}

	return false, nil
}

func claimVerifyAttestationEvidenceRefs(in []ai.ClaimVerifyEvidenceV1) []claimVerifyAttestationEvidenceSourceV1 {
	out := make([]claimVerifyAttestationEvidenceSourceV1, 0, len(in))
	for _, e := range trimClaimVerifySourcesForOutput(in) {
		txt := strings.TrimSpace(e.Text)
		sum := sha256.Sum256([]byte(txt))
		out = append(out, claimVerifyAttestationEvidenceSourceV1{
			SourceID:   strings.TrimSpace(e.SourceID),
			URL:        strings.TrimSpace(e.URL),
			Title:      strings.TrimSpace(e.Title),
			RenderID:   strings.TrimSpace(e.RenderID),
			TextSHA256: hex.EncodeToString(sum[:]),
		})
	}
	return out
}

func claimVerifyAttestationRetrievalMode(in *ai.ClaimVerifyRetrievalV1) string {
	retrievalMode := ai.ClaimVerifyRetrievalModeProvidedOnly
	if in == nil {
		return retrievalMode
	}
	if mode := strings.TrimSpace(in.Mode); mode != "" {
		retrievalMode = mode
	}
	return retrievalMode
}

func claimVerifyResultForAttestation(res ai.ClaimVerifyResultV1) ai.ClaimVerifyResultV1 {
	res.Sources = nil
	return res
}

func claimVerifyEvidenceMaps(in []ai.ClaimVerifyEvidenceV1) (map[string]struct{}, map[string]string) {
	ids := make(map[string]struct{}, len(in))
	text := make(map[string]string, len(in))
	for _, e := range in {
		id := strings.TrimSpace(e.SourceID)
		if id == "" {
			continue
		}
		ids[id] = struct{}{}
		if t := strings.TrimSpace(e.Text); t != "" {
			text[id] = t
		}
	}
	return ids, text
}

func claimVerifyMissingEvidenceResponse(in ai.ClaimVerifyInputsV1, start time.Time) (string, models.AIUsage, []models.AIError, error) {
	res := ai.ClaimVerifyDeterministicV1(in)
	res.Warnings = append(res.Warnings, "missing_evidence")
	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}
	return string(b),
		models.AIUsage{Provider: deterministicValue, Model: deterministicValue, DurationMs: time.Since(start).Milliseconds()},
		[]models.AIError{{Code: "invalid_inputs", Message: "Evidence is required", Retryable: false}},
		nil
}

func (s *Server) claimVerifyWithLLM(
	ctx context.Context,
	modelSet string,
	jobID string,
	in ai.ClaimVerifyInputsV1,
	start time.Time,
) (ai.ClaimVerifyResultV1, models.AIUsage, []models.AIError) {
	var res ai.ClaimVerifyResultV1
	var usage models.AIUsage
	var errs []models.AIError

	modelSetLower := strings.ToLower(strings.TrimSpace(modelSet))

	keyFn := openAIAPIKey
	callFn := llm.ClaimVerifyBatchOpenAI
	switch {
	case strings.HasPrefix(modelSetLower, "openai:"):
		// ok
	case strings.HasPrefix(modelSetLower, "anthropic:"):
		keyFn = anthropicAPIKey
		callFn = llm.ClaimVerifyBatchAnthropic
	default:
		return res, usage, errs
	}

	apiKey, keyErr := keyFn(ctx)
	if keyErr != nil || strings.TrimSpace(apiKey) == "" {
		errs = append(errs, models.AIError{Code: aiErrorCodeLLMUnavailable, Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		return res, usage, errs
	}

	out, u, err := callFn(ctx, apiKey, modelSet, []llm.ClaimVerifyBatchItem{
		{ItemID: strings.TrimSpace(jobID), Input: in},
	})
	if err != nil {
		errs = append(errs, models.AIError{Code: aiErrorCodeLLMFailed, Message: "LLM call failed; used deterministic fallback", Retryable: false})
		return res, usage, errs
	}

	item, ok := out[strings.TrimSpace(jobID)]
	if !ok {
		errs = append(errs, models.AIError{Code: aiErrorCodeLLMMissingOutput, Message: "LLM output missing; used deterministic fallback", Retryable: false})
		return res, usage, errs
	}

	res = item
	usage = u
	usage.DurationMs = time.Since(start).Milliseconds()
	return res, usage, errs
}

func sanitizeClaimVerifyClaims(in []ai.ClaimVerifyClaimV1, evidenceIDs map[string]struct{}, evidenceText map[string]string) []ai.ClaimVerifyClaimV1 {
	out := make([]ai.ClaimVerifyClaimV1, 0, len(in))
	for _, c := range in {
		c = sanitizeClaimVerifyClaim(c, evidenceIDs, evidenceText)
		if c.ClaimID == "" || c.Text == "" {
			continue
		}
		out = append(out, c)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func sanitizeClaimVerifyClaim(c ai.ClaimVerifyClaimV1, evidenceIDs map[string]struct{}, evidenceText map[string]string) ai.ClaimVerifyClaimV1 {
	c.ClaimID = strings.TrimSpace(c.ClaimID)
	c.Text = strings.TrimSpace(c.Text)
	if len(c.Text) > 240 {
		c.Text = strings.TrimSpace(c.Text[:240])
	}

	c.Classification = strings.ToLower(strings.TrimSpace(c.Classification))
	switch c.Classification {
	case "checkable", "opinion", "unclear":
	default:
		c.Classification = "unclear"
	}

	c.Verdict = strings.ToLower(strings.TrimSpace(c.Verdict))
	switch c.Verdict {
	case "supported", "refuted", claimVerdictInconclusive:
	default:
		c.Verdict = claimVerdictInconclusive
	}

	if c.Confidence < 0 {
		c.Confidence = 0
	}
	if c.Confidence > 1 {
		c.Confidence = 1
	}

	c.Reason = strings.TrimSpace(c.Reason)
	if len(c.Reason) > 240 {
		c.Reason = strings.TrimSpace(c.Reason[:240])
	}

	c.Citations = sanitizeClaimVerifyCitations(c.Citations, evidenceIDs, evidenceText)

	if (c.Verdict == "supported" || c.Verdict == "refuted") && len(c.Citations) == 0 {
		c.Verdict = claimVerdictInconclusive
		if c.Reason == "" {
			c.Reason = "missing_citations"
		} else {
			c.Reason = c.Reason + " (missing citations)"
		}
		c.Confidence = 0
	}

	return c
}

func sanitizeClaimVerifyCitations(in []ai.ClaimVerifyCitationV1, evidenceIDs map[string]struct{}, evidenceText map[string]string) []ai.ClaimVerifyCitationV1 {
	out := make([]ai.ClaimVerifyCitationV1, 0, len(in))
	for _, cit := range in {
		cit.SourceID = strings.TrimSpace(cit.SourceID)
		cit.Quote = strings.TrimSpace(cit.Quote)
		if cit.SourceID == "" || cit.Quote == "" {
			continue
		}
		if _, ok := evidenceIDs[cit.SourceID]; !ok {
			continue
		}
		evText := strings.TrimSpace(evidenceText[cit.SourceID])
		if evText == "" {
			continue
		}
		if len(cit.Quote) > 200 {
			cit.Quote = strings.TrimSpace(cit.Quote[:200])
		}
		if !claimVerifyQuoteMatchesEvidence(evText, cit.Quote) {
			continue
		}
		out = append(out, cit)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func claimVerifyQuoteMatchesEvidence(evidenceText string, quote string) bool {
	evidenceText = strings.TrimSpace(evidenceText)
	quote = strings.TrimSpace(quote)
	if evidenceText == "" || quote == "" {
		return false
	}

	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.Join(strings.Fields(s), " ")
		return s
	}

	nEvidence := norm(evidenceText)
	nQuote := norm(quote)
	if len(nQuote) < 8 {
		return false
	}
	return strings.Contains(nEvidence, nQuote)
}

func moderationCategoryCodeFromRekognitionLabel(lowerNameAndParent string) string {
	switch {
	case strings.Contains(lowerNameAndParent, "nudity") ||
		strings.Contains(lowerNameAndParent, "explicit") ||
		strings.Contains(lowerNameAndParent, "sexual"):
		return "nudity"
	case strings.Contains(lowerNameAndParent, "violence") ||
		strings.Contains(lowerNameAndParent, "weapon") ||
		strings.Contains(lowerNameAndParent, "blood"):
		return "violence"
	default:
		return "other"
	}
}

func normalizeConfidence01(confPct float64) float64 {
	conf := confPct / 100
	if conf < 0 {
		return 0
	}
	if conf > 1 {
		return 1
	}
	return conf
}

func moderationSeverityFromConfidence(conf float64) string {
	if conf >= 0.9 {
		return "high"
	}
	return "medium"
}

func moderationCategoriesFromRekognitionLabels(labels []rekognitionLabel) ([]ai.ModerationCategoryV1, bool) {
	out := make([]ai.ModerationCategoryV1, 0, len(labels))
	block := false

	for _, l := range labels {
		name := strings.TrimSpace(l.Name)
		parent := strings.TrimSpace(l.ParentName)
		if name == "" {
			continue
		}

		lower := strings.ToLower(name + " " + parent)
		code := moderationCategoryCodeFromRekognitionLabel(lower)
		conf := normalizeConfidence01(l.Confidence)

		out = append(out, ai.ModerationCategoryV1{
			Code:       code,
			Confidence: conf,
			Severity:   moderationSeverityFromConfidence(conf),
			Summary:    fmt.Sprintf("Tooling flagged %s (%0.1f%%).", name, l.Confidence),
		})
		if len(out) >= 5 {
			break
		}

		if code == "nudity" && conf >= 0.95 {
			block = true
		}
	}

	return out, block
}

func moderationHighlightsFromRekognitionTextDetections(detections []rekognitionTextDetection) []string {
	out := make([]string, 0, len(detections))
	for _, d := range detections {
		t := strings.TrimSpace(d.Text)
		if t == "" {
			continue
		}
		if len(t) > 160 {
			t = strings.TrimSpace(t[:160])
		}
		out = append(out, t)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func moderationImageDeterministicFromRekognition(ev rekognitionImageEvidenceV1) ai.ModerationResultV1 {
	out := ai.ModerationResultV1{
		Kind:     "moderation_image",
		Version:  "v1",
		Decision: "allow",
	}

	if len(ev.ModerationLabels) == 0 {
		return out
	}

	out.Decision = "review"

	categories, block := moderationCategoriesFromRekognitionLabels(ev.ModerationLabels)
	out.Categories = categories
	if block {
		out.Decision = "block"
	}

	out.Highlights = moderationHighlightsFromRekognitionTextDetections(ev.TextDetections)

	return out
}

func openAIAPIKey(ctx context.Context) (string, error) {
	if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
		return k, nil
	}
	return secrets.OpenAIServiceKey(ctx, nil)
}

func anthropicAPIKey(ctx context.Context) (string, error) {
	if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
		return k, nil
	}
	if k := strings.TrimSpace(os.Getenv("CLAUDE_API_KEY")); k != "" {
		return k, nil
	}
	return secrets.ClaudeAPIKey(ctx, nil)
}

func sqsQueueNameFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func roundScore(v *float32) float64 {
	if v == nil {
		return 0
	}
	return math.Round(float64(*v)*10000) / 10000
}
