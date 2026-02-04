package trust

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/attestations"
)

var attestationIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

type attestationResponse struct {
	ID      string          `json:"id"`
	JWS     string          `json:"jws"`
	Header  any             `json:"header,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Server) handleWellKnownJWKS(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.attest == nil || !s.attest.Enabled() {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	jwks, err := s.attest.JWKS(ctx.Context())
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	resp, err := apptheory.JSON(http.StatusOK, jwks)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{"public, max-age=3600"}
	return resp, nil
}

func (s *Server) handleLookupAttestation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	actorURI := strings.TrimSpace(firstQueryValue(ctx.Request.Query, "actor_uri"))
	objectURI := strings.TrimSpace(firstQueryValue(ctx.Request.Query, "object_uri"))
	contentHash := strings.TrimSpace(firstQueryValue(ctx.Request.Query, "content_hash"))
	module := strings.TrimSpace(firstQueryValue(ctx.Request.Query, "module"))
	policyVersion := strings.TrimSpace(firstQueryValue(ctx.Request.Query, "policy_version"))

	if actorURI == "" || objectURI == "" || contentHash == "" || module == "" || policyVersion == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "missing required query parameters"}
	}

	id := attestations.AttestationID(actorURI, objectURI, contentHash, module, policyVersion)
	return s.serveAttestationByID(ctx, id)
}

func (s *Server) handleGetAttestation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if !attestationIDRE.MatchString(id) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid attestation id"}
	}
	return s.serveAttestationByID(ctx, id)
}

func (s *Server) serveAttestationByID(ctx *apptheory.Context, id string) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	id = strings.TrimSpace(id)
	if !attestationIDRE.MatchString(id) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid attestation id"}
	}

	item, err := s.store.GetAttestation(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "attestation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	headerBytes, payloadBytes, _, err := attestations.ParseCompactJWS(strings.TrimSpace(item.JWS))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var header any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	out := attestationResponse{
		ID:      strings.TrimSpace(item.ID),
		JWS:     strings.TrimSpace(item.JWS),
		Header:  header,
		Payload: append([]byte(nil), payloadBytes...),
	}

	resp, err := apptheory.JSON(http.StatusOK, out)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	maxAge := 0
	if item.ExpiresAt.After(time.Time{}) {
		secs := int(item.ExpiresAt.Sub(time.Now().UTC()).Seconds())
		if secs > 0 {
			maxAge = secs
		}
	}

	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	cacheControl := "public, max-age=" + strconv.Itoa(maxAge)
	if maxAge > 0 {
		cacheControl += ", immutable"
	}
	resp.Headers["cache-control"] = []string{cacheControl}
	return resp, nil
}
