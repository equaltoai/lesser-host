package domains

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

var dnsLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeDomain canonicalizes a user-supplied domain for storage and hostId hashing.
//
// Rules:
// - trims whitespace, strips trailing dot, lowercases
// - rejects schemes, paths, ports, credentials, and wildcards
// - converts IDNs to ASCII (UTS#46) via idna.Lookup
// - requires at least one dot (public suffix-like shape)
func NormalizeDomain(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("domain is required")
	}
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.ToLower(raw)

	if strings.Contains(raw, "://") {
		return "", fmt.Errorf("domain must not include scheme")
	}
	if strings.ContainsAny(raw, "/:@") {
		return "", fmt.Errorf("domain must not include path, port, or credentials")
	}
	if strings.Contains(raw, "*") {
		return "", fmt.Errorf("wildcards are not allowed")
	}

	ascii, err := idna.Lookup.ToASCII(raw)
	if err != nil {
		return "", fmt.Errorf("invalid domain")
	}
	ascii = strings.ToLower(strings.TrimSpace(ascii))
	ascii = strings.TrimSuffix(ascii, ".")
	if ascii == "" {
		return "", fmt.Errorf("invalid domain")
	}
	if len(ascii) > 253 {
		return "", fmt.Errorf("domain is too long")
	}

	if ip := net.ParseIP(ascii); ip != nil {
		return "", fmt.Errorf("invalid domain")
	}

	parts := strings.Split(ascii, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("domain must include a public suffix")
	}

	for _, label := range parts {
		if !dnsLabelRE.MatchString(label) {
			return "", fmt.Errorf("invalid domain")
		}
	}

	return ascii, nil
}
