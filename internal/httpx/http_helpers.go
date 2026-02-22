package httpx

import (
	"encoding/json"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

// maxRequestBodySize is the maximum allowed JSON request body size (1 MB).
const maxRequestBodySize = 1 << 20

func ParseJSON(ctx *apptheory.Context, dest any) error {
	if ctx == nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}
	if len(ctx.Request.Body) == 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "empty body"}
	}
	if len(ctx.Request.Body) > maxRequestBodySize {
		return &apptheory.AppError{Code: "app.bad_request", Message: "request body too large"}
	}
	if err := json.Unmarshal(ctx.Request.Body, dest); err != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid JSON"}
	}
	return nil
}

func BearerToken(headers map[string][]string) string {
	raw := FirstHeaderValue(headers, "authorization")
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

func FirstHeaderValue(headers map[string][]string, key string) string {
	if headers == nil {
		return ""
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if values := headers[key]; len(values) > 0 {
		return values[0]
	}

	if lower := strings.ToLower(key); lower != key {
		if values := headers[lower]; len(values) > 0 {
			return values[0]
		}
	}

	for k, values := range headers {
		if strings.EqualFold(strings.TrimSpace(k), key) && len(values) > 0 {
			return values[0]
		}
	}

	return ""
}

func FirstQueryValue(query map[string][]string, key string) string {
	if query == nil {
		return ""
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if values := query[key]; len(values) > 0 {
		return values[0]
	}
	if lower := strings.ToLower(key); lower != key {
		if values := query[lower]; len(values) > 0 {
			return values[0]
		}
	}
	for k, values := range query {
		if strings.EqualFold(strings.TrimSpace(k), key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
