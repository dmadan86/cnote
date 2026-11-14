package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"time"
)

// SSRF guard for client metadata documents (netguard.rs): public addresses only, the connection is pinned to the
// addresses that were checked (no DNS rebinding between check and connect), no redirects, no proxy, short timeout,
// hard size cap.

func allowPrivate() bool {
	v := os.Getenv("OAUTH_CIMD_ALLOW_PRIVATE")
	return v == "1" || v == "true"
}

func isPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is4() {
		o := ip.As4()
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) ||
			o[0] == 0 || o[0] >= 224 ||
			(o[0] == 192 && o[1] == 0 && (o[2] == 2 || o[2] == 0)) || (o[0] == 198 && o[1] == 51 && o[2] == 100) || (o[0] == 203 && o[1] == 0 && o[2] == 113) ||
			(o[0] == 100 && o[1] >= 64 && o[1] <= 127) || (o[0] == 198 && (o[1] == 18 || o[1] == 19)))
	}
	b := ip.As16()
	s := func(i int) uint16 { return uint16(b[2*i])<<8 | uint16(b[2*i+1]) }
	zero6 := true
	for i := 0; i < 6; i++ {
		zero6 = zero6 && s(i) == 0
	}
	return !(ip.IsLoopback() || ip.IsUnspecified() || zero6 ||
		(s(0) == 0x64 && s(1) == 0xff9b && s(2) <= 1) || s(0) == 0x2002 || (s(0) == 0x2001 && s(1) == 0) ||
		s(0)&0xfe00 == 0xfc00 || s(0)&0xffc0 == 0xfe80 || s(0)&0xffc0 == 0xfec0 || s(0)&0xff00 == 0xff00)
}

// fetchJSON GETs a JSON document; the error strings are shown to the user on the consent page, so they match Rust's.
func fetchJSON(ctx context.Context, raw string, maxBytes int) (any, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	if u.User != nil {
		return nil, errors.New("URL must not contain credentials")
	}
	private := allowPrivate()
	if u.Scheme != "https" && !(private && u.Scheme == "http") {
		return nil, errors.New("URL must use https")
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("URL has no host")
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(lctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("host does not resolve")
	}
	if !private {
		for _, ip := range ips {
			if !isPublic(ip) {
				return nil, errors.New("host must resolve to public addresses only")
			}
		}
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, // HTTPS_PROXY would resolve the host itself and bypass the pinned addresses
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var last error
				for _, ip := range ips {
					c, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
					if err == nil {
						return c, nil
					}
					last = err
				}
				return nil, last
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("metadata fetch failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("metadata fetch returned %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, errors.New("metadata read failed")
	}
	if len(body) > maxBytes {
		return nil, errors.New("metadata document too large")
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil, errors.New("metadata is not valid JSON")
	}
	return v, nil
}
