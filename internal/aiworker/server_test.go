package aiworker

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	comprehendtypes "github.com/aws/aws-sdk-go-v2/service/comprehend/types"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rekognitiontypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const decisionBlock = "block"

type fakeAIStore struct {
	mu      sync.Mutex
	jobs    map[string]*models.AIJob
	results map[string]*models.AIResult
}

func (f *fakeAIStore) GetAIJob(_ context.Context, id string) (*models.AIJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs == nil {
		return nil, errNotFound
	}
	j, ok := f.jobs[strings.TrimSpace(id)]
	if !ok {
		return nil, errNotFound
	}
	// Return a copy to avoid test flakiness due to mutation.
	cp := *j
	return &cp, nil
}

func (f *fakeAIStore) PutAIJob(_ context.Context, item *models.AIJob) error {
	if item == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs == nil {
		f.jobs = map[string]*models.AIJob{}
	}
	cp := *item
	f.jobs[strings.TrimSpace(item.ID)] = &cp
	return nil
}

func (f *fakeAIStore) GetAIResult(_ context.Context, id string) (*models.AIResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results == nil {
		return nil, errNotFound
	}
	r, ok := f.results[strings.TrimSpace(id)]
	if !ok {
		return nil, errNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeAIStore) PutAIResult(_ context.Context, item *models.AIResult) error {
	if item == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results == nil {
		f.results = map[string]*models.AIResult{}
	}
	cp := *item
	f.results[strings.TrimSpace(item.ID)] = &cp
	return nil
}

var errNotFound = &testNotFoundError{}

type testNotFoundError struct{}

func (e *testNotFoundError) Error() string { return "not found" }

type fakeComprehend struct{}

func (fakeComprehend) DetectDominantLanguage(_ context.Context, _ *comprehend.DetectDominantLanguageInput, _ ...func(*comprehend.Options)) (*comprehend.DetectDominantLanguageOutput, error) {
	score := float32(0.99)
	return &comprehend.DetectDominantLanguageOutput{
		Languages: []comprehendtypes.DominantLanguage{
			{LanguageCode: aws.String("en"), Score: &score},
		},
	}, nil
}

func (fakeComprehend) DetectEntities(_ context.Context, _ *comprehend.DetectEntitiesInput, _ ...func(*comprehend.Options)) (*comprehend.DetectEntitiesOutput, error) {
	score := float32(0.9)
	return &comprehend.DetectEntitiesOutput{
		Entities: []comprehendtypes.Entity{
			{Text: aws.String("Alice"), Type: comprehendtypes.EntityTypePerson, Score: &score, BeginOffset: aws.Int32(0), EndOffset: aws.Int32(5)},
		},
	}, nil
}

func (fakeComprehend) DetectPiiEntities(_ context.Context, _ *comprehend.DetectPiiEntitiesInput, _ ...func(*comprehend.Options)) (*comprehend.DetectPiiEntitiesOutput, error) {
	score := float32(0.8)
	return &comprehend.DetectPiiEntitiesOutput{
		Entities: []comprehendtypes.PiiEntity{
			{Type: comprehendtypes.PiiEntityTypeName, Score: &score, BeginOffset: aws.Int32(0), EndOffset: aws.Int32(5)},
		},
	}, nil
}

type fakeRekognition struct{}

func (fakeRekognition) DetectModerationLabels(_ context.Context, _ *rekognition.DetectModerationLabelsInput, _ ...func(*rekognition.Options)) (*rekognition.DetectModerationLabelsOutput, error) {
	conf := float32(99)
	return &rekognition.DetectModerationLabelsOutput{
		ModerationLabels: []rekognitiontypes.ModerationLabel{
			{Name: aws.String("Explicit Nudity"), Confidence: &conf},
		},
	}, nil
}

func (fakeRekognition) DetectText(_ context.Context, _ *rekognition.DetectTextInput, _ ...func(*rekognition.Options)) (*rekognition.DetectTextOutput, error) {
	conf := float32(88)
	return &rekognition.DetectTextOutput{
		TextDetections: []rekognitiontypes.TextDetection{
			{DetectedText: aws.String("hello"), Confidence: &conf, Type: rekognitiontypes.TextTypesLine},
		},
	}, nil
}

func (fakeRekognition) DetectFaces(_ context.Context, _ *rekognition.DetectFacesInput, _ ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error) {
	return &rekognition.DetectFacesOutput{
		FaceDetails: []rekognitiontypes.FaceDetail{{}, {}},
	}, nil
}

func runProcessAIJobAndParseResult(t *testing.T, job *models.AIJob, wantKind string) map[string]any {
	t.Helper()

	st := &fakeAIStore{
		jobs: map[string]*models.AIJob{
			job.ID: job,
		},
	}

	srv := NewServer(config.Config{ArtifactBucketName: "bucket"}, st, artifacts.New("bucket"), fakeComprehend{}, fakeRekognition{})
	if err := srv.processAIJob(context.Background(), "req", job.ID); err != nil {
		t.Fatalf("processAIJob error: %v", err)
	}

	res, err := st.GetAIResult(context.Background(), job.ID)
	if err != nil || res == nil {
		t.Fatalf("expected result, got err=%v res=%v", err, res)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(res.ResultJSON), &parsed); err != nil {
		t.Fatalf("unmarshal resultJSON: %v", err)
	}
	if parsed["kind"] != wantKind {
		t.Fatalf("expected kind=%s, got %v", wantKind, parsed["kind"])
	}
	if parsed["version"] != job.PolicyVersion {
		t.Fatalf("expected version=%s, got %v", job.PolicyVersion, parsed["version"])
	}

	j2, _ := st.GetAIJob(context.Background(), job.ID)
	if j2 == nil || strings.TrimSpace(j2.Status) != models.AIJobStatusOK {
		t.Fatalf("expected job status ok, got %+v", j2)
	}

	return parsed
}

func TestProcessAIJob_WritesComprehendEvidenceResult(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{
		ID:            strings.Repeat("a", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_text_comprehend",
		PolicyVersion: "v1",
		ModelSet:      "aws:comprehend",
		InputsHash:    "hash",
		InputsJSON:    `{"text":"hello"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
	}

	runProcessAIJobAndParseResult(t, job, "comprehend_text")
}

func TestProcessAIJob_WritesRekognitionEvidenceResult(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{
		ID:            strings.Repeat("b", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_image_rekognition",
		PolicyVersion: "v1",
		ModelSet:      "aws:rekognition",
		InputsHash:    "hash",
		InputsJSON:    `{"object_key":"x"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
	}

	runProcessAIJobAndParseResult(t, job, "rekognition_image")
}

func TestProcessAIJob_WritesModerationTextResult(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{
		ID:            strings.Repeat("c", 64),
		InstanceSlug:  "inst",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		InputsHash:    "hash",
		InputsJSON:    `{"text":"kill yourself 123-45-6789"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
	}

	parsed := runProcessAIJobAndParseResult(t, job, "moderation_text")
	if parsed["decision"] != decisionBlock {
		t.Fatalf("expected decision=%s, got %v", decisionBlock, parsed["decision"])
	}
}

func TestProcessAIJob_WritesModerationImageResult(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{
		ID:            strings.Repeat("d", 64),
		InstanceSlug:  "inst",
		Module:        "moderation_image_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		InputsHash:    "hash",
		InputsJSON:    `{"object_key":"moderation/inst/img"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
	}

	parsed := runProcessAIJobAndParseResult(t, job, "moderation_image")
	if parsed["decision"] != decisionBlock {
		t.Fatalf("expected decision=%s, got %v", decisionBlock, parsed["decision"])
	}
}
