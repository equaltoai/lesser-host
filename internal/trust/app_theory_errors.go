package trust

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

const (
	appTheoryCodeBadRequest          = "app.bad_request"
	appTheoryCodeValidationFailed    = "app.validation_failed"
	appTheoryCodeUnauthorized        = "app.unauthorized"
	appTheoryCodeForbidden           = "app.forbidden"
	appTheoryCodeNotFound            = "app.not_found"
	appTheoryCodeMethodNotAllowed    = "app.method_not_allowed"
	appTheoryCodeConflict            = "app.conflict"
	appTheoryCodeTooLarge            = "app.too_large"
	appTheoryCodeResponseTooLarge    = "app.response_too_large"
	appTheoryCodeTimeout             = "app.timeout"
	appTheoryCodeRateLimited         = "app.rate_limited"
	appTheoryCodeMicroVMUnavailable  = "app.microvm_unavailable"
	appTheoryCodeOverloaded          = "app.overloaded"
	appTheoryCodeUpstreamUnavailable = "app.upstream_unavailable"
	appTheoryCodeAssistantTurnFailed = "app.assistant_turn_failed"
	appTheoryCodeUpstreamError       = "app.upstream_error"
	appTheoryCodeInternal            = "app.internal"
)

func newAppTheoryError(code string, message string) *apptheory.AppTheoryError {
	return apptheory.NewAppTheoryError(code, message).WithStatusCode(appTheoryStatusForCode(code))
}

func appTheoryStatusForCode(code string) int {
	switch strings.TrimSpace(code) {
	case appTheoryCodeBadRequest:
		return http.StatusBadRequest
	case appTheoryCodeValidationFailed:
		return http.StatusUnprocessableEntity
	case appTheoryCodeUnauthorized:
		return http.StatusUnauthorized
	case appTheoryCodeForbidden:
		return http.StatusForbidden
	case appTheoryCodeNotFound:
		return http.StatusNotFound
	case appTheoryCodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case appTheoryCodeConflict:
		return http.StatusConflict
	case appTheoryCodeTooLarge, appTheoryCodeResponseTooLarge:
		return http.StatusRequestEntityTooLarge
	case appTheoryCodeTimeout:
		return http.StatusRequestTimeout
	case appTheoryCodeRateLimited:
		return http.StatusTooManyRequests
	case appTheoryCodeMicroVMUnavailable, appTheoryCodeOverloaded, appTheoryCodeUpstreamUnavailable:
		return http.StatusServiceUnavailable
	case appTheoryCodeAssistantTurnFailed, appTheoryCodeUpstreamError:
		return http.StatusBadGateway
	case appTheoryCodeInternal:
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}
