package server_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

// Public page links (docs-parity-b 4.2, 9.1, 13.2.3 and 13.2.4). Real Postgres and the real Go server.

type pg struct {
	dg
	owner who
}

func newPG(t *testing.T) pg {
	t.Setenv("CNOTE_MASTER_KEY", testMasterKey)
	t.Setenv("PUBLIC_TRUSTED_PROXIES", "127.0.0.1")
	d, owner := newDG(t)
	return pg{d, owner}
}

// get calls the public endpoint from a fresh client address (the test server is a trusted proxy).
func (x pg) get(path, addr string) (int, http.Header, string) {
	x.t.Helper()
	if addr == "" {
		addr = freshAddr()
	}
	r, _ := http.NewRequest("GET", x.e.gosv.URL+"/api/public/page/"+path, nil)
	r.Header.Set("X-Forwarded-For", addr)
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(r) // never wait forever: a blocked request must fail the test, not hang it
	if err != nil {
		x.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func pgIPKey(domain, ip string) string {
	h := hmac.New(sha256.New, []byte("0123456789abcdef0123456789abcdef"))
	h.Write([]byte(domain + "\x00" + ip))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// link makes a page link as w and returns its token.
func (x pg) link(w who, page string) string {
	x.t.Helper()
	r := x.ok(w, "page_link.create", map[string]any{"note_id": page})
	u := r["url"].(string)
	if !strings.Contains(u, "/pg/") {
		x.t.Fatalf("url: %s", u)
	}
	return u[strings.LastIndex(u, "/pg/")+4:]
}

func (x pg) expectGone(path string) {
	x.t.Helper()
	s, h, b := x.get(path, "")
	if s != 404 {
		x.t.Fatalf("%s: want 404, got %d %s", path, s, b)
	}
	want := `{"error":{"code":"not_found","message":"This page is not available."}}`
	if b != want {
		x.t.Fatalf("404 body: %s", b)
	}
	for k, v := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
		"X-Robots-Tag": "noindex, nofollow", "Content-Type": "application/json"} {
		if h.Get(k) != v {
			x.t.Fatalf("404 header %s = %q", k, h.Get(k))
		}
	}
}

func TestPublicPageServesAndEveryCauseIsTheSame404(t *testing.T) {
	x := newPG(t)
	member := x.member(x.owner)
	sp := x.space(x.owner, "PUB", "open")
	page := x.page(x.owner, sp, "Handbook", nil, "# Hello\n\nbody")
	token := x.link(member, page)
	if s, h, b := x.get(token, ""); s != 200 || !strings.Contains(b, `"Handbook"`) || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("serve: %d %v %s", s, h, b)
	}
	x.expectGone("not-a-token")
	x.expectGone(strings.Repeat("a", 43))
	x.expectGone(token + "/files/zz")
	x.expectGone(token + "/other")

	// Each cause: break it, expect the one 404, mend it, expect the page back.
	causes := []struct {
		name        string
		break_, fix func()
	}{
		{"page trashed", func() { x.sql("update core.notes set deleted_at = now() where id = $1::uuid", page) }, func() { x.sql("update core.notes set deleted_at = null where id = $1::uuid", page) }},
		{"page archived", func() { x.sql("update core.notes set freshness = 'archived' where id = $1::uuid", page) }, func() { x.sql("update core.notes set freshness = 'fresh' where id = $1::uuid", page) }},
		{"page typed", func() { x.sql("update core.notes set types = '{task}' where id = $1::uuid", page) }, func() { x.sql("update core.notes set types = '{}' where id = $1::uuid", page) }},
		{"space switch off", func() { x.sql("update core.spaces set public_links = false where id = $1::uuid", sp) }, func() { x.sql("update core.spaces set public_links = null where id = $1::uuid", sp) }},
		{"space archived", func() { x.archive(sp, true) }, func() { x.archive(sp, false) }},
		{"expired", func() {
			x.sql("update core.public_links set expires_at = now() - interval '1 minute' where note_id = $1::uuid", page)
		}, func() { x.sql("update core.public_links set expires_at = null where note_id = $1::uuid", page) }},
		{"creator left", func() { x.sql("update core.memberships set role = 'guest' where user_id = $1::uuid", member.userID) }, func() { x.sql("update core.memberships set role = 'member' where user_id = $1::uuid", member.userID) }},
		{"creator lost edit", func() { x.ok(x.owner, "note.restrict", map[string]any{"note_id": page, "restriction": "edit"}) }, func() { x.ok(x.owner, "note.restrict", map[string]any{"note_id": page, "restriction": "none"}) }},
		{"view restriction on the page", func() { x.ok(x.owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"}) }, func() { x.ok(x.owner, "note.restrict", map[string]any{"note_id": page, "restriction": "none"}) }},
	}
	for _, c := range causes {
		c.break_()
		x.expectGone(token)
		if c.name == "page trashed" {
			// the page is gone for readers too
		} else if st := x.ok(x.owner, "page_link.get", map[string]any{"note_id": page}); c.name != "page trashed" {
			if l, _ := st["link"].(map[string]any); l != nil && l["status"] == "active" {
				t.Fatalf("%s: the chip would still say Public: %v", c.name, st)
			}
		}
		c.fix()
		if s, _, b := x.get(token, ""); s != 200 {
			t.Fatalf("%s: not back after the fix: %d %s", c.name, s, b)
		}
	}
	// A view restriction on an ancestor.
	parent := x.page(x.owner, sp, "Parent", nil, "p")
	kid := x.child(x.owner, sp, "Kid", parent)
	kt := x.link(member, kid)
	x.ok(x.owner, "note.restrict", map[string]any{"note_id": parent, "restriction": "view"})
	x.expectGone(kt)
	// Revoked, and replaced.
	x.ok(member, "page_link.revoke", map[string]any{"note_id": page})
	x.expectGone(token)
	old := x.link(member, page)
	fresh := x.link(member, page)
	x.expectGone(old)
	if s, _, _ := x.get(fresh, ""); s != 200 {
		t.Fatal("the new link must serve")
	}
	// Other purposes and the billing endpoint never answer for each other.
	vt := "v" + strings.Repeat("x", 38) + rnd()[:4]
	x.sql(`insert into core.public_links (workspace_id, note_id, token_hash, purpose, created_by) values ($1::uuid,$2::uuid,$3,'view',$4::uuid)`,
		x.ws, page, hex.EncodeToString(sha32(vt)), x.owner.userID)
	x.expectGone(vt)
	if r := x.e.do(x.e.gosv.URL, "GET", "/api/public/"+fresh, who{}, nil, map[string]string{"X-Forwarded-For": freshAddr()}); r.status != 404 {
		t.Fatalf("billing endpoint answered for a page token: %d", r.status)
	}
	if s, _, _ := x.get("", ""); s != 404 {
		t.Fatalf("empty token: %d", s)
	}
	r, _ := http.NewRequest("POST", x.e.gosv.URL+"/api/public/page/"+fresh, nil)
	if res, err := http.DefaultClient.Do(r); err != nil || res.StatusCode != 405 {
		t.Fatalf("POST: %v %v", res, err)
	}
	// The token is nowhere in the event log.
	if n := x.scalar("select count(*)::text from core.events where payload::text like '%' || $1 || '%'", fresh); n != "0" {
		t.Fatalf("token in events: %s", n)
	}
	if n := x.scalar("select count(*)::text from core.events where kind in ('share.created','share.revoked') and payload->>'purpose' = 'page'"); n == "0" {
		t.Fatal("no share events")
	}
}

func sha32(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestPageLinkCommandRules(t *testing.T) {
	x := newPG(t)
	member, viewer := x.member(x.owner), x.member(x.owner)
	sp := x.space(x.owner, "PUB", "open")
	page := x.page(x.owner, sp, "Doc", nil, "x")
	get := func(w who) map[string]any { return x.ok(w, "page_link.get", map[string]any{"note_id": page}) }
	if g := get(member); g["link"] != nil || g["allowed"] != true || g["reason"] != nil {
		t.Fatalf("get before: %v", g)
	}
	// typed page, view restriction, switch off, archived space
	typed := x.page(x.owner, sp, "Task", nil, "x")
	x.sql("update core.notes set types = '{task}' where id = $1::uuid", typed)
	x.code("conflict", x.owner, "page_link.create", map[string]any{"note_id": typed})
	res := x.page(x.owner, sp, "Res", nil, "x")
	x.ok(x.owner, "note.restrict", map[string]any{"note_id": res, "restriction": "view"})
	r := x.run(x.owner, "page_link.create", map[string]any{"note_id": res})
	if r.status == 200 || !strings.Contains(string(r.body), "Remove the view restriction to share this page publicly") {
		t.Fatalf("restricted create: %d %s", r.status, r.body)
	}
	x.sql("update core.spaces set public_links = false where id = $1::uuid", sp)
	if g := get(member); g["allowed"] != false || g["reason"] != "Public links are turned off for this space" {
		t.Fatalf("switch off: %v", g)
	}
	x.code("conflict", member, "page_link.create", map[string]any{"note_id": page})
	x.sql("update core.spaces set public_links = null where id = $1::uuid", sp)
	x.code("invalid", member, "page_link.create", map[string]any{"note_id": page, "expires_in_days": 0})
	c := x.ok(member, "page_link.create", map[string]any{"note_id": page, "expires_in_days": 7})
	if c["link"].(map[string]any)["expires_at"] == nil {
		t.Fatal("expiry lost")
	}
	if g := get(viewer); g["link"].(map[string]any)["status"] != "active" || strings.Contains(string(pgJSON(g)), strings.TrimPrefix(c["url"].(string), "http://localhost:3000/pg/")) {
		t.Fatalf("get after: %v", g)
	}
	// revoke_space is internal.
	x.code("not_found", x.owner, "page_link.revoke_space", map[string]any{"space_id": sp})
	x.sql("update core.public_links set revoked_at = now()")
	if g := get(member); g["link"] != nil {
		t.Fatalf("revoked link still reported: %v", g)
	}
}

func pgJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// 13.2.3: the inventory is filtered by the caller's full page access, before anything is counted or returned.
func TestPageLinkInventoryIsFilteredByAccess(t *testing.T) {
	x := newPG(t)
	admin, plain := x.member(x.owner), x.member(x.owner)
	sp := x.space(x.owner, "INV", "open")
	x.sql("insert into core.space_members (workspace_id, space_id, user_id, role) values ($1::uuid,$2::uuid,$3::uuid,'admin')", x.ws, sp, admin.userID)
	shown := x.page(x.owner, sp, "Shown", nil, "x")
	hidden := x.page(x.owner, sp, "Hidden", nil, "x")
	x.link(x.owner, shown)
	x.link(x.owner, hidden)
	x.ok(x.owner, "note.restrict", map[string]any{"note_id": hidden, "restriction": "view"}) // the link outlives the restriction in the table
	if got := x.list(x.owner, "page_link.list", map[string]any{"space_id": sp}); len(got) != 2 {
		t.Fatalf("the workspace owner sees every page: %v", got)
	}
	got := x.list(admin, "page_link.list", map[string]any{"space_id": sp})
	if len(got) != 1 || got[0]["note_id"] != shown {
		t.Fatalf("a space admin must not learn of the restricted page: %v", got)
	}
	if strings.Contains(string(pgJSON(got)), "Hidden") {
		t.Fatal("title leaked")
	}
	x.code("forbidden", plain, "page_link.list", map[string]any{"space_id": sp})
	// and cannot revoke it or read its link state
	x.code("not_found", admin, "page_link.revoke", map[string]any{"note_id": hidden})
	x.code("not_found", admin, "page_link.get", map[string]any{"note_id": hidden})
	// a space admin may turn off a link on a page they can read but not edit
	x.ok(x.owner, "note.restrict", map[string]any{"note_id": shown, "restriction": "edit"})
	if r := x.ok(admin, "page_link.revoke", map[string]any{"note_id": shown}); r["revoked"].(float64) != 1 {
		t.Fatalf("revoke: %v", r)
	}
	// revoke_space, as the internal command space.update will call it
	x.ok(x.owner, "note.restrict", map[string]any{"note_id": shown, "restriction": "none"})
	x.link(x.owner, shown)
}

// space.update calls page_link.revoke_space through c.Call; this stands in for it.
func TestRevokeSpaceKillsEveryLinkOfTheSpaceOnly(t *testing.T) {
	t.Setenv("CNOTE_MASTER_KEY", testMasterKey)
	t.Setenv("PUBLIC_TRUSTED_PROXIES", "127.0.0.1")
	e := newEnv(t, command.NewCommand("p2.revoke_space", command.GroupWriteNotes, func(c *command.Ctx, in json.RawMessage) (any, error) {
		out, err := c.Call("page_link.revoke_space", in)
		return json.RawMessage(out), err
	}))
	owner := e.signup("Tess", true)
	x := pg{dg{gw{t, e}, owner.ws}, owner}
	a, b := x.space(owner, "AAA", "open"), x.space(owner, "BBB", "open")
	pa1, pa2, pb := x.page(owner, a, "A1", nil, "x"), x.page(owner, a, "A2", nil, "x"), x.page(owner, b, "B1", nil, "x")
	ta1, ta2, tb := x.link(owner, pa1), x.link(owner, pa2), x.link(owner, pb)
	if r := x.ok(owner, "p2.revoke_space", map[string]any{"space_id": a}); r["revoked"].(float64) != 2 {
		t.Fatalf("revoked: %v", r)
	}
	x.expectGone(ta1)
	x.expectGone(ta2)
	if s, _, _ := x.get(tb, ""); s != 200 {
		t.Fatalf("the other space's link must still serve: %d", s)
	}
	// turning the switch back on revives nothing
	x.sql("update core.spaces set public_links = true where id = $1::uuid", a)
	x.expectGone(ta1)
}

// ---- the public body ----

func TestPublicBodyHasNothingBeyondThePage(t *testing.T) {
	x := newPG(t)
	sp := x.space(x.owner, "BOD", "open")
	page := x.page(x.owner, sp, "Public page", nil, "x")
	other := x.page(x.owner, sp, "Other page", nil, "x")
	kid := x.child(x.owner, sp, "Secret child title", page)
	up := func(note, name string, data []byte) string {
		return x.ok(x.owner, "file.upload", map[string]any{"note_id": note, "filename": name, "data_base64": base64.StdEncoding.EncodeToString(data)})["id"].(string)
	}
	own, unused, theirs := up(page, "own.png", png), up(page, "unused.png", png), up(other, "theirs.png", png)
	pdf := up(page, "doc.pdf", []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n"))
	f := func(id string) string { return "/api/w/" + x.ws + "/files/" + id }
	body := "Intro ![mine](" + f(own) + ") and [the pdf](" + f(pdf) + ") ![stolen](" + f(theirs) + ") hello [@Tess](/w/" + x.ws + "/people/" + x.owner.userID +
		") see [Other](/w/" + x.ws + "/n/" + other + ") <table><tr><td><a href=\"/w/" + x.ws + "/n/" + kid + "\">Kid</a></td></tr></table>\n\n[[children]]\n\n[web](https://example.com/a)"
	x.ok(x.owner, "note.update", map[string]any{"id": page, "schema": 2, "expected_epoch": 1, "content_md": body})
	x.ok(x.owner, "comment.add", map[string]any{"note_id": page, "body": "internal comment zebra"})
	token := x.link(x.owner, page)

	s, h, b := x.get(token, "")
	if s != 200 {
		t.Fatalf("%d %s", s, b)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(b), &m)
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "body_md,cover_url,cover_y,icon,title,updated_on,wide,workspace_name" {
		t.Fatalf("PublicPage key set drifted: %v", keys)
	}
	md := m["body_md"].(string)
	for _, bad := range []string{x.ws, x.owner.userID, other, kid, theirs, unused, "zebra", "Secret child title", "[[children]]", "/w/", "/api/w/"} {
		if strings.Contains(b, bad) {
			t.Fatalf("public page holds %q: %s", bad, b)
		}
	}
	for _, want := range []string{"/api/public/page/" + token + "/files/" + own, "@Tess", "stolen", "Other", "https://example.com/a", "Kid"} {
		if !strings.Contains(md, want) {
			t.Fatalf("body lacks %q: %s", want, md)
		}
	}
	if h.Get("Cache-Control") != "no-store" {
		t.Fatal("page must not be cached")
	}
	fileOK := func(id string) (int, http.Header) { s, h, _ := x.get(token+"/files/"+id, ""); return s, h }
	if s, h := fileOK(own); s != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("own image: %d %v", s, h)
	}
	if s, h := fileOK(pdf); s != 200 || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("pdf: %d %v", s, h)
	}
	for _, id := range []string{theirs, unused, "00000000-0000-4000-8000-000000000000"} {
		x.expectGone(token + "/files/" + id) // of another page, attached but no longer in the text, unknown
	}
	// a file removed from the text stops at once
	x.ok(x.owner, "note.update", map[string]any{"id": page, "schema": 2, "expected_epoch": 1, "content_md": "no files now"})
	x.expectGone(token + "/files/" + own)
	// the view count is kept and shown to editors
	if v := x.scalar("select view_count::text from core.public_links where purpose = 'page' and revoked_at is null"); v == "0" {
		t.Fatal("view not counted")
	}
}

// ---- limits ----

func TestPageLimitsRunBeforeAnyTokenWork(t *testing.T) {
	x := newPG(t)
	sp := x.space(x.owner, "LIM", "open")
	page := x.page(x.owner, sp, "P", nil, "x")
	token := x.link(x.owner, page)
	bucket := func(key string) int {
		n, err := db.Peek(context.Background(), x.e.pool, key, false)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	seed := func(key string, n int) {
		if _, err := x.e.pool.Exec(context.Background(), `insert into core.rate_buckets (key, bucket, n) values ($1,$2,$3)
			on conflict (key, bucket) do update set n = $3`, key, db.BucketStart(time.Now(), false), n); err != nil {
			t.Fatal(err)
		}
	}
	// A malformed token is counted on the address before the token is looked at.
	a := freshAddr()
	if s, _, _ := x.get("bad", a); s != 404 {
		t.Fatal("malformed")
	}
	if bucket("pg:ip:"+pgIPKey("page-ip", a)) != 1 || bucket("pg:miss:"+pgIPKey("page-ip", a)) != 1 {
		t.Fatal("a malformed token must bump the address and the miss counters")
	}
	// Over the address limit: refused with a valid token, before it is resolved.
	b := freshAddr()
	seed("pg:ip:"+pgIPKey("page-ip", b), 300)
	if s, _, _ := x.get(token, b); s != 429 {
		t.Fatalf("address limit: %d", s)
	}
	// A full miss counter refuses a valid token too.
	c := freshAddr()
	seed("pg:miss:"+pgIPKey("page-ip", c), 20)
	if s, _, _ := x.get(token, c); s != 429 {
		t.Fatalf("miss limit: %d", s)
	}
	// File requests have their own, larger bucket.
	d := freshAddr()
	seed("pg:ipf:"+pgIPKey("page-ip", d), 1500)
	if s, _, _ := x.get(token+"/files/00000000-0000-4000-8000-000000000000", d); s != 429 {
		t.Fatalf("file limit: %d", s)
	}
	if s, _, _ := x.get(token, d); s != 200 {
		t.Fatalf("page limit is separate: %d", s)
	}
	// Per link.
	id := x.scalar("select id::text from core.public_links where purpose = 'page' and revoked_at is null")
	seed("pg:tok:"+id, 2000)
	if s, _, _ := x.get(token, ""); s != 429 {
		t.Fatalf("link limit: %d", s)
	}
	seed("pg:tok:"+id, 0)
	seed("pg:all", 20000)
	if s, _, _ := x.get(token, ""); s != 429 {
		t.Fatalf("platform limit: %d", s)
	}
	seed("pg:all", 0)
}

func TestPageNeedsTheMasterKeyAndHonoursPublicPages0(t *testing.T) {
	x := newPG(t)
	sp := x.space(x.owner, "KEY", "open")
	token := x.link(x.owner, x.page(x.owner, sp, "P", nil, "x"))
	t.Setenv("PUBLIC_PAGES", "0")
	x.expectGone(token)
	t.Setenv("PUBLIC_PAGES", "")
	t.Setenv("CNOTE_MASTER_KEY", "")
	if s, _, _ := x.get(token, ""); s != 503 {
		t.Fatalf("no master key: %d", s)
	}
}

func TestSpoofedForwardedForDoesNotChooseTheAddress(t *testing.T) {
	x := newPG(t)
	// The test server is a trusted proxy: the right-most entry is the client, a forged left part is ignored.
	a := freshAddr()
	for i := 0; i < 3; i++ {
		r, _ := http.NewRequest("GET", x.e.gosv.URL+"/api/public/page/bad", nil)
		r.Header.Set("X-Forwarded-For", "1.2.3."+string(rune('0'+i))+", "+a)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	n, _ := db.Peek(context.Background(), x.e.pool, "pg:ip:"+pgIPKey("page-ip", a), false)
	if n != 3 {
		t.Fatalf("forged prefix changed the key: %d", n)
	}
}

func TestPageResolverRunsAsTheAppRoleWithoutATenant(t *testing.T) {
	x := newPG(t)
	sp := x.space(x.owner, "DEF", "open")
	page := x.page(x.owner, sp, "P", nil, "x")
	token := x.link(x.owner, page)
	count := func(h string) int {
		var n int
		if err := x.e.pool.QueryRow(context.Background(), "select count(*) from core.resolve_page_link($1)", h).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(hex.EncodeToString(sha32(token))) != 1 || count("nope") != 0 {
		t.Fatal("the definer function must answer for a good hash and only that")
	}
}

// ---- revocation, both lock orders (13.2.4) ----

func TestRevocationLinearizesInBothLockOrders(t *testing.T) {
	x := newPG(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	member := x.member(x.owner)
	sp := x.space(x.owner, "LCK", "open")
	page := x.page(x.owner, sp, "P", nil, "x")
	up := x.ok(x.owner, "file.upload", map[string]any{"note_id": page, "filename": "a.png", "data_base64": base64.StdEncoding.EncodeToString(png)})["id"].(string)
	x.ok(x.owner, "note.update", map[string]any{"id": page, "schema": 2, "expected_epoch": 1, "content_md": "![a](/api/w/" + x.ws + "/files/" + up + ")"})
	for _, route := range []string{"page", "file"} {
		path := func(tok string) string {
			if route == "file" {
				return tok + "/files/" + up
			}
			return tok
		}
		// Order A: the revoke holds the row first; a request that arrives waits and then fails.
		tok := x.link(member, page)
		id := x.scalar("select id::text from core.public_links where purpose = 'page' and revoked_at is null")
		tx, err := dbTenant(x.e, x.ws)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.WithoutCancel(ctx)) // a failed check must still release the row, or pool.Close waits on it forever
		if _, err := tx.Exec(ctx, "select 1 from core.public_links where id = $1::uuid for update", id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "update core.public_links set revoked_at = now() where id = $1::uuid", id); err != nil {
			t.Fatal(err)
		}
		done := make(chan int, 1)
		go func() { s, _, _ := x.get(path(tok), ""); done <- s }()
		select {
		case s := <-done:
			t.Fatalf("%s: the request did not wait for the revoke (%d)", route, s)
		case <-time.After(400 * time.Millisecond):
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var s int
		select {
		case s = <-done:
		case <-time.After(30 * time.Second): // the request goroutine may have died on a failed check
			t.Fatalf("%s order A: no answer after the revoke committed", route)
		}
		if s != 404 {
			t.Fatalf("%s order A: want 404 after the revoke committed, got %d", route, s)
		}
		// Order B: a request holds the row FOR SHARE (authorized, still reading); the revoke waits for it, then wins.
		tok = x.link(member, page)
		id = x.scalar("select id::text from core.public_links where purpose = 'page' and revoked_at is null")
		tx, err = dbTenant(x.e, x.ws)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.WithoutCancel(ctx))
		if _, err := tx.Exec(ctx, "select 1 from core.public_links where id = $1::uuid for share", id); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		revoked := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(revoked)
			x.ok(member, "page_link.revoke", map[string]any{"note_id": page})
		}()
		select {
		case <-revoked:
			t.Fatalf("%s: the revoke did not wait for the reader", route)
		case <-time.After(400 * time.Millisecond):
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		x.expectGone(path(tok))
	}
}
