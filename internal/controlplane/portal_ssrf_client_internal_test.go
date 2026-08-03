package controlplane

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/outboundhttp"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalSSRFRewriteTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (t portalSSRFRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = t.base.Scheme
	cloned.URL.Host = t.base.Host
	return t.rt.RoundTrip(cloned)
}

type portalSSRFFakeResolver map[string][]net.IPAddr

func (r portalSSRFFakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := r[strings.ToLower(strings.TrimSpace(host))]
	if !ok {
		return nil, fmt.Errorf("unexpected host lookup: %s", host)
	}
	return ips, nil
}

func TestPortalSoulLesserAgentRejectsRedirectToInternalTarget(t *testing.T) {
	t.Parallel()

	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(internal.Close)

	managed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	t.Cleanup(managed.Close)

	managedURL, err := url.Parse(managed.URL)
	require.NoError(t, err)
	baseClient := managed.Client()
	guardedClient := outboundhttp.NewSSRFProtectedClient(&http.Client{
		Timeout: instanceMetricsTimeout,
		Transport: portalSSRFRewriteTransport{
			base: managedURL,
			rt:   baseClient.Transport,
		},
	})

	s := &Server{
		cfg:                  config.Config{Stage: "lab"},
		portalCostHTTPClient: guardedClient,
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return "https://api.public-managed.example", nil
		},
	}

	got := s.fetchPortalSoulLesserAgent(&apptheory.Context{}, &models.Instance{Slug: "simulacrum"}, "agent-a")
	require.NotNil(t, got)
	require.Equal(t, "unavailable", got.Status)
	require.Equal(t, int32(0), internalHits.Load(), "redirect target must not be requested")
}

func TestPortalSoulLesserAgentRejectsPrivateHostedBaseDomainResolution(t *testing.T) {
	t.Parallel()

	resolver := portalSSRFFakeResolver{
		"api.dev.simulacrum.greater.website": []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}},
	}
	s := &Server{
		cfg: config.Config{Stage: "lab"},
		portalCostHTTPClient: outboundhttp.NewSSRFProtectedClient(nil,
			outboundhttp.WithResolver(resolver),
			outboundhttp.WithTimeout(100*time.Millisecond),
		),
	}

	got := s.fetchPortalSoulLesserAgent(&apptheory.Context{}, &models.Instance{
		Slug:             "simulacrum",
		HostedBaseDomain: "simulacrum.greater.website",
	}, "agent-a")
	require.NotNil(t, got)
	require.Equal(t, "unavailable", got.Status)
}

func TestNewServerInitializesSSRFGuardedPortalClient(t *testing.T) {
	t.Parallel()

	s := NewServer(config.Config{Stage: "lab"}, nil)
	require.NotNil(t, s.portalCostHTTPClient)
	require.NotNil(t, s.portalCostHTTPClient.CheckRedirect)
	require.ErrorIs(t, s.portalCostHTTPClient.CheckRedirect(&http.Request{}, []*http.Request{{}}), outboundhttp.ErrRedirectNotAllowed)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", nil)
	require.NoError(t, err)
	resp, err := s.portalCostHTTPClient.Do(req) //nolint:gosec // fixed negative assertion that the guarded client rejects non-HTTPS before dialing.
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "url scheme must be https")

	require.Same(t, s.portalCostHTTPClient, s.portalManagedHTTPClient())
}
