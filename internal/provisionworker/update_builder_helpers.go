package provisionworker

import (
	"strings"

	"github.com/theory-cloud/tabletheory/pkg/core"
)

func setStringIfNotEmpty(ub core.UpdateBuilder, field, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	ub.Set(field, value)
}

func setBoolIfNil(ub core.UpdateBuilder, existing *bool, field string, value bool) {
	if existing != nil {
		return
	}
	ub.Set(field, value)
}

func setHostURLsIfNotEmpty(ub core.UpdateBuilder, baseURL, attestationsURL string) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return
	}
	ub.Set("LesserHostBaseURL", baseURL)
	ub.Set("LesserHostAttestationsURL", strings.TrimSpace(attestationsURL))
}
