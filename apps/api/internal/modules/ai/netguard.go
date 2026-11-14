package ai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// SSRF guard for workspace-supplied endpoints (crates/cnote-ai/src/netguard.rs).

// errUnparsable marks a URL whose Rust parser error text this port does not reproduce; callers fall back to Rust.
var errUnparsable = errors.New("unparsable url")

func privateAllowed() bool {
	v := os.Getenv("AI_ALLOW_PRIVATE_ENDPOINTS")
	return v == "1" || v == "true"
}

func isPublic(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil { // plain or ::ffff:a.b.c.d
		return !(v4[0] == 127 || v4[0] == 10 || (v4[0] == 172 && v4[1]&0xf0 == 16) || (v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 169 && v4[1] == 254) || v4.Equal(net.IPv4zero) || v4.Equal(net.IPv4bcast) ||
			(v4[0] == 192 && v4[1] == 0 && v4[2] == 2) || (v4[0] == 198 && v4[1] == 51 && v4[2] == 100) || (v4[0] == 203 && v4[1] == 0 && v4[2] == 113) ||
			v4[0] == 0 || (v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) || (v4[0] == 198 && (v4[1] == 18 || v4[1] == 19)))
	}
	return !(ip.IsLoopback() || ip.IsUnspecified() || ip[0]&0xfe == 0xfc || (ip[0] == 0xfe && ip[1]&0xc0 == 0x80))
}

// CheckEndpoint validates a base URL: https, no credentials, every resolved address public. The returned
// error's text is the Rust message ("base_url ..."); callers map it to invalid or forbidden.
func CheckEndpoint(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err == nil && u.Scheme == "" && !strings.Contains(raw, ":"):
		return errors.New("invalid base_url: relative URL without a base")
	case err != nil || u.Scheme == "" || u.Opaque != "":
		return errUnparsable
	case (u.Scheme == "http" || u.Scheme == "https") && u.Host == "":
		return errors.New("invalid base_url: empty host")
	}
	if u.User != nil {
		if pw, _ := u.User.Password(); u.User.Username() != "" || pw != "" { // "http://:@h" has none, as in the url crate
			return errors.New("base_url must not contain credentials")
		}
	}
	if privateAllowed() {
		if u.Scheme == "https" || u.Scheme == "http" {
			return nil
		}
		return errors.New("base_url must be http(s)")
	}
	if u.Scheme != "https" {
		return errors.New("base_url must use https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("base_url has no host")
	}
	if strings.Contains(u.Host, "[") { // tokio's lookup_host does not take bracketed IPv6 literals
		return errors.New("base_url host does not resolve")
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return errors.New("base_url host does not resolve")
	}
	for _, a := range addrs {
		if !isPublic(a.IP) {
			return errors.New("base_url must resolve to public addresses only")
		}
	}
	if len(addrs) == 0 {
		return errors.New("base_url must resolve to public addresses only")
	}
	return nil
}

// httpClient talks to model APIs and hooks: no redirects (a 3xx must not bounce us to an internal host).
func httpClient() *http.Client {
	return &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
