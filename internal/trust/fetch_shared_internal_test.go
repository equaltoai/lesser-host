package trust

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFetchSharedWrappers(t *testing.T) {
	t.Parallel()

	normalized, u, err := NormalizeLinkURL("https://bücher.example/path/../?b=2&a=1#frag")
	require.NoError(t, err)
	require.Equal(t, testNormalizedBucherURL, normalized)
	require.NotNil(t, u)
	require.Equal(t, "xn--bcher-kva.example", u.Host)

	_, out, err := NormalizeLinkURL("http://127.0.0.1/")
	require.NoError(t, err)
	require.Error(t, ValidateOutboundURL(context.Background(), out))

	_, out, err = NormalizeLinkURL("https://93.184.216.34/")
	require.NoError(t, err)
	require.NoError(t, ValidateOutboundURL(context.Background(), out))

	client := NewPreviewHTTPClient(3 * time.Second)
	require.NotNil(t, client)
	require.Equal(t, 3*time.Second, client.Timeout)

	httpClient := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://93.184.216.34/": {
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("ok")),
			},
		}},
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	final, chain, body, ct, err := FetchWithRedirects(ctx, httpClient, out, 0, 16)
	require.NoError(t, err)
	require.NotNil(t, final)
	require.Equal(t, out.String(), final.String())
	require.Len(t, chain, 1)
	require.Equal(t, out.String(), chain[0])
	require.Equal(t, "ok", string(body))
	require.Equal(t, "text/plain", ct)
}
