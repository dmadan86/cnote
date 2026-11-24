package sites

// Visit counts (design 17.8): the public beacon, the site.stats read and the 13-month retention sweep.

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	hitPerIPHour   = 120
	hitPerMinute   = 20000
	istToday       = `(now() at time zone 'Asia/Kolkata')::date`
	statsDays      = 60 // the card compares the last 30 days with the 30 before
	statsPages     = 20
	statsReferrers = 10
)

// ErrHitLimited means a beacon was over a limit; the handler answers 429 and writes nothing.
var ErrHitLimited = command.NewError(command.RateLimited, "too many visits recorded")

var (
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	refHostR = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

// ParseHit reads the beacon body {path, ref_host}: the page slug ("" is Home) and the referrer host ("" when none or
// not a plain host). ok is false when the body or the path is not usable.
func ParseHit(body []byte) (slug, ref string, ok bool) {
	var in struct {
		Path    string `json:"path"`
		RefHost string `json:"ref_host"`
	}
	if json.Unmarshal(body, &in) != nil {
		return "", "", false
	}
	p, _, _ := strings.Cut(in.Path, "#")
	p, _, _ = strings.Cut(p, "?")
	slug = strings.ToLower(strings.Trim(p, "/"))
	if slug != "" && (len(slug) > 40 || !slugRe.MatchString(slug)) {
		return "", "", false
	}
	ref = strings.ToLower(strings.TrimSpace(in.RefHost))
	if strings.Contains(ref, "/") { // a whole URL was sent: keep its host only
		if u, err := url.Parse(ref); err == nil {
			ref = u.Hostname()
		}
	}
	if len(ref) > 100 || !strings.Contains(ref, ".") || !refHostR.MatchString(ref) {
		ref = ""
	}
	return slug, ref, true
}

// HitAllowed counts one beacon against the per-address hourly and the platform-wide per-minute limits. Nothing
// else is written before it passes.
func HitAllowed(ctx context.Context, pool *pgxpool.Pool, ip string) error {
	n, err := db.Bump(ctx, pool, "sitehit:ip:"+IPKey(ip), false)
	if err != nil {
		return err
	}
	if n > hitPerIPHour {
		return ErrHitLimited
	}
	// the key carries the minute, so the helper's hourly bucket becomes a per-minute counter
	n, err = db.Bump(ctx, pool, "sitehit:all:"+time.Now().UTC().Format("200601021504"), false)
	if err != nil {
		return err
	}
	if n > hitPerMinute {
		return ErrHitLimited
	}
	return nil
}

// RecordHit adds one page view; sites.hit ignores sites that are not published, pages that do not exist and
// referrers that are the site's own hosts. The referrer cap is enforced inside it.
func RecordHit(ctx context.Context, pool *pgxpool.Pool, siteID, slug, ref string) error {
	_, err := pool.Exec(ctx, "select sites.hit($1::uuid, $2, $3, $4)", siteID, slug, ref, SitesDomain())
	return err
}

type dayOut struct {
	Day    string `json:"day"`
	Visits int    `json:"visits"`
	Views  int    `json:"views"`
}

func stats(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	days := []dayOut{}
	rows, err := c.Tx.Query(c.Context, `select g::date::text, coalesce(sum(st.n) filter (where st.kind = 'visit'), 0)::int, coalesce(sum(st.n) filter (where st.kind = 'page'), 0)::int
		from generate_series(`+istToday+` - $2::int + 1, `+istToday+`, interval '1 day') g
		left join sites.site_stats st on st.site_id = $1::uuid and st.day = g::date group by g order by g`, site.ID, statsDays)
	if err == nil {
		days, err = pgx.CollectRows(rows, pgx.RowToStructByPos[dayOut])
	}
	if err != nil {
		return nil, command.DB(err)
	}
	type row struct {
		Key   string
		Views int
	}
	top := func(sql string, limit int) ([]row, error) {
		rows, err := c.Tx.Query(c.Context, sql, site.ID, limit)
		if err != nil {
			return nil, command.DB(err)
		}
		out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
		if out == nil {
			out = []row{}
		}
		return out, command.DB(err)
	}
	pages, err := top(`select key, sum(n)::int v from sites.site_stats where site_id = $1::uuid and kind = 'page' and day > `+istToday+` - 30
		group by key order by v desc, key limit $2`, statsPages)
	if err != nil {
		return nil, err
	}
	refs, err := top(`select host, sum(n)::int v from sites.site_referrers where site_id = $1::uuid and day > `+istToday+` - 30
		group by host order by v desc, host limit $2`, statsReferrers)
	if err != nil {
		return nil, err
	}
	var enquiries int
	if err := c.Tx.QueryRow(c.Context, `select count(*)::int from sites.form_log where site_id = $1::uuid and (created_at at time zone 'Asia/Kolkata')::date > `+istToday+` - 30`, site.ID).Scan(&enquiries); err != nil {
		return nil, command.DB(err)
	}
	pageOut, refOut := make([]props, len(pages)), make([]props, len(refs))
	for i, p := range pages {
		pageOut[i] = props{"slug": p.Key, "views": p.Views}
	}
	for i, r := range refs {
		refOut[i] = props{"domain": r.Key, "views": r.Views}
	}
	return props{"days": days, "pages": pageOut, "referrers": refOut, "enquiries": enquiries}, nil
}

// StatsCommands registers site.stats.
func StatsCommands() []command.Command {
	return []command.Command{
		mk("site.stats", command.GroupRead, command.Member, false, "Visits for the last 30 days: per-day visits and page views (60 days, oldest first), top pages, top referrer hosts and website enquiries.",
			obj([]string{"id"}, props{"id": str("site id")}), stats),
	}
}

// StatsSweep deletes visit counters older than 13 months, workspace by workspace. The lead wires it into the worker sweep.
func StatsSweep(ctx context.Context, pool *pgxpool.Pool) error {
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
		for _, t := range []string{"sites.site_stats", "sites.site_referrers"} {
			if err == nil {
				_, err = tx.Exec(ctx, "delete from "+t+" where day < "+istToday+" - interval '13 months'")
			}
		}
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
