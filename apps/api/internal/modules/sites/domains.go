package sites

// Custom domains (docs/design/site-builder-phase2.md 17.1 to 17.3, 17.6). Each hostname is its own claim: proved by a
// TXT record holding a random token, routed by a CNAME (any name but the apex) or A record (the apex). Only verified,
// routed hostnames of a published, Pro, not taken down site are served or approved for TLS (migration 1302).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/publicsuffix"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	txtPrefix      = "_cnote-verify."
	txtValue       = "cnote-verify="
	checksPerHour  = 120
	dnsTimeout     = 5 * time.Second
	failuresToLose = 2
	sweepBatch     = 200
)

// Resolver is the part of *net.Resolver the checks use, so tests can inject answers.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

var resolver atomic.Pointer[Resolver]

func dns() Resolver {
	if r := resolver.Load(); r != nil {
		return *r
	}
	return net.DefaultResolver
}

// SetResolver swaps the resolver (tests only) and returns the function that puts the old one back.
func SetResolver(r Resolver) func() {
	old := resolver.Swap(&r)
	return func() { resolver.Store(old) }
}

// CustomDomainsOn is the only feature gate (SITES_CUSTOM_DOMAINS=1).
func CustomDomainsOn() bool { return os.Getenv("SITES_CUSTOM_DOMAINS") == "1" }

func apexIPs() []netip.Addr {
	var out []netip.Addr
	for _, v := range strings.Split(os.Getenv("SITES_APEX_IP"), ",") {
		if a, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			out = append(out, a.Unmap())
		}
	}
	return out
}

func isApex(host string) bool {
	r, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil && r == host
}

// testDNS is the e2e switch: names under SITES_TEST_DNS pass both DNS checks. Ignored on an https deployment.
func testDNS(host string) bool {
	s := strings.ToLower(strings.Trim(os.Getenv("SITES_TEST_DNS"), ". "))
	if s == "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL"))), "https://") {
		return false
	}
	return host == s || strings.HasSuffix(host, "."+s)
}

type claim struct {
	ID, Hostname, Token, Status string
	Routing                     bool
	Failures                    int
	ExpiresAt                   time.Time
	VerifiedAt                  *time.Time
}

const claimCols = `id::text, hostname, txt_token, status, routing_configured, failures, expires_at, verified_at`

func (c *claim) dest() []any {
	return []any{&c.ID, &c.Hostname, &c.Token, &c.Status, &c.Routing, &c.Failures, &c.ExpiresAt, &c.VerifiedAt}
}

// dnsResult is what the resolver said. A *Known flag is false when the lookup failed for a reason other than "no such
// record" (timeout, SERVFAIL): then nothing is concluded and nothing changes.
type dnsResult struct {
	txt, routing         []string
	txtOK, routeOK       bool
	txtKnown, routeKnown bool
}

func definite(err error) bool {
	var de *net.DNSError
	return err == nil || (errors.As(err, &de) && de.IsNotFound)
}

func lookup(ctx context.Context, r Resolver, cl claim, sub string) dnsResult {
	if testDNS(cl.Hostname) {
		return dnsResult{txtOK: true, routeOK: true, txtKnown: true, routeKnown: true}
	}
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	var res dnsResult
	txt, err := r.LookupTXT(ctx, txtPrefix+cl.Hostname)
	res.txtKnown = definite(err)
	for _, t := range txt {
		res.txt = append(res.txt, t)
		if strings.TrimSpace(t) == txtValue+cl.Token {
			res.txtOK = true
		}
	}
	if isApex(cl.Hostname) {
		ips, err := r.LookupHost(ctx, cl.Hostname)
		res.routeKnown = definite(err)
		want := apexIPs()
		res.routeOK = len(ips) > 0 && len(want) > 0
		for _, s := range ips {
			a, e := netip.ParseAddr(s)
			res.routing = append(res.routing, s)
			if e != nil || !containsAddr(want, a.Unmap()) {
				res.routeOK = false
			}
		}
	} else {
		cname, err := r.LookupCNAME(ctx, cl.Hostname)
		res.routeKnown = definite(err)
		cname = strings.ToLower(strings.TrimSuffix(cname, "."))
		if cname != "" && cname != cl.Hostname {
			res.routing = []string{cname}
			res.routeOK = cname == sub+"."+SitesDomain()
		}
	}
	return res
}

func containsAddr(l []netip.Addr, a netip.Addr) bool {
	for _, x := range l {
		if x == a {
			return true
		}
	}
	return false
}

// view is what the commands return for one hostname: the claim and the exact records to show.
func view(cl claim, sub string, res dnsResult) props {
	txt := props{"type": "TXT", "name": txtPrefix + cl.Hostname, "value": txtValue + cl.Token, "found": nonNil(res.txt), "ok": res.txtOK}
	route := props{"type": "CNAME", "name": cl.Hostname, "value": sub + "." + SitesDomain(), "found": nonNil(res.routing), "ok": res.routeOK}
	if isApex(cl.Hostname) {
		ips := []string{}
		for _, a := range apexIPs() {
			ips = append(ips, a.String())
		}
		route = props{"type": "A", "name": cl.Hostname, "value": strings.Join(ips, ", "), "found": nonNil(res.routing), "ok": res.routeOK}
	}
	return props{
		"domain":   props{"hostname": cl.Hostname, "status": cl.Status, "txt_token": cl.Token, "verified_at": cl.VerifiedAt, "routing": cl.Routing, "expires_at": cl.ExpiresAt},
		"records":  []props{txt, route},
		"verified": cl.Status == "verified",
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DomainsCommands are site.domain_set, site.domain_check and site.domain_remove.
func DomainsCommands() []command.Command {
	id := str("site id")
	return []command.Command{
		mk("site.domain_set", command.GroupWriteItems, command.Admin, true, "Website Pro: add a hostname (www.example.com or example.com) to this site. Returns the TXT record that proves it is yours and the record that routes it.",
			obj([]string{"id", "domain"}, props{"id": id, "domain": str("the hostname to connect")}), domainSet),
		mk("site.domain_check", command.GroupRead, command.Admin, false, "Look up the DNS records of this site's hostnames (all, or one) and mark proven ones verified.",
			obj([]string{"id"}, props{"id": id, "domain": str("one hostname; default all")}), domainCheck),
		mk("site.domain_remove", command.GroupWriteItems, command.Admin, true, "Disconnect a hostname (or all of them): routing and certificates stop at once.",
			obj([]string{"id"}, props{"id": id, "domain": str("one hostname; default all")}), domainRemove),
	}
}

// domainsExtras adds the site's hostnames and whether the feature can be used to site.get.
func domainsExtras(c *command.Ctx, site siteOut, out props) error {
	rows, err := c.Tx.Query(c.Context, "select "+claimCols+" from sites.domain_claims where workspace_id = $1::uuid and site_id = $2::uuid and (status <> 'pending' or expires_at > now()) order by hostname", c.WorkspaceID(), site.ID)
	if err != nil {
		return command.DB(err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (props, error) {
		var cl claim
		err := r.Scan(cl.dest()...)
		return props{"hostname": cl.Hostname, "status": cl.Status, "txt_token": cl.Token, "verified_at": cl.VerifiedAt, "routing": cl.Routing, "expires_at": cl.ExpiresAt}, err
	})
	if err != nil {
		return command.DB(err)
	}
	out["domains"] = list
	out["domains_available"] = CustomDomainsOn() && site.Pro
	return nil
}

// cleanHost accepts what people paste: a scheme and a path are dropped.
func cleanHost(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

func domainSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Domain string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if !CustomDomainsOn() {
		return nil, command.ErrInvalid("Custom domains are not available.")
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if !site.Pro {
		return nil, command.NewError(command.PlanLimit, "Custom domains are part of Website Pro.")
	}
	if site.Subdomain == nil {
		return nil, command.ErrInvalid("Publish your website once to get its address before connecting a domain.")
	}
	host, err := NormalizeHostname(cleanHost(in.Domain))
	if err != nil {
		return nil, command.ErrInvalid("That is not a domain you can connect. Use something like www.yourbusiness.in.")
	}
	for _, l := range strings.Split(strings.ToLower(cleanHost(in.Domain)), ".") { // NormalizeHostname repairs some broken punycode; we do not
		if strings.HasPrefix(l, "xn--") && !strings.Contains("."+host+".", "."+l+".") {
			return nil, command.ErrInvalid("That is not a domain you can connect. Use something like www.yourbusiness.in.")
		}
	}
	if isApex(host) && len(apexIPs()) == 0 {
		return nil, command.ErrInvalid("Connect the www address instead; the address without www is not available.")
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	var status string
	if err := c.Tx.QueryRow(c.Context, "select sites.domain_claim($1::uuid, $2, $3)", site.ID, host, hex.EncodeToString(b)).Scan(&status); err != nil {
		return nil, command.DB(err)
	}
	switch status {
	case "created", "exists":
	case "taken":
		return nil, command.ErrConflict("This domain is connected to another C Note website.")
	case "limit":
		return nil, command.NewError(command.RateLimited, "You can add up to 5 domains in 30 days. Try again later.")
	default:
		return nil, command.ErrNotFound("site")
	}
	var cl claim
	if err := c.Tx.QueryRow(c.Context, "select "+claimCols+" from sites.domain_claims where hostname = $1 and site_id = $2::uuid", host, site.ID).Scan(cl.dest()...); err != nil {
		return nil, command.DB(err)
	}
	return view(cl, *site.Subdomain, dnsResult{}), nil
}

func domainCheck(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Domain string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if !CustomDomainsOn() {
		return nil, command.ErrInvalid("Custom domains are not available.")
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	if site.Subdomain == nil {
		return nil, command.ErrNotFound("domain")
	}
	if n, err := db.Bump(c.Context, c.Tx, "sitedomain:check:"+site.ID, false); err != nil {
		return nil, command.DB(err)
	} else if n > checksPerHour {
		return nil, command.NewError(command.RateLimited, "Too many checks. Try again in a few minutes.")
	}
	if _, err := c.Tx.Exec(c.Context, "delete from sites.domain_claims where site_id = $1::uuid and status = 'pending' and expires_at <= now()", site.ID); err != nil {
		return nil, command.DB(err)
	}
	rows, err := c.Tx.Query(c.Context, "select "+claimCols+" from sites.domain_claims where workspace_id = $1::uuid and site_id = $2::uuid and ($3 = '' or hostname = $3) order by hostname",
		c.WorkspaceID(), site.ID, strings.ToLower(cleanHost(in.Domain)))
	if err != nil {
		return nil, command.DB(err)
	}
	claims, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (claim, error) {
		var cl claim
		return cl, r.Scan(cl.dest()...)
	})
	if err != nil {
		return nil, command.DB(err)
	}
	if len(claims) == 0 {
		return nil, command.ErrNotFound("domain")
	}
	results := make([]dnsResult, len(claims))
	var wg sync.WaitGroup
	for i := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = lookup(c.Context, dns(), claims[i], *site.Subdomain)
		}()
	}
	wg.Wait()
	views := make([]props, len(claims))
	for i := range claims {
		cl, res := &claims[i], results[i]
		waiting := false
		if res.txtKnown && res.txtOK && cl.Status != "verified" {
			var slot bool
			if err := c.Tx.QueryRow(c.Context, "select sites.domain_verify_slot()").Scan(&slot); err != nil {
				return nil, command.DB(err)
			}
			if slot {
				cl.Status = "verified"
			}
			waiting = !slot
		}
		if res.routeKnown {
			cl.Routing = res.routeOK
		}
		failed := res.txtKnown && !res.txtOK && cl.Status == "verified"
		if res.txtKnown && cl.Status == "verified" {
			if res.txtOK {
				cl.Failures = 0
			} else {
				cl.Failures++
				if cl.Failures >= failuresToLose {
					cl.Status = "lost"
					cl.Routing = false
				}
			}
		}
		if err := c.Tx.QueryRow(c.Context, `update sites.domain_claims set status = $2, routing_configured = $3, failures = $4, checked_at = case when $5 then checked_at else now() end,
			verified_at = case when $2 = 'verified' and status <> 'verified' then now() else verified_at end where id = $1::uuid returning verified_at`,
			cl.ID, cl.Status, cl.Routing, cl.Failures, failed).Scan(&cl.VerifiedAt); err != nil {
			return nil, command.DB(err)
		}
		views[i] = view(*cl, *site.Subdomain, res)
		views[i]["waiting"] = waiting
	}
	out := props{"domains": views, "status": claims[0].Status}
	for k, v := range views[0] {
		out[k] = v
	}
	return out, nil
}

func domainRemove(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Domain string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "delete from sites.domain_claims where workspace_id = $1::uuid and site_id = $2::uuid and ($3 = '' or hostname = $3)",
		c.WorkspaceID(), site.ID, strings.ToLower(cleanHost(in.Domain)))
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 0 && in.Domain != "" {
		return nil, command.ErrNotFound("domain")
	}
	return props{"site": site}, nil
}

// ---------- public side ----------

// TLSAsk answers Caddy's on-demand question with one indexed query (the TLS ask endpoint, 17.3).
func tlsAsk(ctx context.Context, pool *pgxpool.Pool, domain string) (bool, error) {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if !CustomDomainsOn() || h == "" || len(h) > 253 {
		return false, nil
	}
	var ok bool
	err := pool.QueryRow(ctx, "select sites.domain_ask($1)", h).Scan(&ok)
	return ok, err
}

// lookupDomain resolves a custom hostname to its site when the hostname is live.
func lookupDomain(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, host string) (Public, error) {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if !CustomDomainsOn() || h == "" || len(h) > 253 {
		return Public{}, command.ErrNotFound("site")
	}
	var p Public
	var data []byte
	var sub string
	err := q.QueryRow(ctx, "select "+publicCols+", subdomain, canonical_host from sites.domain_lookup($1)", h).
		Scan(&p.WorkspaceID, &p.SiteID, &p.VersionID, &p.Name, &data, &p.Pro, &p.PublishedAt, &p.PublishedBy, &p.FormPaused, &p.LeadSpaceID, &p.Catalog, &p.OGImages, &sub, &p.CanonicalHost)
	p.Data = data
	if errors.Is(err, pgx.ErrNoRows) {
		return p, command.ErrNotFound("site")
	}
	if err == nil && p.CanonicalHost != nil {
		p.URL = "https://" + *p.CanonicalHost
	}
	return p, command.DB(err)
}

// withCanonical adds the live custom hostname to a site found by its subdomain, so the web server can redirect to it.
func withCanonical(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, p *Public) error {
	if !CustomDomainsOn() {
		return nil
	}
	if err := q.QueryRow(ctx, "select sites.domain_canonical($1::uuid)", p.SiteID).Scan(&p.CanonicalHost); err != nil {
		return command.DB(err)
	}
	if p.CanonicalHost != nil {
		p.URL = "https://" + *p.CanonicalHost
	}
	return nil
}

// DomainSweep is the daily re-check (worker sweep "sites.domains"): verified hostnames whose TXT record is gone twice in
// a row become lost (not routed, not approved); a hostname whose routing record is gone stops being routed at once.
// A lookup that fails for any reason but "no such record" changes nothing.
func DomainSweep(ctx context.Context, pool *pgxpool.Pool) error {
	if !CustomDomainsOn() {
		return nil
	}
	if _, err := pool.Exec(ctx, "select sites.domain_prune()"); err != nil {
		return err
	}
	rows, err := pool.Query(ctx, "select workspace_id::text, id::text from sites.domains_due($1)", sweepBatch)
	if err != nil {
		return err
	}
	type due struct{ ws, id string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.ws, &d.id)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		if err := sweepOne(ctx, pool, d.ws, d.id); err != nil && ctx.Err() == nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func sweepOne(ctx context.Context, pool *pgxpool.Pool, ws, id string) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	var cl claim
	var sub *string
	err = tx.QueryRow(ctx, "select "+claimCols+", (select subdomain from sites.sites s where s.id = c.site_id) from sites.domain_claims c where id = $1::uuid and status = 'verified'", id).Scan(append(cl.dest(), &sub)...)
	_ = tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrNoRows) || sub == nil {
		return nil
	}
	if err != nil {
		return err
	}
	res := lookup(ctx, dns(), cl, *sub)
	if !res.txtKnown && !res.routeKnown {
		return nil
	}
	if tx, err = db.BeginTenant(ctx, pool, ws); err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if res.routeKnown {
		if _, err = tx.Exec(ctx, "update sites.domain_claims set routing_configured = $2 where id = $1::uuid and status = 'verified'", id, res.routeOK); err != nil {
			return err
		}
	}
	if res.txtKnown {
		// the claim is lost on the second failure in a row; a pass resets the count
		if _, err = tx.Exec(ctx, `update sites.domain_claims set checked_at = now(),
			failures = case when $2 then 0 else failures + 1 end,
			status = case when not $2 and failures + 1 >= $3 then 'lost' else status end,
			routing_configured = routing_configured and ($2 or failures + 1 < $3)
			where id = $1::uuid and status = 'verified'`, id, res.txtOK, failuresToLose); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("sites domain sweep: %w", err)
	}
	return nil
}
