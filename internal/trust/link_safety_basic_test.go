package trust

import (
	"context"
	"net"
	"testing"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestNormalizeLinkURLDeterministic_CanonicalizesQueryAndIDNA(t *testing.T) {
	t.Parallel()

	got := normalizeLinkURLDeterministic("https://bücher.example/path/../?b=2&a=1#frag")
	want := "https://xn--bcher-kva.example/?a=1&b=2"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAnalyzeLinkSafetyBasic_FlagsShortener(t *testing.T) {
	t.Parallel()

	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"bit.ly": {net.ParseIP("93.184.216.34")},
	}}

	got := analyzeLinkSafetyBasic(context.Background(), resolver, "https://bit.ly/x")
	if got.Risk == "invalid" || got.Risk == "blocked" {
		t.Fatalf("unexpected risk %q (flags=%v)", got.Risk, got.Flags)
	}
	if !hasFlag(got.Flags, "shortener") {
		t.Fatalf("expected shortener flag (flags=%v)", got.Flags)
	}
}

func TestAnalyzeLinkSafetyBasic_FlagsRedirector(t *testing.T) {
	t.Parallel()

	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"www.google.com": {net.ParseIP("93.184.216.34")},
	}}

	got := analyzeLinkSafetyBasic(context.Background(), resolver, "https://www.google.com/url?url=https%3A%2F%2Fexample.com")
	if got.Risk == "invalid" || got.Risk == "blocked" {
		t.Fatalf("unexpected risk %q (flags=%v)", got.Risk, got.Flags)
	}
	if !hasFlag(got.Flags, "redirector") {
		t.Fatalf("expected redirector flag (flags=%v)", got.Flags)
	}
}

func TestAnalyzeLinkSafetyBasic_FlagsPunycode(t *testing.T) {
	t.Parallel()

	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"xn--bcher-kva.example": {net.ParseIP("93.184.216.34")},
	}}

	got := analyzeLinkSafetyBasic(context.Background(), resolver, "https://bücher.example/")
	if got.Risk == "invalid" || got.Risk == "blocked" {
		t.Fatalf("unexpected risk %q (flags=%v)", got.Risk, got.Flags)
	}
	if !hasFlag(got.Flags, "punycode") {
		t.Fatalf("expected punycode flag (flags=%v)", got.Flags)
	}
}

func TestAnalyzeLinkSafetyBasic_BlocksLoopback(t *testing.T) {
	t.Parallel()

	got := analyzeLinkSafetyBasic(context.Background(), nil, "http://127.0.0.1/")
	if got.Risk != "blocked" {
		t.Fatalf("expected blocked risk, got %q (flags=%v)", got.Risk, got.Flags)
	}
	if got.ErrorCode != "blocked_ssrf" {
		t.Fatalf("expected blocked_ssrf error_code, got %q", got.ErrorCode)
	}
}

func TestComputeLinkSafetyBasicSummary(t *testing.T) {
	t.Parallel()

	links := []models.LinkSafetyBasicLinkResult{
		{Risk: "low"},
		{Risk: "medium"},
		{Risk: "high"},
		{Risk: "blocked"},
		{Risk: "invalid"},
	}

	s := computeLinkSafetyBasicSummary(links)
	if s.TotalLinks != 5 {
		t.Fatalf("expected total_links=5, got %d", s.TotalLinks)
	}
	if s.LowCount != 1 || s.MediumCount != 1 || s.HighCount != 1 || s.BlockedCount != 1 || s.InvalidCount != 1 {
		t.Fatalf("unexpected counts: %+v", s)
	}
	if s.OverallRisk != "invalid" {
		t.Fatalf("expected overall_risk=invalid, got %q", s.OverallRisk)
	}
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
