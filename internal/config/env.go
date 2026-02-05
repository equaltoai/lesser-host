package config

import (
	"os"
	"strconv"
	"strings"
)

func envString(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func envStringDefault(key string, fallback string) string {
	if v := envString(key); v != "" {
		return v
	}
	return fallback
}

func envLowerStringDefault(key string, fallback string) string {
	return strings.ToLower(strings.TrimSpace(envStringDefault(key, fallback)))
}

func envBoolOn(key string) bool {
	switch envLowerStringDefault(key, "") {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envInt64Bounded(key string, fallback int64, minValue int64, maxValue int64) int64 {
	raw := envString(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < minValue || n > maxValue {
		return fallback
	}
	return n
}

func envInt64Positive(key string, fallback int64) int64 {
	raw := envString(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envUint16Max(key string, fallback uint16, maxValue uint16) uint16 {
	raw := envString(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || n > uint64(maxValue) {
		return fallback
	}
	return uint16(n)
}

func parseCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
