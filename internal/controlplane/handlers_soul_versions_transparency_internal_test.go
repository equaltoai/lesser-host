package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const transparencyProviderOpenAI = "openai"

func TestExtractTransparency(t *testing.T) {
	t.Parallel()

	if got := extractTransparency(map[string]any{"transparency": map[string]any{"provider": "openai"}}); got == nil {
		t.Fatalf("expected explicit transparency")
	}

	gotAny := extractTransparency(map[string]any{
		"model":            "gpt-4o-mini",
		"provider":         transparencyProviderOpenAI,
		"selfDescription":  "Primary description",
		"self_description": "Override description",
	})
	got, ok := gotAny.(map[string]any)
	if !ok {
		t.Fatalf("expected fallback transparency map, got %#v", gotAny)
	}
	if got["model"] != "gpt-4o-mini" || got["provider"] != transparencyProviderOpenAI || got["selfDescription"] != "Override description" {
		t.Fatalf("unexpected fallback transparency: %#v", got)
	}
}

func TestHandleSoulPublicGetVersions(t *testing.T) {
	t.Parallel()

	t.Run("errors", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{SoulEnabled: false}}
		if _, err := s.handleSoulPublicGetVersions(&apptheory.Context{}); err == nil {
			t.Fatalf("expected missing store error")
		}

		tdb := newSoulLifecycleTestDB()
		tdb.qVersion.On("All", mock.Anything).Return(errors.New("boom")).Once()
		s = &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulEnabled: true},
		}
		ctx := &apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}}
		if _, err := s.handleSoulPublicGetVersions(ctx); err == nil {
			t.Fatalf("expected query error")
		}
	})

	t.Run("success sorts and paginates", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		tdb.qVersion.On("All", mock.AnythingOfType("*[]*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
			*dest = []*models.SoulAgentVersion{
				{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 1, CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
				nil,
				{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 3, CreatedAt: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)},
				{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 2, CreatedAt: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)},
			}
		}).Once()

		s := &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulEnabled: true},
		}
		ctx := &apptheory.Context{
			Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex},
			Request: apptheory.Request{
				Query: map[string][]string{
					"cursor": {"3"},
					"limit":  {"1"},
					"origin": {"https://portal.example.com"},
				},
			},
		}
		resp, err := s.handleSoulPublicGetVersions(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("unexpected status: %d", resp.Status)
		}

		var out soulListVersionsResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.Count != 1 || len(out.Versions) != 1 || out.Versions[0].VersionNumber != 2 || !out.HasMore || out.NextCursor != "2" {
			t.Fatalf("unexpected versions response: %#v", out)
		}
	})
}

func TestHandleSoulPublicGetTransparency(t *testing.T) {
	t.Parallel()

	t.Run("not found and parse errors", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulEnabled: true},
		}
		ctx := &apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}}
		if _, err := s.handleSoulPublicGetTransparency(ctx); err == nil {
			t.Fatalf("expected missing soul packs error")
		}

		s.soulPacks = &fakeSoulPackStore{}
		if _, err := s.handleSoulPublicGetTransparency(ctx); err == nil {
			t.Fatalf("expected no such key error")
		}

		s.soulPacks = &fakeSoulPackStore{objects: map[string]fakePut{
			soulRegistrationS3Key(soulLifecycleTestAgentIDHex): {key: soulRegistrationS3Key(soulLifecycleTestAgentIDHex), body: []byte("{")},
		}}
		if _, err := s.handleSoulPublicGetTransparency(ctx); err == nil {
			t.Fatalf("expected parse error")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		body, err := json.Marshal(map[string]any{
			"model":            "gpt-4o-mini",
			"provider":         transparencyProviderOpenAI,
			"self_description": "Fallback description",
		})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		tdb := newSoulLifecycleTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulEnabled: true},
			soulPacks: &fakeSoulPackStore{
				contentType: "application/json",
				objects: map[string]fakePut{
					soulRegistrationS3Key(soulLifecycleTestAgentIDHex): {key: soulRegistrationS3Key(soulLifecycleTestAgentIDHex), body: body},
				},
			},
		}
		ctx := &apptheory.Context{
			Params:  map[string]string{"agentId": soulLifecycleTestAgentIDHex},
			Request: apptheory.Request{Query: map[string][]string{"origin": {"https://portal.example.com"}}},
		}
		resp, err := s.handleSoulPublicGetTransparency(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var out soulTransparencyResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		transparency, ok := out.Transparency.(map[string]any)
		if !ok {
			t.Fatalf("expected transparency map, got %#v", out.Transparency)
		}
		if transparency["provider"] != transparencyProviderOpenAI || transparency["selfDescription"] != "Fallback description" {
			t.Fatalf("unexpected transparency response: %#v", transparency)
		}
	})
}

func TestFakeSoulPackStore_NoSuchKey(t *testing.T) {
	t.Parallel()

	store := &fakeSoulPackStore{}
	if _, _, _, err := store.GetObject(t.Context(), "missing", 1); err == nil {
		t.Fatalf("expected no such key")
	} else {
		var noSuchKey *s3types.NoSuchKey
		if !errors.As(err, &noSuchKey) {
			t.Fatalf("expected NoSuchKey, got %T", err)
		}
	}
}
