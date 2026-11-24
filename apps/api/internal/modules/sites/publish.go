package sites

// Publishing, platform controls, images and the plan allowance (docs/design/site-builder-phase1.md sections 3, 17).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
)

func publishCommands(id, ver props) []command.Command {
	R, W, A := command.GroupRead, command.GroupWriteItems, command.Admin
	img := mk("site.image_upload", W, command.Member, false, "Upload a JPG or PNG (at most 1 MB) to this website; it is re-encoded. Returns the image id to use in image props.",
		obj([]string{"id", "filename", "data_base64"}, props{"id": id, "filename": str("file name"), "data_base64": str("the photo, base64")}), imageUpload)
	pub := mk("site.public_get", R, command.Member, false, "Internal: the published page for a request host (used by the web server).",
		obj([]string{"host"}, props{"host": str("<subdomain>.<SITES_DOMAIN>")}), publicGet)
	pub.Internal = true
	return []command.Command{
		mk("site.subdomain_check", R, A, false, "Check a website address: available, or why not (invalid, reserved, taken).",
			obj([]string{"id", "subdomain"}, props{"id": id, "subdomain": str("the address, without the domain")}), subdomainCheck),
		mk("site.publish", W, A, true, "Publish the draft. The first publish needs a subdomain and fixes it. The person must have a verified email.",
			obj([]string{"id", "expected_version_id"}, props{"id": id, "expected_version_id": ver, "subdomain": str("the address (first publish only)")}), publish),
		mk("site.unpublish", W, A, true, "Take the website offline (the address stays reserved).", obj([]string{"id"}, props{"id": id}), unpublish),
		mk("site.form_pause", W, A, false, "Pause or resume the contact form.",
			obj([]string{"id", "paused"}, props{"id": id, "paused": props{"type": "boolean"}}), formPause),
		img,
		mk("site.image_get", R, command.Member, false, "A photo of this website (the builder preview).",
			obj([]string{"id", "image_id"}, props{"id": id, "image_id": str("image id")}), imageGet),
		mk("site.platform_set", W, A, true, "Platform admins only: pro_on, pro_off, takedown, restore or rename a site, found by address or workspace slug.",
			obj([]string{"action", "reason"}, props{"subdomain": str("current address"), "workspace_slug": str("or the workspace slug"), "action": str("pro_on, pro_off, takedown, restore or rename"),
				"reason": str("3 to 500 characters, kept in the audit"), "new_subdomain": str("for rename")}), platformSet),
		pub,
	}
}

// skipVerify is the test-only switch for the email gate; it is ignored on an https deployment.
func skipVerify() bool {
	return os.Getenv("SITES_TEST_SKIP_VERIFY") == "1" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL"))), "https://")
}

// CheckEnv is called at startup: it refuses a SITES_DOMAIN that overlaps the app's host and the test switch on https.
func CheckEnv() error {
	base := strings.ToLower(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")))
	if os.Getenv("SITES_TEST_SKIP_VERIFY") == "1" && strings.HasPrefix(base, "https://") {
		return errors.New("SITES_TEST_SKIP_VERIFY must not be set when PUBLIC_BASE_URL is https")
	}
	d, _, _ := strings.Cut(SitesDomain(), ":")
	if d == "" || base == "" || d == "localhost" { // local development serves sites as <name>.localhost
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	app := u.Hostname()
	if app == d || strings.HasSuffix(app, "."+d) || strings.HasSuffix(d, "."+app) {
		return fmt.Errorf("SITES_DOMAIN %q must be a different domain from the app host %q", d, app)
	}
	return nil
}

func canPublish(c *command.Ctx) (configured, verified bool, err error) {
	if skipVerify() {
		return SitesDomain() != "", true, nil
	}
	if err = c.Tx.QueryRow(c.Context, "select email_verified_at is not null from core.users where id = $1::uuid", c.Principal.UserID).Scan(&verified); err != nil {
		return false, false, command.DB(err)
	}
	return SitesDomain() != "" && (verified || mailer.Configured()), verified, nil
}

// getExtras adds what the builder needs beside the page: photos, the monthly allowance, the publish gate, where
// enquiries land and how many arrived this week.
func getExtras(c *command.Ctx, site siteOut, out props) error {
	imgs, err := c.Call("file.image_list", props{"site_id": site.ID})
	if err != nil {
		return err
	}
	out["images"] = json.RawMessage(imgs)
	used, start, next, err := editsUsed(c, site.ID)
	if err != nil {
		return err
	}
	_ = start
	out["ai_edits"] = props{"used": used, "limit": editLimit(site.Pro), "resets_at": next}
	configured, verified, err := canPublish(c)
	if err != nil {
		return err
	}
	out["can_publish"] = props{"configured": configured, "verified": verified}
	var spaceID, spaceName *string
	if err := c.Tx.QueryRow(c.Context, `select sp.id::text, sp.name from core.spaces sp where sp.id = coalesce((select lead_space_id from sites.sites where workspace_id = $1::uuid and id = $2::uuid),
		(select id from core.spaces order by created_at, id limit 1))`, c.WorkspaceID(), site.ID).Scan(&spaceID, &spaceName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return command.DB(err)
	}
	if spaceID != nil {
		out["lead_space"] = props{"id": *spaceID, "name": *spaceName}
	} else {
		out["lead_space"] = nil
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from sites.form_log where site_id = $1::uuid and created_at > now() - interval '7 days'", site.ID).Scan(&n); err != nil {
		return command.DB(err)
	}
	out["leads_7d"] = n
	return nil
}

// ---------- images ----------

// siteImages is the set of image ids uploaded to a site.
func siteImages(c *command.Ctx, siteID string) (map[string]bool, error) {
	raw, err := c.Call("file.image_list", props{"site_id": siteID})
	if err != nil {
		return nil, err
	}
	var l []struct{ ID string }
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, x := range l {
		m[x.ID] = true
	}
	return m, nil
}

// checkImages refuses a page that uses a photo which is not one of this site's.
func checkImages(c *command.Ctx, siteID string, data json.RawMessage) error {
	ids := ImageIDs(data)
	if len(ids) == 0 {
		return nil
	}
	owned, err := siteImages(c, siteID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !owned[id] {
			return command.ErrInvalid("the page uses a photo that is not on this website")
		}
	}
	return nil
}

func imageUpload(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID, Filename string
		DataBase64   string `json:"data_base64"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, true) // the site row lock makes the 40-photo count exact
	if err != nil {
		return nil, err
	}
	out, err := c.Call("file.image_put", props{"site_id": site.ID, "filename": in.Filename, "data_base64": in.DataBase64})
	if err != nil {
		return nil, err
	}
	var r struct {
		ID, Filename string
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	return props{"image": props{"id": r.ID, "filename": r.Filename}}, nil
}

func imageGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID      string
		ImageID string `json:"image_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	out, err := c.Call("file.image_get", props{"site_id": site.ID, "id": in.ImageID})
	return json.RawMessage(out), err
}

// ---------- allowance ----------

// editsUsed counts chat turns that changed the page this IST month, plus turns still queued or running.
func editsUsed(c *command.Ctx, siteID string) (n int, start, next time.Time, err error) {
	start, next = monthWindow(time.Now())
	err = c.Tx.QueryRow(c.Context, `select count(*) from sites.site_turns where workspace_id = $1::uuid and site_id = $2::uuid
		and (status in ('queued','running') or (status = 'done' and version_id is not null and created_at >= $3 and created_at < $4))`,
		c.WorkspaceID(), siteID, start, next).Scan(&n)
	return n, start, next, command.DB(err)
}

func checkAllowance(c *command.Ctx, site siteOut) error {
	used, _, next, err := editsUsed(c, site.ID)
	if err != nil {
		return err
	}
	limit := editLimit(site.Pro)
	if used >= limit {
		return command.NewError(command.PlanLimit, fmt.Sprintf("You've used all %d AI edits for this month. You can still move, duplicate and delete sections and replace images. Edits come back on 1 %s.", limit, next.Month()))
	}
	return nil
}

// ---------- publish ----------

func subdomainCheck(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Subdomain string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	sub, reason := CheckSubdomain(in.Subdomain)
	if reason == "" {
		var taken bool
		if err := c.Tx.QueryRow(c.Context, "select sites.subdomain_taken($1, $2::uuid)", sub, site.ID).Scan(&taken); err != nil {
			return nil, command.DB(err)
		}
		if taken {
			reason = "taken"
		}
	}
	out := props{"subdomain": sub, "available": reason == ""}
	if reason != "" {
		out["reason"] = reason
	}
	return out, nil
}

func publish(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		Subdomain         string
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if SitesDomain() == "" {
		return nil, command.ErrInvalid("Publishing is not set up on this server.")
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if site.TakenDown != nil {
		return nil, command.ErrForbidden(site.TakenDown.Reason)
	}
	_, verified, err := canPublish(c)
	if err != nil {
		return nil, err
	}
	if !verified {
		if !mailer.Configured() {
			return nil, command.ErrForbidden("Publishing is unavailable: this server cannot send the email needed to verify your address.")
		}
		return nil, command.ErrForbidden("Verify your email to publish.")
	}
	if err := checkDraft(c, site, in.ExpectedVersionID); err != nil {
		return nil, err
	}
	if err := Validate(data); err != nil {
		return nil, command.ErrInvalid(err.Error())
	}
	if err := checkImages(c, site.ID, data); err != nil {
		return nil, err
	}
	user, err := userID(c)
	if err != nil {
		return nil, err
	}
	if site.Subdomain == nil {
		sub, reason := CheckSubdomain(in.Subdomain)
		if reason == "" {
			var taken bool
			if err := c.Tx.QueryRow(c.Context, "select sites.subdomain_taken($1, $2::uuid)", sub, site.ID).Scan(&taken); err != nil {
				return nil, command.DB(err)
			}
			if taken {
				reason = "taken"
			}
		}
		switch reason {
		case "invalid":
			return nil, command.ErrInvalid("Use 3 to 40 letters, numbers and single hyphens for the address.")
		case "reserved":
			return nil, command.ErrInvalid("That address is not allowed.")
		case "taken":
			return nil, command.ErrConflict("That address was just taken")
		}
		_, err := c.Tx.Exec(c.Context, `update sites.sites set subdomain = $3, lead_space_id = (select id from core.spaces order by created_at, id limit 1)
			where workspace_id = $1::uuid and id = $2::uuid`, c.WorkspaceID(), site.ID, sub)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, command.ErrConflict("That address was just taken")
		}
		if err != nil {
			return nil, command.DB(err)
		}
		site.Subdomain = &sub
	}
	if _, err := c.Tx.Exec(c.Context, `update sites.sites set published_version_id = draft_version_id, published_at = now(), published_by = $3::uuid, updated_at = now()
		where workspace_id = $1::uuid and id = $2::uuid`, c.WorkspaceID(), site.ID, user); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("site.published", nil, props{"site_id": site.ID, "version_id": site.DraftVersionID, "subdomain": *site.Subdomain}); err != nil {
		return nil, err
	}
	site, _, err = loadSite(c, site.ID, false)
	if err != nil || site.URL == nil {
		return nil, err
	}
	return props{"site": site, "url": *site.URL}, nil
}

func unpublish(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update sites.sites set published_version_id = null, published_at = null, updated_at = now() where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), site.ID); err != nil {
		return nil, command.DB(err)
	}
	if site.Subdomain != nil {
		if _, err := c.Emit("site.unpublished", nil, props{"site_id": site.ID, "subdomain": *site.Subdomain}); err != nil {
			return nil, err
		}
	}
	site, _, err = loadSite(c, site.ID, false)
	return props{"site": site}, err
}

func formPause(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID     string
		Paused bool
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update sites.sites set form_paused = $3 where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), site.ID, in.Paused); err != nil {
		return nil, command.DB(err)
	}
	site, _, err = loadSite(c, site.ID, false)
	return props{"site": site}, err
}

// ---------- platform ----------

func platformSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	if err := c.PlatformAdmin(); err != nil {
		return nil, err
	}
	var in struct {
		Subdomain     string `json:"subdomain"`
		WorkspaceSlug string `json:"workspace_slug"`
		Action        string `json:"action"`
		Reason        string `json:"reason"`
		NewSubdomain  string `json:"new_subdomain"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	in.Subdomain = strings.ToLower(strings.TrimSpace(in.Subdomain))
	if (in.Subdomain == "") == (in.WorkspaceSlug == "") {
		return nil, command.ErrInvalid("give the address or the workspace slug, not both")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.Reason)); n < 3 || n > 500 {
		return nil, command.ErrInvalid("reason must be 3 to 500 characters")
	}
	var newSub *string
	if in.Action == "rename" {
		sub, reason := CheckSubdomain(in.NewSubdomain)
		if reason == "invalid" {
			return nil, command.ErrInvalid("new_subdomain is not a valid address")
		}
		newSub = &sub
	}
	var out struct {
		WorkspaceID, SiteID string
		Subdomain           *string
		Pro, TakenDown      bool
	}
	err := c.Tx.QueryRow(c.Context, "select workspace_id::text, site_id::text, subdomain, pro, taken_down from sites.platform_set($1::uuid, nullif($2,''), nullif($3,''), $4, $5, $6)",
		c.Principal.UserID, in.Subdomain, in.WorkspaceSlug, in.Action, strings.TrimSpace(in.Reason), newSub).Scan(&out.WorkspaceID, &out.SiteID, &out.Subdomain, &out.Pro, &out.TakenDown)
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, command.ErrNotFound("site")
	case errors.As(err, &pg) && pg.Code == "23505":
		return nil, command.ErrConflict("That address is taken")
	case errors.As(err, &pg) && pg.Code == "22023":
		return nil, command.ErrInvalid(pg.Message)
	case err != nil:
		return nil, command.DB(err)
	}
	if _, err := c.Emit("site.platform_"+in.Action, nil, props{"site_id": out.SiteID}); err != nil {
		return nil, err
	}
	return props{"site_id": out.SiteID, "subdomain": out.Subdomain, "pro": out.Pro, "taken_down": out.TakenDown}, nil
}

// ---------- public read ----------

func publicGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ Host string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	p, err := LookupHost(c.Context, c.Tx, in.Host)
	if err != nil {
		return nil, err
	}
	return PublicJSON(p), nil
}

// PublicJSON is the answer to the web server's read of a published site.
func PublicJSON(p Public) props {
	return props{"site_id": p.SiteID, "version_id": p.VersionID, "name": p.Name, "data": p.Data, "pro": p.Pro, "published_at": p.PublishedAt,
		"url": p.URL, "form_paused": p.FormPaused}
}
