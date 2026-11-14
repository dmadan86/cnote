// Package netguard is the SSRF guard for workspace-supplied endpoints (port of crates/cnote-ai/src/netguard.rs).
// Unlike Rust it also re-checks the connected IP at dial time, so DNS rebinding between check and use fails.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"syscall"
	"time"
)

// PrivateAllowed: self-hosted editions may point at private servers (AI_ALLOW_PRIVATE_ENDPOINTS=1|true).
func PrivateAllowed() bool {
	v := os.Getenv("AI_ALLOW_PRIVATE_ENDPOINTS")
	return v == "1" || v == "true"
}

func prefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

var blocked4 = []netip.Prefix{
	prefix("0.0.0.0/8"), prefix("10.0.0.0/8"), prefix("127.0.0.0/8"), prefix("169.254.0.0/16"),
	prefix("172.16.0.0/12"), prefix("192.168.0.0/16"), prefix("255.255.255.255/32"),
	prefix("192.0.2.0/24"), prefix("198.51.100.0/24"), prefix("203.0.113.0/24"), // documentation
	prefix("100.64.0.0/10"), prefix("198.18.0.0/15"), // CGNAT, benchmarking
}

// IsPublic classifies an address exactly like the Rust is_public.
func IsPublic(ip netip.Addr) bool {
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	if ip.Is4() {
		for _, p := range blocked4 {
			if p.Contains(ip) {
				return false
			}
		}
		return true
	}
	return !(ip.IsLoopback() || ip.IsUnspecified() || prefix("fc00::/7").Contains(ip) || prefix("fe80::/10").Contains(ip))
}

// CheckEndpoint validates a base URL: https, no credentials, every resolved address public.
func CheckEndpoint(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url: %v", err)
	}
	if u.User != nil {
		return errors.New("base_url must not contain credentials")
	}
	if PrivateAllowed() {
		if u.Scheme != "https" && u.Scheme != "http" {
			return errors.New("base_url must be http(s)")
		}
		return nil
	}
	if u.Scheme != "https" {
		return errors.New("base_url must use https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("base_url has no host")
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return errors.New("base_url host does not resolve")
	}
	for _, a := range addrs {
		if !IsPublic(a) {
			return errors.New("base_url must resolve to public addresses only")
		}
	}
	if len(addrs) == 0 {
		return errors.New("base_url must resolve to public addresses only")
	}
	return nil
}

// Client: no redirects (a 3xx must not bounce us to an internal host), no env proxy, a 120s timeout, and a
// dialer that refuses non-public connected addresses unless private endpoints are allowed.
func Client() *http.Client {
	d := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if PrivateAllowed() {
				return nil
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !IsPublic(ap.Addr()) {
				return errors.New("base_url must resolve to public addresses only")
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DialContext: d.DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 30 * time.Second},
	}
}
