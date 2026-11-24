package sites

// Share images (17.1): rendered at publish, not on request. The API asks the web app's internal route POST /_og/render
// (header x-sites-token) for a 1200x630 PNG per page and per published post, stores it as a generated site image
// through the image pipeline and records its id. A failed render means no image. One render at a time per site.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
)

// OGJobKind is the core.jobs kind the worker must route to RunOGJob.
const OGJobKind = "site.og"

const (
	ogDeadline = 3 * time.Second
	ogMaxBytes = 700_000
	ogPostsMax = 20 // posts rendered per site publish; the rest keep their image or get one when they are published
)

func ogEnabled() bool {
	return strings.TrimSpace(os.Getenv("SITES_WEB_URL")) != "" && os.Getenv("SITES_SERVER_TOKEN") != ""
}

// enqueueOG queues the render of a site's page images (postID empty) or one post's image. It does nothing when the web
// app's address is not configured.
func enqueueOG(c *command.Ctx, siteID, versionID, postID string) error {
	if !ogEnabled() {
		return nil
	}
	payload, _ := command.Marshal(map[string]string{"site_id": siteID, "version_id": versionID, "post_id": postID})
	_, err := c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, max_attempts) values ($1::uuid, $2, $3, 3)", c.WorkspaceID(), OGJobKind, json.RawMessage(payload))
	return command.DB(err)
}

// pruneGenerated deletes the generated images no page or post refers to.
func pruneGenerated(c *command.Ctx, siteID string) error {
	raw, err := c.Call("file.image_list", props{"site_id": siteID, "generated": true})
	if err != nil {
		return err
	}
	var all []struct{ ID string }
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}
	rows, err := c.Tx.Query(c.Context, `select v::text from sites.sites s, jsonb_each_text(s.page_og_images) e(k, v) where s.workspace_id = $1::uuid and s.id = $2::uuid
		union select og_image_id::text from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and og_image_id is not null`, c.WorkspaceID(), siteID)
	if err != nil {
		return command.DB(err)
	}
	used, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return command.DB(err)
	}
	keep := map[string]bool{}
	for _, u := range used {
		keep[u] = true
	}
	for _, g := range all {
		if !keep[g.ID] {
			if _, err := c.Call("file.image_delete", props{"site_id": siteID, "id": g.ID}); err != nil {
				return err
			}
		}
	}
	return nil
}

type ogRequest struct {
	Kind     string  `json:"kind"`
	SiteName string  `json:"site_name"`
	Title    string  `json:"title"`
	Theme    ogTheme `json:"theme"`
	LogoURL  string  `json:"logo_url,omitempty"`
	PhotoURL string  `json:"photo_url,omitempty"`
}
type ogTheme struct {
	Primary    string `json:"primary"`
	Background string `json:"background"`
	Text       string `json:"text"`
}

var ogClient = &http.Client{Timeout: ogDeadline, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// renderOG asks the web app for one PNG. Anything but a small PNG is a failure.
func renderOG(ctx context.Context, req ogRequest) ([]byte, error) {
	body, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(ctx, ogDeadline)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(strings.TrimSpace(os.Getenv("SITES_WEB_URL")), "/")+"/_og/render", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-sites-token", os.Getenv("SITES_SERVER_TOKEN"))
	res, err := ogClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/png") {
		return nil, fmt.Errorf("share image: web app answered %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, ogMaxBytes+1))
	if err != nil || len(b) > ogMaxBytes || len(b) < 8 || string(b[1:4]) != "PNG" {
		return nil, errors.New("share image: not a usable PNG")
	}
	return b, nil
}

type ogTarget struct {
	key, postID string // key is "" for Home or a page address for a page; postID for a post
	req         ogRequest
}

// ogTargets reads what to render. Pages come from the published version; posts are the published ones without an
// image (or just the named one).
func ogTargets(c *command.Ctx, siteID, version, postID string) ([]ogTarget, bool, error) {
	var name string
	var pubVersion *string
	var data []byte
	err := c.Tx.QueryRow(c.Context, `select s.name, s.published_version_id::text, coalesce(v.data, 'null'::jsonb) from sites.sites s
		left join sites.site_versions v on v.workspace_id = s.workspace_id and v.site_id = s.id and v.id = s.published_version_id
		where s.workspace_id = $1::uuid and s.id = $2::uuid and not exists (select 1 from sites.site_platform p where p.site_id = s.id and p.taken_down_at is not null)`,
		c.WorkspaceID(), siteID).Scan(&name, &pubVersion, &data)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (pubVersion == nil || (postID == "" && *pubVersion != version))) {
		return nil, false, nil // gone, unpublished, taken down, or a newer publish has its own job
	}
	if err != nil {
		return nil, false, command.DB(err)
	}
	var d struct {
		Root struct {
			Props struct {
				Title string  `json:"title"`
				Theme ogTheme `json:"theme"`
			} `json:"props"`
		} `json:"root"`
		Content []docBlock `json:"content"`
		Pages   []docPage  `json:"pages"`
	}
	if json.Unmarshal(data, &d) != nil {
		return nil, false, nil
	}
	img := func(id string) string {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
		if base == "" || id == "" || !imageIDPattern.MatchString(id) {
			return ""
		}
		return base + "/api/sites/img/" + siteID + "/" + id
	}
	prop := func(typ, key string) string {
		for _, b := range d.Content {
			if b.Type == typ {
				s, _ := b.Props[key].(string)
				return s
			}
		}
		return ""
	}
	logo, hero := prop("Header", "logo"), prop("Hero", "image")
	req := func(kind, title, photo string) ogRequest {
		return ogRequest{Kind: kind, SiteName: name, Title: title, Theme: d.Root.Props.Theme, LogoURL: img(logo), PhotoURL: img(photo)}
	}
	var out []ogTarget
	if postID == "" {
		out = append(out, ogTarget{key: "", req: req("page", firstNonEmpty(d.Root.Props.Title, name), hero)})
		for _, p := range d.Pages {
			out = append(out, ogTarget{key: p.Slug, req: req("page", p.Title, hero)})
		}
	}
	rows, err := c.Tx.Query(c.Context, `select id::text, title, coalesce(cover_image_id::text, '') from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and status = 'published'
		and (($3 = '' and og_image_id is null) or id::text = $3) order by published_at desc, id limit $4`, c.WorkspaceID(), siteID, postID, ogPostsMax)
	if err != nil {
		return nil, false, command.DB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, cover string
		if err := rows.Scan(&id, &title, &cover); err != nil {
			return nil, false, command.DB(err)
		}
		out = append(out, ogTarget{postID: id, req: req("post", title, firstNonEmpty(cover, hero))})
	}
	return out, true, command.DB(rows.Err())
}

// RunOGJob is the handler of OGJobKind jobs. It returns an error only when another render of the same site is running
// (the job is retried); a render that fails just leaves that page or post without an image.
func RunOGJob(pool *pgxpool.Pool, reg *command.Registry) func(context.Context, jobs.Job) error {
	return func(ctx context.Context, j jobs.Job) error {
		var p struct {
			SiteID    string `json:"site_id"`
			VersionID string `json:"version_id"`
			PostID    string `json:"post_id"`
		}
		if !ogEnabled() || json.Unmarshal(j.Payload, &p) != nil || !uuidRe.MatchString(p.SiteID) || j.WorkspaceID == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		var got bool
		if err := conn.QueryRow(ctx, "select pg_try_advisory_lock(hashtextextended($1, 0))", "siteog:"+p.SiteID).Scan(&got); err != nil {
			return err
		}
		if !got {
			return errors.New("share images: another render of this site is running")
		}
		defer func() {
			_, _ = conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock(hashtextextended($1, 0))", "siteog:"+p.SiteID)
		}()
		sys := command.Principal{Kind: command.KindSystem, WorkspaceID: *j.WorkspaceID, Role: command.Admin}
		var targets []ogTarget
		live := false
		if err := reg.WithTx(ctx, pool, sys, func(c *command.Ctx) (err error) {
			targets, live, err = ogTargets(c, p.SiteID, p.VersionID, p.PostID)
			return err
		}); err != nil || !live {
			return err
		}
		for _, t := range targets {
			png, rerr := renderOG(ctx, t.req)
			if rerr != nil {
				png = nil
			}
			err := reg.WithTx(ctx, pool, sys, func(c *command.Ctx) error { return storeOG(c, p.SiteID, p.VersionID, t, png) })
			if err != nil && !errors.Is(err, errStaleOG) {
				return err
			}
			if errors.Is(err, errStaleOG) {
				break
			}
		}
		return reg.WithTx(ctx, pool, sys, func(c *command.Ctx) error { return pruneGenerated(c, p.SiteID) })
	}
}

var errStaleOG = errors.New("share image: the site changed while rendering")

// storeOG records one result under the site row lock; a nil png clears the reference.
func storeOG(c *command.Ctx, siteID, version string, t ogTarget, png []byte) error {
	var pub *string
	if err := c.Tx.QueryRow(c.Context, "select published_version_id::text from sites.sites where workspace_id = $1::uuid and id = $2::uuid for update", c.WorkspaceID(), siteID).Scan(&pub); err != nil {
		return command.DB(err)
	}
	if pub == nil || (t.postID == "" && *pub != version) {
		return errStaleOG
	}
	id := ""
	if png != nil {
		out, err := c.Call("file.image_put", props{"site_id": siteID, "filename": "share.png", "data_base64": base64.StdEncoding.EncodeToString(png), "generated": true})
		if err != nil {
			return nil // over the generated-image cap: no image
		}
		var r struct{ ID string }
		if err := json.Unmarshal(out, &r); err != nil {
			return err
		}
		id = r.ID
	}
	if t.postID != "" {
		_, err := c.Tx.Exec(c.Context, "update sites.posts set og_image_id = nullif($4, '')::uuid where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid and status = 'published'", c.WorkspaceID(), siteID, t.postID, id)
		return command.DB(err)
	}
	if id == "" {
		_, err := c.Tx.Exec(c.Context, "update sites.sites set page_og_images = page_og_images - $3 where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), siteID, t.key)
		return command.DB(err)
	}
	_, err := c.Tx.Exec(c.Context, "update sites.sites set page_og_images = page_og_images || jsonb_build_object($3::text, $4::text) where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), siteID, t.key, id)
	return command.DB(err)
}

// ogText reports whether a string is fit to be drawn: the web app draws it only when every letter is Latin. The check
// is repeated here so a request never asks for text the image cannot show.
func ogText(s string) string {
	for _, r := range s {
		if r > 0x024F && r != '₹' && !(r >= 0x2010 && r <= 0x2027) {
			return ""
		}
	}
	if utf8.RuneCountInString(s) > 120 {
		return string([]rune(s)[:120])
	}
	return s
}
