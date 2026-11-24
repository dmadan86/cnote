package sites

// Public (no session) side of a published site: the lookups that cross workspaces through SECURITY DEFINER functions,
// the trusted-proxy client address, the form rate limits and the form submission itself (design 17.4 to 17.7).

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/notify"
	"github.com/cnote/api/internal/secretbox"
)

// Public is a published, not taken down site as the public sees it.
type Public struct {
	WorkspaceID, SiteID, VersionID, Name, PublishedBy string
	Data                                              json.RawMessage
	Pro, FormPaused                                   bool
	PublishedAt                                       time.Time
	LeadSpaceID                                       *string
	URL                                               string
	CanonicalHost                                     *string         // the live custom hostname, when the site has one (custom domains)
	Catalog, OGImages                                 json.RawMessage // the catalogue snapshot and the share image of each page
}

const publicCols = `workspace_id::text, site_id::text, version_id::text, name, data, pro, published_at, published_by::text, form_paused, lead_space_id::text, catalog_published, og_images`

func scanPublic(row pgx.Row) (Public, error) {
	var p Public
	var data []byte
	err := row.Scan(&p.WorkspaceID, &p.SiteID, &p.VersionID, &p.Name, &data, &p.Pro, &p.PublishedAt, &p.PublishedBy, &p.FormPaused, &p.LeadSpaceID, &p.Catalog, &p.OGImages)
	p.Data = data
	if errors.Is(err, pgx.ErrNoRows) {
		return p, command.ErrNotFound("site")
	}
	return p, command.DB(err)
}

// SitesDomain is SITES_DOMAIN (host[:port]), lowercased; empty when publishing is not set up.
func SitesDomain() string { return strings.ToLower(strings.TrimSpace(os.Getenv("SITES_DOMAIN"))) }

// SiteURL is the address of a published site.
func SiteURL(sub string) string {
	d := SitesDomain()
	scheme := "https"
	if h, _, _ := strings.Cut(d, ":"); h == "localhost" {
		scheme = "http"
	}
	return scheme + "://" + sub + "." + d
}

// SubdomainOf extracts the one label of host under SITES_DOMAIN (port included in the match); "" when it is not one.
func SubdomainOf(host string) string {
	d, host := SitesDomain(), strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if d == "" || !strings.HasSuffix(host, "."+d) {
		return ""
	}
	sub := strings.TrimSuffix(host, "."+d)
	if len(sub) < 3 || len(sub) > 40 || !subdomainRe.MatchString(sub) {
		return ""
	}
	return sub
}

// LookupHost resolves a request host to its published site; unknown, unpublished and taken down all answer not found.
func LookupHost(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, host string) (Public, error) {
	sub := SubdomainOf(host)
	if sub == "" {
		return lookupDomain(ctx, q, host)
	}
	p, err := scanPublic(q.QueryRow(ctx, "select "+publicCols+" from sites.host_lookup($1)", sub))
	p.URL = SiteURL(sub)
	if err == nil {
		err = withCanonical(ctx, q, &p)
	}
	return p, err
}

// LookupID resolves a site id the same way.
func LookupID(ctx context.Context, pool *pgxpool.Pool, id string) (Public, error) {
	return scanPublic(pool.QueryRow(ctx, "select "+publicCols+" from sites.public_by_id($1::uuid)", id))
}

// PublicImage returns the workspace of an image when it belongs to the site and the published version uses it.
func PublicImage(ctx context.Context, pool *pgxpool.Pool, site, image string) (string, error) {
	var ws string
	err := pool.QueryRow(ctx, "select workspace_id::text from sites.public_image($1::uuid, $2::uuid)", site, image).Scan(&ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("image")
	}
	return ws, command.DB(err)
}

// ---------- client address and limits ----------

// TrustedProxies parses PUBLIC_TRUSTED_PROXIES (comma-separated CIDRs or addresses).
func TrustedProxies() []netip.Prefix {
	var out []netip.Prefix
	for _, v := range strings.Split(os.Getenv("PUBLIC_TRUSTED_PROXIES"), ",") {
		v = strings.TrimSpace(v)
		if p, err := netip.ParsePrefix(v); err == nil {
			out = append(out, p)
		} else if a, err := netip.ParseAddr(v); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func trusted(a netip.Addr, nets []netip.Prefix) bool {
	for _, p := range nets {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

// ClientIP is the peer address, unless the peer is a trusted proxy: then the rightmost X-Forwarded-For entry that is
// not itself a trusted proxy (a client cannot forge entries to the right of what its own proxy appended).
func ClientIP(r *http.Request, nets []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !trusted(peer, nets) {
		return host
	}
	xs := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(xs) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(xs[i]))
		if err != nil {
			return host // garbage on the trusted path: do not guess
		}
		if !trusted(a, nets) {
			return a.Unmap().String()
		}
	}
	return host
}

var (
	ipKeyOnce sync.Once
	ipKey     []byte
)

// IPKey is a keyed hash of an address: counters never hold the address itself. The key is the master key; without one
// a per-process random key is used (counters then reset on restart and are per replica).
func IPKey(ip string) string {
	ipKeyOnce.Do(func() {
		if k, err := secretbox.MasterKey(); err == nil {
			ipKey = k
		} else {
			ipKey = make([]byte, 32)
			_, _ = rand.Read(ipKey)
		}
	})
	h := hmac.New(sha256.New, ipKey)
	h.Write([]byte("site-ip\x00" + ip))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

const (
	formPerIPHour  = 5
	formPerSiteDay = 200
	dupWindow      = 10 * time.Minute
	consentVersion = "v1"
)

// FormAllowed counts this submission against the per-address hourly and per-site daily limits.
func FormAllowed(ctx context.Context, pool *pgxpool.Pool, siteID, ip string) error {
	busy := command.NewError(command.RateLimited, "Too many messages right now. Please call us instead.")
	n, err := db.Bump(ctx, pool, "siteform:ip:"+siteID+":"+IPKey(ip), false)
	if err != nil {
		return command.DB(err)
	}
	if n > formPerIPHour {
		return busy
	}
	n, err = db.Bump(ctx, pool, "siteform:site:"+siteID, true)
	if err != nil {
		return command.DB(err)
	}
	if n > formPerSiteDay {
		return busy
	}
	return nil
}

// ---------- the form ----------

// Submission is one validated contact-form post.
type Submission struct{ Name, Phone, Email, Message, ConsentText, ItemID string }

// ConsentTexts are the approved consent sentences, by id. The form sends an id; the text and its version are the
// server's own (17.8). The first is the default for a client that sends none.
const DefaultConsentID = "enquiry-v1"

// ConsentFor returns the approved text for a consent id and the site's name, and its version.
func ConsentFor(id, siteName string) (text, version string, ok bool) {
	if id == "" {
		id = DefaultConsentID
	}
	if id != DefaultConsentID {
		return "", "", false
	}
	return "I agree that " + siteName + " may use these details to reply to my enquiry.", consentVersion, true
}

var errNoSpace = command.NewError(command.Conflict, "This website cannot take messages right now. Please call or email instead.")

// Submit stores a lead for a published site. It re-reads the site under a row lock, so a takedown, unpublish or pause
// that happened after the caller's lookup wins. The same email and message (or phone and message when email is empty) within ten minutes is a quiet success.
func Submit(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, p Public, in Submission) error {
	sys := command.Principal{Kind: command.KindSystem, WorkspaceID: p.WorkspaceID, UserID: &p.PublishedBy, Role: command.Admin}
	noSpace := false
	err := reg.WithTx(ctx, pool, sys, func(c *command.Ctx) error {
		var data, snap []byte
		var paused, down bool
		var lead *string
		var locked string
		err := c.Tx.QueryRow(ctx, `select id::text from sites.sites where workspace_id = $1::uuid and id = $2::uuid for update`, p.WorkspaceID, p.SiteID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return command.ErrNotFound("site")
		}
		if err != nil {
			return command.DB(err)
		}
		err = c.Tx.QueryRow(ctx, `select v.data, s.form_paused, s.lead_space_id::text, coalesce(pl.taken_down_at is not null, false), s.catalog_published
			from sites.sites s join sites.site_versions v on v.workspace_id = s.workspace_id and v.site_id = s.id and v.id = s.published_version_id
			left join sites.site_platform pl on pl.site_id = s.id
			where s.workspace_id = $1::uuid and s.id = $2::uuid`, p.WorkspaceID, p.SiteID).Scan(&data, &paused, &lead, &down, &snap)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (down || paused || !HasBlock(data, "ContactForm"))) {
			return command.ErrNotFound("site")
		}
		if err != nil {
			return command.DB(err)
		}
		sum := submissionFingerprint(in)
		print := hex.EncodeToString(sum[:])
		var dup bool
		if err := c.Tx.QueryRow(ctx, `select exists (select 1 from sites.form_log where site_id = $1::uuid and fingerprint = $2 and created_at > now() - $3 * interval '1 second')`,
			p.SiteID, print, dupWindow.Seconds()).Scan(&dup); err != nil {
			return command.DB(err)
		}
		if dup {
			return nil
		}
		var space *string
		if err := c.Tx.QueryRow(ctx, `select coalesce((select id from core.spaces where id = $1::uuid), (select id from core.spaces order by created_at, id limit 1))::text`, lead).Scan(&space); err != nil {
			return command.DB(err)
		}
		admins, err := adminIDs(c)
		if err != nil {
			return err
		}
		note := func(title string, noteID *string) error {
			ns := make([]notify.Notification, 0, len(admins))
			for _, a := range admins {
				ns = append(ns, notify.Notification{WorkspaceID: p.WorkspaceID, UserID: a, Kind: "lead", Title: title, NoteID: noteID})
			}
			_, err := notify.Send(ctx, c.Tx, ns...)
			return err
		}
		if space == nil {
			noSpace = true
			return note("A website enquiry could not be saved: this workspace has no space for leads", nil)
		}
		if about := aboutLine(snap, in.ItemID); about != "" { // the name and price come from the published snapshot, never from the client
			in.Message = about + "\n" + in.Message
		}
		consent := fmt.Sprintf("Consent given on the website contact form (text %s, %s). Shown: %s", consentVersion, time.Now().UTC().Format(time.RFC3339), in.ConsentText)
		out, err := c.Call("inquiry.receive", map[string]any{"space_id": *space, "name": in.Name, "phone": in.Phone, "email": in.Email,
			"message": in.Message, "source": "website", "consent": truncate(consent, 500)})
		if err != nil {
			return err
		}
		var r struct {
			DealID string `json:"deal_id"`
		}
		if err := json.Unmarshal(out, &r); err != nil {
			return err
		}
		if _, err := c.Tx.Exec(ctx, "insert into sites.form_log (workspace_id, site_id, fingerprint, deal_id) values ($1::uuid, $2::uuid, $3, $4::uuid)", p.WorkspaceID, p.SiteID, print, r.DealID); err != nil {
			return command.DB(err)
		}
		if _, err := c.Tx.Exec(ctx, "update sites.sites set form_count = form_count + 1 where workspace_id = $1::uuid and id = $2::uuid", p.WorkspaceID, p.SiteID); err != nil {
			return command.DB(err)
		}
		return note("New website enquiry from "+in.Name, &r.DealID)
	})
	if err == nil && noSpace {
		return errNoSpace
	}
	return err
}

func submissionFingerprint(in Submission) [32]byte {
	identity := strings.ToLower(strings.TrimSpace(in.Email))
	if identity == "" {
		identity = digits(in.Phone)
	}
	return sha256.Sum256([]byte(identity + "\x00" + in.Message))
}

// adminIDs are the active owners and admins (at most 20); they read every space, so each can read the lead.
func adminIDs(c *command.Ctx) ([]string, error) {
	rows, err := c.Tx.Query(c.Context, `select user_id::text from core.memberships where workspace_id = $1::uuid and role in ('owner','admin') and deactivated_at is null order by created_at limit 20`, c.WorkspaceID())
	if err != nil {
		return nil, command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, command.DB(err)
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// PruneForm drops old form-log rows (the 10-minute duplicate check and the 7-day count need far less than 30 days) and counters.
func PruneForm(ctx context.Context, pool *pgxpool.Pool) error {
	if err := db.PruneBuckets(ctx, pool); err != nil {
		return err
	}
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, ws := range wss {
		tx, err := db.BeginTenant(ctx, pool, ws)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "delete from sites.form_log where created_at < now() - interval '30 days'")
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
	}
	return nil
}

// PostSummary is a published post without its body.
type PostSummary struct {
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	CoverImageID *string   `json:"cover_image_id"`
	OGImageID    *string   `json:"og_image_id"`
	PublishedAt  time.Time `json:"published_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const BlogPerPage = 10

// PublicPosts is one page of a live site's published posts, newest first. limit 0 means BlogPerPage; the RSS feed asks for 20.
func PublicPosts(ctx context.Context, pool *pgxpool.Pool, siteID string, page, limit int) (map[string]any, error) {
	if limit <= 0 || limit > 20 {
		limit = BlogPerPage
	}
	if page < 1 || page > 10000 {
		page = 1
	}
	rows, err := pool.Query(ctx, "select slug, title, description, cover_image_id::text, og_image_id::text, published_at, updated_at, total from sites.public_posts($1::uuid, $2, $3)", siteID, limit, (page-1)*limit)
	if err != nil {
		return nil, command.DB(err)
	}
	posts := []PostSummary{}
	var total int64
	for rows.Next() {
		var p PostSummary
		if err := rows.Scan(&p.Slug, &p.Title, &p.Description, &p.CoverImageID, &p.OGImageID, &p.PublishedAt, &p.UpdatedAt, &total); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if len(posts) == 0 { // no published post, or past the end: the web answers 404
		return nil, command.ErrNotFound("posts")
	}
	return map[string]any{"posts": posts, "page": page, "per_page": limit, "total": total, "pages": (total + int64(limit) - 1) / int64(limit)}, nil
}

// PublicPost is one published post with its body, checked against the grammar again before it is served.
func PublicPost(ctx context.Context, pool *pgxpool.Pool, siteID, slug string) (map[string]any, error) {
	var p PostSummary
	var id string
	var body []byte
	err := pool.QueryRow(ctx, "select id::text, slug, title, description, cover_image_id::text, og_image_id::text, body, published_at, updated_at from sites.public_post($1::uuid, $2)", siteID, slug).
		Scan(&id, &p.Slug, &p.Title, &p.Description, &p.CoverImageID, &p.OGImageID, &body, &p.PublishedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("post")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if ValidatePost(body, nil) != nil {
		return nil, command.ErrNotFound("post")
	}
	return map[string]any{"slug": p.Slug, "title": p.Title, "description": p.Description, "cover_image_id": p.CoverImageID, "og_image_id": p.OGImageID,
		"body": json.RawMessage(body), "published_at": p.PublishedAt, "updated_at": p.UpdatedAt}, nil
}
