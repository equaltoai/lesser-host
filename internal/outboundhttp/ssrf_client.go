package outboundhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const DefaultSSRFProtectedTimeout = 10 * time.Second

var ErrRedirectNotAllowed = errors.New("redirects not allowed")

type IPResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type SSRFProtectedClientOption func(*ssrfProtectedClientConfig)

type ssrfProtectedClientConfig struct {
	resolver IPResolver
	timeout  time.Duration
}

func WithResolver(resolver IPResolver) SSRFProtectedClientOption {
	return func(cfg *ssrfProtectedClientConfig) {
		cfg.resolver = resolver
	}
}

func WithTimeout(timeout time.Duration) SSRFProtectedClientOption {
	return func(cfg *ssrfProtectedClientConfig) {
		if timeout > 0 {
			cfg.timeout = timeout
		}
	}
}

func NewSSRFProtectedClient(base *http.Client, opts ...SSRFProtectedClientOption) *http.Client {
	cfg := ssrfProtectedClientConfig{timeout: DefaultSSRFProtectedTimeout}
	if base != nil && base.Timeout > 0 {
		cfg.timeout = base.Timeout
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	transport := http.DefaultTransport
	protectTransport := true
	if base != nil && base.Transport != nil {
		transport = base.Transport
		protectTransport = false
	}

	if tr, ok := transport.(*http.Transport); ok {
		clone := tr.Clone()
		// Never use environment proxies in guarded outbound clients. SSRF
		// enforcement is only sound for direct connections to the request host.
		clone.Proxy = nil
		if protectTransport {
			clone.DialContext = newSSRFProtectedDialContext(cfg.resolver)
			transport = ssrfProtectedRoundTripper{base: clone, resolver: cfg.resolver}
		} else {
			// Caller-provided transports are preserved as the test/injection seam, but
			// proxies are still stripped when the transport is cloneable.
			transport = clone
		}
	} else if protectTransport {
		transport = ssrfProtectedRoundTripper{base: transport, resolver: cfg.resolver}
	}

	return &http.Client{
		Timeout:   cfg.timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return ErrRedirectNotAllowed
		},
	}
}

type ssrfProtectedRoundTripper struct {
	base     http.RoundTripper
	resolver IPResolver
}

func (t ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is required")
	}
	if err := validateOutboundURL(req.Context(), t.resolver, req.URL); err != nil {
		return nil, err
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func validateOutboundURL(ctx context.Context, resolver IPResolver, u *url.URL) error {
	if u == nil {
		return errors.New("url is required")
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "https" {
		return errors.New("url scheme must be https")
	}
	if port := strings.TrimSpace(u.Port()); port != "" && port != "443" {
		return fmt.Errorf("port %q is not allowed", port)
	}
	host, err := normalizeOutboundHost(u.Hostname())
	if err != nil {
		return err
	}
	_, err = resolveAndValidateOutboundHostIPs(ctx, resolver, host)
	return err
}

func newSSRFProtectedDialContext(resolver IPResolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := parseAndValidateDialTarget(addr)
		if err != nil {
			return nil, err
		}

		ips, err := resolveAndValidateOutboundHostIPs(ctx, resolver, host)
		if err != nil {
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
		if lastErr == nil {
			lastErr = errors.New("host did not resolve")
		}
		return nil, lastErr
	}
}

func parseAndValidateDialTarget(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	port = strings.TrimSpace(port)
	if port != "443" {
		return "", "", fmt.Errorf("port %q is not allowed", port)
	}
	host, err = normalizeOutboundHost(host)
	if err != nil {
		return "", "", err
	}
	return host, port, nil
}

func resolveAndValidateOutboundHostIPs(ctx context.Context, resolver IPResolver, host string) ([]net.IPAddr, error) {
	normalized, err := normalizeOutboundHost(host)
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(normalized); ip != nil {
		return validateOutboundIP(ip)
	}

	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(contextOrBackground(ctx), normalized)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("failed to resolve host")
	}
	if err := validateOutboundResolvedIPs(ips); err != nil {
		return nil, err
	}
	return ips, nil
}

func normalizeOutboundHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("host is required")
	}

	if strings.Contains(host, ":") {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	host = strings.Trim(host, "[]")
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", errors.New("host is required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", errors.New("host is not allowed")
	}
	return host, nil
}

func validateOutboundIP(ip net.IP) ([]net.IPAddr, error) {
	if isDeniedIP(ip) {
		return nil, errors.New("ip is not allowed")
	}
	return []net.IPAddr{{IP: ip}}, nil
}

func validateOutboundResolvedIPs(ips []net.IPAddr) error {
	for _, ipAddr := range ips {
		if isDeniedIP(ipAddr.IP) {
			return errors.New("host resolves to blocked ip")
		}
	}
	return nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
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
