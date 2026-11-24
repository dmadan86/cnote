package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/modules/sites"
)

// siteEnv is a workspace with an owner and two members (agentEnv), plus site helpers.
type siteEnv struct{ *agentEnv }

func newSiteEnv(t *testing.T) *siteEnv { return &siteEnv{newAgentEnv(t)} }

func (s *siteEnv) create() map[string]any {
	return s.ok(s.owner, "site.create", map[string]any{"template": "services"})
}

func (s *siteEnv) get(w who, id string) map[string]any {
	return s.ok(w, "site.get", map[string]any{"id": id})
}

func blockOf(data any, typ string) string {
	for _, b := range data.(map[string]any)["content"].([]any) {
		if m := b.(map[string]any); m["type"] == typ {
			return m["props"].(map[string]any)["id"].(string)
		}
	}
	return ""
}

func propOf(data any, typ, prop string) any {
	for _, b := range data.(map[string]any)["content"].([]any) {
		if m := b.(map[string]any); m["type"] == typ {
			return m["props"].(map[string]any)[prop]
		}
	}
	return nil
}

func order(data any) string {
	var l []string
	for _, b := range data.(map[string]any)["content"].([]any) {
		l = append(l, b.(map[string]any)["type"].(string))
	}
	return strings.Join(l, ",")
}

func (s *siteEnv) draft() string {
	return s.sql("select draft_version_id::text from sites.sites")
}

func (s *siteEnv) versions() string {
	return s.sql("select count(*)::text from sites.site_versions")
}

func (s *siteEnv) chat(w who, site, expected, msg string) string {
	s.t.Helper()
	return s.ok(w, "site.chat", map[string]any{"id": site, "expected_version_id": expected, "message": msg})["turn_id"].(string)
}

// wait polls site.turn_get until the turn leaves queued and running.
func (s *siteEnv) wait(w who, turn string) map[string]any {
	s.t.Helper()
	for i := 0; i < 600; i++ {
		r := s.ok(w, "site.turn_get", map[string]any{"id": turn})
		if st := r["turn"].(map[string]any)["status"]; st != "queued" && st != "running" {
			return r
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("turn %s did not finish: %s", turn, s.sql("select status || ' ' || attempt from sites.site_turns where id = $1::uuid", turn))
	return nil
}

func (s *siteEnv) editOps(w who, site, expected string, ops ...map[string]any) resp {
	return s.call(w, "site.edit", map[string]any{"id": site, "expected_version_id": expected, "ops": ops})
}

// gate is a scripted model that holds every request until released.
type gate struct {
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gate) open() { g.once.Do(func() { close(g.release) }) }

func newGate(t *testing.T, answer func(modelReq) []map[string]any) (*scriptedModel, *gate) {
	g := &gate{reached: make(chan struct{}, 8), release: make(chan struct{})}
	m := newScriptedModel(t, func(r modelReq) []map[string]any {
		g.reached <- struct{}{}
		<-g.release
		return answer(r)
	})
	t.Cleanup(g.open) // runs before the server closes, so a failed test cannot hang on a held request
	return m, g
}

func (g *gate) waitReached(t *testing.T) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(30 * time.Second):
		t.Fatal("the model was never called")
	}
}

func editHero(hero, title string) func(modelReq) []map[string]any {
	return func(r modelReq) []map[string]any {
		if r.Turn == 0 {
			return []map[string]any{muse("t1", "update_block_props", map[string]any{"id": hero, "props": map[string]any{"title": title}})}
		}
		return []map[string]any{mtext("Done.")}
	}
}

// 1. Template data is filled from the company profile; a value that does not fit is left empty and reported.
func TestSiteTemplateFilledFromCompany(t *testing.T) {
	s := newSiteEnv(t)
	s.ok(s.owner, "company.update", map[string]any{"trade_name": "Sharma Dental", "phone": "+919876543210", "email": "hello@sharma.example.test",
		"address": "12 MG Road, Pune", "pincode": "411001", "email_accent": "#0f766e"})
	tpl := s.call(s.mia, "site.templates", map[string]any{}).jsonList()
	if len(tpl) != 1 || tpl[0]["key"] != "services" || len(tpl[0]["too_long"].([]any)) != 0 {
		t.Fatalf("templates: %v", tpl)
	}
	eq(t, propOf(tpl[0]["data"], "Hero", "title"), "Sharma Dental")

	s.fail(s.mia, "site.create", map[string]any{"template": "services"}, 403) // admins only
	s.fail(s.owner, "site.create", map[string]any{"template": "nope"}, 400)
	r := s.create()
	data := r["data"]
	eq(t, propOf(data, "Hero", "title"), "Sharma Dental")
	eq(t, propOf(data, "ContactForm", "phone"), "+919876543210")
	eq(t, propOf(data, "ContactForm", "email"), "hello@sharma.example.test")
	eq(t, propOf(data, "Footer", "address"), "12 MG Road, Pune, 411001")
	eq(t, data.(map[string]any)["root"].(map[string]any)["props"].(map[string]any)["theme"].(map[string]any)["primary"], "#0f766e")
	site := r["site"].(map[string]any)
	eq(t, site["draft_version_id"], s.draft())
	eq(t, s.versions(), "1")
	s.fail(s.owner, "site.create", map[string]any{"template": "services"}, 409) // one site per workspace
	eq(t, s.sql("select count(*)::text from sites.sites"), "1")

	// too long: the name (80 on the hero) and the address (200 in the contact form) are left empty, never cut
	s2 := newSiteEnv(t)
	long := strings.Repeat("a", 100)
	s2.ok(s2.owner, "company.update", map[string]any{"trade_name": long, "phone": "+919876543210", "address": strings.Repeat("b", 300)})
	tl := s2.call(s2.mia, "site.templates", map[string]any{}).jsonList()[0]
	eq(t, strings.Join(strs(tl["too_long"]), ","), "name,address")
	eq(t, propOf(tl["data"], "ContactForm", "phone"), "+919876543210")
	eq(t, propOf(tl["data"], "Footer", "address"), "")
	c := s2.ok(s2.owner, "site.create", map[string]any{"template": "services"})
	eq(t, strings.Join(strs(c["too_long"]), ","), "name,address")
	if err := sites.Validate(json.RawMessage(mustJSON(c["data"]))); err != nil {
		t.Fatalf("saved page invalid: %v", err)
	}
}

// 2. A chat turn goes through the worker and the Local provider's /tool mode and produces one version.
func TestSiteChatTurnLocalProvider(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	g := s.get(s.mia, id)
	hero := blockOf(g["data"], "Hero")
	turn := s.chat(s.mia, id, v1, `/tool update_block_props {"id":"`+hero+`","props":{"title":"Hello Pune"}}`)
	done := s.wait(s.mia, turn)
	tr := done["turn"].(map[string]any)
	eq(t, tr["status"], "done")
	eq(t, tr["summary"], "Updated Hero")
	if !strings.HasPrefix(tr["reply"].(string), "Result of update_block_props") {
		t.Fatalf("reply: %v", tr["reply"])
	}
	eq(t, propOf(done["data"], "Hero", "title"), "Hello Pune")
	eq(t, tr["base_version_id"], v1)
	eq(t, tr["version_id"], s.draft())
	eq(t, s.versions(), "2")
	eq(t, s.sql("select (source = 'chat' and parent_id = $1::uuid)::text from sites.site_versions where id = $2::uuid", v1, tr["version_id"]), "true")
	eq(t, s.sql("select (calls = 2 and attempt = 1 and tokens_in > 0)::text from sites.site_turns"), "true")
	// audited as site.chat with the site and turn, and the budget reservation was settled
	eq(t, s.sql("select count(*)::text from ai.audit where purpose = 'site.chat' and prompt->>'turn_id' = $1", turn), "2")
	eq(t, s.sql("select (sum(reserved) = 0 and sum(used) > 0)::text from core.agent_budget"), "true")
	// a member's own history shows the turn, oldest first
	turns := s.get(s.oz, id)["turns"].([]any)
	if len(turns) != 1 || turns[0].(map[string]any)["id"] != turn {
		t.Fatalf("turns: %v", turns)
	}
	// a chat on the old draft is stale
	s.fail(s.mia, "site.chat", map[string]any{"id": id, "expected_version_id": v1, "message": "again"}, 409)
}

// 3. Two invalid tool calls fail the turn and leave the page and its versions alone.
func TestSiteInvalidToolCallTwiceFailsTurn(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	model := newScriptedModel(t, func(r modelReq) []map[string]any {
		return []map[string]any{muse("t"+string(rune('a'+r.Turn)), "update_block_props", map[string]any{"id": "Hero-ffffffff", "props": map[string]any{"title": "x"}})}
	})
	s.useModel(s.owner, model)
	turn := s.chat(s.mia, id, v1, "change the title")
	tr := s.wait(s.mia, turn)["turn"].(map[string]any)
	eq(t, tr["status"], "failed")
	if !strings.Contains(tr["error"].(string), "rephras") {
		t.Fatalf("error: %v", tr["error"])
	}
	eq(t, s.draft(), v1)
	eq(t, s.versions(), "1")
	if len(tr["steps"].([]any)) != 2 {
		t.Fatalf("steps: %v", tr["steps"])
	}
	// the site is free again
	s.wait(s.mia, s.chat(s.mia, id, v1, "second try"))
}

// 4. Edit (move, duplicate, delete) makes versions; restore is undo, redo and restore-to-older; stale views conflict.
func TestSiteEditRestoreAndConflicts(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	d := s.get(s.mia, id)["data"]
	eq(t, order(d), "Header,Hero,Services,ContactForm,Footer")
	hero, services := blockOf(d, "Hero"), blockOf(d, "Services")

	r := s.editOps(s.mia, id, v1, map[string]any{"tool": "move_block", "input": map[string]any{"id": hero, "index": 0}}).json()
	v2 := r["site"].(map[string]any)["draft_version_id"].(string)
	eq(t, order(r["data"]), "Hero,Header,Services,ContactForm,Footer")
	eq(t, r["site"].(map[string]any)["parent_version_id"], v1)
	r = s.editOps(s.mia, id, v2, map[string]any{"tool": "duplicate_block", "input": map[string]any{"id": services}}).json()
	v3 := r["site"].(map[string]any)["draft_version_id"].(string)
	eq(t, order(r["data"]), "Hero,Header,Services,Services,ContactForm,Footer")
	r = s.editOps(s.mia, id, v3, map[string]any{"tool": "remove_block", "input": map[string]any{"id": services}}).json()
	v4 := r["site"].(map[string]any)["draft_version_id"].(string)
	eq(t, order(r["data"]), "Hero,Header,Services,ContactForm,Footer")
	eq(t, s.versions(), "4")
	eq(t, s.sql("select string_agg(summary, '|' order by seq) from sites.site_versions"), "Started from the template|Moved Hero|Duplicated Services|Deleted Services")

	// invalid op: nothing saved; a no-op saves no version
	if rs := s.editOps(s.mia, id, v4, map[string]any{"tool": "remove_block", "input": map[string]any{"id": "Hero-ffffffff"}}); rs.status != 400 {
		t.Fatalf("unknown block: %d %s", rs.status, rs.body)
	}
	if rs := s.editOps(s.mia, id, v4, map[string]any{"tool": "move_block", "input": map[string]any{"id": hero, "index": 0}}); rs.status != 200 {
		t.Fatalf("noop: %d %s", rs.status, rs.body)
	}
	eq(t, s.versions(), "4")
	eq(t, s.draft(), v4)

	// stale view: a conflict with the standard message, for every write
	for cmd, in := range map[string]map[string]any{
		"site.chat":    {"message": "hi"},
		"site.restore": {"version_id": v1},
		"site.edit":    {"ops": []any{map[string]any{"tool": "remove_block", "input": map[string]any{"id": hero}}}},
	} {
		in["id"], in["expected_version_id"] = id, v2
		rs := s.call(s.mia, cmd, in)
		if rs.status != 409 || !strings.Contains(string(rs.body), "The site changed. Reload to see the latest.") {
			t.Fatalf("%s stale: %d %s", cmd, rs.status, rs.body)
		}
	}
	eq(t, s.draft(), v4)

	// undo to v3, redo to v4, restore to the oldest: only the pointer moves
	restore := func(expected, to string) map[string]any {
		return s.ok(s.mia, "site.restore", map[string]any{"id": id, "expected_version_id": expected, "version_id": to})
	}
	u := restore(v4, v3)
	eq(t, order(u["data"]), "Hero,Header,Services,Services,ContactForm,Footer")
	eq(t, s.draft(), v3)
	restore(v3, v4)
	eq(t, s.draft(), v4)
	o := restore(v4, v1)
	eq(t, order(o["data"]), "Header,Hero,Services,ContactForm,Footer")
	eq(t, o["site"].(map[string]any)["parent_version_id"], nil)
	eq(t, s.versions(), "4")
	s.fail(s.mia, "site.restore", map[string]any{"id": id, "expected_version_id": v1, "version_id": missingID}, 404)

	// editing after an undo branches from the current draft; the undone versions stay restorable
	r = s.editOps(s.mia, id, v1, map[string]any{"tool": "set_theme", "input": map[string]any{"primary": "#b91c1c"}}).json()
	v5 := r["site"].(map[string]any)["draft_version_id"].(string)
	eq(t, r["site"].(map[string]any)["parent_version_id"], v1)
	eq(t, s.versions(), "5")
	restore(v5, v4)
	// a historical version can be read without moving the draft
	old := s.ok(s.mia, "site.get", map[string]any{"id": id, "version_id": v2})
	eq(t, order(old["data"]), "Hero,Header,Services,ContactForm,Footer")
	eq(t, s.draft(), v4)
}

// 5. Cancel: a running turn becomes cancelled, never commits, and frees the site.
func TestSiteTurnCancel(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	hero := blockOf(s.get(s.mia, id)["data"], "Hero")
	model, g := newGate(t, editHero(hero, "Never saved"))
	s.useModel(s.owner, model)
	turn := s.chat(s.mia, id, v1, "change the title")
	g.waitReached(t)
	// one active turn per site; edits wait for it
	s.fail(s.oz, "site.chat", map[string]any{"id": id, "expected_version_id": v1, "message": "me too"}, 409)
	if rs := s.editOps(s.oz, id, v1, map[string]any{"tool": "remove_block", "input": map[string]any{"id": hero}}); rs.status != 409 {
		t.Fatalf("edit during turn: %d %s", rs.status, rs.body)
	}
	eq(t, s.ok(s.oz, "site.turn_cancel", map[string]any{"id": turn})["status"], "cancelled")
	g.open()
	// the job notices (its next write is fenced) and ends without a version
	settled(t, "the job", func() bool {
		return s.sql("select count(*)::text from core.jobs where kind = 'site.chat' and status in ('queued','running')") == "0"
	})
	eq(t, s.sql("select status from sites.site_turns where id = $1::uuid", turn), "cancelled")
	eq(t, s.versions(), "1")
	eq(t, s.draft(), v1)
	// cancelling again, or a finished turn, only reports the status
	eq(t, s.ok(s.mia, "site.turn_cancel", map[string]any{"id": turn})["status"], "cancelled")
	s.fail(s.mia, "site.turn_cancel", map[string]any{"id": missingID}, 404)
}

// 6. Overlapping attempts: a job whose turn was claimed by another attempt writes nothing; the sweep then fails the
// turn once no job is alive for five minutes.
func TestSiteOverlappingAttemptAndStaleSweep(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	hero := blockOf(s.get(s.mia, id)["data"], "Hero")
	model, g := newGate(t, editHero(hero, "Stale attempt"))
	s.useModel(s.owner, model)
	turn := s.chat(s.mia, id, v1, "change the title")
	g.waitReached(t)
	adminSQL(s.env, s.owner.ws, "update sites.site_turns set attempt = attempt + 1 where id = $1::uuid", turn) // another attempt claimed it
	g.open()
	settled(t, "the job", func() bool {
		return s.sql("select count(*)::text from core.jobs where kind = 'site.chat' and status in ('queued','running')") == "0"
	})
	eq(t, s.sql("select status || ' ' || attempt from sites.site_turns where id = $1::uuid", turn), "running 2")
	eq(t, s.versions(), "1")
	// the paid call still counts toward the turn's cap and usage, even though its attempt was fenced
	eq(t, s.sql("select (calls = 1)::text from sites.site_turns where id = $1::uuid", turn), "true")

	// not stale yet: a recent turn stays; then it ages
	if err := sites.Sweep(context.Background(), s.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, s.sql("select status from sites.site_turns where id = $1::uuid", turn), "running")
	adminSQL(s.env, s.owner.ws, "update sites.site_turns set updated_at = now() - interval '6 minutes' where id = $1::uuid", turn)
	if err := sites.Sweep(context.Background(), s.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, s.sql("select status from sites.site_turns where id = $1::uuid", turn), "failed")
	s.wait(s.mia, s.chat(s.mia, id, v1, "the site is free again"))
}

// 7. The initiating user's current role is rechecked when the job starts.
func TestSiteRevokedMemberFailsTurn(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	enqueue := func(user who) string {
		turn := s.sql("select gen_random_uuid()::text")
		adminSQL(s.env, s.owner.ws, `insert into sites.site_turns (id, workspace_id, site_id, created_by, message, base_version_id)
			select $3::uuid, workspace_id, id, $2::uuid, 'hello', draft_version_id from sites.sites where id = $1::uuid`, id, user.userID, turn)
		adminSQL(s.env, s.owner.ws, `insert into core.jobs (workspace_id, kind, payload, max_attempts) values ($1::uuid, 'site.chat', jsonb_build_object('turn_id', $2::text), 2)`, s.owner.ws, turn)
		return turn
	}
	for _, c := range []struct{ name, sql string }{
		{"demoted to guest", "update core.memberships set role = 'guest' where user_id = $1::uuid"},
		{"deactivated", "update core.memberships set deactivated_at = now() where user_id = $1::uuid"},
	} {
		adminSQL(s.env, s.owner.ws, c.sql, s.mia.userID)
		turn := enqueue(s.mia)
		settled(t, c.name, func() bool { return s.sql("select status from sites.site_turns where id = $1::uuid", turn) == "failed" })
		eq(t, s.sql("select error from sites.site_turns where id = $1::uuid", turn), "You no longer have access to this website")
		adminSQL(s.env, s.owner.ws, "update core.memberships set role = 'member', deactivated_at = null where user_id = $1::uuid", s.mia.userID)
	}
	eq(t, s.versions(), "1")
	eq(t, s.draft(), v1)
}

// 8. Quota of 200 chat turns per workspace and day, and the daily AI budget.
func TestSiteChatQuotaAndBudget(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)

	adminSQL(s.env, s.owner.ws, `update core.workspaces set settings = settings || '{"ai_daily_tokens": 100}'::jsonb where id = $1::uuid`, s.owner.ws)
	turn := s.chat(s.mia, id, v1, "hello")
	tr := s.wait(s.mia, turn)["turn"].(map[string]any)
	eq(t, tr["status"], "failed")
	if !strings.Contains(tr["error"].(string), "budget") {
		t.Fatalf("error: %v", tr["error"])
	}
	adminSQL(s.env, s.owner.ws, `update core.workspaces set settings = settings - 'ai_daily_tokens' where id = $1::uuid`, s.owner.ws)

	adminSQL(s.env, s.owner.ws, `insert into sites.site_turns (workspace_id, site_id, created_by, message, base_version_id, status)
		select workspace_id, id, created_by, 'old', draft_version_id, 'done' from sites.sites, generate_series(1, 198)`)
	s.wait(s.mia, s.chat(s.mia, id, v1, "the 200th")) // 200 of 200: allowed
	rs := s.call(s.mia, "site.chat", map[string]any{"id": id, "expected_version_id": v1, "message": "the 201st"})
	if rs.status != 402 || !strings.Contains(string(rs.body), "200") {
		t.Fatalf("quota: %d %s", rs.status, rs.body)
	}
}

// 9. Roles: admins create, members read and edit, guests and API keys are kept out; internal commands are not HTTP.
func TestSiteRolesAndInternalCommands(t *testing.T) {
	s := newSiteEnv(t)
	guest := s.addMember(s.owner, s.signup("Gus", false), "guest")
	key := s.apiKey(s.owner)
	for _, cmd := range []string{"site.templates", "site.list"} {
		s.fail(guest, cmd, map[string]any{}, 403)
	}
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	eq(t, len(s.call(s.mia, "site.list", map[string]any{}).jsonList()), 1)
	s.get(s.mia, id)
	s.fail(guest, "site.get", map[string]any{"id": id}, 403)
	s.fail(guest, "site.chat", map[string]any{"id": id, "expected_version_id": v1, "message": "hi"}, 403)
	s.fail(guest, "site.edit", map[string]any{"id": id, "expected_version_id": v1, "ops": []any{}}, 403)
	s.fail(guest, "site.restore", map[string]any{"id": id, "expected_version_id": v1, "version_id": v1}, 403)
	s.fail(guest, "site.turn_cancel", map[string]any{"id": missingID}, 403)
	s.fail(guest, "site.turn_get", map[string]any{"id": missingID}, 403)
	// an API key follows the owner's role for reads but never drives the paid model loop
	s.get(key, id)
	s.fail(key, "site.chat", map[string]any{"id": id, "expected_version_id": v1, "message": "hi"}, 403)
	s.fail(s.mia, "site.get", map[string]any{"id": "not-an-id"}, 400)

	for _, cmd := range []string{"ai.step", "agent.budget_reserve", "agent.budget_settle"} {
		s.fail(s.owner, cmd, map[string]any{}, 404)
	}
}

// 10. Another workspace sees nothing, and the database refuses ids that belong to another site or workspace.
func TestSiteCrossWorkspaceIsolation(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id, v1 := site["id"].(string), site["draft_version_id"].(string)
	turn := s.chat(s.owner, id, v1, "hello")
	s.wait(s.owner, turn)

	other := s.signup("Other", true)
	call := func(cmd string, in any) resp { return s.cmd(s.gosv.URL, other, other.ws, cmd, in, nil) }
	otherSite := adminOK(t, call("site.create", map[string]any{"template": "services"}))["site"].(map[string]any)
	ov := otherSite["draft_version_id"].(string)
	eq(t, len(call("site.list", map[string]any{}).jsonList()), 1)
	for cmd, in := range map[string]any{
		"site.get":         map[string]any{"id": id},
		"site.turn_get":    map[string]any{"id": turn},
		"site.turn_cancel": map[string]any{"id": turn},
		"site.chat":        map[string]any{"id": id, "expected_version_id": v1, "message": "x"},
		"site.edit":        map[string]any{"id": id, "expected_version_id": v1, "ops": []any{map[string]any{"tool": "set_theme", "input": map[string]any{}}}},
		"site.restore":     map[string]any{"id": id, "expected_version_id": v1, "version_id": v1},
	} {
		if r := call(cmd, in); r.status != 404 {
			t.Fatalf("%s across workspaces: %d %s", cmd, r.status, r.body)
		}
	}
	// a version of another workspace's site is not found in mine
	s.fail(s.owner, "site.restore", map[string]any{"id": id, "expected_version_id": v1, "version_id": ov}, 404)
	s.fail(s.owner, "site.get", map[string]any{"id": id, "version_id": ov}, 404)

	// composite foreign keys: the draft, a turn's base and a version's parent cannot point at another site's version
	for _, q := range []string{
		"update sites.sites set draft_version_id = $1::uuid where id = $2::uuid",
		"update sites.site_turns set base_version_id = $1::uuid where site_id = $2::uuid",
		"update sites.site_turns set version_id = $1::uuid where site_id = $2::uuid",
	} {
		tx, err := dbTenant(s.env, s.owner.ws)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(context.Background(), q, ov, id); err == nil {
			t.Fatalf("accepted a foreign version: %s", q)
		}
		_ = tx.Rollback(context.Background())
	}
}
