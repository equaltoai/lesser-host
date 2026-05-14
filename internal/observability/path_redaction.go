package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const redactedPathHashPrefix = "sha256:"

// SanitizeLogPath normalizes private route path segments before generic
// request logs persist them. Dedicated audit records carry their own hashed
// identifiers; this is the defense-in-depth layer for framework/API access
// style logs that otherwise receive only a raw request path.
func SanitizeLogPath(raw string) string {
	path, _ := splitLogPathSuffix(strings.TrimSpace(raw))
	if path == "" {
		return strings.TrimSpace(raw)
	}
	if sanitized, ok := sanitizeSoulMintInstanceConversationPath(path); ok {
		return sanitized
	}
	if sanitized, ok := sanitizeLesserSelfScopeMintConversationPath(path); ok {
		return sanitized
	}
	return strings.TrimSpace(raw)
}

// SanitizeMetricTags copies framework metric tags and normalizes any request
// path tag before generic metric logs persist it. AppTheory emits the raw
// request path in MetricRecord.Tags["path"] independently from LogRecord.Path,
// so Host must apply the same redaction at the metric hook boundary.
func SanitizeMetricTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return tags
	}
	sanitized := make(map[string]string, len(tags))
	for key, value := range tags {
		sanitized[key] = value
	}
	if path, ok := sanitized["path"]; ok {
		sanitized["path"] = SanitizeLogPath(path)
	}
	return sanitized
}

func splitLogPathSuffix(raw string) (path string, suffix string) {
	for i, r := range raw {
		if r == '?' || r == '#' {
			return raw[:i], raw[i:]
		}
	}
	return raw, ""
}

func sanitizeSoulMintInstanceConversationPath(path string) (string, bool) {
	segments, leadingSlash := splitPathSegments(path)
	if len(segments) != 8 ||
		segments[0] != "api" ||
		segments[1] != "v1" ||
		segments[2] != "soul" ||
		segments[3] != "instance" ||
		segments[4] != "agents" ||
		segments[6] != "mint-conversations" ||
		strings.TrimSpace(segments[5]) == "" ||
		strings.TrimSpace(segments[7]) == "" {
		return "", false
	}
	segments[7] = "conversation-" + shortLogHash(segments[7])
	return joinPathSegments(segments, leadingSlash), true
}

func sanitizeLesserSelfScopeMintConversationPath(path string) (string, bool) {
	segments, leadingSlash := splitPathSegments(path)
	if len(segments) != 7 ||
		segments[0] != "api" ||
		segments[1] != "v1" ||
		segments[2] != "souls" ||
		segments[3] != "bound" ||
		segments[4] != "me" ||
		segments[5] != "mint-conversations" ||
		strings.TrimSpace(segments[6]) == "" {
		return "", false
	}
	segments[6] = "conversation-" + shortLogHash(segments[6])
	return joinPathSegments(segments, leadingSlash), true
}

func splitPathSegments(path string) ([]string, bool) {
	leadingSlash := strings.HasPrefix(path, "/")
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil, leadingSlash
	}
	return strings.Split(trimmed, "/"), leadingSlash
}

func joinPathSegments(segments []string, leadingSlash bool) string {
	joined := strings.Join(segments, "/")
	if leadingSlash {
		return "/" + joined
	}
	return joined
}

func shortLogHash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return redactedPathHashPrefix + hex.EncodeToString(sum[:])[:16]
}
