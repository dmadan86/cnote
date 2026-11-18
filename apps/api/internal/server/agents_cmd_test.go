package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/agents"
)

type agentEnv struct {
	*env
	owner, mia, oz who
}

func newAgentEnv(t *testing.T) *agentEnv {
	e := newEnv(t)
	o := e.signup("Owner", true)
	return &agentEnv{e, o, e.addMember(o, e.signup("Mia", false), "member"), e.addMember(o, e.signup("Oz", false), "member")}
}

func (a *agentEnv) call(w who, cmd string, in any) resp {
	return a.cmd(a.gosv.URL, w, a.owner.ws, cmd, in, nil)
}

func (a *agentEnv) ok(w who, cmd string, in any) map[string]any {
	a.t.Helper()
	return adminOK(a.t, a.call(w, cmd, in))
}

func (a *agentEnv) fail(w who, cmd string, in any, status int) {
	a.t.Helper()
	if r := a.call(w, cmd, in); r.status != status {
		a.t.Fatalf("%s %v: want %d, got %d %s", cmd, in, status, r.status, r.body)
	}
}

func (a *agentEnv) sql(q string, args ...any) string {
	return adminScalar(a.env, a.owner.ws, q, args...)
}

func strs(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAgentTemplatesInstallAndUpdate(t *testing.T) {
	a := newAgentEnv(t)
	// templates are readable by members; the support template needs send_messages switched on
	tl := a.call(a.mia, "agent.templates", map[string]any{}).jsonList()
	if len(tl) != 4 {
		t.Fatalf("templates: %v", tl)
	}
	for _, x := range tl {
		needs := x["needs"].(map[string]any)
		if x["key"] == "support" && !has(strs(needs["groups_off"]), "send_messages") || x["key"] == "briefing" && needs["chat_agents"] != true {
			t.Fatalf("needs: %v", x)
		}
		if x["agent_id"] != nil {
			t.Fatalf("nothing installed yet: %v", x)
		}
	}

	// install: admin and person only, once per template
	a.fail(a.mia, "agent.install", map[string]any{"template": "support"}, 403)
	a.fail(a.owner, "agent.install", map[string]any{"template": "nope"}, 400)
	a.fail(a.owner, "agent.install", map[string]any{"template": "support", "quotes": true}, 400)
	sup := a.ok(a.owner, "agent.install", map[string]any{"template": "support"})
	eq(t, sup["kind"], "internal")
	eq(t, sup["runtime"], true)
	eq(t, sup["owner_user_id"], a.owner.userID)
	eq(t, sup["instructions"], nil)
	if sup["instructions_default"] == nil || !has(strs(sup["tools"]), "ticket.reply") || !has(strs(sup["events"]), "ticket.created") {
		t.Fatalf("support: %v", sup)
	}
	eq(t, a.sql(`select ((settings->'agent_tool_groups') ? 'send_messages')::text from core.workspaces where id = $1::uuid`, a.owner.ws), "true")
	a.fail(a.owner, "agent.install", map[string]any{"template": "support"}, 409)
	a.fail(a.mia, "agent.get", map[string]any{"id": "nope"}, 400)
	eq(t, a.ok(a.mia, "agent.get", map[string]any{"id": sup["id"]})["name"], sup["name"])

	// the lead responder gets no quote tools or invoice scope without the switch
	lead := a.ok(a.owner, "agent.install", map[string]any{"template": "lead_responder"})
	if has(strs(lead["tools"]), "quote.create_draft") || has(strs(lead["scopes"]), "manage_invoices") || !has(strs(lead["tools"]), "contact.email") {
		t.Fatalf("lead: %v", lead)
	}
	o2 := a.signup("Owner2", true)
	q := adminOK(t, a.cmd(a.gosv.URL, o2, o2.ws, "agent.install", map[string]any{"template": "lead_responder", "quotes": true}, nil))
	if !has(strs(q["tools"]), "quote.create_draft") || !has(strs(q["scopes"]), "manage_invoices") {
		t.Fatalf("lead with quotes: %v", q)
	}

	// the briefing: daily at the given IST time, delivered to a channel the agent can post in
	br := a.ok(a.owner, "agent.install", map[string]any{"template": "briefing", "at": "09:15"})
	eq(t, n(br["every_minutes"]), int64(1440))
	if br["deliver_channel_id"] == nil {
		t.Fatalf("briefing channel: %v", br)
	}
	next, err := time.Parse(time.RFC3339Nano, br["next_run_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if ist := next.In(time.FixedZone("IST", 5*3600+1800)); ist.Hour() != 9 || ist.Minute() != 15 || !next.After(time.Now()) {
		t.Fatalf("next_run_at %v", next)
	}
	eq(t, a.sql(`select count(*)::text from chat.agent_members where agent_id = $1::uuid and channel_id = $2::uuid`, br["id"], br["deliver_channel_id"]), "1")
	eq(t, a.sql(`select settings->>'chat_agents' from core.workspaces where id = $1::uuid`, a.owner.ws), "true")

	// update: admin and person only; tools and scopes only narrow the template's
	id := sup["id"]
	a.fail(a.mia, "agent.update", map[string]any{"id": id, "name": "x"}, 403)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "bogus": 1}, 400)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "tools": []string{"invoice.issue"}}, 400)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "scopes": []string{"read", "admin"}}, 400)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "events": []string{"agent.run"}}, 400)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "events": []string{"*"}}, 400)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "max_steps": 99}, 400)
	a.fail(a.owner, "agent.update", map[string]any{"id": id, "at": "10:00"}, 400) // no schedule to anchor
	u := a.ok(a.owner, "agent.update", map[string]any{"id": id, "instructions": "Be brief.", "tools": []string{"ticket.get", "ticket.reply"},
		"scopes": []string{"read", "send_messages"}, "max_steps": 5, "model_tier": "routine", "events": []string{"ticket.created"}, "enabled": false})
	eq(t, u["instructions"], "Be brief.")
	eq(t, strings.Join(strs(u["tools"]), ","), "ticket.get,ticket.reply")
	eq(t, u["enabled"], false)
	u = a.ok(a.owner, "agent.update", map[string]any{"id": id, "instructions": nil, "tools": nil, "enabled": true})
	eq(t, u["instructions"], nil)
	eq(t, len(strs(u["tools"])), len(agents.ToolAllowlist["support"]))
	if r := a.ok(a.owner, "agent.update", map[string]any{"id": br["id"], "every_minutes": 720}); r["next_run_at"] == nil || n(r["every_minutes"]) != 720 {
		t.Fatalf("schedule: %v", r)
	}
	if r := a.ok(a.owner, "agent.update", map[string]any{"id": br["id"], "every_minutes": nil}); r["next_run_at"] != nil {
		t.Fatalf("schedule off: %v", r)
	}
	eq(t, a.sql(`select count(*)::text from core.events where kind = 'agent.updated' and payload->>'agent_id' = $1`, id), "2")

	// outside agents with an API key are never changed here, and an agent key can never install or update
	ext := a.env.must(a.owner, "agent.create", map[string]any{"name": "Outside", "scopes": []string{"read"}})
	a.fail(a.owner, "agent.update", map[string]any{"id": ext["agent_id"], "name": "y"}, 400)
	key := who{ws: a.owner.ws, bearer: ext["api_key"].(string)}
	a.fail(key, "agent.install", map[string]any{"template": "ask"}, 403)
	a.fail(key, "agent.update", map[string]any{"id": id, "name": "z"}, 403)
}

func TestAgentRunsAreVisibleToTheirPeopleOnly(t *testing.T) {
	a := newAgentEnv(t)
	ask := a.ok(a.owner, "agent.install", map[string]any{"template": "ask"})
	// the Ask box: members, with a conversation only its person can continue
	a.fail(a.mia, "agent.ask", map[string]any{"question": ""}, 400)
	r1 := a.ok(a.mia, "agent.ask", map[string]any{"question": "what is overdue?"})
	conv := r1["conversation_id"].(string)
	a.fail(a.oz, "agent.ask", map[string]any{"question": "more", "conversation_id": conv}, 404)
	r2 := a.ok(a.mia, "agent.ask", map[string]any{"question": "and next week?", "conversation_id": conv})
	eq(t, r2["conversation_id"], conv)
	// running an agent by hand is for admins
	a.fail(a.mia, "agent.run", map[string]any{"agent_id": ask["id"]}, 403)
	a.ok(a.owner, "agent.run", map[string]any{"agent_id": ask["id"], "input": "test"})
	// an agent's own key cannot start one
	ext := a.env.must(a.owner, "agent.create", map[string]any{"name": "Outside", "scopes": []string{"read", "write_notes"}})
	a.fail(who{ws: a.owner.ws, bearer: ext["api_key"].(string)}, "agent.ask", map[string]any{"question": "hi"}, 403)

	// transcripts: the person it ran for, the agent's owner (here also admin), nobody else
	mk := func(who, status string) string {
		id := a.sql(`select gen_random_uuid()::text`)
		adminSQL(a.env, a.owner.ws, `insert into core.agent_runs (id, workspace_id, agent_id, on_behalf_of, trigger, status, input, output)
			values ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'manual',$5,'q','a')`, id, a.owner.ws, ask["id"], who, status)
		return id
	}
	run := mk(a.mia.userID, "queued")
	adminSQL(a.env, a.owner.ws, `insert into core.agent_run_steps (workspace_id, run_id, seq, kind, text, stop, tokens_in, tokens_out) values ($1::uuid,$2::uuid,1,'model','hello','end',5,6)`, a.owner.ws, run)
	adminSQL(a.env, a.owner.ws, `insert into core.agent_run_steps (workspace_id, run_id, seq, kind, command, input, result, outcome, ms) values ($1::uuid,$2::uuid,2,'tool','home.summary','{}','ok','ok',3)`, a.owner.ws, run)
	g := a.ok(a.mia, "agent.run_get", map[string]any{"id": run})
	eq(t, g["run"].(map[string]any)["on_behalf_name"], "Mia")
	steps := g["steps"].([]any)
	if len(steps) != 2 || steps[0].(map[string]any)["kind"] != "model" || steps[1].(map[string]any)["command"] != "home.summary" {
		t.Fatalf("steps: %v", steps)
	}
	a.ok(a.owner, "agent.run_get", map[string]any{"id": run})
	a.fail(a.oz, "agent.run_get", map[string]any{"id": run}, 404)
	a.fail(a.oz, "agent.run_cancel", map[string]any{"id": run}, 404)
	ids := func(w who, in map[string]any) []string {
		out := []string{}
		for _, x := range a.call(w, "agent.run_list", in).jsonList() {
			out = append(out, x["id"].(string))
		}
		return out
	}
	if l := ids(a.oz, map[string]any{}); has(l, run) {
		t.Fatalf("oz sees mia's run: %v", l)
	}
	if l := ids(a.mia, map[string]any{"agent_id": ask["id"], "status": "queued"}); !has(l, run) {
		t.Fatalf("mia's list: %v", l)
	}
	if l := ids(a.owner, map[string]any{"agent_id": ask["id"], "limit": 2}); len(l) != 2 {
		t.Fatalf("owner's list: %v", l)
	}
	eq(t, a.ok(a.mia, "agent.run_cancel", map[string]any{"id": run})["status"], "cancelled")
	eq(t, a.ok(a.mia, "agent.run_cancel", map[string]any{"id": run})["status"], "cancelled") // already finished: unchanged
	done := mk(a.mia.userID, "completed")
	eq(t, a.ok(a.mia, "agent.run_cancel", map[string]any{"id": done})["status"], "completed")
}

// ctxAs runs fn in a tenant transaction as the system, the way the worker calls OnEvent and the sweeps.
func (a *agentEnv) ctxAs(fn func(c *command.Ctx) error) {
	a.t.Helper()
	reg := command.NewRegistry(modules.Commands()...)
	if err := reg.WithTx(context.Background(), a.pool, command.Principal{Kind: command.KindSystem, WorkspaceID: a.owner.ws, Role: command.Admin}, fn); err != nil {
		a.t.Fatal(err)
	}
}

func TestAgentEventsDedupeAndGuards(t *testing.T) {
	a := newAgentEnv(t)
	sup := a.ok(a.owner, "agent.install", map[string]any{"template": "support"})["id"].(string)
	count := func(where string) string {
		return a.sql(`select count(*)::text from core.agent_runs where agent_id = $1::uuid and `+where, sup)
	}
	ev := jobs.Event{ID: 880001, WorkspaceID: a.owner.ws, Kind: "ticket.created", ActorType: "user", ActorID: &a.mia.userID}
	for i := 0; i < 2; i++ { // the same event twice: one run
		a.ctxAs(func(c *command.Ctx) error { return agents.OnEvent(c, ev) })
	}
	eq(t, count(`dedupe_key = 'event:880001' and trigger = 'event' and on_behalf_of = '`+a.owner.userID+`'`), "1") // runs for the owner
	// no prefix match, an agent's own event, and agent.* kinds start nothing
	for _, e := range []jobs.Event{
		{ID: 880002, Kind: "inquiry.received", ActorType: "system"},
		{ID: 880003, Kind: "ticket.created", ActorType: "agent", ActorID: &sup},
		{ID: 880004, Kind: "agent.run.completed", ActorType: "user"},
	} {
		a.ctxAs(func(c *command.Ctx) error { return agents.OnEvent(c, e) })
	}
	eq(t, count(`dedupe_key in ('event:880002','event:880003','event:880004')`), "0")
	// a paused agent starts nothing
	a.ok(a.owner, "agent.update", map[string]any{"id": sup, "enabled": false})
	a.ctxAs(func(c *command.Ctx) error {
		return agents.OnEvent(c, jobs.Event{ID: 880005, Kind: "ticket.created", ActorType: "user", ActorID: &a.mia.userID})
	})
	eq(t, count(`dedupe_key = 'event:880005'`), "0")
	a.ok(a.owner, "agent.update", map[string]any{"id": sup, "enabled": true})
	// a chat mention runs for the author, once per message
	mention := jobs.Event{ID: 880006, Kind: "chat.agent_mentioned", ActorType: "user", ActorID: &a.mia.userID,
		Payload: []byte(`{"channel_id":"` + a.owner.ws + `","message_id":"` + a.owner.userID + `","agent_id":"` + sup + `","body":"help"}`)}
	for i := 0; i < 2; i++ {
		a.ctxAs(func(c *command.Ctx) error { return agents.OnEvent(c, mention) })
	}
	eq(t, count(`trigger = 'chat' and on_behalf_of = '`+a.mia.userID+`' and input = 'help'`), "1")
}

func TestAgentSweepSchedulesAndPurges(t *testing.T) {
	a := newAgentEnv(t)
	br := a.ok(a.owner, "agent.install", map[string]any{"template": "briefing"})["id"].(string)
	adminSQL(a.env, a.owner.ws, `update core.agents set next_run_at = now() - interval '3 hours 10 minutes' where id = $1::uuid`, br)
	runs := func() string {
		return a.sql(`select count(*)::text from core.agent_runs where agent_id = $1::uuid and trigger = 'schedule' and on_behalf_of = $2::uuid`, br, a.owner.userID)
	}
	for i := 0; i < 2; i++ { // a second sweep finds nothing due
		if err := agents.Sweep(context.Background(), a.pool); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, runs(), "1")
	eq(t, a.sql(`select (next_run_at > now() and next_run_at <= now() + interval '24 hours')::text from core.agents where id = $1::uuid`, br), "true")

	// retention: older than the workspace's days (here 10) goes; finished and recent ones stay
	a.ok(a.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_run_retention_days": 10}})
	old := func(days int, status string) string {
		id := a.sql(`select gen_random_uuid()::text`)
		adminSQL(a.env, a.owner.ws, `insert into core.agent_runs (id, workspace_id, agent_id, on_behalf_of, trigger, status, input, output, created_at)
			values ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'manual',$5,'q','a', now() - make_interval(days => $6))`, id, a.owner.ws, br, a.owner.userID, status, days)
		adminSQL(a.env, a.owner.ws, `insert into core.agent_run_steps (workspace_id, run_id, seq, kind, text) values ($1::uuid,$2::uuid,1,'model','t')`, a.owner.ws, id)
		return id
	}
	gone, recent, held := old(15, "completed"), old(5, "completed"), old(15, "held")
	if err := agents.Purge(context.Background(), a.pool); err != nil {
		t.Fatal(err)
	}
	state := func(id string) string {
		return a.sql(`select input || '|' || coalesce(output,'-') || '|' || (select count(*) from core.agent_run_steps where run_id = $1::uuid) from core.agent_runs where id = $1::uuid`, id)
	}
	eq(t, state(gone), "|-|0")
	eq(t, state(recent), "q|a|1")
	eq(t, state(held), "q|a|1")
}

// chat.send tells the agents module a person linked an agent that is in the channel; and mentions still notify people.
func TestChatEmitsAgentMentionedOnlyForAgentsInTheChannel(t *testing.T) {
	b := newBT(t, false)
	ws := b.owner.ws
	ann := b.memberAs("Ann", "member")
	sup := b.ok("agent.install", map[string]any{"template": "support"})["id"].(string)
	b.env.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"chat_agents": true}})
	ch := b.chan_(b.owner, "help", false)
	link := "[@Support](/w/" + ws + "/agents/" + sup + ")"
	b.say(b.owner, ch, "hello "+link) // not in the channel yet
	eq(t, b.dbStrings(ws, "select count(*)::text from core.events where kind = 'chat.agent_mentioned'"), []string{"0"})
	b.ok("chat.agent_add", map[string]any{"channel_id": ch, "agent_id": sup})
	msg := b.say(b.owner, ch, "again "+link+" and [@Ann](/w/"+ws+"/people/"+ann.userID+")")
	eq(t, b.dbStrings(ws, "select count(*)::text from core.events where kind = 'chat.agent_mentioned' and payload->>'message_id' = $1 and payload->>'agent_id' = $2", msg, sup), []string{"1"})
	eq(t, b.dbStrings(ws, "select count(*)::text from core.notifications where kind = 'chat_mention' and user_id = $1::uuid", ann.userID), []string{"1"})
	b.env.settle(ws)
	eq(t, b.dbStrings(ws, "select count(*)::text from core.agent_runs where trigger = 'chat' and dedupe_key = $1", "chat:"+msg), []string{"1"})
}

// contact.email is a held send: an agent only drafts it; a person's call needs working mail.
func TestContactEmailIsHeldForAgents(t *testing.T) {
	b := newBT(t, false)
	b.env.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "send_messages"}}})
	adminSQL(b.env, b.owner.ws, `update crm.contacts set email = 'buyer@example.test' where note_id = $1::uuid`, b.contact)
	k := b.env.must(b.owner, "agent.create", map[string]any{"name": "Mailer", "scopes": []string{"read", "manage_crm", "send_messages"}})
	agent := who{ws: b.owner.ws, bearer: k["api_key"].(string)}
	in := map[string]any{"contact_id": b.contact, "subject": "Re: your enquiry", "body": "Hello, here is our reply."}
	r := b.okAs(agent, "contact.email", in)
	if r["confirmation_id"] == nil {
		t.Fatalf("agent send must be held: %v", r)
	}
	b.errAs(b.owner, "contact.email", map[string]any{"contact_id": b.contact, "subject": "x"}, 400, "invalid")
	t.Setenv("SMTP_URL", "")
	b.errAs(b.owner, "contact.email", in, 400, "invalid") // mail is not set up: nothing is sent or logged
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.activities where kind = 'email'"), []string{"0"})
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "Acme <hello@acme.example.test>")
	eq(t, b.ok("contact.email", in)["sent"], true)
	subject, _, _, text, _ := parseMail(t, lastMail(t, sink))
	if subject != "Re: your enquiry" || !strings.Contains(text, "here is our reply") {
		t.Fatalf("mail: %q %q", subject, text)
	}
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from crm.activities where kind = 'email' and note_id = $1::uuid", b.contact), []string{"1"})
}
