package httpx

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
)

// TrustedSourceInfo is the sanitized, provider-derived source metadata host uses
// for audit and rate-limit context. It deliberately comes from AppTheory's
// SourceProvenance API rather than Forwarded/X-Forwarded-* headers.
type TrustedSourceInfo struct {
	SourceIP string
	Provider string
	Source   string
	Valid    bool
}

const trustedSourceUnknown = "unknown"

// ContextKeyTrustedSource is the AppTheory context key used when auth
// middleware has already captured trusted source provenance for downstream
// audit/rate-limit code.
const ContextKeyTrustedSource = "httpx.trusted_source"

// TrustedSource returns provider-derived source metadata from AppTheory. It
// does not parse or trust client-controlled forwarding headers.
func TrustedSource(ctx *apptheory.Context) TrustedSourceInfo {
	if ctx == nil {
		return TrustedSourceInfo{Provider: trustedSourceUnknown, Source: trustedSourceUnknown}
	}
	prov := ctx.SourceProvenance()
	return TrustedSourceInfo{
		SourceIP: strings.TrimSpace(prov.SourceIP),
		Provider: strings.TrimSpace(prov.Provider),
		Source:   strings.TrimSpace(prov.Source),
		Valid:    prov.Valid,
	}
}

// SetTrustedSource stores trusted source metadata on the AppTheory context for
// downstream auth/audit code. The metadata is still derived only from
// AppTheory's provider context.
func SetTrustedSource(ctx *apptheory.Context) TrustedSourceInfo {
	source := TrustedSource(ctx)
	if ctx != nil {
		ctx.Set(ContextKeyTrustedSource, source)
	}
	return source
}

// TrustedSourceFromContext returns previously captured trusted source metadata
// when present, falling back to AppTheory provider context extraction.
func TrustedSourceFromContext(ctx *apptheory.Context) TrustedSourceInfo {
	if ctx == nil {
		return TrustedSource(nil)
	}
	if source, ok := ctx.Get(ContextKeyTrustedSource).(TrustedSourceInfo); ok {
		return source
	}
	return TrustedSource(ctx)
}

// SourceIP returns the trusted provider-derived source IP convenience value.
func SourceIP(ctx *apptheory.Context) string {
	return TrustedSourceFromContext(ctx).SourceIP
}

// Fingerprint returns a non-secret, deterministic fingerprint for source-IP
// based rate-limit keys so DynamoDB partition keys do not contain raw IPs.
func (s TrustedSourceInfo) Fingerprint() string {
	if !s.Valid || strings.TrimSpace(s.SourceIP) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(s.Provider) + "\x00" + strings.TrimSpace(s.SourceIP)))
	return hex.EncodeToString(sum[:])
}

// SourceRateLimitIdentifier returns a stable rate-limit identifier for callers
// without a stronger authenticated identity. The identifier is derived only
// from trusted provider context and falls back to a shared unknown bucket when
// provider source metadata is unavailable or invalid.
func SourceRateLimitIdentifier(ctx *apptheory.Context, prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), ":")
	if prefix == "" {
		prefix = "source"
	}
	source := TrustedSourceFromContext(ctx)
	fp := source.Fingerprint()
	if fp == "" {
		return prefix + ":source:" + trustedSourceUnknown
	}
	provider := strings.TrimSpace(source.Provider)
	if provider == "" {
		provider = trustedSourceUnknown
	}
	return prefix + ":source:" + provider + ":" + fp
}

// SourceRateLimitMetadata returns safe source metadata for rate-limit audit
// rows. It intentionally excludes the raw source IP; audit logs may store the
// raw provider-derived IP, but rate-limit rows use a fingerprint only.
func SourceRateLimitMetadata(ctx *apptheory.Context) map[string]string {
	source := TrustedSourceFromContext(ctx)
	provider := strings.TrimSpace(source.Provider)
	if provider == "" {
		provider = trustedSourceUnknown
	}
	sourceName := strings.TrimSpace(source.Source)
	if sourceName == "" {
		sourceName = trustedSourceUnknown
	}
	valid := "false"
	if source.Valid {
		valid = "true"
	}
	out := map[string]string{
		"source_provider": provider,
		"source":          sourceName,
		"source_valid":    valid,
	}
	if fp := source.Fingerprint(); fp != "" {
		out["source_ip_sha256"] = fp
	}
	return out
}
