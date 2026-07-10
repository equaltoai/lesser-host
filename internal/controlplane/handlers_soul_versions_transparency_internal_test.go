package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

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

func TestHandleSoulPublicGetVersionsErrors(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulEnabled: false}}
	if _, err := s.handleSoulPublicGetVersions(&apptheory.Context{}); err == nil {
		t.Fatalf("expected missing store error")
	}

	tdb := newSoulLifecycleTestDB()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()
	s = &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true},
	}
	ctx := &apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}}
	if _, err := s.handleSoulPublicGetVersions(ctx); err == nil {
		t.Fatalf("expected query error")
	}
}

func TestHandleSoulPublicGetVersionsNumericHeadPagination(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: soulLifecycleTestAgentIDHex, SelfDescriptionVersion: 12}
	}).Once()
	for _, version := range []int{12, 11} {
		version := version
		tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentVersion](t, args, 0)
			*dest = models.SoulAgentVersion{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: version}
		}).Once()
	}

	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true},
	}
	ctx := &apptheory.Context{
		Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex},
		Request: apptheory.Request{
			Query: map[string][]string{
				"limit":  {"2"},
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

	out := decodeSoulVersionsResponse(t, resp)
	if out.Count != 2 || len(out.Versions) != 2 || out.Versions[0].VersionNumber != 12 || out.Versions[1].VersionNumber != 11 || !out.HasMore || out.NextCursor != "version:10" {
		t.Fatalf("unexpected versions response: %#v", out)
	}
}

func TestHandleSoulPublicGetVersionsMissingIdentity(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true},
	}
	ctx := &apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}}
	resp, err := s.handleSoulPublicGetVersions(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := decodeSoulVersionsResponse(t, resp)
	if resp.Status != http.StatusOK || out.Count != 0 || len(out.Versions) != 0 || out.HasMore || out.NextCursor != "" {
		t.Fatalf("unexpected empty versions response: status=%d out=%#v", resp.Status, out)
	}
}

func TestLoadSoulAgentVersionsCursorAndMissingRecords(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: soulLifecycleTestAgentIDHex, SelfDescriptionVersion: 12}
	}).Twice()
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentVersion](t, args, 0)
		*dest = models.SoulAgentVersion{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 11}
	}).Once()
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(errors.New("boom")).Once()

	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}
	versions, hasMore, nextCursor, appErr := s.loadSoulAgentVersions(ctx, soulLifecycleTestAgentIDHex, "version:12", 2)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if len(versions) != 1 || versions[0].VersionNumber != 11 || !hasMore || nextCursor != "version:10" {
		t.Fatalf("unexpected sparse page: versions=%#v hasMore=%v next=%q", versions, hasMore, nextCursor)
	}

	_, _, _, appErr = s.loadSoulAgentVersions(ctx, soulLifecycleTestAgentIDHex, "version:9", 1)
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected version read error, got %#v", appErr)
	}
}

func TestSoulVersionsStartVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		cursor      string
		latest      int
		want        int
		wantAppCode string
	}{
		{name: "empty uses latest", latest: 12, want: 12},
		{name: "numeric cursor", cursor: "10", latest: 12, want: 10},
		{name: "namespaced cursor", cursor: "version:10", latest: 12, want: 10},
		{name: "key cursor", cursor: "VERSION#9", latest: 12, want: 9},
		{name: "clamps future cursor", cursor: "99", latest: 12, want: 12},
		{name: "zero latest", latest: 0, want: 0},
		{name: "invalid cursor", cursor: "opaque", latest: 12, wantAppCode: appErrCodeBadRequest},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, appErr := soulVersionsStartVersion(tc.cursor, tc.latest)
			if tc.wantAppCode != "" {
				if appErr == nil || appErr.Code != tc.wantAppCode {
					t.Fatalf("expected %s, got %#v", tc.wantAppCode, appErr)
				}
				return
			}
			if appErr != nil || got != tc.want {
				t.Fatalf("got version=%d appErr=%#v; want version=%d", got, appErr, tc.want)
			}
		})
	}
}

func decodeSoulVersionsResponse(t *testing.T, resp *apptheory.Response) soulListVersionsResponse {
	t.Helper()
	var out soulListVersionsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return out
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
