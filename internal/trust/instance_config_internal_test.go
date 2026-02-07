package trust

import (
	"context"
	"testing"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestDefaultInstanceTrustConfig_Invariants(t *testing.T) {
	t.Parallel()

	cfg := defaultInstanceTrustConfig()
	if !cfg.HostedPreviewsEnabled || !cfg.LinkSafetyEnabled || !cfg.RendersEnabled {
		t.Fatalf("expected core features enabled by default: %#v", cfg)
	}
	if cfg.RenderPolicy != renderPolicySuspicious {
		t.Fatalf("expected render policy %q, got %q", renderPolicySuspicious, cfg.RenderPolicy)
	}
	if cfg.OveragePolicy != overagePolicyBlock {
		t.Fatalf("expected overage policy %q, got %q", overagePolicyBlock, cfg.OveragePolicy)
	}
	if cfg.AIPricingMultiplierBps != 10000 || cfg.AIMaxInflightJobs <= 0 {
		t.Fatalf("unexpected ai defaults: %#v", cfg)
	}
}

func TestTrustConfigStoreReady(t *testing.T) {
	t.Parallel()

	if (&Server{}).trustConfigStoreReady() {
		t.Fatalf("expected false with nil store")
	}
	if (&Server{store: store.New(nil)}).trustConfigStoreReady() {
		t.Fatalf("expected false with nil db")
	}
	db := ttmocks.NewMockExtendedDB()
	if !(&Server{store: store.New(db)}).trustConfigStoreReady() {
		t.Fatalf("expected true with db")
	}
}

func TestApplyInstanceTrustConfigOverrides(t *testing.T) {
	t.Parallel()

	cfg := defaultInstanceTrustConfig()

	hpe := false
	lse := false
	re := false
	me := true

	inst := &models.Instance{
		HostedPreviewsEnabled: &hpe,
		LinkSafetyEnabled:     &lse,
		RendersEnabled:        &re,
		RenderPolicy:          "ALWAYS",
		OveragePolicy:         "allow",
		ModerationEnabled:     &me,
		ModerationTrigger:     moderationTriggerVirality,
		ModerationViralityMin: 7,
	}

	applyInstanceTrustConfigOverrides(&cfg, inst)
	if cfg.HostedPreviewsEnabled || cfg.LinkSafetyEnabled || cfg.RendersEnabled {
		t.Fatalf("expected overrides applied: %#v", cfg)
	}
	if cfg.RenderPolicy != renderPolicyAlways {
		t.Fatalf("expected render policy override, got %q", cfg.RenderPolicy)
	}
	if cfg.OveragePolicy != overagePolicyAllow {
		t.Fatalf("expected overage policy override, got %q", cfg.OveragePolicy)
	}
	if !cfg.ModerationEnabled || cfg.ModerationTrigger != moderationTriggerVirality || cfg.ModerationViralityMin != 7 {
		t.Fatalf("unexpected moderation overrides: %#v", cfg)
	}
}

func TestLoadInstanceTrustConfig_DefaultsWhenDBMissingOrError(t *testing.T) {
	t.Parallel()

	s := &Server{store: store.New(nil)}
	if got := s.loadInstanceTrustConfig(context.Background(), "inst"); got != defaultInstanceTrustConfig() {
		t.Fatalf("expected defaults for missing db, got %#v", got)
	}

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("First", mock.Anything).Return(assertErr{}).Once()

	s = &Server{store: store.New(db)}
	if got := s.loadInstanceTrustConfig(context.Background(), "inst"); got != defaultInstanceTrustConfig() {
		t.Fatalf("expected defaults on db error, got %#v", got)
	}
}

func TestLoadInstanceTrustConfig_AppliesOverridesFromInstance(t *testing.T) {
	t.Parallel()

	hpe := false
	re := false
	aiEnabled := true

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{
			Slug:                  "inst",
			HostedPreviewsEnabled: &hpe,
			RendersEnabled:        &re,
			AIEnabled:             &aiEnabled,
			AIModelSet:            "openai:gpt",
		}
	}).Once()

	s := &Server{store: store.New(db)}
	got := s.loadInstanceTrustConfig(context.Background(), "inst")
	if got.HostedPreviewsEnabled {
		t.Fatalf("expected override HostedPreviewsEnabled=false, got %#v", got)
	}
	if got.RendersEnabled {
		t.Fatalf("expected override RendersEnabled=false, got %#v", got)
	}
	if !got.AIEnabled || got.AIModelSet != "openai:gpt" {
		t.Fatalf("expected AI overrides, got %#v", got)
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "boom" }
