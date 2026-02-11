package trust

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type stubResolver struct {
	ipsByHost map[string][]net.IP
	errByHost map[string]error
}

func (r stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if err := r.errByHost[host]; err != nil {
		return nil, err
	}
	var out []net.IPAddr
	for _, ip := range r.ipsByHost[host] {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

type sequenceResolver struct {
	host string
	seq  [][]net.IP

	calls int
}

func (r *sequenceResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.calls++
	var ips []net.IP
	if strings.TrimSpace(r.host) == "" || host == r.host {
		if r.calls <= len(r.seq) {
			ips = r.seq[r.calls-1]
		} else if len(r.seq) > 0 {
			ips = r.seq[len(r.seq)-1]
		}
	}

	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

type stubTransport struct {
	responses map[string]*http.Response
	errByURL  map[string]error
}

func (t stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, io.ErrUnexpectedEOF
	}
	key := req.URL.String()
	if err := t.errByURL[key]; err != nil {
		return nil, err
	}
	resp := t.responses[key]
	if resp == nil {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	// Clone minimal fields to avoid sharing Body between calls.
	out := *resp
	if out.Header == nil {
		out.Header = make(http.Header)
	}
	out.Request = req
	return &out, nil
}

type errorReadCloser struct {
	readErr  error
	closeErr error
}

func (e errorReadCloser) Read(_ []byte) (int, error) { return 0, e.readErr }
func (e errorReadCloser) Close() error               { return e.closeErr }

func TestNormalizeLinkURL_IDNA_QueryAndFragment(t *testing.T) {
	t.Parallel()

	got, _, err := normalizeLinkURL("https://bücher.example/path/../?b=2&a=1#frag")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	want := testNormalizedBucherURL
	if got != want {
		t.Fatalf("normalized mismatch: got %q want %q", got, want)
	}
}

func TestNormalizeLinkURL_RejectsNonDefaultPort(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeLinkURL("https://example.com:8443/")
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeInvalidURL {
		t.Fatalf("expected invalid_url, got %T: %v", err, err)
	}
}

func TestValidateOutboundURL_BlocksPrivateIP(t *testing.T) {
	t.Parallel()

	_, u, err := normalizeLinkURL("http://127.0.0.1/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	err = validateOutboundURL(context.Background(), nil, u)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestValidateOutboundURL_BlocksHostnameResolvingToPrivateIP(t *testing.T) {
	t.Parallel()

	_, u, err := normalizeLinkURL("https://example.com/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"example.com": {net.ParseIP("10.0.0.1")},
	}}
	err = validateOutboundURL(context.Background(), resolver, u)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestFetchWithRedirects_BlocksRedirectToPrivateIP(t *testing.T) {
	t.Parallel()

	_, start, err := normalizeLinkURL("https://good.example/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"good.example": {net.ParseIP("93.184.216.34")},
	}}

	client := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://good.example/": {
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"http://127.0.0.1/"}},
				Body:       io.NopCloser(strings.NewReader("")),
			},
		}},
		Timeout: linkPreviewFetchTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, chain, _, _, err := fetchWithRedirects(ctx, resolver, client, start, 3, 64)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 urls in redirect chain, got %d (%v)", len(chain), chain)
	}
	if !strings.Contains(chain[1], "127.0.0.1") {
		t.Fatalf("expected redirect target to include 127.0.0.1, got %q", chain[1])
	}
}

func TestFetchWithRedirects_BlocksDNSRebindingAtDialTime(t *testing.T) {
	t.Parallel()

	_, start, err := normalizeLinkURL("https://rebinding.example/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}

	resolver := &sequenceResolver{
		host: "rebinding.example",
		seq: [][]net.IP{
			{net.ParseIP("93.184.216.34")}, // passes validateOutboundURL
			{net.ParseIP("10.0.0.1")},      // blocked at dial-time
		},
	}

	client := newPreviewHTTPClient(2*time.Second, resolver)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, _, _, err = fetchWithRedirects(ctx, resolver, client, start, 0, 64)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestFetchWithRedirects_EnforcesByteLimit(t *testing.T) {
	t.Parallel()

	_, start, err := normalizeLinkURL("https://good.example/large")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"good.example": {net.ParseIP("93.184.216.34")},
	}}

	client := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://good.example/large": {
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 65))),
			},
		}},
		Timeout: linkPreviewFetchTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, _, _, err = fetchWithRedirects(ctx, resolver, client, start, 0, 64)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != "too_large" {
		t.Fatalf("expected too_large, got %T: %v", err, err)
	}
}

func TestCanonicalizeLinkSchemeAndUserinfo_RejectsInvalidAndUserinfo(t *testing.T) {
	t.Parallel()

	if err := canonicalizeLinkSchemeAndUserinfo(nil); err == nil {
		t.Fatalf("expected error")
	}

	u := &url.URL{Scheme: "ftp", Host: "example.com"}
	if err := canonicalizeLinkSchemeAndUserinfo(u); err == nil {
		t.Fatalf("expected invalid scheme error")
	}

	u = &url.URL{Scheme: "HTTP", Host: "example.com"}
	u.User = url.User("user")
	if err := canonicalizeLinkSchemeAndUserinfo(u); err == nil {
		t.Fatalf("expected userinfo rejection")
	}

	u = &url.URL{Scheme: "HTTPS", Host: "example.com"}
	if err := canonicalizeLinkSchemeAndUserinfo(u); err != nil || u.Scheme != schemeHTTPS {
		t.Fatalf("expected scheme normalized, got scheme=%q err=%v", u.Scheme, err)
	}
}

func TestValidateDefaultPort_RejectsBadPortsAndNonDefault(t *testing.T) {
	t.Parallel()

	if err := validateDefaultPort(nil); err == nil {
		t.Fatalf("expected error")
	}

	for _, host := range []string{"example.com:0", "example.com:65536"} {
		u := &url.URL{Scheme: "https", Host: host}
		if err := validateDefaultPort(u); err == nil {
			t.Fatalf("expected invalid port error for host %q", host)
		}
	}

	u := &url.URL{Scheme: "http", Host: "example.com:443"}
	if err := validateDefaultPort(u); err == nil {
		t.Fatalf("expected non-default port error")
	}

	u = &url.URL{Scheme: "https", Host: "example.com:443"}
	if err := validateDefaultPort(u); err != nil {
		t.Fatalf("expected default https port allowed, got %v", err)
	}
}

func TestValidateOutboundURL_BlocksDeniedHostnamesAndResolverFailures(t *testing.T) {
	t.Parallel()

	_, u, err := normalizeLinkURL("https://localhost/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	if validateErr := validateOutboundURL(context.Background(), nil, u); validateErr == nil {
		t.Fatalf("expected localhost to be blocked")
	}

	_, u, err = normalizeLinkURL("https://example.com/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}

	resolver := stubResolver{errByHost: map[string]error{"example.com": errors.New("boom")}}
	if err := validateOutboundURL(context.Background(), resolver, u); err == nil {
		t.Fatalf("expected resolve error")
	}

	resolver = stubResolver{ipsByHost: map[string][]net.IP{"example.com": nil}}
	if err := validateOutboundURL(context.Background(), resolver, u); err == nil {
		t.Fatalf("expected empty resolve to block")
	}
}

func TestFetchWithRedirects_ValidatesInputsAndRedirectErrors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, start, err := normalizeLinkURL("https://good.example/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}

	resolver := stubResolver{ipsByHost: map[string][]net.IP{"good.example": {net.ParseIP("93.184.216.34")}}}

	if _, _, _, _, err := fetchWithRedirects(ctx, resolver, nil, start, 1, 10); err == nil {
		t.Fatalf("expected client required error")
	}
	if _, _, _, _, err := fetchWithRedirects(ctx, resolver, &http.Client{}, nil, 1, 10); err == nil {
		t.Fatalf("expected start required error")
	}

	client := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://good.example/": {
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/"}},
				Body:       io.NopCloser(strings.NewReader("")),
			},
		}},
		Timeout: linkPreviewFetchTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if _, _, _, _, err := fetchWithRedirects(ctx, resolver, client, start, 0, 10); err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("expected too many redirects error, got %v", err)
	}

	// followRedirect: missing Location.
	if _, err := followRedirect(start, &http.Response{StatusCode: http.StatusFound, Header: make(http.Header)}); err == nil {
		t.Fatalf("expected missing location error")
	}

	// resolveRedirect: invalid location.
	if _, err := resolveRedirect(start, "http://%zz"); err == nil {
		t.Fatalf("expected invalid redirect location error")
	}
}

func TestReadBodyLimit_InvalidLimitAndReadError(t *testing.T) {
	t.Parallel()

	if _, err := readBodyLimit(strings.NewReader("x"), 0); err == nil {
		t.Fatalf("expected invalid limit error")
	}

	if _, err := readBodyLimit(errorReadCloser{readErr: io.ErrUnexpectedEOF}, 10); err == nil {
		t.Fatalf("expected read failed error")
	}
}

func TestExtractLinkPreviewMeta_ParsesTitleDescriptionAndImage(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("https://example.com/path/")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	htmlDoc := `<!doctype html>
<html>
  <head>
    <title> Page Title </title>
    <meta property="og:description" content=" OG desc "/>
    <meta name="description" content=" fallback "/>
    <meta property="og:image" content="/img.png"/>
  </head>
  <body>hello</body>
</html>`

	meta := extractLinkPreviewMeta(base, []byte(htmlDoc))
	if meta.Title != "Page Title" {
		t.Fatalf("expected title, got %q", meta.Title)
	}
	if meta.Description != "OG desc" {
		t.Fatalf("expected description from og, got %q", meta.Description)
	}
	if meta.ImageURL != "https://example.com/img.png" {
		t.Fatalf("expected resolved image url, got %q", meta.ImageURL)
	}
}

func TestParseAndValidateDialTarget_AllowsAndNormalizesDefaultPorts(t *testing.T) {
	t.Parallel()

	host, port, err := parseAndValidateDialTarget("Example.COM:443")
	if err != nil {
		t.Fatalf("parseAndValidateDialTarget error: %v", err)
	}
	if host != "example.com" {
		t.Fatalf("expected host normalized, got %q", host)
	}
	if port != "443" {
		t.Fatalf("expected port 443, got %q", port)
	}
}

func TestParseAndValidateDialTarget_RejectsEmptyHostAndNonDefaultPorts(t *testing.T) {
	t.Parallel()

	if _, _, err := parseAndValidateDialTarget(":443"); err == nil {
		t.Fatalf("expected error for empty host")
	}

	_, _, err := parseAndValidateDialTarget("example.com:8443")
	if err == nil {
		t.Fatalf("expected error for non-default port")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeInvalidURL {
		t.Fatalf("expected invalid_url, got %T: %v", err, err)
	}
}

func TestParseAndValidateDialTarget_BlocksDeniedHostnames(t *testing.T) {
	t.Parallel()

	_, _, err := parseAndValidateDialTarget("localhost:443")
	if err == nil {
		t.Fatalf("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestDialSSRFProtected_BlocksLoopbackAndBlockedResolutions(t *testing.T) {
	t.Parallel()

	dialer := &net.Dialer{Timeout: 10 * time.Millisecond}

	_, err := dialSSRFProtected(context.Background(), dialer, nil, "tcp", "127.0.0.1:443")
	if err == nil {
		t.Fatalf("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}

	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"example.com": {net.ParseIP("10.0.0.1")},
	}}
	_, err = dialSSRFProtected(context.Background(), dialer, resolver, "tcp", "example.com:443")
	if err == nil {
		t.Fatalf("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestDialSSRFProtected_ResolverFailureBlocksRequest(t *testing.T) {
	t.Parallel()

	dialer := &net.Dialer{Timeout: 10 * time.Millisecond}
	resolver := stubResolver{errByHost: map[string]error{"example.com": errors.New("boom")}}

	_, err := dialSSRFProtected(context.Background(), dialer, resolver, "tcp", "example.com:443")
	if err == nil {
		t.Fatalf("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestDialSSRFProtected_RejectsInvalidDialTargets(t *testing.T) {
	t.Parallel()

	dialer := &net.Dialer{Timeout: 10 * time.Millisecond}
	_, err := dialSSRFProtected(context.Background(), dialer, nil, "tcp", "example.com:8443")
	if err == nil {
		t.Fatalf("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != errorCodeInvalidURL {
		t.Fatalf("expected invalid_url, got %T: %v", err, err)
	}
}

func TestDialSSRFProtected_DialsForAllowedIPLiteral(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	dialer := &net.Dialer{Timeout: 10 * time.Millisecond}
	conn, err := dialSSRFProtected(ctx, dialer, nil, "unix", "93.184.216.34:443")
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected error")
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatalf("expected nil conn on error")
	}
}

func TestDialResolvedIPs_SucceedsWhenAnyIPConnects(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		_ = conn.Close()
	}()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("expected TCPAddr, got %T", listener.Addr())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialResolvedIPs(ctx, dialer, "tcp", strconv.Itoa(tcpAddr.Port), []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatalf("dialResolvedIPs error: %v", err)
	}
	if conn == nil {
		t.Fatalf("expected connection")
	}
	_ = conn.Close()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("accept timed out: %v", ctx.Err())
	}
}

func TestDialResolvedIPs_ReturnsErrorWhenAllIPsFail(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}
	conn, err := dialResolvedIPs(ctx, dialer, "tcp", "0", []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}})
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected error")
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatalf("expected nil connection on error")
	}
}
