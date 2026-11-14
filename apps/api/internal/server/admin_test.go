package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
)

var adminCommands = []string{"member.list", "member.add", "agent.create", "agent.list", "apikey.create", "apikey.list", "apikey.revoke", "workspace.get", "workspace.update", "confirmation.list", "confirmation.decide", "usage.summary"}

func adminOK(t *testing.T, r resp) map[string]any {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	return r.json()
}
func sameAdmin(t *testing.T, g, r resp) {
	t.Helper()
	if g.status != r.status || string(g.body) != string(r.body) {
		t.Fatalf("replay differs: %d %s vs %d %s", g.status, g.body, r.status, r.body)
	}
}
func adminSQL(e *env, ws, sql string, args ...any) {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}
func adminScalar(e *env, ws, sql string, args ...any) string {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out string
	if err = tx.QueryRow(context.Background(), sql, args...).Scan(&out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestAdminSecretsAndRoles(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	admin := e.addMember(owner, e.signup("Admin", false), "admin")
	peer := e.addMember(owner, e.signup("Peer", false), "admin")
	member := e.addMember(owner, e.signup("Member", false), "member")
	guest := e.addMember(owner, e.signup("Guest", false), "guest")
	for _, w := range []who{admin, owner} {
		r := e.cmd(e.gosv.URL, w, owner.ws, "member.add", map[string]any{"email": owner.email, "role": "guest"}, nil)
		if r.status != 403 {
			t.Fatalf("owner demotion: %s", r.body)
		}
	}
	for _, email := range []string{admin.email, peer.email} {
		r := e.cmd(e.gosv.URL, admin, owner.ws, "member.add", map[string]any{"email": email, "role": "guest"}, nil)
		if r.status != 403 {
			t.Fatalf("peer demotion: %s", r.body)
		}
	}
	added := adminOK(t, e.cmd(e.gosv.URL, admin, owner.ws, "member.add", map[string]any{"email": "  " + rnd() + "@EXAMPLE.TEST  ", "role": "guest"}, nil))
	if added["role"] != "guest" || strings.Contains(added["email"].(string), " ") {
		t.Fatal(added)
	}
	for _, w := range []who{member, guest} {
		for _, cmd := range []string{"member.add", "agent.create", "apikey.list", "apikey.revoke", "workspace.update", "usage.summary"} {
			if r := e.cmd(e.gosv.URL, w, owner.ws, cmd, map[string]any{}, nil); r.status != 403 {
				t.Fatalf("%s: %d %s", cmd, r.status, r.body)
			}
		}
	}
	for _, cmd := range []string{"apikey.create", "agent.create"} {
		for _, base := range []string{e.gosv.URL} {
			hdr := map[string]string{"Idempotency-Key": rnd()}
			in := map[string]any{"name": "  Bot ünïcode 🚀  "}
			a := adminOK(t, e.cmd(base, owner, owner.ws, cmd, in, hdr))
			b := adminOK(t, e.cmd(base, owner, owner.ws, cmd, in, hdr))
			if a["api_key"] == b["api_key"] {
				t.Fatal("secret replayed")
			}
			secret := a["api_key"].(string)
			if !regexp.MustCompile(`^cn_[0-9a-f]{8}_[A-Za-z0-9_-]{32}$`).MatchString(secret) {
				t.Fatal("wrong key format")
			}
			hash := sha256.Sum256([]byte(secret))
			kid := a["id"]
			if cmd == "agent.create" {
				kid = a["api_key_id"]
			}
			stored := adminScalar(e, owner.ws, "select key_hash from core.api_keys where id=$1::uuid", kid)
			if stored != hex.EncodeToString(hash[:]) {
				t.Fatal("wrong stored hash")
			}
			if n := adminScalar(e, owner.ws, "select count(*)::text from core.idempotency where key=$1", hdr["Idempotency-Key"]); n != "0" {
				t.Fatal("secret stored for replay")
			}
			w := who{ws: owner.ws, bearer: secret}
			for _, target := range []string{e.gosv.URL} {
				adminOK(t, e.cmd(target, w, owner.ws, "workspace.get", map[string]any{}, nil))
			}
			if cmd == "agent.create" {
				if n := adminScalar(e, owner.ws, "select count(*)::text from core.agents where id=$1::uuid and owner_user_id=$2::uuid and scopes=array['read','write_notes','write_items','manage_crm']", a["agent_id"], owner.userID); n != "1" {
					t.Fatal("agent ownership/default scopes")
				}
			}
			adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "admin"}}}, nil))
			for _, forbidden := range []string{"agent.create", "apikey.create", "confirmation.decide"} {
				r := e.cmd(e.gosv.URL, w, owner.ws, forbidden, map[string]any{"name": "x", "id": command.NilUUID, "approve": true}, nil)
				if r.status != 403 {
					t.Fatalf("machine %s: %d %s", forbidden, r.status, r.body)
				}
			}
			r := adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "apikey.revoke", map[string]any{"id": kid}, nil))
			if r["revoked"] != true {
				t.Fatal(r)
			}
			r = adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "apikey.revoke", map[string]any{"id": kid}, nil))
			if r["revoked"] != false {
				t.Fatal(r)
			}
			for _, target := range []string{e.gosv.URL} {
				if r := e.cmd(target, w, owner.ws, "workspace.get", map[string]any{}, nil); r.status != 401 {
					t.Fatalf("revoked key: %d %s", r.status, r.body)
				}
			}
		}
	}
}

func TestAdminConfirmationSemantics(t *testing.T) {
	confirm := command.NewCommand("test.admin.confirm", command.GroupWriteNotes, func(c *command.Ctx, b json.RawMessage) (any, error) { return json.RawMessage(b), nil })
	confirm.Confirm = true
	e := newEnv(t, confirm)
	owner := e.signup("Confirm", true)
	member := e.addMember(owner, e.signup("Member", false), "member")
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "Requester", "scopes": []string{"*"}})["api_key"].(string)}
	for _, approve := range []bool{true, false} {
		requested := adminOK(t, e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{"approved_input": true}, nil))
		cid := requested["confirmation_id"].(string)
		hdr := map[string]string{"X-Confirmation-Id": cid}
		if r := e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{}, hdr); r.status != 403 {
			t.Fatal("pending confirmation accepted")
		}
		adminOK(t, e.cmd(e.gosv.URL, owner, owner.ws, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes", "admin"}}}, nil))
		if r := e.cmd(e.gosv.URL, agent, owner.ws, "confirmation.decide", map[string]any{"id": cid, "approve": approve}, nil); r.status != 403 {
			t.Fatal("agent confirmed itself")
		}
		out := adminOK(t, e.cmd(e.gosv.URL, member, owner.ws, "confirmation.decide", map[string]any{"id": cid, "approve": approve}, nil))
		status := "rejected"
		if approve {
			status = "confirmed"
		}
		if out["status"] != status {
			t.Fatal(out)
		}
		if n := adminScalar(e, owner.ws, "select count(*)::text from core.confirmations where id=$1::uuid and status=$2 and confirmed_by=$3::uuid and decided_at is not null", cid, status, member.userID); n != "1" {
			t.Fatal("decision not persisted")
		}
		if r := e.cmd(e.gosv.URL, owner, owner.ws, "confirmation.decide", map[string]any{"id": cid, "approve": !approve}, nil); r.status != 404 {
			t.Fatal("decision changed")
		}
		r := e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{"tampered": true}, hdr)
		if approve {
			if r.status != 200 || string(r.body) != `{"approved_input":true}` {
				t.Fatalf("confirmed input: %d %s", r.status, r.body)
			}
		} else if r.status != 403 {
			t.Fatal("rejected confirmation accepted")
		}
		if r := e.cmd(e.gosv.URL, agent, owner.ws, "test.admin.confirm", map[string]any{}, hdr); r.status != 403 {
			t.Fatal("confirmation replayed")
		}
		if n := adminScalar(e, owner.ws, "select count(*)::text from core.events where kind='confirmation.decided' and payload->>'confirmation_id'=$1", cid); n != "1" {
			t.Fatal("decision event missing")
		}
	}
}

func TestAdminUsageWindow(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Usage", true)
	other := e.signup("Other", true)
	adminSQL(e, owner.ws, "insert into core.usage (workspace_id,kind,quantity,created_at) values ($1::uuid,'alpha',2,now()),($1::uuid,'alpha',3,now()-interval '29 days'),($1::uuid,'alpha',100,now()-interval '31 days'),($1::uuid,'beta',7,now())", owner.ws)
	adminSQL(e, other.ws, "insert into core.usage (workspace_id,kind,quantity) values ($1::uuid,'alpha',999)", other.ws)
	g := e.cmd(e.gosv.URL, owner, owner.ws, "usage.summary", map[string]any{}, nil)
	if string(g.body) != `[{"kind":"alpha","quantity":5},{"kind":"beta","quantity":7}]` {
		t.Fatalf("usage window/isolation: %s", g.body)
	}
}
