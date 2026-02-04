package controlplane

import (
	"encoding/json"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func parseJSON(ctx *apptheory.Context, dest any) error {
	if ctx == nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}
	if len(ctx.Request.Body) == 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "empty body"}
	}
	if err := json.Unmarshal(ctx.Request.Body, dest); err != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid JSON"}
	}
	return nil
}

func bearerToken(headers map[string][]string) string {
	raw := firstHeaderValue(headers, "authorization")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(parts[0]), "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func firstHeaderValue(headers map[string][]string, key string) string {
	if headers == nil {
		return ""
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	values := headers[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

