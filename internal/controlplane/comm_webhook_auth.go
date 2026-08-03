package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	hostsecrets "github.com/equaltoai/lesser-host/internal/secrets"
)

const (
	commWebhookTimestampTolerance = 5 * time.Minute
	commWebhookSignatureHeader    = "x-lesser-host-signature"
	commWebhookTimestampHeader    = "x-lesser-host-timestamp"
)

func (s *Server) requireCommWebhookAdapterAuth(ctx *apptheory.Context) *apptheory.AppTheoryError {
	if s == nil || ctx == nil {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	secret, err := s.loadCommWebhookSharedSecret(ctx.Context())
	if err != nil || strings.TrimSpace(secret) == "" {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	timestamp := strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, commWebhookTimestampHeader))
	signature := strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, commWebhookSignatureHeader))
	if !validRecentUnixTimestamp(timestamp, commWebhookTimestampTolerance) {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if !verifyHMACWebhookSignature(secret, timestamp, ctx.Request.Body, signature) {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	return nil
}

func (s *Server) requireTelnyxCommWebhookAuth(ctx *apptheory.Context) *apptheory.AppTheoryError {
	if s == nil || ctx == nil {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if hasTelnyxSignatureHeaders(ctx) {
		publicKey, err := s.loadTelnyxWebhookPublicKey(ctx.Context())
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
		}
		timestamp := strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, "telnyx-timestamp"))
		signature := strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, "telnyx-signature-ed25519"))
		if !validRecentUnixTimestamp(timestamp, commWebhookTimestampTolerance) {
			return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
		}
		if !verifyTelnyxWebhookSignature(publicKey, timestamp, ctx.Request.Body, signature) {
			return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
		}
		return nil
	}
	return s.requireCommWebhookAdapterAuth(ctx)
}

func hasTelnyxSignatureHeaders(ctx *apptheory.Context) bool {
	if ctx == nil {
		return false
	}
	return strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, "telnyx-timestamp")) != "" ||
		strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, "telnyx-signature-ed25519")) != ""
}

func (s *Server) loadCommWebhookSharedSecret(ctx context.Context) (string, error) {
	if s == nil || s.ssmGetParameter == nil {
		return "", fmt.Errorf("ssm getter is not configured")
	}
	param := strings.TrimSpace(s.cfg.CommWebhookSharedSecretSSMParam)
	if param == "" {
		return "", fmt.Errorf("comm webhook secret parameter is not configured")
	}
	return s.ssmGetParameter(ctx, param)
}

func (s *Server) loadTelnyxWebhookPublicKey(ctx context.Context) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("server is nil")
	}
	if key, err := decodeEd25519PublicKey(s.cfg.TelnyxWebhookPublicKey); err == nil {
		return key, nil
	}
	if s.ssmGetParameter == nil {
		return nil, fmt.Errorf("ssm getter is not configured")
	}
	raw, err := s.ssmGetParameter(ctx, hostsecrets.TelnyxAPITokenSSMParameterName)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &obj); err != nil {
		return nil, err
	}
	for _, key := range []string{"webhook_public_key", "webhookPublicKey", "public_key", "publicKey"} {
		if value, ok := obj[key].(string); ok {
			return decodeEd25519PublicKey(value)
		}
	}
	return nil, fmt.Errorf("telnyx webhook public key is not configured")
}

func verifyHMACWebhookSignature(secret string, timestamp string, body []byte, signature string) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(signature)
	if strings.HasPrefix(strings.ToLower(signature), "sha256=") {
		signature = strings.TrimSpace(signature[len("sha256="):])
	}
	if secret == "" || timestamp == "" || signature == "" {
		return false
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	want := mac.Sum(nil)
	return hmac.Equal(got, want)
}

func verifyTelnyxWebhookSignature(publicKey ed25519.PublicKey, timestamp string, body []byte, signature string) bool {
	if len(publicKey) != ed25519.PublicKeySize || strings.TrimSpace(timestamp) == "" || strings.TrimSpace(signature) == "" {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	payload := append([]byte(strings.TrimSpace(timestamp)+"|"), body...)
	return ed25519.Verify(publicKey, payload, sig)
}

func validRecentUnixTimestamp(raw string, tolerance time.Duration) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}
	ts := time.Unix(sec, 0)
	now := time.Now()
	if ts.After(now.Add(tolerance)) {
		return false
	}
	return now.Sub(ts) <= tolerance
}

func decodeEd25519PublicKey(raw string) (ed25519.PublicKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty public key")
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == ed25519.PublicKeySize {
		return ed25519.PublicKey(decoded), nil
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}
