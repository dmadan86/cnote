package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
)

func isGateDenial(err error) bool {
	var ce *command.Error
	if !errors.As(err, &ce) || ce.Code != command.Forbidden {
		return false
	}
	for _, s := range []string{"requires role", "scope ", "finance access", "tool group"} {
		if strings.Contains(ce.Message, s) {
			return true
		}
	}
	return false
}

func agentFixture(e *env, owner who, scopes ...string) string {
	return e.must(owner, "agent.create", map[string]any{"name": "T-" + rnd(), "kind": "internal", "scopes": scopes})["agent_id"].(string)
}

// Allowed must agree with what Registry.run refuses at the gate, for people and for agents.
func TestAgentAllowedMatchesRun(t *testing.T) {
	testConfirmed := command.NewCommand("test.confirmed", command.GroupWriteNotes, func(*command.Ctx, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil })
	testConfirmed.Confirm = true
	e := newEnv(t)
	reg := command.NewRegistry(append(modules.Commands(), testConfirmed)...)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Mem", false), "member")
	guest := e.addMember(owner, e.signup("Gst", false), "guest")
	nofin := e.addMember(owner, e.signup("NoFin", false), "member")
	adminSQL(e, owner.ws, "update core.memberships set finance_access = false where user_id = $1::uuid", nofin.userID)
	agent := agentFixture(e, owner, "read", "write_notes", "manage_invoices")
	ctx := context.Background()
	sys := command.Principal{Kind: command.KindSystem, WorkspaceID: owner.ws, Role: command.Owner}
	var principals []command.Principal
	for _, w := range []who{owner, member, guest, nofin} {
		uid := w.userID
		role := map[string]command.Role{owner.userID: command.Owner, member.userID: command.Member, guest.userID: command.Guest, nofin.userID: command.Member}[uid]
		principals = append(principals, command.Principal{Kind: command.KindUser, UserID: &uid, WorkspaceID: owner.ws, Role: role})
	}
	for _, u := range []who{owner, nofin} {
		if err := reg.WithTx(ctx, e.pool, sys, func(c *command.Ctx) error {
			p, err := command.AgentPrincipal(c, agent, u.userID)
			principals = append(principals, p)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"space.list", "note.list", "note.create", "invoice.get", "invoice.list", "member.update", "agent.list", "contact.list", "search.hybrid", "ai.get_provider", "report.profit_loss", "company.get", "test.confirmed"}
	denied := 0
	for _, p := range principals {
		for _, name := range names {
			var allowed error
			if err := reg.WithTx(ctx, e.pool, p, func(c *command.Ctx) error { allowed = c.Allowed(name); return nil }); err != nil {
				t.Fatal(err)
			}
			_, runErr := reg.Execute(ctx, e.pool, name, json.RawMessage(`{}`), p, command.ExecOptions{})
			if (allowed != nil) != isGateDenial(runErr) {
				t.Errorf("%s as %s %v: Allowed=%v, run=%v", name, p.Kind, p.Role, allowed, runErr)
			}
			if allowed != nil {
				denied++
			}
		}
	}
	if denied < 10 {
		t.Fatalf("only %d denials sampled", denied)
	}
	if err := reg.WithTx(ctx, e.pool, principals[0], func(c *command.Ctx) error { return c.Allowed("no.such") }); err == nil {
		t.Fatal("unknown command allowed")
	}
}

func TestAgentPrincipalIdentity(t *testing.T) {
	testConfirmed := command.NewCommand("test.confirmed", command.GroupWriteNotes, func(*command.Ctx, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil })
	testConfirmed.Confirm = true
	e := newEnv(t)
	reg := command.NewRegistry(append(modules.Commands(), testConfirmed)...)
	owner := e.signup("Owner", true)
	member := e.addMember(owner, e.signup("Mem", false), "member")
	other := e.signup("Other", true)
	agent := agentFixture(e, owner, "read", "write_notes", "manage_invoices")
	otherAgent := agentFixture(e, other, "read")
	ctx := context.Background()
	sys := command.Principal{Kind: command.KindSystem, WorkspaceID: owner.ws, Role: command.Owner}
	with := func(fn func(c *command.Ctx) error) error { return reg.WithTx(ctx, e.pool, sys, fn) }
	principal := func(agentID, uid string) (p command.Principal, err error) {
		err = with(func(c *command.Ctx) error { p, err = command.AgentPrincipal(c, agentID, uid); return err })
		return
	}

	// role is capped at admin; scopes are cut to the workspace's tool groups (manage_invoices is off by default)
	p, err := principal(agent, owner.userID)
	if err != nil || p.Role != command.Admin || p.Kind != command.KindAgent || *p.AgentID != agent || *p.UserID != owner.userID {
		t.Fatalf("owner: %+v %v", p, err)
	}
	if !slices.Equal(p.Scopes, []string{"read", "write_notes"}) {
		t.Fatalf("scopes %v", p.Scopes)
	}
	if p, err = principal(agent, member.userID); err != nil || p.Role != command.Member {
		t.Fatalf("member: %+v %v", p, err)
	}
	// refusals
	if _, err = principal(otherAgent, owner.userID); err == nil {
		t.Error("an agent of another workspace was accepted")
	}
	if _, err = principal(agent, other.userID); err == nil {
		t.Error("a user outside the workspace was accepted")
	}
	adminSQL(e, owner.ws, "update core.agents set enabled = false where id = $1::uuid", agent)
	if _, err = principal(agent, owner.userID); err == nil {
		t.Error("a paused agent was accepted")
	}
	adminSQL(e, owner.ws, "update core.agents set enabled = true where id = $1::uuid", agent)

	// CallConfirmed: a Confirm command is held, approved by a person, then replays once as the agent
	var confID string
	if err := with(func(c *command.Ctx) error {
		ac := &command.Ctx{Context: ctx, Tx: c.Tx, Principal: p}
		reg.Bind(ac)
		out, err := ac.Call("test.confirmed", map[string]any{})
		if err != nil {
			return err
		}
		var r struct {
			ID string `json:"confirmation_id"`
		}
		_ = json.Unmarshal(out, &r)
		confID = r.ID
		return nil
	}); err != nil || confID == "" {
		t.Fatalf("hold: %q %v", confID, err)
	}
	// the held request is the agent's: replay is refused until a person confirms it
	replay := func(who string) error {
		return with(func(c *command.Ctx) error {
			_, err := c.CallConfirmed(command.Principal{Kind: command.KindAgent, AgentID: &agent, UserID: &who, WorkspaceID: owner.ws}, "test.confirmed", confID)
			return err
		})
	}
	if err := replay(member.userID); err == nil {
		t.Error("replayed before confirmation")
	}
	adminSQL(e, owner.ws, "update core.confirmations set status = 'confirmed' where id = $1::uuid", confID)
	adminSQL(e, owner.ws, "update core.memberships set deactivated_at = now() where user_id = $1::uuid", member.userID)
	if err := replay(member.userID); err == nil {
		t.Error("replayed for a deactivated member")
	}
	adminSQL(e, owner.ws, "update core.memberships set deactivated_at = null where user_id = $1::uuid", member.userID)
	if err := replay(member.userID); err != nil {
		t.Errorf("replay: %v", err)
	}
	if err := replay(member.userID); err == nil {
		t.Error("a confirmation replayed twice")
	}
}
