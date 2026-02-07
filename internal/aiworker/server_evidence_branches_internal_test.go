package aiworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
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

type errorComprehend struct{}

func (errorComprehend) DetectDominantLanguage(_ context.Context, _ *comprehend.DetectDominantLanguageInput, _ ...func(*comprehend.Options)) (*comprehend.DetectDominantLanguageOutput, error) {
	return nil, errors.New("boom")
}

func (errorComprehend) DetectEntities(_ context.Context, _ *comprehend.DetectEntitiesInput, _ ...func(*comprehend.Options)) (*comprehend.DetectEntitiesOutput, error) {
	return nil, errors.New("boom")
}

func (errorComprehend) DetectPiiEntities(_ context.Context, _ *comprehend.DetectPiiEntitiesInput, _ ...func(*comprehend.Options)) (*comprehend.DetectPiiEntitiesOutput, error) {
	return nil, errors.New("boom")
}

type manyComprehend struct{}

func (manyComprehend) DetectDominantLanguage(_ context.Context, _ *comprehend.DetectDominantLanguageInput, _ ...func(*comprehend.Options)) (*comprehend.DetectDominantLanguageOutput, error) {
	scoreHi := float32(0.9)
	scoreLo := float32(0.1)
	return &comprehend.DetectDominantLanguageOutput{
		Languages: []comprehendtypes.DominantLanguage{
			{LanguageCode: aws.String(" "), Score: &scoreLo},
			{LanguageCode: aws.String("fr"), Score: &scoreHi},
			{LanguageCode: aws.String("en"), Score: &scoreHi},
		},
	}, nil
}

func (manyComprehend) DetectEntities(_ context.Context, _ *comprehend.DetectEntitiesInput, _ ...func(*comprehend.Options)) (*comprehend.DetectEntitiesOutput, error) {
	score := float32(0.8)
	ents := make([]comprehendtypes.Entity, 0, 60)
	for i := 0; i < 60; i++ {
		txt := fmt.Sprintf("entity-%02d-%s", i, strings.Repeat("x", 100))
		begin := int32(100 - i) // reverse order to force sorting
		end := begin + 10
		ents = append(ents, comprehendtypes.Entity{
			Text:        aws.String(txt),
			Type:        comprehendtypes.EntityTypeOrganization,
			Score:       &score,
			BeginOffset: aws.Int32(begin),
			EndOffset:   aws.Int32(end),
		})
	}
	ents = append(ents, comprehendtypes.Entity{Text: aws.String(" "), Score: &score})
	return &comprehend.DetectEntitiesOutput{Entities: ents}, nil
}

func (manyComprehend) DetectPiiEntities(_ context.Context, _ *comprehend.DetectPiiEntitiesInput, _ ...func(*comprehend.Options)) (*comprehend.DetectPiiEntitiesOutput, error) {
	score := float32(0.7)
	out := make([]comprehendtypes.PiiEntity, 0, 60)
	for i := 0; i < 60; i++ {
		out = append(out, comprehendtypes.PiiEntity{
			Type:        comprehendtypes.PiiEntityTypeName,
			Score:       &score,
			BeginOffset: aws.Int32(int32(i)),
			EndOffset:   aws.Int32(int32(i + 1)),
		})
	}
	return &comprehend.DetectPiiEntitiesOutput{Entities: out}, nil
}

type errorRekognition struct{}

func (errorRekognition) DetectModerationLabels(_ context.Context, _ *rekognition.DetectModerationLabelsInput, _ ...func(*rekognition.Options)) (*rekognition.DetectModerationLabelsOutput, error) {
	return nil, errors.New("boom")
}

func (errorRekognition) DetectText(_ context.Context, _ *rekognition.DetectTextInput, _ ...func(*rekognition.Options)) (*rekognition.DetectTextOutput, error) {
	return nil, errors.New("boom")
}

func (errorRekognition) DetectFaces(_ context.Context, _ *rekognition.DetectFacesInput, _ ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error) {
	return nil, errors.New("boom")
}

type manyRekognition struct{}

func (manyRekognition) DetectModerationLabels(_ context.Context, _ *rekognition.DetectModerationLabelsInput, _ ...func(*rekognition.Options)) (*rekognition.DetectModerationLabelsOutput, error) {
	conf := float32(99)
	labels := make([]rekognitiontypes.ModerationLabel, 0, 60)
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("Label-%02d", 59-i) // reverse for sorting
		labels = append(labels, rekognitiontypes.ModerationLabel{Name: aws.String(name), Confidence: &conf})
	}
	labels = append(labels, rekognitiontypes.ModerationLabel{Name: aws.String(" ")})
	return &rekognition.DetectModerationLabelsOutput{ModerationLabels: labels}, nil
}

func (manyRekognition) DetectText(_ context.Context, _ *rekognition.DetectTextInput, _ ...func(*rekognition.Options)) (*rekognition.DetectTextOutput, error) {
	dets := make([]rekognitiontypes.TextDetection, 0, 60)
	for i := 0; i < 60; i++ {
		conf := float32(i)
		dets = append(dets, rekognitiontypes.TextDetection{
			DetectedText: aws.String(fmt.Sprintf("t%02d", i)),
			Confidence:   &conf,
			Type:         rekognitiontypes.TextTypesLine,
		})
	}
	dets = append(dets, rekognitiontypes.TextDetection{DetectedText: aws.String(" ")})
	return &rekognition.DetectTextOutput{TextDetections: dets}, nil
}

func (manyRekognition) DetectFaces(_ context.Context, _ *rekognition.DetectFacesInput, _ ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error) {
	return &rekognition.DetectFacesOutput{FaceDetails: []rekognitiontypes.FaceDetail{{}, {}}}, nil
}

type failingPutResultStore struct {
	*fakeAIStore
	err error
}

func (f failingPutResultStore) PutAIResult(_ context.Context, _ *models.AIResult) error { return f.err }

func TestParseComprehendTextInputs_TruncatesTo5000Bytes(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{InputsJSON: `{"text":"` + strings.Repeat("a", 6000) + `"}`}
	text, ok := parseComprehendTextInputs(job)
	if !ok {
		t.Fatalf("expected ok")
	}
	if len([]byte(text)) != 5000 {
		t.Fatalf("expected 5000 bytes, got %d", len([]byte(text)))
	}
}

func TestRunComprehendTextEvidenceV1_WarningsOnToolErrors(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{}, &fakeAIStore{}, nil, errorComprehend{}, fakeRekognition{})
	job := &models.AIJob{InputsJSON: `{"text":"hello"}`}
	outJSON, _, _, err := srv.runComprehendTextEvidenceV1(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out comprehendTextEvidenceV1
	if err := json.Unmarshal([]byte(outJSON), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Warnings) < 1 {
		t.Fatalf("expected warnings, got %#v", out.Warnings)
	}
}

func TestRunComprehendTextEvidenceV1_TruncatesEntitiesAndPIIAndSortsLanguage(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{}, &fakeAIStore{}, nil, manyComprehend{}, fakeRekognition{})
	job := &models.AIJob{InputsJSON: `{"text":"hello"}`}
	outJSON, _, _, err := srv.runComprehendTextEvidenceV1(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out comprehendTextEvidenceV1
	if err := json.Unmarshal([]byte(outJSON), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.DominantLanguage != "en" {
		t.Fatalf("expected dominant language en, got %q (%#v)", out.DominantLanguage, out.Language)
	}
	if !out.Truncated {
		t.Fatalf("expected truncated")
	}
	if len(out.Entities) != 50 || len(out.PIIEntities) != 50 {
		t.Fatalf("expected entity/pii truncation to 50, got entities=%d pii=%d", len(out.Entities), len(out.PIIEntities))
	}
	for _, e := range out.Entities {
		if len(e.Text) > 64 {
			t.Fatalf("expected entity text truncated to 64, got %d", len(e.Text))
		}
	}
}

func TestRunRekognitionImageEvidenceV1_WarningsOnToolErrors(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{ArtifactBucketName: "bucket"}, &fakeAIStore{}, nil, fakeComprehend{}, errorRekognition{})
	job := &models.AIJob{InputsJSON: `{"object_key":"k"}`}

	outJSON, _, _, err := srv.runRekognitionImageEvidenceV1(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out rekognitionImageEvidenceV1
	if err := json.Unmarshal([]byte(outJSON), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %#v", out.Warnings)
	}
}

func TestRunRekognitionImageEvidenceV1_TruncatesAndSortsLabels(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{ArtifactBucketName: "bucket"}, &fakeAIStore{}, nil, fakeComprehend{}, manyRekognition{})
	job := &models.AIJob{InputsJSON: `{"object_key":"k"}`}

	outJSON, _, _, err := srv.runRekognitionImageEvidenceV1(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out rekognitionImageEvidenceV1
	if err := json.Unmarshal([]byte(outJSON), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Truncated || len(out.ModerationLabels) != 50 || len(out.TextDetections) != 50 {
		t.Fatalf("expected truncation, got truncated=%v labels=%d text=%d", out.Truncated, len(out.ModerationLabels), len(out.TextDetections))
	}
	if out.FaceCount != 2 {
		t.Fatalf("expected face count 2, got %d", out.FaceCount)
	}
	if len(out.ModerationLabels) > 1 && out.ModerationLabels[0].Name > out.ModerationLabels[1].Name {
		t.Fatalf("expected moderation labels sorted by name, got %q then %q", out.ModerationLabels[0].Name, out.ModerationLabels[1].Name)
	}
}

func TestModerationImageDeterministicFromRekognition_BlocksHighConfidenceNudity(t *testing.T) {
	t.Parallel()

	out := moderationImageDeterministicFromRekognition(rekognitionImageEvidenceV1{
		ModerationLabels: []rekognitionLabel{{Name: "Explicit Nudity", Confidence: 99}},
		TextDetections:   []rekognitionTextDetection{{Text: "hello"}},
	})
	if out.Decision != decisionBlock {
		t.Fatalf("expected block, got %#v", out)
	}
	if len(out.Categories) == 0 || out.Categories[0].Code != "nudity" {
		t.Fatalf("expected nudity category, got %#v", out.Categories)
	}
	if len(out.Highlights) != 1 || out.Highlights[0] != "hello" {
		t.Fatalf("expected highlight, got %#v", out.Highlights)
	}
}

func TestBumpModerationTextWithPII_BumpsAllowToReviewAndAddsCategory(t *testing.T) {
	t.Parallel()

	in := `{"kind":"comprehend_text","version":"v1","pii_entities":[{"type":"NAME","score":0.9}]}`
	got := bumpModerationTextWithPII(
		ai.ModerationResultV1{Kind: "moderation_text", Version: "v1", Decision: "allow"},
		in,
	)
	if got.Decision != "review" {
		t.Fatalf("expected review, got %#v", got)
	}
	found := false
	for _, c := range got.Categories {
		if c.Code == "pii" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pii category, got %#v", got.Categories)
	}

	unchanged := bumpModerationTextWithPII(ai.ModerationResultV1{Decision: "allow"}, "{")
	if unchanged.Decision != "allow" {
		t.Fatalf("expected invalid evidence to no-op, got %#v", unchanged)
	}
}

func TestProcessAIJob_MarksJobOKWhenResultExists(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	job := &models.AIJob{ID: strings.Repeat("a", 64), Status: models.AIJobStatusQueued, ExpiresAt: now.Add(1 * time.Hour)}

	st := &fakeAIStore{
		jobs:    map[string]*models.AIJob{job.ID: job},
		results: map[string]*models.AIResult{job.ID: {ID: job.ID, ResultJSON: `{"kind":"x","version":"v1"}`}},
	}

	srv := NewServer(config.Config{}, st, nil, fakeComprehend{}, fakeRekognition{})
	if err := srv.processAIJob(context.Background(), "rid", job.ID); err != nil {
		t.Fatalf("processAIJob: %v", err)
	}

	j2, _ := st.GetAIJob(context.Background(), job.ID)
	if j2 == nil || strings.TrimSpace(j2.Status) != models.AIJobStatusOK {
		t.Fatalf("expected ok, got %#v", j2)
	}
}

func TestProcessAIJob_DropsUnknownModuleOrPolicy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	job := &models.AIJob{
		ID:            strings.Repeat("b", 64),
		InstanceSlug:  "inst",
		Module:        "unknown_module",
		PolicyVersion: "v1",
		Status:        models.AIJobStatusQueued,
		ExpiresAt:     now.Add(1 * time.Hour),
	}

	st := &fakeAIStore{jobs: map[string]*models.AIJob{job.ID: job}}
	srv := NewServer(config.Config{}, st, nil, fakeComprehend{}, fakeRekognition{})
	if err := srv.processAIJob(context.Background(), "rid", job.ID); err != nil {
		t.Fatalf("processAIJob: %v", err)
	}

	if _, err := st.GetAIResult(context.Background(), job.ID); err == nil {
		t.Fatalf("expected no result written for unknown module")
	}

	// Unknown policy version is also dropped.
	job2 := &models.AIJob{
		ID:            strings.Repeat("c", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_text_comprehend",
		PolicyVersion: "v2",
		Status:        models.AIJobStatusQueued,
		ExpiresAt:     now.Add(1 * time.Hour),
		InputsJSON:    `{"text":"hello"}`,
	}
	st.jobs[job2.ID] = job2
	if err := srv.processAIJob(context.Background(), "rid", job2.ID); err != nil {
		t.Fatalf("processAIJob: %v", err)
	}
	if _, err := st.GetAIResult(context.Background(), job2.ID); err == nil {
		t.Fatalf("expected no result written for unknown policy")
	}
}

func TestProcessAIJob_ReturnsErrorWhenPersistResultFails(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	job := &models.AIJob{
		ID:            strings.Repeat("d", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_text_comprehend",
		PolicyVersion: "v1",
		ModelSet:      "aws:comprehend",
		Status:        models.AIJobStatusQueued,
		ExpiresAt:     now.Add(1 * time.Hour),
		InputsJSON:    `{"text":"hello"}`,
	}

	base := &fakeAIStore{jobs: map[string]*models.AIJob{job.ID: job}}
	st := failingPutResultStore{fakeAIStore: base, err: errors.New("nope")}

	srv := NewServer(config.Config{}, &st, nil, fakeComprehend{}, fakeRekognition{})
	if err := srv.processAIJob(context.Background(), "rid", job.ID); err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleSafetyQueueMessage_DropsInvalidAndUnknown(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	if err := srv.handleSafetyQueueMessage(&apptheory.EventContext{RequestID: "rid"}, events.SQSMessage{}); err == nil {
		t.Fatalf("expected store not initialized error")
	}

	srv = NewServer(config.Config{}, &fakeAIStore{}, nil, fakeComprehend{}, fakeRekognition{})
	if err := srv.handleSafetyQueueMessage(nil, events.SQSMessage{}); err == nil {
		t.Fatalf("expected event context nil error")
	}

	evctx := &apptheory.EventContext{RequestID: "rid"}

	// Invalid JSON is dropped.
	if err := srv.handleSafetyQueueMessage(evctx, events.SQSMessage{Body: "{"}); err != nil {
		t.Fatalf("expected drop, got %v", err)
	}

	// Unknown kind is dropped.
	if err := srv.handleSafetyQueueMessage(evctx, events.SQSMessage{Body: `{"kind":"nope"}`}); err != nil {
		t.Fatalf("expected drop, got %v", err)
	}

	// Missing job id is dropped.
	if err := srv.handleSafetyQueueMessage(evctx, events.SQSMessage{Body: `{"kind":"ai_job"}`}); err != nil {
		t.Fatalf("expected drop, got %v", err)
	}
}
