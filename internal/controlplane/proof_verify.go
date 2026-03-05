package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func fetchWellKnownHTTPSBody(ctx context.Context, domainNormalized string, wellKnownPath string) (int, string, error) {
	domainNormalized = strings.TrimSpace(domainNormalized)
	wellKnownPath = strings.TrimSpace(wellKnownPath)
	if domainNormalized == "" || wellKnownPath == "" {
		return 0, "", errors.New("domain and path are required")
	}

	lookupCtx := ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()
	if err := validateOutboundHost(rc, domainNormalized); err != nil {
		return 0, "", err
	}

	u := &url.URL{
		Scheme: "https",
		Host:   domainNormalized,
		Path:   path.Clean(wellKnownPath),
	}

	reqCtx := rc
	if ctx != nil {
		reqCtx = ctx
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, "", err
	}

	transport := http.DefaultTransport
	if base, ok := transport.(*http.Transport); ok {
		tr := base.Clone()
		tr.Proxy = nil
		tr.DialContext = newTipRegistrySSRFProtectedDialContext()
		transport = tr
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects not allowed")
		},
	}

	resp, err := client.Do(req) //nolint:gosec // SSRF mitigated by validateOutboundHost + custom DialContext + redirects disabled.
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

func verifyWellKnownHTTPS(ctx context.Context, domainNormalized string, wellKnownPath string, proofValue string) bool {
	proofValue = strings.TrimSpace(proofValue)
	if strings.TrimSpace(domainNormalized) == "" || proofValue == "" {
		return false
	}

	status, body, err := fetchWellKnownHTTPSBody(ctx, domainNormalized, wellKnownPath)
	if err != nil {
		return false
	}
	if status != http.StatusOK {
		return false
	}
	return body == proofValue
}
