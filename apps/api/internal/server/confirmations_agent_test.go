package server_test

import (
	"encoding/json"
	"testing"

	"github.com/cnote/api/internal/command"
)

// An AI employee's held draft is a confirmation linked to a run step. Only an admin or the person the run acted for
// can see and decide it; it executes the exact stored input once; deciding twice is idempotent.
func TestConfirmationDecideForAgentRunDrafts(t *testing.T) {
	send := command.NewCommand("test.a4.send", command.GroupWriteNotes, func(c *command.Ctx, b json.RawMessage) (any, error) { return json.RawMessage(b), nil })
	send.Confirm = true
	money := command.NewCommand("test.a4.money", command.GroupWriteNotes, func(c *command.Ctx, b json.RawMessage) (any, error) { return json.RawMessage(b), nil })
	money.Confirm = true
	money.Amount = func(*command.Ctx, json.RawMessage) (string, int64, error) { return "", 5000, nil }
	e := newEnv(t, send, money)
	owner := e.signup("Owner", true)
	mia := e.addMember(owner, e.signup("Mia", false), "member")
	other := e.addMember(owner, e.signup("Oz", false), "member")
	guest := e.addMember(owner, e.signup("Gus", false), "guest")
	e.must(owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes", "admin"}}})
	created := e.must(owner, "agent.create", map[string]any{"name": "Drafty", "scopes": []string{"*"}})
	agent := who{ws: owner.ws, bearer: created["api_key"].(string)}
	agentID := created["agent_id"].(string)

	// hold a draft and link it to a run made for Mia, as the runtime does
	hold := func(cmd string, in map[string]any) string {
		cid := adminOK(t, e.cmd(e.gosv.URL, agent, owner.ws, cmd, in, nil))["confirmation_id"].(string)
		adminSQL(e, owner.ws, `with r as (insert into core.agent_runs (workspace_id, agent_id, on_behalf_of, trigger, status)
				values ($1::uuid,$2::uuid,$3::uuid,'manual','held') returning id)
			insert into core.agent_run_steps (workspace_id, run_id, seq, kind, command, outcome, confirmation_id)
			select $1::uuid, r.id, 1, 'tool', $4, 'held', $5::uuid from r`, owner.ws, agentID, mia.userID, cmd, cid)
		return cid
	}
	decide := func(w who, cid string, approve bool, extra map[string]any) resp {
		in := map[string]any{"id": cid, "approve": approve}
		for k, v := range extra {
			in[k] = v
		}
		return e.cmd(e.gosv.URL, w, owner.ws, "confirmation.decide", in, nil)
	}
	listed := func(w who) []map[string]any {
		return e.cmd(e.gosv.URL, w, owner.ws, "confirmation.list", map[string]any{}, nil).jsonList()
	}

	cid := hold("test.a4.send", map[string]any{"text": "hello"})
	// visibility: guest refused, a stranger sees nothing, Mia and admins see it with agent and run
	if r := e.cmd(e.gosv.URL, guest, owner.ws, "confirmation.list", map[string]any{}, nil); r.status != 403 {
		t.Fatalf("guest list %d", r.status)
	}
	eq(t, len(listed(other)), 0)
	for _, w := range []who{mia, owner} {
		l := listed(w)
		eq(t, len(l), 1)
		eq(t, l[0]["agent_name"], "Drafty")
		eq(t, l[0]["run_id"] != nil, true)
	}
	// decide: no editing, not a stranger, not the agent, not a guest
	if r := decide(owner, cid, true, map[string]any{"input": map[string]any{"text": "changed"}}); r.status != 400 {
		t.Fatalf("input override %d %s", r.status, r.body)
	}
	for _, w := range []who{other, agent, guest} {
		if r := decide(w, cid, true, nil); r.status != 403 {
			t.Fatalf("decide by %s: %d %s", w.userID, r.status, r.body)
		}
	}
	eq(t, adminScalar(e, owner.ws, "select status from core.confirmations where id = $1::uuid", cid), "pending")
	// Mia approves: executes the stored input once; the double click returns the same answer; the opposite is refused
	out := adminOK(t, decide(mia, cid, true, nil))
	eq(t, out["status"], "confirmed")
	eq(t, out["executed"], true)
	eq(t, out["result"].(map[string]any)["text"], "hello")
	eq(t, adminOK(t, decide(mia, cid, true, nil))["executed"], true)
	eq(t, adminScalar(e, owner.ws, "select count(*)::text from core.usage where meta->>'command' = 'test.a4.send'"), "1")
	if r := decide(mia, cid, false, nil); r.status != 404 {
		t.Fatalf("opposite decision %d", r.status)
	}
	eq(t, len(listed(mia)), 0)

	// reject executes nothing
	cid = hold("test.a4.send", map[string]any{"text": "no"})
	eq(t, adminOK(t, decide(owner, cid, false, nil))["status"], "rejected")
	eq(t, adminScalar(e, owner.ws, "select count(*)::text from core.usage where meta->>'command' = 'test.a4.send'"), "1")

	// re-evaluation: a rule added after the draft was held turns the approval into an approval request in Mia's name
	cid = hold("test.a4.money", map[string]any{"n": 1})
	adminSQL(e, owner.ws, `insert into core.approval_rules (workspace_id, command, min_amount_minor, approver_role, steps)
		values ($1::uuid,'test.a4.money',1,'admin','[{"mode":"any","min_amount_minor":0,"approvers":[{"kind":"role","role":"admin"}]}]')`, owner.ws)
	out = adminOK(t, decide(mia, cid, true, nil))
	eq(t, out["executed"], false)
	eq(t, out["result"].(map[string]any)["approval_required"], true)
	eq(t, adminScalar(e, owner.ws, "select count(*)::text from core.confirmations where kind = 'approval' and requested_by = $1", mia.userID), "1")
	eq(t, adminOK(t, decide(mia, cid, true, nil))["executed"], false)
}

// DPDP erasure deletes AI employee runs attached to the contact, with their held drafts.
func TestEraseSubjectDeletesAgentRunsAndDrafts(t *testing.T) {
	b := newBT(t, false)
	agent := b.must(b.owner, "agent.create", map[string]any{"name": "Eraseable", "scopes": []string{"read"}})["agent_id"].(string)
	cid := b.dbStrings(b.owner.ws, "select gen_random_uuid()::text")[0]
	adminSQL(b.env, b.owner.ws, `with c as (insert into core.confirmations (id, workspace_id, command, input, requested_by)
			values ($4::uuid,$1::uuid,'share.email','{"body":"Dear Buyer"}',$2) returning id),
		r as (insert into core.agent_runs (workspace_id, agent_id, on_behalf_of, trigger, status, note_id)
			values ($1::uuid,$2::uuid,$3::uuid,'event','held',$5::uuid) returning id)
		insert into core.agent_run_steps (workspace_id, run_id, seq, kind, outcome, confirmation_id)
		select $1::uuid, r.id, 1, 'tool', 'held', c.id from r, c`, b.owner.ws, agent, b.owner.userID, cid, b.contact)
	b.enableGroups("read", "admin", "destructive")
	b.ok("data.erase_subject", map[string]any{"contact_id": b.contact, "reason": "customer request"})
	eq(t, b.dbStrings(b.owner.ws, "select (select count(*) from core.agent_runs) || '/' || (select count(*) from core.confirmations where id = $1::uuid)", cid), []string{"0/0"})
}
