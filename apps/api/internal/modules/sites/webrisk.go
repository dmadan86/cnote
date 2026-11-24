package sites

// Google Web Risk Lookup client (design 18.11, 18.12). The key comes from WEBRISK_API_KEY and travels in a header only;
// it is never logged and never part of an error. Every lookup is counted first against a daily cap (the shared rate
// bucket) and a monthly cap, so a runaway sweep cannot spend more than the operator allowed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/idna"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

// WebRiskHTTP and WebRiskURL are the injection points for tests; production uses the defaults.
var (
	WebRiskHTTP = &http.Client{Timeout: 10 * time.Second}
	WebRiskURL  = "https://webrisk.googleapis.com/v1/uris:search"
)

const (
	linksPerScan   = 200
	cleanRecheck   = 24 * time.Hour // a clean answer carries no cache duration: look again after a day
	threatFallback = time.Hour      // a threat answer without an expireTime
)

func WebRiskCommands() []command.Command { return nil }

// WebRiskKey is the API key; empty switches the Web Risk part of the scan off.
func WebRiskKey() string { return strings.TrimSpace(os.Getenv("WEBRISK_API_KEY")) }

func wrEnvInt(name string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n >= 0 {
		return n
	}
	return def
}

// CanonicalURL is scheme, host and path of an http(s) URL: lower-case ASCII (punycode) host, default port dropped, an
// empty path becomes "/", query and fragment dropped. Rule matching decodes the host itself; stored URLs stay ASCII.
func CanonicalURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid URL")
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") { // IPv6 literal
		host = "[" + host + "]"
	} else if host, err = idna.Lookup.ToASCII(host); err != nil {
		return "", err
	}
	if port := u.Port(); port != "" && !((u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")) {
		host += ":" + port
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return strings.ToLower(u.Scheme) + "://" + host + path, nil
}

// wrResult is one Web Risk answer: Threat is empty for a clean link; Expires is when the answer must be asked again.
type wrResult struct {
	URL, Threat string
	Expires     time.Time
}

// wrLookup asks about one canonical URL. Errors never contain the key or the request headers.
func wrLookup(ctx context.Context, link string) (wrResult, error) {
	q := url.Values{"threatTypes": {"MALWARE", "SOCIAL_ENGINEERING", "UNWANTED_SOFTWARE"}, "uri": {link}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, WebRiskURL+"?"+q.Encode(), nil)
	if err != nil {
		return wrResult{}, errors.New("web risk: bad request")
	}
	req.Header.Set("X-Goog-Api-Key", WebRiskKey())
	res, err := WebRiskHTTP.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return wrResult{}, errors.New("web risk: timeout")
		}
		return wrResult{}, errors.New("web risk: request failed")
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return wrResult{}, fmt.Errorf("web risk: status %d", res.StatusCode)
	}
	var out struct {
		Threat *struct {
			ThreatTypes []string `json:"threatTypes"`
			ExpireTime  string   `json:"expireTime"`
		} `json:"threat"`
	}
	if json.Unmarshal(body, &out) != nil {
		return wrResult{}, errors.New("web risk: unreadable answer")
	}
	now := time.Now()
	r := wrResult{URL: link, Expires: now.Add(cleanRecheck)}
	if out.Threat != nil && len(out.Threat.ThreatTypes) > 0 {
		r.Threat = strings.ToLower(out.Threat.ThreatTypes[0])
		r.Expires = now.Add(threatFallback)
		if t, err := time.Parse(time.RFC3339Nano, out.Threat.ExpireTime); err == nil && t.After(now) {
			r.Expires = t
		}
	}
	return r, nil
}

// wrAllow counts one lookup against the daily cap (shared bucket, atomic) and the monthly cap (one atomic upsert).
// SITES_WEBRISK_DAILY (default 2000) and SITES_WEBRISK_MONTHLY (default 90000); 0 stops all lookups.
func wrAllow(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	n, err := db.Bump(ctx, pool, "webrisk:day", true)
	if err != nil {
		return false, err
	}
	if n > wrEnvInt("SITES_WEBRISK_DAILY", 2000) {
		return false, nil
	}
	var m int
	if err := pool.QueryRow(ctx, `insert into sites.webrisk_usage (month, n) values (to_char(now() at time zone 'UTC', 'YYYY-MM'), 1)
		on conflict (month) do update set n = sites.webrisk_usage.n + 1 returning n`).Scan(&m); err != nil {
		return false, err
	}
	return m <= wrEnvInt("SITES_WEBRISK_MONTHLY", 90000), nil
}

// wrPickLinks decides what a scan asks Web Risk about: links never asked first (alphabetical, so the order is
// deterministic), then links whose answer expired, oldest-checked first. A link whose answer is still valid is skipped.
// At most linksPerScan are returned; the rest of the due links are listed as not checked.
func wrPickLinks(links []string, expires, checked map[string]time.Time, now time.Time) (due, notChecked []string) {
	var fresh, stale []string
	for _, l := range links {
		e, known := expires[l]
		switch {
		case !known:
			fresh = append(fresh, l)
		case !e.After(now):
			stale = append(stale, l)
		}
	}
	sort.Strings(fresh)
	sort.Slice(stale, func(i, j int) bool {
		if a, b := checked[stale[i]], checked[stale[j]]; !a.Equal(b) {
			return a.Before(b)
		}
		return stale[i] < stale[j]
	})
	all := append(fresh, stale...)
	if len(all) > linksPerScan {
		return all[:linksPerScan], all[linksPerScan:]
	}
	return all, nil
}

// wrCheckLinks asks Web Risk about the due links in order until a cap stops it or the service keeps failing; what was not
// asked is returned as not checked.
func wrCheckLinks(ctx context.Context, pool *pgxpool.Pool, due []string) (done []wrResult, notChecked []string) {
	fails := 0
	for i, l := range due {
		ok, err := wrAllow(ctx, pool)
		if err != nil || !ok || fails >= 3 || ctx.Err() != nil {
			return done, append(notChecked, due[i:]...)
		}
		r, err := wrLookup(ctx, l)
		if err != nil {
			fails++
			notChecked = append(notChecked, l)
			continue
		}
		fails = 0
		done = append(done, r)
	}
	return done, notChecked
}
