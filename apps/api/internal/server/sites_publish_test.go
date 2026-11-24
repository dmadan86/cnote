package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	pngenc "image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/sites"
)

const (
	siteDomain = "sites.example.test"
	siteToken  = "test-server-token"
)

// pubEnv is a signed-in owner with a site, a verified-email bypass, a sites domain and the owner as platform admin.
type pubEnv struct {
	*siteEnv
	site string
}

func newPubEnv(t *testing.T) *pubEnv {
	t.Helper()
	s := newSiteEnv(t)
	t.Setenv("SITES_DOMAIN", siteDomain)
	t.Setenv("SITES_TEST_SKIP_VERIFY", "1")
	t.Setenv("SITES_SERVER_TOKEN", siteToken)
	t.Setenv("PUBLIC_TRUSTED_PROXIES", "127.0.0.0/8")
	t.Setenv("CNOTE_PLATFORM_ADMIN_USER_IDS", s.owner.userID)
	return &pubEnv{s, s.create()["site"].(map[string]any)["id"].(string)}
}

func uniq() string { return "t" + rnd()[:10] }

func (p *pubEnv) publish(w who, sub string) resp {
	in := map[string]any{"id": p.site, "expected_version_id": p.draft()}
	if sub != "" {
		in["subdomain"] = sub
	}
	return p.call(w, "site.publish", in)
}

func (p *pubEnv) publishOK(sub string) string {
	p.t.Helper()
	r := adminOK(p.t, p.publish(p.owner, sub))
	return r["url"].(string)
}

// get does an HTTP request against the Go server and returns status, headers and body.
func (p *pubEnv) hit(method, path string, body any, hdr map[string]string) (int, http.Header, []byte) {
	p.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, p.api+path, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func (p *pubEnv) publicGet(sub string) (int, map[string]any) {
	p.t.Helper()
	st, _, b := p.hit("POST", "/api/sites/public_get", map[string]string{"host": sub + "." + siteDomain}, map[string]string{"x-sites-token": siteToken, "Content-Type": "application/json"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return st, m
}

func (p *pubEnv) platform(w who, in map[string]any) resp { return p.call(w, "site.platform_set", in) }

func pngBytes(t *testing.T, w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := pngenc.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func (p *pubEnv) upload(w who, data string) resp {
	return p.call(w, "site.image_upload", map[string]any{"id": p.site, "filename": "logo.png", "data_base64": data})
}

func (p *pubEnv) form(ip string, body map[string]any) (int, []byte) {
	st, _, b := p.hit("POST", "/api/sites/form/"+p.site, body, map[string]string{"Content-Type": "application/json", "X-Forwarded-For": ip})
	return st, b
}

func goodForm(msg string) map[string]any {
	return map[string]any{"name": "Asha", "phone": "+919876543210", "email": "asha@example.test", "message": msg, "consent": true, "consent_text": "I agree that Acme may reply."}
}

// 1. Publish, republish, unpublish; who may; the address rules; the public read serves the published version only.
func TestSitePublishFlow(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	p.fail(p.mia, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft(), "subdomain": sub}, 403) // admins only
	p.fail(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft()}, 400)                 // address required
	p.fail(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft(), "subdomain": "www"}, 400)
	p.fail(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft(), "subdomain": "Bad_Name"}, 400)
	p.fail(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": "00000000-0000-0000-0000-000000000000", "subdomain": sub}, 409)
	key := p.apiKey(p.owner)
	if r := p.cmd(p.gosv.URL, key, p.owner.ws, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft(), "subdomain": sub}, nil); r.status != 403 {
		t.Fatalf("api key publish: %d %s", r.status, r.body)
	}

	chk := func(s string) map[string]any {
		return p.ok(p.owner, "site.subdomain_check", map[string]any{"id": p.site, "subdomain": s})
	}
	eq(t, chk(" " + strings.ToUpper(sub) + " ")["available"], true)
	eq(t, chk("g00gle")["reason"], "reserved")
	eq(t, chk("ab")["reason"], "invalid")

	if st, _ := p.publicGet(sub); st != 404 {
		t.Fatalf("unpublished: %d", st)
	}
	url := p.publishOK(sub)
	eq(t, url, "https://"+sub+"."+siteDomain)
	g := p.get2()
	eq(t, g["subdomain"], sub)
	eq(t, g["published_version_id"], g["draft_version_id"])
	eq(t, chk(sub)["available"], true) // its own address is not "taken" for itself

	// a second workspace sees the address as taken and cannot publish it
	other := newSiteEnvOn(t, p)
	eq(t, other.ok(other.owner, "site.subdomain_check", map[string]any{"id": other.site, "subdomain": sub})["reason"], "taken")
	other.fail(other.owner, "site.publish", map[string]any{"id": other.site, "expected_version_id": other.draft(), "subdomain": sub}, 409)
	// ... and cannot see or touch the first workspace's site
	other.fail(other.owner, "site.get", map[string]any{"id": p.site}, 404)

	st, m := p.publicGet(sub)
	eq(t, st, 200)
	eq(t, m["site_id"], p.site)
	eq(t, m["url"], url)
	eq(t, propOf(m["data"], "Hero", "title"), propOf(p.getData(), "Hero", "title"))

	// a later draft does not change what is live until it is published again
	hero := blockOf(p.getData(), "Hero")
	p.ok(p.owner, "site.edit", map[string]any{"id": p.site, "expected_version_id": p.draft(), "ops": []map[string]any{{"tool": "update_block_props", "input": map[string]any{"id": hero, "props": map[string]any{"title": "Fresh title"}}}}})
	_, m = p.publicGet(sub)
	if propOf(m["data"], "Hero", "title") == "Fresh title" {
		t.Fatal("draft leaked to the public read")
	}
	if g := p.get2(); g["published_version_id"] == g["draft_version_id"] {
		t.Fatal("published pointer moved with the draft")
	}
	p.ok(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft(), "subdomain": "ignored-later"})
	_, m = p.publicGet(sub)
	eq(t, propOf(m["data"], "Hero", "title"), "Fresh title")
	eq(t, p.get2()["subdomain"], sub) // the address is fixed after the first publish

	// wrong token, unknown, odd hosts
	if st, _, _ := p.hit("POST", "/api/sites/public_get", map[string]string{"host": sub + "." + siteDomain}, map[string]string{"x-sites-token": "nope"}); st != 404 {
		t.Fatalf("bad token: %d", st)
	}
	for _, h := range []string{"nope-" + uniq() + "." + siteDomain, "a." + sub + "." + siteDomain, sub + ".other.test", siteDomain, sub + "." + siteDomain + ":3000"} {
		st, _, _ := p.hit("POST", "/api/sites/public_get", map[string]string{"host": h}, map[string]string{"x-sites-token": siteToken})
		eq(t, st, 404)
	}

	p.fail(p.mia, "site.unpublish", map[string]any{"id": p.site}, 403)
	p.ok(p.owner, "site.unpublish", map[string]any{"id": p.site})
	if st, _ := p.publicGet(sub); st != 404 {
		t.Fatalf("after unpublish: %d", st)
	}
	eq(t, p.get2()["subdomain"], sub) // still reserved for this site
	eq(t, other.ok(other.owner, "site.subdomain_check", map[string]any{"id": other.site, "subdomain": sub})["reason"], "taken")
}

func (p *pubEnv) getData() any { return p.get(p.owner, p.site)["data"] }
func (p *pubEnv) get2() map[string]any {
	return p.get(p.owner, p.site)["site"].(map[string]any)
}

// newSiteEnvOn is a second workspace (its own owner and site) on the same server.
func newSiteEnvOn(t *testing.T, p *pubEnv) *pubEnv {
	t.Helper()
	o := p.signup("Other", true)
	e := &agentEnv{p.env, o, o, o}
	s := &pubEnv{&siteEnv{e}, ""}
	s.site = s.create()["site"].(map[string]any)["id"].(string)
	return s
}

// 2. The verify gate: SMTP present means a code is needed; no SMTP fails closed; the flag lifts it for tests only.
func TestSitePublishNeedsVerifiedEmail(t *testing.T) {
	p := newPubEnv(t)
	t.Setenv("SITES_TEST_SKIP_VERIFY", "")
	t.Setenv("SMTP_URL", "")
	sub := uniq()
	r := p.publish(p.owner, sub)
	if r.status != 403 || !strings.Contains(string(r.body), "verify") {
		t.Fatalf("no smtp: %d %s", r.status, r.body)
	}
	eq(t, p.get(p.owner, p.site)["can_publish"], map[string]any{"configured": false, "verified": false})
	if r := p.do(p.api, "POST", "/api/auth/verify/request", p.owner, map[string]any{}, nil); r.status != 400 {
		t.Fatalf("request without smtp: %d %s", r.status, r.body)
	}

	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "C Note <noreply@example.test>")
	r = p.publish(p.owner, sub)
	if r.status != 403 || !strings.Contains(string(r.body), "Verify your email") {
		t.Fatalf("smtp: %d %s", r.status, r.body)
	}
	eq(t, p.get(p.owner, p.site)["can_publish"], map[string]any{"configured": true, "verified": false})
	me := func() any {
		return p.do(p.api, "GET", "/api/auth/me", p.owner, nil, nil).json()["user"].(map[string]any)["email_verified"]
	}
	eq(t, me(), false)

	if r := p.do(p.api, "POST", "/api/auth/verify/request", p.owner, map[string]any{}, nil); r.status != 200 {
		t.Fatalf("request: %d %s", r.status, r.body)
	}
	mail := waitMailRaw(t, sink, "verification code")
	code := regexp.MustCompile(`code is (\d{6})`).FindStringSubmatch(mail)[1]
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	confirm := func(c string) int {
		return p.do(p.api, "POST", "/api/auth/verify/confirm", p.owner, map[string]any{"code": c}, nil).status
	}
	eq(t, confirm(wrong), 400)
	eq(t, confirm(code), 200)
	eq(t, confirm(code), 400) // consumed
	eq(t, me(), true)
	p.publishOK(sub)

	// five wrong tries void a code; five sends an hour at most
	u2 := p.addMember(p.owner, p.signup("Zed", false), "admin")
	req := func() int { return p.do(p.api, "POST", "/api/auth/verify/request", u2, map[string]any{}, nil).status }
	eq(t, req(), 200)
	good := regexp.MustCompile(`code is (\d{6})`).FindStringSubmatch(waitMailRaw(t, sink, u2.email))[1]
	for i := 0; i < 5; i++ {
		p.do(p.api, "POST", "/api/auth/verify/confirm", u2, map[string]any{"code": "12345" + string(rune('0'+i))}, nil)
	}
	eq(t, p.do(p.api, "POST", "/api/auth/verify/confirm", u2, map[string]any{"code": good}, nil).status, 400)
	for i := 0; i < 4; i++ {
		eq(t, req(), 200)
	}
	eq(t, req(), 429)
}

func waitMailRaw(t *testing.T, s *smtpSink, substr string) string {
	t.Helper()
	m := waitMail(t, s, substr)
	return strings.ReplaceAll(strings.ReplaceAll(m, "=\r\n", ""), "=20", " ")
}

// 3. Platform actions: only listed user ids; the app role cannot write the platform tables or rename directly.
func TestSitePlatformActions(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	p.publishOK(sub)
	act := func(w who, action string, extra map[string]any) resp {
		in := map[string]any{"subdomain": sub, "action": action, "reason": "support request"}
		for k, v := range extra {
			in[k] = v
		}
		return p.platform(w, in)
	}
	eq(t, act(p.mia, "pro_on", nil).status, 403)
	other := p.addMember(p.owner, p.signup("Adm", false), "admin") // an admin who is not listed
	eq(t, act(other, "takedown", nil).status, 403)
	if r := p.cmd(p.gosv.URL, p.apiKey(p.owner), p.owner.ws, "site.platform_set", map[string]any{"subdomain": sub, "action": "pro_on", "reason": "abc"}, nil); r.status != 403 {
		t.Fatalf("api key: %d", r.status)
	}
	eq(t, p.platform(p.owner, map[string]any{"subdomain": sub, "action": "pro_on", "reason": "x"}).status, 400)
	eq(t, act(p.owner, "frobnicate", nil).status, 400)

	eq(t, p.get(p.owner, p.site)["ai_edits"].(map[string]any)["limit"], float64(100))
	r := adminOK(t, act(p.owner, "pro_on", nil))
	eq(t, r["pro"], true)
	g := p.get(p.owner, p.site)
	eq(t, g["site"].(map[string]any)["pro"], true)
	eq(t, g["ai_edits"].(map[string]any)["limit"], float64(500))
	_, m := p.publicGet(sub)
	eq(t, m["pro"], true)
	adminOK(t, act(p.owner, "pro_off", nil))

	adminOK(t, act(p.owner, "takedown", map[string]any{"reason": "phishing report"}))
	if st, _ := p.publicGet(sub); st != 404 {
		t.Fatalf("taken down: %d", st)
	}
	g = p.get(p.owner, p.site)["site"].(map[string]any)
	eq(t, g["taken_down"].(map[string]any)["reason"], "phishing report")
	rr := p.publish(p.owner, "")
	if rr.status != 403 || !strings.Contains(string(rr.body), "phishing report") {
		t.Fatalf("publish while down: %d %s", rr.status, rr.body)
	}
	adminOK(t, act(p.owner, "restore", nil))
	if st, _ := p.publicGet(sub); st != 200 {
		t.Fatalf("restored: %d", st)
	}

	neu := uniq()
	adminOK(t, act(p.owner, "rename", map[string]any{"new_subdomain": neu}))
	if st, _ := p.publicGet(sub); st != 404 {
		t.Fatalf("old name after rename: %d", st)
	}
	if st, _ := p.publicGet(neu); st != 200 {
		t.Fatalf("new name: %d", st)
	}
	eq(t, p.get2()["subdomain"], neu)
	eq(t, p.platform(p.owner, map[string]any{"subdomain": neu, "action": "rename", "new_subdomain": "Bad Name", "reason": "typo fix"}).status, 400)
	eq(t, p.platform(p.owner, map[string]any{"subdomain": "nosuch-" + uniq(), "action": "pro_on", "reason": "typo fix"}).status, 404)
	// by workspace slug
	slug := p.sql("select slug from core.workspaces where id = $1::uuid", p.owner.ws)
	eq(t, p.platform(p.owner, map[string]any{"workspace_slug": slug, "action": "pro_on", "reason": "typo fix"}).status, 200)

	// the audit keeps the actor, and old and new names
	if ap := adminPool(t); ap != nil {
		var n int
		if err := ap.QueryRow(context.Background(), "select count(*) from sites.platform_actions where site_id = $1::uuid and action = 'rename' and old_subdomain = $2 and new_subdomain = $3 and actor_user_id = $4::uuid", p.site, sub, neu, p.owner.userID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("audit: %d %v", n, err)
		}
	}

	// direct SQL as the app role: no INSERT, UPDATE or DELETE on the platform tables, and no renaming a published site
	ctx := context.Background()
	for _, q := range []string{
		"insert into sites.site_platform (workspace_id, site_id, pro) values ('" + p.owner.ws + "', '" + p.site + "', true)",
		"update sites.site_platform set pro = true",
		"update sites.site_platform set taken_down_at = null",
		"delete from sites.site_platform",
		"insert into sites.platform_actions (actor_user_id, actor_email, workspace_id, site_id, action, reason) values ('" + p.owner.userID + "', 'x', '" + p.owner.ws + "', '" + p.site + "', 'pro_on', 'abc')",
		"select count(*) from sites.platform_actions",
		"select * from sites.platform_set('" + p.owner.userID + "'::uuid, '" + sub + "', null, 'pro_on', 'abc', null)",
	} {
		tx, err := dbTenant(p.env, p.owner.ws)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, q)
		_ = tx.Rollback(ctx)
		if sqlState(err) != "42501" {
			t.Errorf("%q: want permission denied, got %v", q, err)
		}
	}
	tx, _ := dbTenant(p.env, p.owner.ws)
	_, err := tx.Exec(ctx, "update sites.sites set subdomain = 'stolen-name'")
	_ = tx.Rollback(ctx)
	eq(t, sqlState(err), "42501")
	tx, _ = dbTenant(p.env, p.owner.ws)
	_, err = tx.Exec(ctx, "update sites.sites set subdomain = null")
	_ = tx.Rollback(ctx)
	eq(t, sqlState(err), "42501")
}

// 4. Images: upload limits, ownership, public only when the published version references them.
func TestSiteImages(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	up := adminOK(t, p.upload(p.mia, pngBytes(t, 40, 30)))["image"].(map[string]any) // members may upload
	img := up["id"].(string)
	g := p.get(p.owner, p.site)
	eq(t, len(g["images"].([]any)), 1)

	// refusals: not an image, gif, too big, too many pixels
	eq(t, p.upload(p.owner, base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 hello"))).status, 400)
	eq(t, p.upload(p.owner, base64.StdEncoding.EncodeToString([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))).status, 400)
	eq(t, p.upload(p.owner, "!!!").status, 400)
	big := make([]byte, 1<<20+10)
	copy(big, []byte("\x89PNG\r\n\x1a\n"))
	eq(t, p.upload(p.owner, base64.StdEncoding.EncodeToString(big)).status, 400)
	eq(t, p.upload(p.owner, pngBytes(t, 4100, 4100)).status, 400) // 16.8 MP
	// a stranger cannot upload to this site
	str := p.signup("Str", true)
	eq(t, p.cmd(p.gosv.URL, str, str.ws, "site.image_upload", map[string]any{"id": p.site, "filename": "a.png", "data_base64": pngBytes(t, 4, 4)}, nil).status, 404)

	// not referenced by the published page: 404 in public, even published
	p.publishOK(sub)
	imgURL := "/api/sites/img/" + p.site + "/" + img
	if st, _, _ := p.hit("GET", imgURL, nil, nil); st != 404 {
		t.Fatalf("unreferenced: %d", st)
	}
	// draft preview for members only
	prev := "/api/w/" + p.owner.ws + "/sites/" + p.site + "/img/" + img
	if r := p.do(p.api, "GET", prev, p.mia, nil, nil); r.status != 200 || !bytes.HasPrefix(r.body, []byte("\x89PNG")) {
		t.Fatalf("preview: %d", r.status)
	}
	if st, _, _ := p.hit("GET", prev, nil, nil); st != 401 {
		t.Fatalf("preview without session: %d", st)
	}
	if r := p.do(p.api, "GET", prev, str, nil, nil); r.status == 200 {
		t.Fatal("preview for a stranger")
	}

	// reference it, republish, now public with a one-minute cache
	hero := blockOf(p.getData(), "Hero")
	p.ok(p.owner, "site.edit", map[string]any{"id": p.site, "expected_version_id": p.draft(), "ops": []map[string]any{{"tool": "update_block_props", "input": map[string]any{"id": hero, "props": map[string]any{"image": img}}}}})
	if st, _, _ := p.hit("GET", imgURL, nil, nil); st != 404 {
		t.Fatalf("draft-only reference: %d", st)
	}
	p.ok(p.owner, "site.publish", map[string]any{"id": p.site, "expected_version_id": p.draft()})
	st, h, b := p.hit("GET", imgURL, nil, nil)
	if st != 200 || h.Get("Cache-Control") != "public, max-age=60" || h.Get("Content-Type") != "image/png" || h.Get("Set-Cookie") != "" || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatalf("public image: %d %v", st, h)
	}
	// the image id under another site id is not found
	if st, _, _ := p.hit("GET", "/api/sites/img/"+strings.Replace(p.site, p.site[:4], "ffff", 1)+"/"+img, nil, nil); st != 404 {
		t.Fatalf("other site id: %d", st)
	}
	// another workspace's image id is refused by site.edit and cannot be chat-attached
	o := newSiteEnvOn(t, p)
	foreign := adminOK(t, o.upload(o.owner, pngBytes(t, 8, 8)))["image"].(map[string]any)["id"].(string)
	eq(t, p.editOps(p.owner, p.site, p.draft(), map[string]any{"tool": "update_block_props", "input": map[string]any{"id": hero, "props": map[string]any{"image": foreign}}}).status, 400)
	eq(t, p.call(p.owner, "site.chat", map[string]any{"id": p.site, "expected_version_id": p.draft(), "message": "use it", "image_ids": []string{foreign}}).status, 400)
	eq(t, o.call(o.owner, "site.image_get", map[string]any{"id": o.site, "image_id": img}).status, 404)

	// takedown and unpublish stop the image at once
	adminOK(t, p.platform(p.owner, map[string]any{"subdomain": sub, "action": "takedown", "reason": "abuse report"}))
	if st, _, _ := p.hit("GET", imgURL, nil, nil); st != 404 {
		t.Fatalf("taken down image: %d", st)
	}
}

func TestSiteImageCounts(t *testing.T) {
	p := newPubEnv(t)
	// 39 rows already: the 40th upload works, the 41st is refused (counted under the site-row lock)
	adminSQL(p.env, p.owner.ws, `insert into core.attachments (workspace_id, site_id, filename, content_type, size_bytes, storage_key, data)
		select $1::uuid, $2::uuid, 'x.png', 'image/png', 1, 'pg', '\x00' from generate_series(1, 39)`, p.owner.ws, p.site)
	adminOK(t, p.upload(p.owner, pngBytes(t, 4, 4)))
	eq(t, p.upload(p.owner, pngBytes(t, 4, 4)).status, 409)
	// 30 uploads an hour per person
	adminSQL(p.env, p.owner.ws, "delete from core.attachments where site_id = $1::uuid", p.site)
	adminSQL(p.env, p.owner.ws, "insert into core.rate_buckets (key, bucket, n) values ('img:' || $1, $2, 30) on conflict (key, bucket) do update set n = 30", p.owner.userID, db.BucketStart(time.Now(), false))
	eq(t, p.upload(p.owner, pngBytes(t, 4, 4)).status, 429)
}

// 5. The contact form and the lead it makes.
func TestSiteContactForm(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	if st, _ := p.form("10.0.0.1", goodForm("hi")); st != 404 {
		t.Fatalf("unpublished form: %d", st)
	}
	p.publishOK(sub)
	// the lead space is fixed at the first publish: the oldest space, even after a newer one appears
	oldest := p.sql("select id::text from core.spaces order by created_at, id limit 1")
	p.ok(p.owner, "space.create", map[string]any{"name": "Later space", "key": "LATE"})
	eq(t, p.sql("select lead_space_id::text from sites.sites"), oldest)
	eq(t, p.get(p.owner, p.site)["lead_space"].(map[string]any)["id"], oldest)

	st, body := p.form("10.0.0.1", goodForm("Do you do crowns?"))
	if st != 200 {
		t.Fatalf("form: %d %s", st, body)
	}
	eq(t, p.sql("select count(*)::text from crm.deals where source = 'website'"), "1")
	eq(t, p.sql("select n.space_id::text from crm.deals d join core.notes n on n.id = d.note_id where d.source = 'website'"), oldest)
	eq(t, p.sql("select count(*)::text from crm.activities where summary like 'Consent given on the website%'"), "1")
	eq(t, p.sql("select count(*)::text from crm.activities where summary = 'Do you do crowns?'"), "1")
	eq(t, p.sql("select form_count::text from sites.sites"), "1")
	eq(t, p.sql("select count(*)::text from core.notifications where kind = 'lead' and user_id = $1::uuid and title = 'New website enquiry from Asha'", p.owner.userID), "1")
	eq(t, p.sql("select count(*)::text from core.notifications where kind = 'lead' and user_id = $1::uuid", p.mia.userID), "0") // members are not told
	eq(t, p.get(p.owner, p.site)["leads_7d"], float64(1))

	// the same enquiry again within ten minutes is a quiet success
	dup := goodForm("Do you do crowns?")
	dup["phone"] = "+919876543211"
	if st, _ := p.form("10.0.0.2", dup); st != 200 {
		t.Fatalf("dup: %d", st)
	}
	eq(t, p.sql("select count(*)::text from crm.deals where source = 'website'"), "1")
	eq(t, p.sql("select form_count::text from sites.sites"), "1")

	// honeypot: success, nothing stored; no consent: 400; bad phone: 400; too big: 413
	hp := goodForm("bot")
	hp["company_website"] = "http://spam.test"
	if st, _ := p.form("10.0.0.3", hp); st != 200 {
		t.Fatalf("honeypot: %d", st)
	}
	nc := goodForm("no consent")
	nc["consent"] = false
	if st, _ := p.form("10.0.0.3", nc); st != 400 {
		t.Fatalf("no consent: %d", st)
	}
	bp := goodForm("bad phone")
	bp["phone"] = "12"
	if st, _ := p.form("10.0.0.3", bp); st != 400 {
		t.Fatalf("bad phone: %d", st)
	}
	if st, _ := p.form("10.0.0.3", goodForm(strings.Repeat("x", 5000))); st != 413 {
		t.Fatalf("big: %d", st)
	}
	eq(t, p.sql("select count(*)::text from crm.deals where source = 'website'"), "1")

	// five an hour from one address; another address still works; a forged X-Forwarded-For entry on the left changes nothing
	for i := 0; i < 5; i++ {
		if st, _ := p.form("10.0.0.9", goodForm("msg "+string(rune('a'+i)))); st != 200 {
			t.Fatalf("post %d: %d", i, st)
		}
	}
	if st, _ := p.form("10.0.0.9", goodForm("msg x")); st != 429 {
		t.Fatalf("sixth from one address: %d", st)
	}
	if st, _ := p.form("1.2.3.4, 10.0.0.9", goodForm("msg f")); st != 429 {
		t.Fatalf("forged left entry: %d", st)
	}
	if st, _ := p.form("10.0.0.10", goodForm("msg g")); st != 200 {
		t.Fatalf("other address: %d", st)
	}

	// paused, then resumed
	p.fail(p.mia, "site.form_pause", map[string]any{"id": p.site, "paused": true}, 403)
	eq(t, p.ok(p.owner, "site.form_pause", map[string]any{"id": p.site, "paused": true})["site"].(map[string]any)["form_paused"], true)
	if st, _ := p.form("10.0.0.20", goodForm("while paused")); st != 404 {
		t.Fatalf("paused: %d", st)
	}
	p.ok(p.owner, "site.form_pause", map[string]any{"id": p.site, "paused": false})
	if st, _ := p.form("10.0.0.20", goodForm("after pause")); st != 200 {
		t.Fatalf("resumed: %d", st)
	}

	// takedown: the form stops, and a submission that looked the site up before the takedown is refused by the recheck
	pub, err := sites.LookupID(context.Background(), p.pool, p.site)
	if err != nil {
		t.Fatal(err)
	}
	adminOK(t, p.platform(p.owner, map[string]any{"subdomain": sub, "action": "takedown", "reason": "abuse report"}))
	reg := command.NewRegistry(modules.Commands()...)
	if err := sites.Submit(context.Background(), p.pool, reg, pub, sites.Submission{Name: "Late", Phone: "+919876543211", Message: "race", ConsentText: "ok"}); err == nil {
		t.Fatal("submit after takedown was accepted")
	}
	if st, _ := p.form("10.0.0.21", goodForm("after takedown")); st != 404 {
		t.Fatalf("taken down: %d", st)
	}
	eq(t, p.sql("select count(*)::text from crm.deals where source = 'website' and (select title from core.notes where id = note_id) like '%Late%'"), "0")

	// the oldest space is gone: the lead goes to the current oldest one
	adminOK(t, p.platform(p.owner, map[string]any{"subdomain": sub, "action": "restore", "reason": "reviewed ok"}))
	adminSQL(p.env, p.owner.ws, "update sites.sites set lead_space_id = gen_random_uuid()")
	if st, _ := p.form("10.0.0.22", goodForm("space gone")); st != 200 {
		t.Fatalf("missing space: %d", st)
	}
	eq(t, p.sql("select count(*)::text from crm.deals d join core.notes n on n.id = d.note_id where d.source = 'website' and n.space_id = $1::uuid", oldest), "9")
}

// A site with no ContactForm block takes no leads.
func TestSiteFormNeedsContactFormBlock(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	cf := blockOf(p.getData(), "ContactForm")
	p.ok(p.owner, "site.edit", map[string]any{"id": p.site, "expected_version_id": p.draft(), "ops": []map[string]any{{"tool": "remove_block", "input": map[string]any{"id": cf}}}})
	p.publishOK(sub)
	if st, _ := p.form("10.0.0.1", goodForm("hello")); st != 404 {
		t.Fatalf("no form block: %d", st)
	}
}

// 6. The monthly allowance: changed turns count, the cap refuses chat only, Pro lifts it.
func TestSiteEditAllowance(t *testing.T) {
	p := newPubEnv(t)
	adminSQL(p.env, p.owner.ws, `insert into sites.site_turns (workspace_id, site_id, created_by, message, status, base_version_id, version_id, finished_at)
		select $1::uuid, $2::uuid, $3::uuid, 'm', 'done', draft_version_id, draft_version_id, now() from sites.sites, generate_series(1, 100)`, p.owner.ws, p.site, p.owner.userID)
	ed := p.get(p.owner, p.site)["ai_edits"].(map[string]any)
	eq(t, ed["used"], float64(100))
	eq(t, ed["limit"], float64(100))
	r := p.call(p.owner, "site.chat", map[string]any{"id": p.site, "expected_version_id": p.draft(), "message": "make it blue"})
	if r.status != 402 || !strings.Contains(string(r.body), "used all 100 AI edits") || !strings.Contains(string(r.body), "plan_limit") {
		t.Fatalf("at cap: %d %s", r.status, r.body)
	}
	hero := blockOf(p.getData(), "Hero")
	p.ok(p.owner, "site.edit", map[string]any{"id": p.site, "expected_version_id": p.draft(), "ops": []map[string]any{{"tool": "update_block_props", "input": map[string]any{"id": hero, "props": map[string]any{"title": "Still works"}}}}})
	// below the cap chat works again (a scripted model answers; it changes nothing, so it is free)
	adminSQL(p.env, p.owner.ws, "delete from sites.site_turns where id in (select id from sites.site_turns limit 5)")
	p.useModel(p.owner, newScriptedModel(t, func(modelReq) []map[string]any { return []map[string]any{mtext("Nothing to change.")} }))
	turn := p.chat(p.owner, p.site, p.draft(), "x")
	p.wait(p.owner, turn)
	eq(t, p.get(p.owner, p.site)["ai_edits"].(map[string]any)["used"], float64(95))
}

func TestSiteProLiftsAllowance(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	p.publishOK(sub)
	adminSQL(p.env, p.owner.ws, `insert into sites.site_turns (workspace_id, site_id, created_by, message, status, base_version_id, version_id, finished_at)
		select $1::uuid, $2::uuid, $3::uuid, 'm', 'done', draft_version_id, draft_version_id, now() from sites.sites, generate_series(1, 100)`, p.owner.ws, p.site, p.owner.userID)
	eq(t, p.call(p.owner, "site.chat", map[string]any{"id": p.site, "expected_version_id": p.draft(), "message": "x"}).status, 402)
	adminOK(t, p.platform(p.owner, map[string]any{"subdomain": sub, "action": "pro_on", "reason": "paid plan"}))
	p.useModel(p.owner, newScriptedModel(t, func(modelReq) []map[string]any { return []map[string]any{mtext("Nothing to change.")} }))
	p.wait(p.owner, p.chat(p.owner, p.site, p.draft(), "x"))
	eq(t, p.get(p.owner, p.site)["ai_edits"].(map[string]any)["limit"], float64(500))
}

// 7. Internal commands are not reachable over HTTP.
func TestSiteInternalCommandsHidden(t *testing.T) {
	p := newPubEnv(t)
	for cmd, in := range map[string]any{
		"inquiry.receive": map[string]any{"space_id": p.space(p.owner), "name": "A", "phone": "+919876543210", "source": "website"},
		"file.image_put":  map[string]any{"site_id": p.site, "filename": "a.png", "data_base64": pngBytes(t, 4, 4)},
		"file.image_get":  map[string]any{"site_id": p.site, "id": p.site},
		"file.image_list": map[string]any{"site_id": p.site},
		"site.public_get": map[string]any{"host": "a." + siteDomain},
	} {
		eq(t, p.call(p.owner, cmd, in).status, 404)
	}
	// site images never show up through the page-file commands
	img := adminOK(t, p.upload(p.owner, pngBytes(t, 4, 4)))["image"].(map[string]any)["id"].(string)
	eq(t, p.call(p.owner, "file.download", map[string]any{"id": img}).status, 404)
}

func TestSiteInvalidUploadConsumesLimit(t *testing.T) {
	p := newPubEnv(t)
	for i := 0; i < 30; i++ {
		if r := p.upload(p.mia, "invalid!"); r.status != 400 {
			t.Fatalf("attempt %d: %d", i, r.status)
		}
	}
	if r := p.upload(p.mia, pngBytes(t, 4, 4)); r.status != 429 {
		t.Fatalf("limit: %d", r.status)
	}
}

func TestSitePublicImageRateLimitBeforeLookup(t *testing.T) {
	p := newPubEnv(t)
	ip := "198.51.100.89"
	for i := 0; i < 600; i++ {
		if _, err := db.Bump(context.Background(), p.pool, "siteimage:ip:"+sites.IPKey(ip), false); err != nil {
			t.Fatal(err)
		}
	}
	st, _, _ := p.hit("GET", "/api/sites/img/"+p.site+"/"+p.site, nil, map[string]string{"X-Forwarded-For": ip})
	if st != 429 {
		t.Fatalf("got %d", st)
	}
}

func TestSiteTakedownWaitsBeforeRecheck(t *testing.T) {
	p := newPubEnv(t)
	sub := uniq()
	p.publishOK(sub)
	ctx := context.Background()
	pub, err := sites.LookupID(ctx, p.pool, p.site)
	if err != nil {
		t.Fatal(err)
	}
	admin := adminPool(t)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "select * from sites.platform_set($1::uuid, $2, null, 'takedown', 'abuse report', null)", p.owner.userID, sub)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- sites.Submit(ctx, p.pool, command.NewRegistry(modules.Commands()...), pub, sites.Submission{Name: "Late", Phone: "+919876543211", Message: "race"})
	}()
	select {
	case err := <-done:
		t.Fatalf("submit did not wait: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("submission accepted after takedown")
	}
}

func TestSitePlatformControlsRequireAdminURL(t *testing.T) {
	p := newPubEnv(t)
	t.Setenv("DATABASE_ADMIN_URL", "")
	r := p.platform(p.owner, map[string]any{"subdomain": "example-site", "action": "pro_on", "reason": "valid reason"})
	if r.status != 400 || !strings.Contains(string(r.body), "platform controls are not configured") {
		t.Fatalf("unexpected response: %+v", r)
	}
}
