package httpx

import (
	"encoding/json"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
)

// maxRequestBodySize is the maximum allowed JSON request body size (1 MB).
const maxRequestBodySize = 1 << 20

func ParseJSON(ctx *apptheory.Context, dest any) error {
	if err := validateJSONBody(ctx); err != nil {
		return err
	}
	if err := json.Unmarshal(ctx.Request.Body, dest); err != nil {
		return newAppTheoryError("app.bad_request", "invalid JSON")
	}
	return nil
}

// BindJSON uses AppTheory typed request binding for JSON bodies while
// preserving host's existing ParseJSON request-size and error semantics.
func BindJSON[Req any](ctx *apptheory.Context) (Req, error) {
	var zero Req
	if err := validateJSONBody(ctx); err != nil {
		return zero, err
	}

	req, err := apptheory.BindRequest(ctx, apptheory.BindConfig[Req]{
		Body: true,
	})
	if err != nil {
		return zero, newAppTheoryError("app.bad_request", "invalid JSON")
	}
	return req, nil
}

func validateJSONBody(ctx *apptheory.Context) error {
	if ctx == nil {
		return newAppTheoryError("app.bad_request", "invalid request")
	}
	if len(ctx.Request.Body) == 0 {
		return newAppTheoryError("app.bad_request", "empty body")
	}
	if len(ctx.Request.Body) > maxRequestBodySize {
		return newAppTheoryError("app.bad_request", "request body too large")
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
