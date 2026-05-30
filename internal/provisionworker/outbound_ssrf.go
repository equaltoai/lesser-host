package provisionworker

import (
	"net/http"

	"github.com/equaltoai/lesser-host/internal/outboundhttp"
)

func ssrfProtectedHTTPClient(base *http.Client) *http.Client {
	return outboundhttp.NewSSRFProtectedClient(base)
}
