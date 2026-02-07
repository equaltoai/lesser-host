package trust

import (
	"context"
	"net"
	"net/url"
	"strings"
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
	if got.Risk == statusInvalid || got.Risk == statusBlocked {
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
	if got.Risk == statusInvalid || got.Risk == statusBlocked {
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
	if got.Risk == statusInvalid || got.Risk == statusBlocked {
		t.Fatalf("unexpected risk %q (flags=%v)", got.Risk, got.Flags)
	}
	if !hasFlag(got.Flags, "punycode") {
		t.Fatalf("expected punycode flag (flags=%v)", got.Flags)
	}
}

func TestAnalyzeLinkSafetyBasic_BlocksLoopback(t *testing.T) {
	t.Parallel()

	got := analyzeLinkSafetyBasic(context.Background(), nil, "http://127.0.0.1/")
	if got.Risk != statusBlocked {
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
		{Risk: statusBlocked},
		{Risk: statusInvalid},
	}

	s := computeLinkSafetyBasicSummary(links)
	if s.TotalLinks != 5 {
		t.Fatalf("expected total_links=5, got %d", s.TotalLinks)
	}
	if s.LowCount != 1 || s.MediumCount != 1 || s.HighCount != 1 || s.BlockedCount != 1 || s.InvalidCount != 1 {
		t.Fatalf("unexpected counts: %+v", s)
	}
	if s.OverallRisk != statusInvalid {
		t.Fatalf("expected overall_risk=invalid, got %q", s.OverallRisk)
	}
}

func TestLinkSafetyBasicAttestationParts_ValidatesFields(t *testing.T) {
	t.Parallel()

	if _, _, _, ok := linkSafetyBasicAttestationParts(nil); ok {
		t.Fatalf("expected nil to be invalid")
	}

	if _, _, _, ok := linkSafetyBasicAttestationParts(&models.LinkSafetyBasicResult{ActorURI: "a"}); ok {
		t.Fatalf("expected missing fields to be invalid")
	}

	actor, object, hash, ok := linkSafetyBasicAttestationParts(&models.LinkSafetyBasicResult{
		ActorURI:    " actor ",
		ObjectURI:   " object ",
		ContentHash: " hash ",
	})
	if !ok || actor != "actor" || object != "object" || hash != "hash" {
		t.Fatalf("unexpected parts: ok=%v actor=%q object=%q hash=%q", ok, actor, object, hash)
	}
}

func TestParseLinkSafetyBasicPort_ValidatesAndFlagsNonDefault(t *testing.T) {
	t.Parallel()

	u, _ := url.Parse("https://example.com:8443/")
	port, flags, invalid := parseLinkSafetyBasicPort(u, u.String(), schemeHTTPS, nil)
	if invalid != nil || port != "8443" || !hasFlag(flags, "non_default_port") {
		t.Fatalf("unexpected: port=%q flags=%v invalid=%#v", port, flags, invalid)
	}

	u = &url.URL{Scheme: "https", Host: "example.com:999999999999999999999999999999"}
	if _, _, invalid = parseLinkSafetyBasicPort(u, "https://example.com:999999999999999999999999999999/", schemeHTTPS, nil); invalid == nil || invalid.ErrorCode != "invalid_url" {
		t.Fatalf("expected invalid_url for bad port, got %#v", invalid)
	}

	u, _ = url.Parse("https://example.com:0/")
	if _, _, invalid = parseLinkSafetyBasicPort(u, u.String(), schemeHTTPS, nil); invalid == nil || invalid.ErrorCode != "invalid_url" {
		t.Fatalf("expected invalid_url for port 0, got %#v", invalid)
	}

	u, _ = url.Parse("https://example.com:65536/")
	if _, _, invalid = parseLinkSafetyBasicPort(u, u.String(), schemeHTTPS, nil); invalid == nil || invalid.ErrorCode != "invalid_url" {
		t.Fatalf("expected invalid_url for port overflow, got %#v", invalid)
	}
}

func TestAnalyzeLinkSafetyBasic_BlocksInternalHostname(t *testing.T) {
	t.Parallel()

	got := analyzeLinkSafetyBasic(context.Background(), nil, "https://localhost/path")
	if got.Risk != statusBlocked || got.ErrorCode != "blocked_ssrf" || !hasFlag(got.Flags, "internal_host") {
		t.Fatalf("unexpected blocked output: %#v", got)
	}
}

func TestAnalyzeLinkSafetyBasic_FlagsUnresolvedHostWhenDNSFails(t *testing.T) {
	t.Parallel()

	resolver := stubResolver{errByHost: map[string]error{"example.com": context.Canceled}}
	got := analyzeLinkSafetyBasic(context.Background(), resolver, "https://example.com/")
	if got.Risk == statusInvalid || got.Risk == statusBlocked {
		t.Fatalf("unexpected risk %q (flags=%v)", got.Risk, got.Flags)
	}
	if !hasFlag(got.Flags, "unresolved_host") {
		t.Fatalf("expected unresolved_host flag (flags=%v)", got.Flags)
	}
}

func TestAnalyzeLinkSafetyBasic_BlocksHostnameResolvingToPrivateIP(t *testing.T) {
	t.Parallel()

	resolver := stubResolver{ipsByHost: map[string][]net.IP{"example.com": {net.ParseIP("127.0.0.1")}}}
	got := analyzeLinkSafetyBasic(context.Background(), resolver, "https://example.com/")
	if got.Risk != statusBlocked || got.ErrorCode != "blocked_ssrf" || !hasFlag(got.Flags, "private_ip") {
		t.Fatalf("unexpected blocked output: %#v", got)
	}
}

func TestNormalizeLinkURLDeterministic_EarlyReturnsForInvalidInputs(t *testing.T) {
	t.Parallel()

	if got := normalizeLinkURLDeterministic(" "); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := normalizeLinkURLDeterministic("http://%"); got != "http://%" {
		t.Fatalf("expected raw on parse error, got %q", got)
	}
	if got := normalizeLinkURLDeterministic("//example.com"); got != "//example.com" {
		t.Fatalf("expected raw when scheme missing, got %q", got)
	}
	if got := normalizeLinkURLDeterministic("https:///path"); got != "https:///path" {
		t.Fatalf("expected raw when host missing, got %q", got)
	}
}

func TestLooksLikeRedirector_DetectsByQueryAndPath(t *testing.T) {
	t.Parallel()

	u, _ := url.Parse("https://example.com/redirect?target=https%3A%2F%2Ffoo.example")
	if !looksLikeRedirector(u, "example.com") {
		t.Fatalf("expected redirector")
	}

	u2, _ := url.Parse("https://example.com/path?u=not-a-url")
	if looksLikeRedirector(u2, "example.com") {
		t.Fatalf("expected non-url query value to not trigger")
	}

	if looksLikeRedirector(nil, "example.com") {
		t.Fatalf("expected nil url to be false")
	}

	google, _ := url.Parse("https://www.google.com/url?q=x")
	if !looksLikeRedirector(google, "www.google.com") {
		t.Fatalf("expected known redirector host to be true")
	}
	if strings.Contains(strings.Join(uniqueSorted([]string{"B", "b", " a "}), ","), " ") {
		t.Fatalf("expected uniqueSorted to normalize and trim")
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
