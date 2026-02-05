package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	linkSafetyBasicPolicyVersion = "v1"
)

func normalizeLinkURLDeterministic(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme == "" {
		return raw
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return raw
	}

	asciiHost, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return raw
	}
	asciiHost = strings.ToLower(strings.TrimSpace(asciiHost))
	if asciiHost == "" {
		return raw
	}

	port := strings.TrimSpace(u.Port())
	return normalizeURLForSafety(u, scheme, asciiHost, port)
}

func linkSafetyBasicJobID(actorURI, objectURI, contentHash, linksHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"link_safety_basic",
		linkSafetyBasicPolicyVersion,
		strings.TrimSpace(actorURI),
		strings.TrimSpace(objectURI),
		strings.TrimSpace(contentHash),
		strings.TrimSpace(linksHash),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

func linkSafetyBasicLinksHash(normalized []string) string {
	parts := make([]string, 0, len(normalized))
	for _, s := range normalized {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		parts = append(parts, s)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

type linkSafetyBasicInput struct {
	Raw           string
	ParsedURL     *url.URL
	Scheme        string
	Host          string
	Port          string
	NormalizedURL string
	Flags         []string
}

func analyzeLinkSafetyBasic(ctx context.Context, resolver ipResolver, raw string) models.LinkSafetyBasicLinkResult {
	in, invalid := parseLinkSafetyBasicInput(raw)
	if invalid != nil {
		return *invalid
	}

	flags, blocked := applyLinkSafetyBasicHostChecks(ctx, resolver, in)
	if blocked != nil {
		return *blocked
	}

	risk := riskFromFlags(flags)
	return models.LinkSafetyBasicLinkResult{
		URL:           in.Raw,
		NormalizedURL: in.NormalizedURL,
		Host:          in.Host,
		Flags:         uniqueSorted(flags),
		Risk:          risk,
	}
}

func parseLinkSafetyBasicInput(raw string) (linkSafetyBasicInput, *models.LinkSafetyBasicLinkResult) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
			URL:          raw,
			Risk:         "invalid",
			ErrorCode:    "invalid_url",
			ErrorMessage: "empty url",
		}
	}

	u, err := url.Parse(raw)
	if err != nil {
		return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
			URL:          raw,
			Risk:         "invalid",
			ErrorCode:    "invalid_url",
			ErrorMessage: "invalid url",
		}
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme == "" {
		return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
			URL:          raw,
			Risk:         "invalid",
			ErrorCode:    "invalid_url",
			ErrorMessage: "missing scheme",
		}
	}

	flags := []string{}
	if scheme != schemeHTTP && scheme != schemeHTTPS {
		flags = append(flags, "scheme_non_http")
	}
	if u.User != nil {
		flags = append(flags, "userinfo")
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
			URL:          raw,
			Risk:         "invalid",
			ErrorCode:    "invalid_url",
			ErrorMessage: "missing host",
		}
	}

	asciiHost, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
			URL:          raw,
			Risk:         "invalid",
			ErrorCode:    "invalid_url",
			ErrorMessage: "invalid host",
		}
	}
	asciiHost = strings.ToLower(strings.TrimSpace(asciiHost))
	if asciiHost == "" {
		return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
			URL:          raw,
			Risk:         "invalid",
			ErrorCode:    "invalid_url",
			ErrorMessage: "invalid host",
		}
	}
	if strings.Contains(asciiHost, "xn--") {
		flags = append(flags, "punycode")
	}

	port := strings.TrimSpace(u.Port())
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return linkSafetyBasicInput{}, &models.LinkSafetyBasicLinkResult{
				URL:          raw,
				Risk:         "invalid",
				ErrorCode:    "invalid_url",
				ErrorMessage: "invalid port",
			}
		}
		if (scheme == schemeHTTP && n != 80) || (scheme == schemeHTTPS && n != 443) {
			flags = append(flags, "non_default_port")
		}
	}

	if scheme == schemeHTTP {
		flags = append(flags, "unencrypted_http")
	}
	if isKnownShortener(asciiHost) {
		flags = append(flags, "shortener")
	}
	if looksLikeRedirector(u, asciiHost) {
		flags = append(flags, "redirector")
	}

	normalizedURL := normalizeURLForSafety(u, scheme, asciiHost, port)
	return linkSafetyBasicInput{
		Raw:           raw,
		ParsedURL:     u,
		Scheme:        scheme,
		Host:          asciiHost,
		Port:          port,
		NormalizedURL: normalizedURL,
		Flags:         flags,
	}, nil
}

func applyLinkSafetyBasicHostChecks(ctx context.Context, resolver ipResolver, in linkSafetyBasicInput) ([]string, *models.LinkSafetyBasicLinkResult) {
	flags := append([]string(nil), in.Flags...)

	// Host safety / "broken where possible".
	if isInternalHostname(in.Host) {
		flags = append(flags, "internal_host")
		return flags, &models.LinkSafetyBasicLinkResult{
			URL:           in.Raw,
			NormalizedURL: in.NormalizedURL,
			Host:          in.Host,
			Flags:         uniqueSorted(flags),
			Risk:          "blocked",
			ErrorCode:     "blocked_ssrf",
			ErrorMessage:  "host is not allowed",
		}
	}

	if ip := net.ParseIP(in.Host); ip != nil {
		if isDeniedIP(ip) {
			flags = append(flags, "private_ip")
			return flags, &models.LinkSafetyBasicLinkResult{
				URL:           in.Raw,
				NormalizedURL: in.NormalizedURL,
				Host:          in.Host,
				Flags:         uniqueSorted(flags),
				Risk:          "blocked",
				ErrorCode:     "blocked_ssrf",
				ErrorMessage:  "ip is not allowed",
			}
		}
		return flags, nil
	}

	if in.Scheme != schemeHTTP && in.Scheme != schemeHTTPS {
		return flags, nil
	}

	// DNS resolution check (best-effort).
	resolutionCtx := ctx
	if resolutionCtx == nil {
		resolutionCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(resolutionCtx, 800*time.Millisecond)
	defer cancel()

	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(rc, in.Host)
	if err != nil || len(ips) == 0 {
		flags = append(flags, "unresolved_host")
		return flags, nil
	}

	for _, ipAddr := range ips {
		if isDeniedIP(ipAddr.IP) {
			flags = append(flags, "private_ip")
			return flags, &models.LinkSafetyBasicLinkResult{
				URL:           in.Raw,
				NormalizedURL: in.NormalizedURL,
				Host:          in.Host,
				Flags:         uniqueSorted(flags),
				Risk:          "blocked",
				ErrorCode:     "blocked_ssrf",
				ErrorMessage:  "host resolves to a blocked ip",
			}
		}
	}

	return flags, nil
}

func normalizeURLForSafety(u *url.URL, scheme string, asciiHost string, port string) string {
	if u == nil {
		return ""
	}

	nu := &url.URL{}
	nu.Scheme = strings.ToLower(strings.TrimSpace(scheme))
	if port != "" {
		nu.Host = asciiHost + ":" + strings.TrimSpace(port)
	} else {
		nu.Host = asciiHost
	}

	nu.Path = u.EscapedPath()
	if nu.Path == "" {
		nu.Path = "/"
	}
	nu.Path = path.Clean(nu.Path)
	if !strings.HasPrefix(nu.Path, "/") {
		nu.Path = "/" + nu.Path
	}

	if u.RawQuery != "" {
		nu.RawQuery = u.Query().Encode()
	}

	nu.Fragment = ""
	return nu.String()
}

func uniqueSorted(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func riskFromFlags(flags []string) string {
	score := 0
	for _, f := range flags {
		switch f {
		case "scheme_non_http":
			score += 3
		case "non_default_port":
			score += 3
		case "punycode":
			score += 3
		case "redirector":
			score += 2
		case "shortener":
			score += 2
		case "userinfo":
			score++
		case "unencrypted_http":
			score++
		case "unresolved_host":
			score++
		}
	}
	if score >= 4 {
		return riskHigh
	}
	if score >= 2 {
		return riskMedium
	}
	return riskLow
}

func computeLinkSafetyBasicSummary(links []models.LinkSafetyBasicLinkResult) models.LinkSafetyBasicSummary {
	summary := models.LinkSafetyBasicSummary{
		TotalLinks: len(links),
	}

	overall := riskLow
	for _, l := range links {
		switch strings.ToLower(strings.TrimSpace(l.Risk)) {
		case statusInvalid:
			summary.InvalidCount++
			overall = maxRisk(overall, statusInvalid)
		case statusBlocked:
			summary.BlockedCount++
			overall = maxRisk(overall, statusBlocked)
		case riskHigh:
			summary.HighCount++
			overall = maxRisk(overall, riskHigh)
		case riskMedium:
			summary.MediumCount++
			overall = maxRisk(overall, riskMedium)
		default:
			summary.LowCount++
		}
	}

	summary.OverallRisk = overall
	return summary
}

func maxRisk(a, b string) string {
	order := map[string]int{
		riskLow:       1,
		riskMedium:    2,
		riskHigh:      3,
		statusBlocked: 4,
		statusInvalid: 5,
	}
	if order[b] > order[a] {
		return b
	}
	return a
}

func isInternalHostname(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	return false
}

func isKnownShortener(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "t.co",
		"bit.ly",
		"tinyurl.com",
		"goo.gl",
		"is.gd",
		"ow.ly",
		"buff.ly",
		"rebrand.ly",
		"shorturl.at":
		return true
	default:
		return false
	}
}

func looksLikeRedirector(u *url.URL, asciiHost string) bool {
	if u == nil {
		return false
	}
	asciiHost = strings.ToLower(strings.TrimSpace(asciiHost))

	// Known redirectors.
	switch asciiHost {
	case "l.facebook.com", "lm.facebook.com", "www.google.com":
		// google redirector is typically /url
		// facebook redirector uses ?u=
		return true
	}

	p := strings.ToLower(strings.TrimSpace(u.Path))
	if strings.Contains(p, "redirect") || strings.Contains(p, "redir") || strings.Contains(p, "out") {
		return true
	}

	q := u.Query()
	for _, key := range []string{"url", "u", "target", "dest", "destination", "redirect", "redirect_uri", "redirect_url"} {
		val := strings.TrimSpace(q.Get(key))
		if val == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(val), "http://") || strings.HasPrefix(strings.ToLower(val), "https://") {
			return true
		}
	}

	return false
}
