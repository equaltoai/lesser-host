package models

import "strings"

func normalizeSoulLocalID(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "@")
	raw = strings.TrimSuffix(raw, "/")
	return strings.ToLower(raw)
}

func normalizeSoulEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeSoulENSName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.TrimSuffix(raw, ".")
	return raw
}

func normalizeSoulPhoneE164(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "-", "")
	raw = strings.ReplaceAll(raw, "(", "")
	raw = strings.ReplaceAll(raw, ")", "")
	raw = strings.ReplaceAll(raw, ".", "")
	return raw
}
