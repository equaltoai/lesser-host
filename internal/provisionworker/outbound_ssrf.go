package provisionworker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

func ssrfProtectedHTTPClient(base *http.Client) *http.Client {
	timeout := 10 * time.Second
	if base != nil && base.Timeout > 0 {
		timeout = base.Timeout
	}

	transport := http.DefaultTransport
	if base != nil && base.Transport != nil {
		transport = base.Transport
	}

	if base == nil || base.Transport == nil {
		if tr, ok := transport.(*http.Transport); ok {
			clone := tr.Clone()
			clone.Proxy = nil
			clone.DialContext = newSSRFProtectedDialContext()
			transport = clone
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects not allowed")
		},
	}
}

func newSSRFProtectedDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		ips, err := resolveAndValidateOutboundHostIPs(ctx, host)
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
		return nil, lastErr
	}
}

func resolveAndValidateOutboundHostIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	normalized, err := normalizeOutboundHost(host)
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(normalized); ip != nil {
		return validateOutboundIP(ip)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(contextOrBackground(ctx), normalized)
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
