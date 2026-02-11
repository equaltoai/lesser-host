package trust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/idna"
)

const (
	linkPreviewPolicyVersion = "v1"

	linkPreviewCacheTTL      = 24 * time.Hour
	linkPreviewMaxRedirects  = 5
	linkPreviewMaxHTMLBytes  = int64(1024 * 1024)     // 1MiB
	linkPreviewMaxImageBytes = int64(5 * 1024 * 1024) // 5MiB
	linkPreviewFetchTimeout  = 5 * time.Second
	linkPreviewUA            = "lesser-host/preview-fetcher"
)

type linkPreviewError struct {
	Code    string
	Message string
}

func (e *linkPreviewError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type fetchedLinkPreview struct {
	PolicyVersion string

	NormalizedURL string
	ResolvedURL   string
	RedirectChain []string

	Title       string
	Description string
	ImageURL    string
}

func linkPreviewID(policyVersion, normalizedURL string) string {
	sum := sha256.Sum256([]byte(policyVersion + ":" + normalizedURL))
	return hex.EncodeToString(sum[:])
}

func normalizeLinkURL(raw string) (string, *url.URL, error) {
	u, err := parseRawLinkURL(raw)
	if err != nil {
		return "", nil, err
	}

	if err := canonicalizeLinkSchemeAndUserinfo(u); err != nil {
		return "", nil, err
	}
	if err := canonicalizeLinkHostAndPort(u); err != nil {
		return "", nil, err
	}
	canonicalizeLinkPathQuery(u)

	u.Fragment = ""
	normalized := u.String()
	return normalized, u, nil
}

func parseRawLinkURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, &linkPreviewError{Code: "invalid_url", Message: "url is required"}
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
	}
	return u, nil
}

func canonicalizeLinkSchemeAndUserinfo(u *url.URL) error {
	if u == nil {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
	}

	u.Scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	switch u.Scheme {
	case schemeHTTP, schemeHTTPS:
		// ok
	default:
		return &linkPreviewError{Code: "invalid_url", Message: "url scheme must be http or https"}
	}

	if u.User != nil {
		return &linkPreviewError{Code: "invalid_url", Message: "url must not include userinfo"}
	}

	return nil
}

func canonicalizeLinkHostAndPort(u *url.URL) error {
	if u == nil {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return &linkPreviewError{Code: "invalid_url", Message: "url host is required"}
	}

	asciiHost, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid host"}
	}
	asciiHost = strings.ToLower(strings.TrimSpace(asciiHost))
	if asciiHost == "" {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid host"}
	}

	if err := validateDefaultPort(u); err != nil {
		return err
	}

	u.Host = asciiHost
	return nil
}

func validateDefaultPort(u *url.URL) error {
	if u == nil {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
	}

	port := strings.TrimSpace(u.Port())
	if port == "" {
		return nil
	}

	n, err := strconv.Atoi(port)
	if err != nil {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid port"}
	}
	if n <= 0 || n > 65535 {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid port"}
	}

	// Only allow default ports (SSRF hardening).
	allowed := defaultPortForScheme(u.Scheme)
	if allowed == 0 || n != allowed {
		return &linkPreviewError{Code: "invalid_url", Message: "non-default ports are not allowed"}
	}
	return nil
}

func defaultPortForScheme(scheme string) int {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case schemeHTTP:
		return 80
	case schemeHTTPS:
		return 443
	default:
		return 0
	}
}

func canonicalizeLinkPathQuery(u *url.URL) {
	if u == nil {
		return
	}

	if u.Path == "" {
		u.Path = "/"
	}
	u.Path = path.Clean(u.Path)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}

	// Canonicalize query ordering/encoding.
	if u.RawQuery != "" {
		u.RawQuery = u.Query().Encode()
	}
}

func validateOutboundURL(ctx context.Context, resolver ipResolver, u *url.URL) error {
	if u == nil {
		return &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
	}

	if err := validateOutboundScheme(u.Scheme); err != nil {
		return err
	}

	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return &linkPreviewError{Code: "invalid_url", Message: "url host is required"}
	}

	if isDeniedHostname(host) {
		return &linkPreviewError{Code: errorCodeBlockedSSRF, Message: "host is not allowed"}
	}

	if ip := net.ParseIP(host); ip != nil {
		return validateOutboundIP(ip)
	}

	ips, err := resolveHostIPs(ctx, resolver, host)
	if err != nil {
		return err
	}
	return validateResolvedIPAddrs(ips)
}

func validateOutboundScheme(scheme string) error {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case schemeHTTP, schemeHTTPS:
		return nil
	default:
		return &linkPreviewError{Code: "invalid_url", Message: "url scheme must be http or https"}
	}
}

func validateOutboundIP(ip net.IP) error {
	if isDeniedIP(ip) {
		return &linkPreviewError{Code: errorCodeBlockedSSRF, Message: "ip is not allowed"}
	}
	return nil
}

func isDeniedHostname(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func resolveHostIPs(ctx context.Context, resolver ipResolver, host string) ([]net.IPAddr, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, &linkPreviewError{Code: errorCodeBlockedSSRF, Message: "failed to resolve host"}
	}
	if len(ips) == 0 {
		return nil, &linkPreviewError{Code: errorCodeBlockedSSRF, Message: "host did not resolve"}
	}
	return ips, nil
}

func validateResolvedIPAddrs(ips []net.IPAddr) error {
	for _, ipAddr := range ips {
		if isDeniedIP(ipAddr.IP) {
			return &linkPreviewError{Code: errorCodeBlockedSSRF, Message: "host resolves to a blocked ip"}
		}
	}
	return nil
}

func isDeniedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()

	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}

	for _, pfx := range deniedIPRanges() {
		if pfx.Contains(addr) {
			return true
		}
	}

	// Also block RFC1918 + ULA via stdlib helpers.
	if ip.IsPrivate() {
		return true
	}

	return false
}

func deniedIPRanges() []netip.Prefix {
	// Keep this small and explicit; add ranges as SSRF regressions are found.
	return []netip.Prefix{
		mustPrefix("0.0.0.0/8"),
		mustPrefix("10.0.0.0/8"),
		mustPrefix("100.64.0.0/10"), // CGNAT
		mustPrefix("127.0.0.0/8"),
		mustPrefix("169.254.0.0/16"), // link-local + metadata
		mustPrefix("172.16.0.0/12"),
		mustPrefix("192.0.0.0/24"), // IETF protocol assignments
		mustPrefix("192.0.2.0/24"), // TEST-NET-1
		mustPrefix("192.168.0.0/16"),
		mustPrefix("198.18.0.0/15"),   // benchmark
		mustPrefix("198.51.100.0/24"), // TEST-NET-2
		mustPrefix("203.0.113.0/24"),  // TEST-NET-3
		mustPrefix("224.0.0.0/4"),     // multicast
		mustPrefix("240.0.0.0/4"),     // reserved

		mustPrefix("::/128"),
		mustPrefix("::1/128"),
		mustPrefix("fc00::/7"),      // ULA
		mustPrefix("fe80::/10"),     // link-local
		mustPrefix("ff00::/8"),      // multicast
		mustPrefix("2001:db8::/32"), // documentation
	}
}

func mustPrefix(cidr string) netip.Prefix {
	pfx, err := netip.ParsePrefix(cidr)
	if err != nil {
		panic(err)
	}
	return pfx
}

func fetchLinkPreview(ctx context.Context, resolver ipResolver, rawURL string) (*fetchedLinkPreview, error) {
	normalized, u, err := normalizeLinkURL(rawURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, linkPreviewFetchTimeout)
	defer cancel()

	if validateErr := validateOutboundURL(ctx, resolver, u); validateErr != nil {
		return &fetchedLinkPreview{
			PolicyVersion: linkPreviewPolicyVersion,
			NormalizedURL: normalized,
		}, validateErr
	}

	client := newPreviewHTTPClient(linkPreviewFetchTimeout, resolver)
	finalURL, chain, body, contentType, err := fetchWithRedirects(ctx, resolver, client, u, linkPreviewMaxRedirects, linkPreviewMaxHTMLBytes)
	if err != nil {
		return &fetchedLinkPreview{
			PolicyVersion: linkPreviewPolicyVersion,
			NormalizedURL: normalized,
			RedirectChain: chain,
			ResolvedURL:   safeURLString(finalURL),
		}, err
	}

	out := &fetchedLinkPreview{
		PolicyVersion: linkPreviewPolicyVersion,
		NormalizedURL: normalized,
		ResolvedURL:   safeURLString(finalURL),
		RedirectChain: chain,
	}

	if isHTMLContentType(contentType) {
		meta := extractLinkPreviewMeta(finalURL, body)
		out.Title = meta.Title
		out.Description = meta.Description
		out.ImageURL = meta.ImageURL
	}

	return out, nil
}

type linkPreviewMeta struct {
	Title       string
	Description string
	ImageURL    string
}

type linkPreviewMetaParser struct {
	inHead    bool
	titleText string
	meta      linkPreviewMeta
}

func extractLinkPreviewMeta(base *url.URL, doc []byte) linkPreviewMeta {
	tok := html.NewTokenizer(bytes.NewReader(doc))

	parser := linkPreviewMetaParser{}
	for {
		tt := tok.Next()
		switch tt {
		case html.ErrorToken:
			return parser.result()
		default:
			parser.consume(tt, tok, base)
		}
	}
}

func (p *linkPreviewMetaParser) consume(tt html.TokenType, tok *html.Tokenizer, base *url.URL) {
	if p == nil || tok == nil {
		return
	}

	switch tt {
	case html.StartTagToken, html.SelfClosingTagToken:
		p.consumeStartTag(tok, base)
	case html.EndTagToken:
		p.consumeEndTag(tok)
	}
}

func (p *linkPreviewMetaParser) consumeStartTag(tok *html.Tokenizer, base *url.URL) {
	name, hasAttr := tok.TagName()
	tag := strings.ToLower(string(name))

	if tag == "head" {
		p.inHead = true
		return
	}

	if tag == "title" && p.inHead && p.meta.Title == "" {
		p.titleText = readTokenizerText(tok)
		return
	}

	if tag != "meta" || !p.inHead || !hasAttr {
		return
	}

	property, metaName, content := readLinkPreviewMetaAttrs(tok)
	applyLinkPreviewMeta(&p.meta, base, property, metaName, content)
}

func (p *linkPreviewMetaParser) consumeEndTag(tok *html.Tokenizer) {
	name, _ := tok.TagName()
	tag := strings.ToLower(string(name))
	if tag == "head" {
		p.inHead = false
	}
}

func (p *linkPreviewMetaParser) result() linkPreviewMeta {
	if p == nil {
		return linkPreviewMeta{}
	}
	if p.meta.Title == "" {
		p.meta.Title = strings.TrimSpace(p.titleText)
	}
	return p.meta
}

func readTokenizerText(tok *html.Tokenizer) string {
	if tok == nil {
		return ""
	}
	if tok.Next() == html.TextToken {
		return string(tok.Text())
	}
	return ""
}

func readLinkPreviewMetaAttrs(tok *html.Tokenizer) (property, metaName, content string) {
	if tok == nil {
		return "", "", ""
	}

	for {
		k, v, more := tok.TagAttr()
		key := strings.ToLower(string(k))
		val := strings.TrimSpace(string(v))
		switch key {
		case "property":
			property = strings.ToLower(val)
		case "name":
			metaName = strings.ToLower(val)
		case "content":
			content = val
		}
		if !more {
			break
		}
	}

	return strings.TrimSpace(property), strings.TrimSpace(metaName), strings.TrimSpace(content)
}

func applyLinkPreviewMeta(meta *linkPreviewMeta, base *url.URL, property, metaName, content string) {
	if meta == nil {
		return
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return
	}

	switch {
	case (property == "og:title" || property == "twitter:title") && meta.Title == "":
		meta.Title = content
	case (property == "og:description" || property == "twitter:description") && meta.Description == "":
		meta.Description = content
	case (property == "og:image" || property == "og:image:url" || property == "twitter:image") && meta.ImageURL == "":
		meta.ImageURL = resolveMaybeRelativeURL(base, content)
	case metaName == "description" && meta.Description == "":
		meta.Description = content
	}
}

func resolveMaybeRelativeURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base == nil {
		return u.String()
	}
	return base.ResolveReference(u).String()
}

func isHTMLContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(ct, "text/html") || strings.Contains(ct, "text/html")
}

func newPreviewHTTPClient(timeout time.Duration, resolver ipResolver) *http.Client {
	dialContext := newSSRFProtectedDialContext(resolver)
	tr := &http.Transport{
		// Never use environment proxies here. SSRF enforcement is implemented at dial-time and
		// is only sound for direct connections to the request host.
		Proxy:       nil,
		DialContext: dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		// We follow redirects manually to capture and validate each hop.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newSSRFProtectedDialContext(resolver ipResolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	if resolver == nil {
		resolver = net.DefaultResolver
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		host = strings.ToLower(strings.TrimSpace(host))
		port = strings.TrimSpace(port)
		if host == "" {
			return nil, &linkPreviewError{Code: "invalid_url", Message: "url host is required"}
		}
		if port != "80" && port != "443" {
			return nil, &linkPreviewError{Code: "invalid_url", Message: "non-default ports are not allowed"}
		}

		if isDeniedHostname(host) {
			return nil, &linkPreviewError{Code: errorCodeBlockedSSRF, Message: "host is not allowed"}
		}

		if ip := net.ParseIP(host); ip != nil {
			if err := validateOutboundIP(ip); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		}

		ips, err := resolveHostIPs(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		if err := validateResolvedIPAddrs(ips); err != nil {
			return nil, err
		}

		var lastErr error
		for _, ipAddr := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func fetchWithRedirects(
	ctx context.Context,
	resolver ipResolver,
	client *http.Client,
	start *url.URL,
	maxRedirects int,
	maxBytes int64,
) (*url.URL, []string, []byte, string, error) {
	if client == nil {
		return nil, nil, nil, "", &linkPreviewError{Code: "internal", Message: "http client is required"}
	}
	if start == nil {
		return nil, nil, nil, "", &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
	}

	current := start
	chain := []string{start.String()}

	for redirects := 0; redirects <= maxRedirects; redirects++ {
		if err := validateOutboundURL(ctx, resolver, current); err != nil {
			return current, chain, nil, "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return current, chain, nil, "", &linkPreviewError{Code: "invalid_url", Message: "invalid url"}
		}
		req.Header.Set("User-Agent", linkPreviewUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, err := client.Do(req)
		if err != nil {
			var pe *linkPreviewError
			if errors.As(err, &pe) {
				return current, chain, nil, "", pe
			}
			return current, chain, nil, "", &linkPreviewError{Code: "fetch_failed", Message: "fetch failed"}
		}

		contentType := resp.Header.Get("Content-Type")
		if isRedirectStatus(resp.StatusCode) {
			_ = drainAndClose(resp.Body)
			next, followErr := followRedirect(current, resp)
			if followErr != nil {
				return current, chain, nil, contentType, followErr
			}
			current = next
			chain = append(chain, current.String())
			continue
		}

		body, err := readBodyLimit(resp.Body, maxBytes)
		_ = resp.Body.Close()
		if err != nil {
			return current, chain, nil, contentType, err
		}
		return current, chain, body, contentType, nil
	}

	return current, chain, nil, "", &linkPreviewError{Code: "fetch_failed", Message: "too many redirects"}
}

func followRedirect(current *url.URL, resp *http.Response) (*url.URL, error) {
	if resp == nil {
		return nil, &linkPreviewError{Code: "internal", Message: "redirect response is required"}
	}

	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if loc == "" {
		return nil, &linkPreviewError{Code: "fetch_failed", Message: "redirect missing location"}
	}

	next, err := resolveRedirect(current, loc)
	if err != nil {
		return nil, err
	}

	_, normalizedNext, err := normalizeLinkURL(next.String())
	if err != nil {
		return nil, err
	}
	return normalizedNext, nil
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func resolveRedirect(base *url.URL, location string) (*url.URL, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return nil, &linkPreviewError{Code: "fetch_failed", Message: "redirect missing location"}
	}

	locURL, err := url.Parse(location)
	if err != nil {
		return nil, &linkPreviewError{Code: "fetch_failed", Message: "invalid redirect location"}
	}

	if base == nil {
		return locURL, nil
	}
	return base.ResolveReference(locURL), nil
}

func readBodyLimit(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, &linkPreviewError{Code: "internal", Message: "invalid read limit"}
	}

	lr := &io.LimitedReader{R: r, N: maxBytes + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, &linkPreviewError{Code: "fetch_failed", Message: "read failed"}
	}
	if int64(len(buf)) > maxBytes {
		return nil, &linkPreviewError{Code: "too_large", Message: "response too large"}
	}
	return buf, nil
}

func drainAndClose(r io.ReadCloser) error {
	if r == nil {
		return nil
	}
	_, _ = io.CopyN(io.Discard, r, 1024)
	return r.Close()
}

func safeURLString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.TrimSpace(u.String())
}
