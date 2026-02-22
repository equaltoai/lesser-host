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

func verifyWellKnownHTTPS(ctx context.Context, domainNormalized string, wellKnownPath string, proofValue string) bool {
	domainNormalized = strings.TrimSpace(domainNormalized)
	proofValue = strings.TrimSpace(proofValue)
	if domainNormalized == "" || proofValue == "" {
		return false
	}

	lookupCtx := ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()
	if err := validateOutboundHost(rc, domainNormalized); err != nil {
		return false
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
		return false
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
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == proofValue
}
