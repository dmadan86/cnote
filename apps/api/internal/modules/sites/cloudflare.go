package sites

// Cloudflare for SaaS as the domain provider (docs/design/site-builder-phase3.md 18.15 to 18.18). Off unless
// SITES_DOMAIN_PROVIDER=cloudflare (and SITES_CUSTOM_DOMAINS=1). Ownership is still proved by the TXT record; the provider then
// carries the routing and the certificate: a hostname is routed only when Cloudflare says status and ssl.status are both active.
// sites.cloudflare_reconciliation holds what we want per hostname; CloudflareSweep plans it, creates, polls and deletes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	cfAPIBase     = "https://api.cloudflare.com/client/v4"
	cfMetaKey     = "cnote_installation" // custom_metadata key holding SITES_CF_INSTALLATION_ID
	cfPerWS       = 5
	cfDefaultMax  = 90
	cfBatch       = 50
	cfMaxPages    = 20
	cfPollPending = 5 * time.Minute
	cfPollActive  = 6 * time.Hour
	cfPollFailing = time.Hour // two failures in a row lose the claim, so a failing state has to last about an hour
)

// CloudflareCommands is empty: the provider has no commands of its own, it drives site.domain_set, site.domain_check and
// site.domain_remove (domains.go) and the sweep.
func CloudflareCommands() []command.Command { return nil }

// CloudflareOn is the provider switch (SITES_DOMAIN_PROVIDER=cloudflare).
func CloudflareOn() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SITES_DOMAIN_PROVIDER")), "cloudflare")
}

func cfPaused() bool { return os.Getenv("SITES_CF_PAUSED") == "1" }

func cfMax() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("SITES_CF_MAX_HOSTNAMES"))); err == nil && n > 0 {
		return n
	}
	return cfDefaultMax
}

func cfInstall() string { return strings.TrimSpace(os.Getenv("SITES_CF_INSTALLATION_ID")) }

// routeTarget is the name customers point their CNAME at: the proxied record in the SaaS zone (SITES_CF_CNAME_TARGET) in
// provider mode, the site's own address otherwise.
func routeTarget(sub string) string {
	if t := strings.ToLower(strings.TrimSpace(os.Getenv("SITES_CF_CNAME_TARGET"))); CloudflareOn() && t != "" {
		return t
	}
	return sub + "." + SitesDomain()
}

// ---------- client ----------

// CloudflareClient talks to the custom hostnames API of one zone. The token only ever goes into the Authorization header.
type CloudflareClient struct {
	BaseURL, Token, ZoneID string
	HTTP                   *http.Client
	Sleep                  func(time.Duration) // between retries; tests replace it
}

func (c *CloudflareClient) String() string   { return "cloudflare client for zone " + c.ZoneID }
func (c *CloudflareClient) GoString() string { return c.String() }

// NewCloudflareClient is the client with the real API address and timeouts.
func NewCloudflareClient(token, zone string) *CloudflareClient {
	return &CloudflareClient{BaseURL: cfAPIBase, Token: token, ZoneID: zone, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

var cfInjected atomic.Pointer[CloudflareClient]

// SetCloudflareClient swaps the client (tests only) and returns the function that puts the old one back.
func SetCloudflareClient(c *CloudflareClient) func() {
	old := cfInjected.Swap(c)
	return func() { cfInjected.Store(old) }
}

// cfClient is the injected client or one built from SITES_CF_API_TOKEN and SITES_CF_ZONE_ID.
func cfClient() (*CloudflareClient, error) {
	if c := cfInjected.Load(); c != nil {
		return c, nil
	}
	tok, zone := strings.TrimSpace(os.Getenv("SITES_CF_API_TOKEN")), strings.TrimSpace(os.Getenv("SITES_CF_ZONE_ID"))
	if tok == "" || zone == "" {
		return nil, errors.New("cloudflare provider: SITES_CF_API_TOKEN and SITES_CF_ZONE_ID are required")
	}
	return NewCloudflareClient(tok, zone), nil
}

// cfReady: the provider is switched on and fully configured. Anything less leaves custom domains unavailable.
func cfReady() bool {
	if !CloudflareOn() || cfInstall() == "" {
		return false
	}
	_, err := cfClient()
	return err == nil
}

// CFHostname is the part of a custom hostname object we use.
type CFHostname struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
	SSL      struct {
		Status string `json:"status"`
	} `json:"ssl"`
	CustomMetadata     map[string]any `json:"custom_metadata"`
	VerificationErrors []string       `json:"verification_errors"`
}

func (h *CFHostname) ours(install string) bool {
	v, ok := h.CustomMetadata[cfMetaKey]
	return ok && install != "" && fmt.Sprint(v) == install
}

// CFError is an API failure. It carries the HTTP status and the API's own error codes and messages, never a request header.
type CFError struct {
	HTTP       int
	Codes      []int
	Msg        string
	RetryAfter time.Duration // 429 only
}

func (e *CFError) Error() string {
	return fmt.Sprintf("cloudflare: http %d codes %v: %s", e.HTTP, e.Codes, e.Msg)
}

func (e *CFError) notFound() bool { return e.HTTP == http.StatusNotFound }

// fatal errors stop the whole sweep: nothing we send next will work until someone acts.
func (e *CFError) fatal() bool {
	return e.HTTP == http.StatusUnauthorized || e.HTTP == http.StatusForbidden || e.HTTP == http.StatusTooManyRequests
}

func isNotFound(err error) bool {
	var ce *CFError
	return errors.As(err, &ce) && ce.notFound()
}

type cfEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

// do sends one request. A 5xx or a network error is retried twice (1s, 2s); a 429 is not retried (Cloudflare blocks the token for
// five minutes), it comes back with the wait so the caller can stop.
func (c *CloudflareClient) do(ctx context.Context, method, path string, q url.Values, body, out any) (*cfEnvelope, error) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/zones/" + url.PathEscape(c.ZoneID) + "/custom_hostnames" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var last error
	for try := 0; try < 3; try++ {
		if try > 0 {
			sleep(time.Duration(try) * time.Second)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		hc := c.HTTP
		if hc == nil {
			hc = http.DefaultClient
		}
		res, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = errors.New("cloudflare: request failed: " + redactURLErr(err))
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		var env cfEnvelope
		_ = json.Unmarshal(raw, &env)
		if res.StatusCode/100 == 2 && env.Success {
			if out != nil && len(env.Result) > 0 {
				if err := json.Unmarshal(env.Result, out); err != nil {
					return nil, fmt.Errorf("cloudflare: unreadable result: %w", err)
				}
			}
			return &env, nil
		}
		ce := &CFError{HTTP: res.StatusCode}
		for _, e := range env.Errors {
			ce.Codes = append(ce.Codes, e.Code)
			if ce.Msg != "" {
				ce.Msg += "; "
			}
			ce.Msg += e.Message
		}
		if ce.Msg == "" {
			ce.Msg = http.StatusText(res.StatusCode)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			ce.RetryAfter = 5 * time.Minute
			if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
				ce.RetryAfter = time.Duration(s) * time.Second
			}
			return nil, ce
		}
		if res.StatusCode >= 500 {
			last = ce
			continue
		}
		return nil, ce
	}
	return nil, last
}

// redactURLErr keeps the cause of a transport error and drops the URL (it holds the zone id).
func redactURLErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// Get returns one custom hostname by its Cloudflare id.
func (c *CloudflareClient) Get(ctx context.Context, id string) (*CFHostname, error) {
	var h CFHostname
	_, err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(id), nil, nil, &h)
	return &h, err
}

// Find looks a hostname up by name across all pages; nil when the zone has none.
func (c *CloudflareClient) Find(ctx context.Context, host string) (*CFHostname, error) {
	for page := 1; page <= cfMaxPages; page++ {
		var list []CFHostname
		env, err := c.do(ctx, http.MethodGet, "", url.Values{"hostname": {host}, "page": {strconv.Itoa(page)}, "per_page": {"50"}}, nil, &list)
		if err != nil {
			return nil, err
		}
		for i := range list {
			if strings.EqualFold(list[i].Hostname, host) {
				return &list[i], nil
			}
		}
		if page >= env.ResultInfo.TotalPages {
			break
		}
	}
	return nil, nil
}

// Count is how many custom hostnames the zone holds, whoever created them.
func (c *CloudflareClient) Count(ctx context.Context) (int, error) {
	var list []CFHostname
	env, err := c.do(ctx, http.MethodGet, "", url.Values{"page": {"1"}, "per_page": {"5"}}, nil, &list)
	if err != nil {
		return 0, err
	}
	return env.ResultInfo.TotalCount, nil
}

// Create adds a hostname, validated over HTTP, carrying the installation id in its custom metadata.
func (c *CloudflareClient) Create(ctx context.Context, host, install string) (*CFHostname, error) {
	var h CFHostname
	_, err := c.do(ctx, http.MethodPost, "", nil, map[string]any{
		"hostname":        host,
		"ssl":             map[string]any{"method": "http", "type": "dv", "settings": map[string]any{"min_tls_version": "1.2"}},
		"custom_metadata": map[string]any{cfMetaKey: install},
	}, &h)
	return &h, err
}

// Delete removes a hostname; one that is already gone counts as deleted.
func (c *CloudflareClient) Delete(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/"+url.PathEscape(id), nil, nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// cfAllowSet is site.domain_set in provider mode: subdomains only, not while paused, not past the hostname budget, and at most
// cfPerWS hostnames per workspace (the per-site 5 in 30 days and the 50 verifications an hour stay in the SQL functions).
func cfAllowSet(c *command.Ctx, host string) error {
	if !cfReady() || cfPaused() {
		return command.ErrInvalid("Custom domains are not available right now. Try again later.")
	}
	if isApex(host) {
		return command.ErrInvalid("Connect a www or other subdomain address (such as www.yourbusiness.in); the address without www is not available.")
	}
	var mine, used int
	if err := c.Tx.QueryRow(c.Context, `select count(*)::int from sites.domain_claims where workspace_id = $1::uuid and hostname <> $2
		and status <> 'lost' and (status <> 'pending' or expires_at > now())`, c.WorkspaceID(), host).Scan(&mine); err != nil {
		return command.DB(err)
	}
	if mine >= cfPerWS {
		return command.NewError(command.PlanLimit, "A workspace can connect up to 5 domains.")
	}
	if err := c.Tx.QueryRow(c.Context, "select sites.cf_used()").Scan(&used); err != nil {
		return command.DB(err)
	}
	if used >= cfMax() {
		return command.NewError(command.RateLimited, "No more domains can be connected right now. Try again later.")
	}
	return nil
}

// ---------- states ----------

const (
	cfOK      = iota // active and certified: routed
	cfPending        // still being set up: not routed, nothing wrong
	cfFailure        // broken: not routed, one strike
	cfLost           // blocked by Cloudflare: lost at once
)

// cfJudge maps a hostname's status and certificate status to routing and a verdict.
func cfJudge(status, ssl string) (routing bool, verdict int) {
	switch status {
	case "blocked", "pending_blocked":
		return false, cfLost
	case "moved", "deleted", "pending_deletion":
		return false, cfFailure
	}
	if ssl == "expired" || ssl == "deleted" || strings.HasSuffix(ssl, "_timed_out") {
		return false, cfFailure
	}
	if (status == "active" || status == "active_redeploying") && ssl == "active" {
		return true, cfOK
	}
	return false, cfPending
}

// cfOverlay replaces the DNS answer about routing by the provider's state, for one claim of site.domain_check. What the
// provider cannot say now changes nothing; a verified claim not yet at the provider is simply not routed.
func cfOverlay(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, cl claim, res *dnsResult) {
	res.routeKnown, res.routeOK = true, false
	if cl.Status != "verified" {
		return
	}
	var id string
	if err := q.QueryRow(ctx, "select coalesce(provider_id, '') from sites.cloudflare_reconciliation where hostname = $1 and desired_state = 'present'", cl.Hostname).Scan(&id); err != nil || id == "" {
		return
	}
	c, err := cfClient()
	if err != nil {
		res.routeKnown = false
		return
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	h, err := c.Get(cctx, id)
	switch {
	case isNotFound(err):
		res.cfVerdict = cfFailure
	case err != nil:
		res.routeKnown = false
	default:
		res.routeOK, res.cfVerdict = cfJudge(h.Status, h.SSL.Status)
	}
}

// ---------- the sweep ----------

// CloudflareAlert is told once a day when the hostnames held reach 80% of SITES_CF_MAX_HOSTNAMES. The default logs a warning;
// the lead can point it at a platform notification.
var CloudflareAlert = func(ctx context.Context, used, max int) {
	slog.WarnContext(ctx, "cloudflare for saas: hostname budget is at 80 percent or more", "used", used, "max", max)
}

var cfAdminMu sync.Mutex
var cfAdminPool *pgxpool.Pool
var cfAdminURL string
var cfMissingAdminLogged bool

func cfAdmin(ctx context.Context) (*pgxpool.Pool, error) {
	cfAdminMu.Lock()
	defer cfAdminMu.Unlock()
	u := strings.TrimSpace(os.Getenv("DATABASE_ADMIN_URL"))
	if u == "" {
		if !cfMissingAdminLogged {
			slog.WarnContext(ctx, "cloudflare sweep: DATABASE_ADMIN_URL is not configured")
			cfMissingAdminLogged = true
		}
		return nil, nil
	}
	if cfAdminPool == nil || cfAdminURL != u {
		p, err := pgxpool.New(ctx, u)
		if err != nil {
			return nil, err
		}
		cfAdminPool, cfAdminURL = p, u
	}
	return cfAdminPool, nil
}

// CloudflareSweep is the reconciler (worker sweep "sites.cloudflare", every minute or two is cheap): it plans the desired state,
// then creates, polls and deletes the hostnames that are due. Hostnames of unpublished, taken down, Pro-lapsed and disconnected
// sites are deleted at the provider within the next runs (routing stopped at once, in the proxy lookup). It does nothing unless
// SITES_DOMAIN_PROVIDER=cloudflare and SITES_CUSTOM_DOMAINS=1, and nothing at all while SITES_CF_PAUSED=1.
func CloudflareSweep(ctx context.Context, pool *pgxpool.Pool) error {
	if !CloudflareOn() || !CustomDomainsOn() || cfPaused() {
		return nil
	}
	if cfInstall() == "" {
		return errors.New("cloudflare provider: SITES_CF_INSTALLATION_ID is required")
	}
	cl, err := cfClient()
	if err != nil {
		return err
	}
	admin, err := cfAdmin(ctx)
	if err != nil {
		return err
	}
	if admin == nil {
		return nil
	}
	if _, err := admin.Exec(ctx, "select sites.cf_plan($1)", cfPerWS); err != nil {
		return err
	}
	rows, err := pool.Query(ctx, "select workspace_id::text, hostname from sites.cf_due($1)", cfBatch)
	if err != nil {
		return err
	}
	type due struct{ ws, host string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.ws, &d.host)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		if err := cfReconcile(ctx, pool, cl, d.ws, d.host); err != nil && ctx.Err() == nil {
			errs = append(errs, err)
			var ce *CFError
			if errors.As(err, &ce) && ce.fatal() {
				break
			}
		}
	}
	var used int
	if err := pool.QueryRow(ctx, "select sites.cf_used()").Scan(&used); err == nil && used*100 >= cfMax()*80 {
		if n, err := db.Bump(ctx, pool, "sitecf:alert", true); err == nil && n == 1 {
			CloudflareAlert(ctx, used, cfMax())
		}
	}
	return errors.Join(errs...)
}

// cfLease takes the row for this run: it moves next_attempt_at forward by an exponential backoff (so a failure retries later and
// a second sweep skips the row) and counts the attempt. tried is the count before this one; a row with tried > 0 and no
// provider id may have been created without us recording it.
func cfLease(ctx context.Context, pool *pgxpool.Pool, ws, host string) (desired, id string, tried, gen int, ok bool, err error) {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `update sites.cloudflare_reconciliation set attempts = attempts + 1, last_attempt_at = now(), updated_at = now(),
		next_attempt_at = now() + least(interval '1 hour', interval '30 seconds' * power(2, least(attempts, 7)))
		where hostname = $1 and next_attempt_at <= now() returning desired_state, coalesce(provider_id, ''), attempts - 1, generation`, host).Scan(&desired, &id, &tried, &gen)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, 0, false, nil
	}
	if err != nil {
		return
	}
	return desired, id, tried, gen, true, tx.Commit(ctx)
}

type cfResult struct {
	id, status, ssl string
	gone            bool          // the row can go: nothing is left at the provider
	note            string        // shown in last_error
	next            time.Duration // when to look again
	judged          bool          // verdict and routing are about the claim
	routing         bool
	verdict         int
}

func cfReconcile(ctx context.Context, pool *pgxpool.Pool, cl *CloudflareClient, ws, host string) error {
	desired, id, tried, gen, ok, err := cfLease(ctx, pool, ws, host)
	if err != nil || !ok {
		return err
	}
	var res cfResult
	if desired == "absent" {
		res, err = cfRemove(ctx, cl, host, id, tried)
	} else {
		res, err = cfEnsure(ctx, pool, cl, host, id)
	}
	if err != nil {
		var ce *CFError
		wait := time.Duration(0)
		if errors.As(err, &ce) {
			wait = ce.RetryAfter
		}
		if serr := cfStoreError(ctx, pool, ws, host, err, wait); serr != nil {
			return errors.Join(err, serr)
		}
		return err
	}
	return cfStore(ctx, pool, ws, host, gen, res)
}

// cfEnsure makes the hostname exist at the provider and reports its state. Create is idempotent: the provider is asked about
// the hostname first, and one that is not ours is left alone.
func cfEnsure(ctx context.Context, pool *pgxpool.Pool, cl *CloudflareClient, host, id string) (cfResult, error) {
	var h *CFHostname
	var err error
	if id != "" {
		if h, err = cl.Get(ctx, id); isNotFound(err) { // Cloudflare deletes a hostname that never validated; start over
			return cfResult{status: "pending", note: "hostname gone at the provider", next: time.Minute, judged: true, verdict: cfFailure}, nil
		} else if err != nil {
			return cfResult{}, err
		}
	} else {
		if h, err = cl.Find(ctx, host); err != nil {
			return cfResult{}, err
		}
		if h != nil && !h.ours(cfInstall()) {
			return cfResult{status: "conflict", note: "the zone already holds this hostname and it is not ours", next: time.Hour}, nil
		}
		if h == nil {
			// the slot is reserved before the provider is called, so two workers cannot both take the last one; cfStore or cfStoreError releases it
			var got bool
			if err := pool.QueryRow(ctx, "select sites.cf_reserve($1, $2)", host, cfMax()).Scan(&got); err != nil {
				return cfResult{}, err
			}
			if !got {
				return cfResult{status: "budget", note: "hostname budget reached", next: 15 * time.Minute}, nil
			}
			remote, err := cl.Count(ctx)
			if err != nil {
				return cfResult{}, err
			}
			if remote >= cfMax() {
				return cfResult{status: "budget", note: "hostname budget reached", next: 15 * time.Minute}, nil
			}
			if h, err = cl.Create(ctx, host, cfInstall()); err != nil {
				var ce *CFError
				if errors.As(err, &ce) && ce.HTTP < 500 && !ce.fatal() { // a duplicate is not an error: take the one that exists
					if f, ferr := cl.Find(ctx, host); ferr == nil && f != nil && f.ours(cfInstall()) {
						h, err = f, nil
					}
				}
				if err != nil {
					return cfResult{}, err
				}
			}
		}
	}
	r := cfResult{id: h.ID, status: h.Status, ssl: h.SSL.Status, judged: true}
	r.routing, r.verdict = cfJudge(h.Status, h.SSL.Status)
	r.next = map[int]time.Duration{cfOK: cfPollActive, cfPending: cfPollPending, cfFailure: cfPollFailing, cfLost: time.Minute}[r.verdict]
	return r, nil
}

// cfRemove deletes what we created, and only that: the hostname must carry this installation's id.
func cfRemove(ctx context.Context, cl *CloudflareClient, host, id string, tried int) (cfResult, error) {
	var h *CFHostname
	var err error
	switch {
	case id != "":
		h, err = cl.Get(ctx, id)
		if isNotFound(err) {
			return cfResult{gone: true}, nil
		}
	case tried > 0:
		h, err = cl.Find(ctx, host)
	default:
		return cfResult{gone: true}, nil
	}
	if err != nil {
		return cfResult{}, err
	}
	if h == nil || !h.ours(cfInstall()) { // gone, or somebody else's: nothing of ours is left
		return cfResult{gone: true}, nil
	}
	if err := cl.Delete(ctx, h.ID); err != nil {
		return cfResult{}, err
	}
	return cfResult{gone: true}, nil
}

// cfStore writes what the run found: the row, and for a present hostname the claim (routing, strikes, lost).
func cfStore(ctx context.Context, pool *pgxpool.Pool, ws, host string, gen int, r cfResult) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if r.gone {
		if _, err = tx.Exec(ctx, "delete from sites.cloudflare_reconciliation where hostname = $1 and desired_state = 'absent'", host); err != nil {
			return err
		}
		// wanted again while the delete ran: start from nothing
		_, err = tx.Exec(ctx, `update sites.cloudflare_reconciliation set provider_id = null, status = 'pending', ssl_status = null, attempts = 0, next_attempt_at = now()
			where hostname = $1 and desired_state = 'present'`, host)
	} else {
		var note *string
		if r.note != "" {
			note = &r.note
		}
		// only for the row as it was read: if the planner changed it meanwhile (disconnect, unpublish) the result is stale and dropped,
		// and a hostname created meanwhile is found again by the tombstone's delete
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `update sites.cloudflare_reconciliation set provider_id = nullif($2, ''), status = $3, ssl_status = nullif($4, ''), last_error = $5,
			attempts = 0, next_attempt_at = now() + make_interval(secs => $6), updated_at = now(), reserved_at = null
			where hostname = $1 and desired_state = 'present' and generation = $7`, host, r.id, r.status, r.ssl, note, r.next.Seconds(), gen)
		if err == nil && tag.RowsAffected() == 0 {
			_, err = tx.Exec(ctx, "update sites.cloudflare_reconciliation set reserved_at = null where hostname = $1", host)
		}
		if err == nil && r.judged && tag.RowsAffected() == 1 {
			_, err = tx.Exec(ctx, `update sites.domain_claims set routing_configured = $2, checked_at = now(),
				failures = case $3::int when 0 then 0 when 2 then failures + 1 else failures end,
				status = case when $3::int = 3 or ($3::int = 2 and failures + 1 >= $4::int) then 'lost' else status end
				where hostname = $1 and status = 'verified'`, host, r.routing, r.verdict, failuresToLose)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// cfStoreError records a failed run. The lease already pushed the next try out; a 429 pushes it out by the wait it asked for.
func cfStoreError(ctx context.Context, pool *pgxpool.Pool, ws, host string, cause error, wait time.Duration) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	msg := cause.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if _, err = tx.Exec(ctx, `update sites.cloudflare_reconciliation set last_error = $2, reserved_at = null,
		next_attempt_at = greatest(next_attempt_at, now() + make_interval(secs => $3)) where hostname = $1`, host, msg, wait.Seconds()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
