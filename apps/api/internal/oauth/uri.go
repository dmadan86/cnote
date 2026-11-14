package oauth

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Rust parses redirect URIs with the `url` crate (WHATWG). Go's net/url disagrees on odd inputs, so we only decide
// natively on "plain" URIs whose serialization is the string itself, and report everything else as unsure; handlers
// then hand the request to Rust before any side effect.

type uri struct {
	scheme, host, port, path, query string
	hasQuery                        bool
	hasFragment, hasCreds           bool
}

var (
	uriRe    = regexp.MustCompile(`^([a-z][a-z0-9+.-]*)://([^/?#]*)(/[^?#]*)?(\?[^#]*)?(#.*)?$`)
	safeRe   = regexp.MustCompile(`^[A-Za-z0-9\-._~!$&'()*+,;=:@%/?]*$`)
	domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	quadRe   = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})(\.(0|[1-9][0-9]{0,2})){3}$`)
)

func isSpecial(scheme string) bool { return scheme == "http" || scheme == "https" }

// plainURI parses the plain subset; ok=false means "unsure, let Rust decide".
func plainURI(raw string) (u uri, ok bool) {
	m := uriRe.FindStringSubmatch(raw)
	if m == nil || !safeRe.MatchString(m[3]+strings.TrimPrefix(m[4], "?")) {
		return u, false
	}
	u = uri{scheme: m[1], path: m[3], hasQuery: m[4] != "", hasFragment: m[5] != ""}
	if u.hasQuery {
		u.query = m[4][1:]
	}
	auth := m[2]
	if i := strings.LastIndex(auth, "@"); i >= 0 {
		if i == 0 || strings.Contains(auth[:i], "@") || strings.ContainsAny(auth[:i], "[]") {
			return u, false
		}
		u.hasCreds = true
		auth = auth[i+1:]
	}
	host, port := auth, ""
	switch {
	case strings.HasPrefix(auth, "["):
		end := strings.Index(auth, "]")
		if end < 0 {
			return u, false
		}
		host, port = auth[:end+1], strings.TrimPrefix(auth[end+1:], ":")
		if host != "[::1]" || (auth[end+1:] != "" && !strings.HasPrefix(auth[end+1:], ":")) {
			return u, false
		}
	case strings.Contains(auth, ":"):
		host, port, _ = strings.Cut(auth, ":")
	}
	switch {
	case quadRe.MatchString(host):
		a, err := netip.ParseAddr(host)
		if err != nil || !a.Is4() {
			return u, false
		}
	case host == "[::1]":
	case domainRe.MatchString(host) && strings.IndexFunc(host[strings.LastIndex(host, ".")+1:], func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0:
	default:
		return u, false
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n > 65535 || strconv.Itoa(n) != port || (u.scheme == "http" && n == 80) || (u.scheme == "https" && n == 443) {
			return u, false
		}
	}
	if strings.Contains(strings.ToLower(u.path), "%2e") || hasDotSegment(u.path) {
		return u, false
	}
	u.host, u.port = host, port
	return u, true
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// normalized reports whether `raw` equals its own WHATWG serialization (http(s) need a non-empty path).
func (u uri) normalized() bool { return !(isSpecial(u.scheme) && u.path == "") }

func (u uri) loopback() bool {
	switch {
	case u.host == "localhost" || u.host == "[::1]":
		return true
	case quadRe.MatchString(u.host):
		return strings.HasPrefix(u.host, "127.")
	}
	return false
}

// hostLabel is host_label(): what the consent page shows as "where you will be sent back".
func (u uri) hostLabel() string {
	h := u.host
	if isSpecial(u.scheme) {
		if u.port != "" {
			return h + ":" + u.port
		}
		return h
	}
	return u.scheme + "://" + h
}

var dangerous = map[string]bool{"javascript": true, "data": true, "vbscript": true, "file": true, "blob": true, "about": true, "ftp": true, "ws": true, "wss": true}

// validateRedirectURI is validate_redirect_uri. msg=="" means valid; sure=false means Rust might answer differently.
func validateRedirectURI(raw string) (msg string, sure bool) {
	if len(raw) > 2048 {
		return "redirect_uri too long", true
	}
	if raw == "" || strings.HasPrefix(raw, "/") {
		return "redirect_uri is not a valid absolute URI", true
	}
	if i := strings.Index(raw, ":"); i > 0 && !strings.Contains(raw, "//") && !strings.ContainsAny(raw, "#@ \\") && dangerous[raw[:i]] {
		return "redirect_uri scheme not allowed", true
	}
	u, ok := plainURI(raw)
	if !ok {
		return "", false
	}
	if u.hasFragment || u.hasCreds {
		return "redirect_uri must not contain a fragment or credentials", true
	}
	switch {
	case u.scheme == "https":
		return "", true
	case u.scheme == "http":
		if u.loopback() {
			return "", true
		}
		return "http redirect_uri is only allowed for loopback", true
	case dangerous[u.scheme]:
		return "redirect_uri scheme not allowed", true
	}
	return "", true
}

// formEscape is application/x-www-form-urlencoded as the url crate writes it: alphanumerics and *-._ stay, space is +.
func formEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// withQuery is with_query() for a plain, normalized base URI.
func withQuery(base string, u uri, pairs [][2]string) string {
	var b strings.Builder
	b.WriteString(base)
	switch {
	case !u.hasQuery:
		b.WriteByte('?')
	case u.query != "":
		b.WriteByte('&')
	}
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(formEscape(p[0]) + "=" + formEscape(p[1]))
	}
	return b.String()
}
