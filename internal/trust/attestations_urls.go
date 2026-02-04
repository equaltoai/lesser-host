package trust

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func attestationURL(ctx *apptheory.Context, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	base := requestBaseURL(ctx)
	path := "/attestations/" + id
	if base != "" {
		return base + path
	}
	return path
}
