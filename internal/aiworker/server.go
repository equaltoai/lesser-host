package aiworker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
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
	"github.com/equaltoai/lesser-host/internal/config"
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
	if strings.TrimSpace(jm.Kind) != "ai_job" {
		return nil
	}
	jobID := strings.TrimSpace(jm.JobID)
	if jobID == "" {
		return nil
	}

	return s.processAIJob(ctx.Context(), ctx.RequestID, jobID)
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
