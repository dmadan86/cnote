package sites

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"
)

func TLSAsk(ctx context.Context, pool *pgxpool.Pool, domain string) (bool, error) {
	return tlsAsk(ctx, pool, domain)
}

func NormalizeHostname(h string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil || len(ascii) > 253 || net.ParseIP(ascii) != nil || !strings.Contains(ascii, ".") {
		return "", fmt.Errorf("invalid hostname")
	}
	for _, label := range strings.Split(ascii, ".") {
		if len(label) == 0 || len(label) > 63 || !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`).MatchString(label) {
			return "", fmt.Errorf("invalid hostname")
		}
	}
	if _, err = publicsuffix.EffectiveTLDPlusOne(ascii); err != nil {
		return "", fmt.Errorf("invalid hostname")
	}
	for i, raw := range append([]string{os.Getenv("SITES_DOMAIN"), os.Getenv("PUBLIC_BASE_URL")}, strings.Split(os.Getenv("APP_HOSTS"), ",")...) {
		if u, e := url.Parse(raw); e == nil && u.Hostname() != "" {
			raw = u.Hostname()
		}
		raw = strings.Trim(strings.ToLower(raw), ".")
		if i > 0 {
			if registrable, e := publicsuffix.EffectiveTLDPlusOne(raw); e == nil {
				raw = registrable
			}
		}
		if raw != "" && (ascii == raw || strings.HasSuffix(ascii, "."+raw) || strings.HasSuffix(raw, "."+ascii)) {
			return "", fmt.Errorf("reserved hostname")
		}
	}
	return ascii, nil
}

func NormalizeEmbed(provider, input string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "<") {
		m := regexp.MustCompile(`(?i)\bsrc\s*=\s*["']([^"']+)["']`).FindStringSubmatch(input)
		if len(m) != 2 {
			return "", fmt.Errorf("invalid iframe")
		}
		input = strings.ReplaceAll(m[1], "&amp;", "&")
	}
	id := input
	if u, e := url.Parse(input); e == nil && u.IsAbs() {
		host := strings.ToLower(u.Hostname())
		if u.Scheme != "https" || u.User != nil {
			return "", fmt.Errorf("invalid URL")
		}
		switch provider {
		case "youtube":
			if host == "youtu.be" {
				id = strings.Trim(u.Path, "/")
			} else if host == "www.youtube.com" || host == "youtube.com" || host == "www.youtube-nocookie.com" {
				id = u.Query().Get("v")
				if strings.HasPrefix(u.Path, "/embed/") {
					id = strings.TrimPrefix(u.Path, "/embed/")
				}
			} else {
				return "", fmt.Errorf("invalid host")
			}
		case "google_forms":
			if host != "docs.google.com" {
				return "", fmt.Errorf("invalid host")
			}
			m := regexp.MustCompile(`^/forms/d/e/([^/]+)/(?:viewform)?$`).FindStringSubmatch(u.Path)
			if len(m) != 2 {
				return "", fmt.Errorf("invalid form")
			}
			id = m[1]
		case "calendly":
			if host != "calendly.com" && host != "www.calendly.com" {
				return "", fmt.Errorf("invalid host")
			}
			id = strings.Trim(u.Path, "/")
		case "google_maps":
			if host != "www.google.com" && host != "google.com" && host != "maps.google.com" {
				return "", fmt.Errorf("invalid host")
			}
			id = u.Query().Get("q")
			if id == "" {
				id = u.Query().Get("query")
			}
		default:
			return "", fmt.Errorf("invalid provider")
		}
	}
	if EmbedSrc(provider, id) == "" {
		return "", fmt.Errorf("invalid embed")
	}
	return id, nil
}
func EmbedSrc(provider, id string) string {
	switch provider {
	case "youtube":
		if regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`).MatchString(id) {
			return "https://www.youtube-nocookie.com/embed/" + id
		}
	case "google_forms":
		if regexp.MustCompile(`^[A-Za-z0-9_-]{20,120}$`).MatchString(id) {
			return "https://docs.google.com/forms/d/e/" + id + "/viewform?embedded=true"
		}
	case "calendly":
		if regexp.MustCompile(`^[a-z0-9_-]{1,60}(/[a-z0-9_-]{1,60})?$`).MatchString(id) {
			return "https://calendly.com/" + id
		}
	case "google_maps":
		if len(id) > 0 && len(id) <= 2000 && strings.TrimSpace(id) != "" && !strings.ContainsFunc(id, unicode.IsControl) {
			return "https://www.google.com/maps?q=" + url.QueryEscape(id) + "&output=embed"
		}
	}
	return ""
}
