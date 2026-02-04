package aiworker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
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

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

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

type Server struct {
	cfg config.Config

	store       aiStore
	comprehend  comprehendAPI
	rekognition rekognitionAPI
}

func NewServer(cfg config.Config, st aiStore, comp comprehendAPI, rek rekognitionAPI) *Server {
	return &Server{
		cfg:         cfg,
		store:       st,
		comprehend:  comp,
		rekognition: rek,
	}
}

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

	job, err := s.store.GetAIJob(ctx, jobID)
	if err != nil || job == nil {
		return nil // drop missing
	}

	now := time.Now().UTC()
	if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
		return nil // drop expired
	}
	if strings.TrimSpace(job.Status) != models.AIJobStatusQueued {
		return nil
	}

	// Idempotency: if result already exists, mark job OK and exit.
	if existing, err := s.store.GetAIResult(ctx, jobID); err == nil && existing != nil {
		job.Status = models.AIJobStatusOK
		job.ErrorCode = ""
		job.ErrorMessage = ""
		job.RequestID = strings.TrimSpace(requestID)
		_ = job.UpdateKeys()
		_ = s.store.PutAIJob(ctx, job)
		return nil
	}

	module := strings.ToLower(strings.TrimSpace(job.Module))
	policyVersion := strings.TrimSpace(job.PolicyVersion)

	var resultJSON string
	var usage models.AIUsage
	var errs []models.AIError

	switch module {
	case "evidence_text_comprehend":
		if policyVersion != "v1" {
			return nil
		}
		resultJSON, usage, errs, err = s.runComprehendTextEvidenceV1(ctx, job)
	case "evidence_image_rekognition":
		if policyVersion != "v1" {
			return nil
		}
		resultJSON, usage, errs, err = s.runRekognitionImageEvidenceV1(ctx, job)
	case "render_summary_llm":
		if policyVersion != "v1" {
			return nil
		}
		resultJSON, usage, errs, err = s.runRenderSummaryLLMV1(ctx, job)
	case "moderation_text_llm":
		if policyVersion != "v1" {
			return nil
		}
		resultJSON, usage, errs, err = s.runModerationTextLLMV1(ctx, job)
	case "moderation_image_llm":
		if policyVersion != "v1" {
			return nil
		}
		resultJSON, usage, errs, err = s.runModerationImageLLMV1(ctx, job)
	default:
		return nil
	}
	if err != nil {
		job.Status = models.AIJobStatusError
		job.Attempts++
		job.ErrorCode = "tool_failed"
		job.ErrorMessage = "tool execution failed"
		job.RequestID = strings.TrimSpace(requestID)
		_ = job.UpdateKeys()
		_ = s.store.PutAIJob(ctx, job)
		return err
	}

	res := &models.AIResult{
		ID:            strings.TrimSpace(job.ID),
		InstanceSlug:  strings.TrimSpace(job.InstanceSlug),
		Module:        module,
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

	type parsedJob struct {
		Job   *models.AIJob
		Input ai.RenderSummaryInputsV1
	}

	// Parse + filter jobs; split by model set for grouping rules.
	byModelSet := map[string][]parsedJob{}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		if strings.TrimSpace(job.Status) != models.AIJobStatusQueued {
			continue
		}
		if !job.ExpiresAt.IsZero() && job.ExpiresAt.Before(now) {
			continue
		}

		// Idempotency: skip jobs that already have results.
		if existing, err := s.store.GetAIResult(ctx, strings.TrimSpace(job.ID)); err == nil && existing != nil {
			job.Status = models.AIJobStatusOK
			job.ErrorCode = ""
			job.ErrorMessage = ""
			job.RequestID = strings.TrimSpace(requestID)
			_ = job.UpdateKeys()
			_ = s.store.PutAIJob(ctx, job)
			continue
		}

		var in ai.RenderSummaryInputsV1
		if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
			continue
		}

		ms := strings.TrimSpace(job.ModelSet)
		if ms == "" {
			ms = "deterministic"
		}
		byModelSet[ms] = append(byModelSet[ms], parsedJob{Job: job, Input: in})
	}

	for modelSet, group := range byModelSet {
		if len(group) == 0 {
			continue
		}

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
		if len(items) == 0 {
			continue
		}

		start := time.Now()
		results := map[string]ai.RenderSummaryResultV1{}
		usage := models.AIUsage{}
		commonErrs := []models.AIError{}
		usedDeterministic := false

		if strings.HasPrefix(strings.ToLower(modelSet), "openai:") {
			apiKey, keyErr := openAIAPIKey(ctx)
			if keyErr != nil || strings.TrimSpace(apiKey) == "" {
				usedDeterministic = true
				commonErrs = append(commonErrs, models.AIError{Code: "llm_unavailable", Message: "LLM unavailable; used deterministic fallback", Retryable: false})
			} else {
				outMap, u, err := llm.RenderSummaryBatchOpenAI(ctx, apiKey, modelSet, items)
				if err != nil {
					usedDeterministic = true
					commonErrs = append(commonErrs, models.AIError{Code: "llm_failed", Message: "LLM call failed; used deterministic fallback", Retryable: false})
				} else {
					results = outMap
					usage = u
				}
			}
		} else {
			usedDeterministic = true
		}

		if usedDeterministic {
			for _, it := range items {
				results[it.ItemID] = ai.RenderSummaryDeterministicV1(it.Input)
			}
			usage = models.AIUsage{
				Provider:   "deterministic",
				Model:      "deterministic",
				ToolCalls:  0,
				DurationMs: time.Since(start).Milliseconds(),
			}
		}

		for _, pj := range group {
			job := pj.Job
			if job == nil {
				continue
			}
			id := strings.TrimSpace(job.ID)
			if id == "" {
				continue
			}
			res, ok := results[id]
			itemErrs := append([]models.AIError(nil), commonErrs...)
			if !ok || strings.TrimSpace(res.ShortSummary) == "" {
				res = ai.RenderSummaryDeterministicV1(pj.Input)
				itemErrs = append(itemErrs, models.AIError{Code: "llm_missing_output", Message: "LLM output missing; used deterministic fallback", Retryable: false})
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
				ModelSet:      strings.TrimSpace(job.ModelSet),
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
		}
	}

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

	var in comprehendTextInputsV1
	if err := json.Unmarshal([]byte(job.InputsJSON), &in); err != nil {
		return "", models.AIUsage{}, nil, nil // drop invalid
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return "", models.AIUsage{}, nil, nil
	}
	if len([]byte(text)) > 5000 {
		b := []byte(text)
		text = string(b[:5000])
	}

	start := time.Now()

	out := comprehendTextEvidenceV1{
		Kind:    "comprehend_text",
		Version: "v1",
	}

	// Detect language.
	langOut, err := s.comprehend.DetectDominantLanguage(ctx, &comprehend.DetectDominantLanguageInput{
		Text: aws.String(text),
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_dominant_language_failed")
	} else {
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
	}

	languageCode := strings.TrimSpace(out.DominantLanguage)
	if languageCode == "" {
		languageCode = "en"
	}

	// Entities.
	entOut, err := s.comprehend.DetectEntities(ctx, &comprehend.DetectEntitiesInput{
		Text:         aws.String(text),
		LanguageCode: comprehendtypes.LanguageCode(languageCode),
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_entities_failed")
	} else {
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

	// PII: Comprehend PII is best-effort and not available for all languages.
	if strings.EqualFold(languageCode, "en") {
		piiOut, err := s.comprehend.DetectPiiEntities(ctx, &comprehend.DetectPiiEntitiesInput{
			Text:         aws.String(text),
			LanguageCode: comprehendtypes.LanguageCodeEn,
		})
		if err != nil {
			out.Warnings = append(out.Warnings, "detect_pii_failed")
		} else {
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

	mlOut, err := s.rekognition.DetectModerationLabels(ctx, &rekognition.DetectModerationLabelsInput{
		Image:         img,
		MinConfidence: aws.Float32(60),
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_moderation_failed")
	} else {
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

	txtOut, err := s.rekognition.DetectText(ctx, &rekognition.DetectTextInput{Image: img})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_text_failed")
	} else {
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

	fOut, err := s.rekognition.DetectFaces(ctx, &rekognition.DetectFacesInput{
		Image:      img,
		Attributes: []rekognitiontypes.Attribute{rekognitiontypes.AttributeDefault},
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "detect_faces_failed")
	} else {
		out.FaceCount = len(fOut.FaceDetails)
	}

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
		modelSet = "deterministic"
	}

	start := time.Now()
	var res ai.RenderSummaryResultV1
	var usage models.AIUsage
	var errs []models.AIError

	if strings.HasPrefix(strings.ToLower(modelSet), "openai:") {
		apiKey, keyErr := openAIAPIKey(ctx)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			errs = append(errs, models.AIError{Code: "llm_unavailable", Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		} else {
			out, u, err := llm.RenderSummaryBatchOpenAI(ctx, apiKey, modelSet, []llm.RenderSummaryBatchItem{
				{ItemID: strings.TrimSpace(job.ID), Input: in},
			})
			if err != nil {
				errs = append(errs, models.AIError{Code: "llm_failed", Message: "LLM call failed; used deterministic fallback", Retryable: false})
			} else if item, ok := out[strings.TrimSpace(job.ID)]; ok && strings.TrimSpace(item.ShortSummary) != "" {
				res = item
				usage = u
			} else {
				errs = append(errs, models.AIError{Code: "llm_missing_output", Message: "LLM output missing; used deterministic fallback", Retryable: false})
			}
		}

		if strings.TrimSpace(res.ShortSummary) == "" {
			res = ai.RenderSummaryDeterministicV1(in)
			usage = models.AIUsage{
				Provider:   "deterministic",
				Model:      "deterministic",
				ToolCalls:  0,
				DurationMs: time.Since(start).Milliseconds(),
			}
		}
	} else {
		res = ai.RenderSummaryDeterministicV1(in)
		usage = models.AIUsage{
			Provider:   "deterministic",
			Model:      "deterministic",
			ToolCalls:  0,
			DurationMs: time.Since(start).Milliseconds(),
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
		modelSet = "deterministic"
	}

	start := time.Now()
	var errs []models.AIError

	evidenceJSON, evidenceUsage, _, evidenceErr := s.runComprehendTextEvidenceV1(ctx, job)
	if evidenceErr != nil {
		errs = append(errs, models.AIError{Code: "tool_failed", Message: "Tool evidence failed; continuing with limited signals", Retryable: false})
		evidenceJSON = ""
		evidenceUsage = models.AIUsage{}
	}

	var res ai.ModerationResultV1
	var usage models.AIUsage

	if strings.HasPrefix(strings.ToLower(modelSet), "openai:") {
		apiKey, keyErr := openAIAPIKey(ctx)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			errs = append(errs, models.AIError{Code: "llm_unavailable", Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		} else {
			out, u, err := llm.ModerationTextBatchOpenAI(ctx, apiKey, modelSet, []llm.ModerationTextBatchItem{
				{ItemID: strings.TrimSpace(job.ID), Input: in, Evidence: json.RawMessage(evidenceJSON)},
			})
			if err != nil {
				errs = append(errs, models.AIError{Code: "llm_failed", Message: "LLM call failed; used deterministic fallback", Retryable: false})
			} else if item, ok := out[strings.TrimSpace(job.ID)]; ok && strings.TrimSpace(item.Decision) != "" {
				res = item
				usage = u
				usage.ToolCalls += evidenceUsage.ToolCalls
				usage.DurationMs = time.Since(start).Milliseconds()
			} else {
				errs = append(errs, models.AIError{Code: "llm_missing_output", Message: "LLM output missing; used deterministic fallback", Retryable: false})
			}
		}
	}

	if strings.TrimSpace(res.Decision) == "" {
		res = ai.ModerationTextDeterministicV1(text)

		// Evidence-driven bump: if Comprehend PII signals exist, force at least review + category.
		var ev comprehendTextEvidenceV1
		if strings.TrimSpace(evidenceJSON) != "" && json.Unmarshal([]byte(evidenceJSON), &ev) == nil {
			if len(ev.PIIEntities) > 0 {
				res.Kind = "moderation_text"
				res.Version = "v1"
				if strings.TrimSpace(res.Decision) == "" || res.Decision == "allow" {
					res.Decision = "review"
				}
				hasPII := false
				for _, c := range res.Categories {
					if strings.TrimSpace(c.Code) == "pii" {
						hasPII = true
						break
					}
				}
				if !hasPII {
					res.Categories = append(res.Categories, ai.ModerationCategoryV1{
						Code:       "pii",
						Confidence: 0.8,
						Severity:   "medium",
						Summary:    "Tooling detected potential PII in the text.",
					})
				}
			}
		}

		usage = evidenceUsage
		if strings.TrimSpace(usage.Provider) == "" {
			usage = models.AIUsage{Provider: "deterministic", Model: "deterministic"}
		}
		usage.DurationMs = time.Since(start).Milliseconds()
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}
	return string(b), usage, errs, nil
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
		modelSet = "deterministic"
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

	if strings.HasPrefix(strings.ToLower(modelSet), "openai:") {
		apiKey, keyErr := openAIAPIKey(ctx)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			errs = append(errs, models.AIError{Code: "llm_unavailable", Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		} else {
			out, u, err := llm.ModerationImageBatchOpenAI(ctx, apiKey, modelSet, []llm.ModerationImageBatchItem{
				{ItemID: strings.TrimSpace(job.ID), Input: in, Evidence: json.RawMessage(evidenceJSON)},
			})
			if err != nil {
				errs = append(errs, models.AIError{Code: "llm_failed", Message: "LLM call failed; used deterministic fallback", Retryable: false})
			} else if item, ok := out[strings.TrimSpace(job.ID)]; ok && strings.TrimSpace(item.Decision) != "" {
				res = item
				usage = u
				usage.ToolCalls += evidenceUsage.ToolCalls
				usage.DurationMs = time.Since(start).Milliseconds()
			} else {
				errs = append(errs, models.AIError{Code: "llm_missing_output", Message: "LLM output missing; used deterministic fallback", Retryable: false})
			}
		}
	}

	if strings.TrimSpace(res.Decision) == "" {
		var ev rekognitionImageEvidenceV1
		if strings.TrimSpace(evidenceJSON) != "" {
			_ = json.Unmarshal([]byte(evidenceJSON), &ev)
		}
		res = moderationImageDeterministicFromRekognition(ev)
		usage = evidenceUsage
		if strings.TrimSpace(usage.Provider) == "" {
			usage = models.AIUsage{Provider: "deterministic", Model: "deterministic"}
		}
		usage.DurationMs = time.Since(start).Milliseconds()
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", models.AIUsage{}, nil, err
	}
	return string(b), usage, errs, nil
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
	block := false

	for _, l := range ev.ModerationLabels {
		name := strings.TrimSpace(l.Name)
		parent := strings.TrimSpace(l.ParentName)
		if name == "" {
			continue
		}

		lower := strings.ToLower(name + " " + parent)
		code := "other"
		switch {
		case strings.Contains(lower, "nudity") || strings.Contains(lower, "explicit") || strings.Contains(lower, "sexual"):
			code = "nudity"
		case strings.Contains(lower, "violence") || strings.Contains(lower, "weapon") || strings.Contains(lower, "blood"):
			code = "violence"
		}

		conf := l.Confidence / 100
		if conf < 0 {
			conf = 0
		}
		if conf > 1 {
			conf = 1
		}

		severity := "medium"
		if conf >= 0.9 {
			severity = "high"
		}

		out.Categories = append(out.Categories, ai.ModerationCategoryV1{
			Code:       code,
			Confidence: conf,
			Severity:   severity,
			Summary:    fmt.Sprintf("Tooling flagged %s (%0.1f%%).", name, l.Confidence),
		})
		if len(out.Categories) >= 5 {
			break
		}

		if code == "nudity" && conf >= 0.95 {
			block = true
		}
	}

	if block {
		out.Decision = "block"
	}

	for _, d := range ev.TextDetections {
		t := strings.TrimSpace(d.Text)
		if t == "" {
			continue
		}
		if len(t) > 160 {
			t = strings.TrimSpace(t[:160])
		}
		out.Highlights = append(out.Highlights, t)
		if len(out.Highlights) >= 5 {
			break
		}
	}

	return out
}

func openAIAPIKey(ctx context.Context) (string, error) {
	if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
		return k, nil
	}
	return secrets.OpenAIServiceKey(ctx, nil)
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
