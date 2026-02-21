package models

import "strings"

func normalizeSoulLocalID(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "@")
	raw = strings.TrimSuffix(raw, "/")
	return strings.ToLower(raw)
}
