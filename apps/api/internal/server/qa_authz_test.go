package server_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
)

// CN-QA-001 section 8 "Matrix" (principals x actions), SEC-007 (revoked guest share), SEC-014 (agent escalation).

type principal struct {
	name string
	w    who
	role command.Role
	bot  bool // API key or agent key: also subject to the workspace's agent tool groups
}

// gateDenied reports whether the response is the command gate refusing the call (role, scope or tool group),
// as opposed to the command running and failing on its own input.
func gateDenied(r resp, cmd string) bool {
	if r.status != 403 {
		return false
	}
	var v struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(r.body, &v)
	m := v.Error.Message
	return v.Error.Code == "forbidden" && (strings.HasPrefix(m, cmd+" requires role ") || strings.HasPrefix(m, "scope ") || strings.HasPrefix(m, "tool group "))
}

// The matrix is generated from the Go registry (name, group, MinRole) and checked against the Rust server, which
// is the authority on what each module declares: a Go command whose gate differs from Rust fails here.
func TestQARoleMatrixFromRegistry(t *testing.T) {
	t.Setenv("FAIR_USE_BURST", "100000") // 127 commands x 7 principals through one process
	e := newEnv(t)
	owner := e.signup("MxOwner", true)
	admin := e.addMember(owner, e.signup("MxAdmin", false), "admin")
	member := e.addMember(owner, e.signup("MxMember", false), "member")
	guest := e.addMember(owner, e.signup("MxGuest", false), "guest")
	keyAdmin := e.addMember(owner, e.signup("MxKeyAdmin", false), "admin")
	botAdmin := e.addMember(owner, e.signup("MxBotAdmin", false), "admin")
	keyMember := e.addMember(owner, e.signup("MxKeyMember", false), "member")
	bearer := func(creator who, cmd string, in map[string]any, field string) who {
		return who{ws: owner.ws, bearer: e.must(creator, cmd, in)[field].(string)}
	}
	ps := []principal{
		{"guest", guest, command.Guest, false},
		{"member", member, command.Member, false},
		{"admin", admin, command.Admin, false},
		{"owner", owner, command.Owner, false},
		{"api key (admin)", bearer(keyAdmin, "apikey.create", map[string]any{"name": "mx"}, "api_key"), command.Admin, true},
		{"api key (member)", bearer(keyMember, "apikey.create", map[string]any{"name": "mx"}, "api_key"), command.Member, true},
		{"agent key", bearer(botAdmin, "agent.create", map[string]any{"name": "mx-bot"}, "api_key"), command.Admin, true},
	}
	agentGroups := map[command.Group]bool{command.GroupRead: true, command.GroupWriteNotes: true, command.GroupWriteItems: true, command.GroupManageCRM: true}

	cmds := modules.Commands()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	if len(cmds) < 100 {
		t.Fatalf("registry unexpectedly small: %d", len(cmds))
	}
	// Least privileged first so a successful owner call cannot change what the others are allowed to see.
	denied, allowed, goOnly := 0, 0, 0
	for _, p := range ps {
		for _, c := range cmds {
			wantDenied := p.role < c.MinRole || p.bot && !agentGroups[c.Group]
			g := e.cmd(e.gosv.URL, p.w, owner.ws, c.Name, map[string]any{}, nil)
			r := e.cmd(e.rust, p.w, owner.ws, c.Name, map[string]any{}, nil)
			label := p.name + " x " + c.Name + " (" + string(c.Group) + ", min " + c.MinRole.String() + ")"
			if gateDenied(g, c.Name) != wantDenied {
				t.Errorf("Go %s: want denied=%v, got %d %s", label, wantDenied, g.status, g.body)
			}
			if r.status == 404 && strings.Contains(string(r.body), "command "+c.Name+" not found") {
				goOnly++ // new in Go (quotations): the running Rust build has no such command, nothing to compare
				continue
			}
			if gateDenied(r, c.Name) != wantDenied {
				t.Errorf("Rust %s: want denied=%v, got %d %s", label, wantDenied, r.status, r.body)
			}
			if wantDenied {
				denied++
				if string(g.body) != string(r.body) || g.status != r.status {
					t.Errorf("%s: denial differs from Rust\n go:   %d %s\n rust: %d %s", label, g.status, g.body, r.status, r.body)
				}
			} else {
				allowed++
			}
		}
	}
	t.Logf("matrix: %d commands x %d principals, %d denials and %d allowed cells; %d cells have no Rust command", len(cmds), len(ps), denied, allowed, goOnly)
	if denied == 0 || allowed == 0 {
		t.Fatal("degenerate matrix")
	}
}

// SEC-007: a revoked guest share takes effect on the very next request, on every read and write path.
func TestQAGuestShareRevocationIsImmediate(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("RvOwner", true)
	guest := e.addMember(owner, e.signup("RvGuest", false), "guest")
	word := "zyx" + rnd()
	note := e.must(owner, "note.create", map[string]any{"space_id": e.space(owner), "title": word, "content_md": word + " body"})["id"].(string)
	e.must(owner, "comment.add", map[string]any{"note_id": note, "body": "c1"})
	reads := []struct {
		cmd string
		in  map[string]any
	}{
		{"note.get", map[string]any{"id": note}},
		{"note.history", map[string]any{"id": note}},
		{"comment.list", map[string]any{"note_id": note}},
		{"note.permissions", map[string]any{"note_id": note}},
		{"note.revisions", map[string]any{"note_id": note}},
	}
	listed := func(base, cmd string, in map[string]any) bool {
		r := e.cmd(base, guest, owner.ws, cmd, in, nil)
		return r.status == 200 && strings.Contains(string(r.body), note)
	}
	for _, level := range []string{"view", "edit"} {
		t.Run(level, func(t *testing.T) {
			e.must(owner, "note.share", map[string]any{"note_id": note, "email": guest.email, "level": level})
			for _, base := range []string{e.gosv.URL, e.rust} {
				if r := e.cmd(base, guest, owner.ws, "note.get", map[string]any{"id": note}, nil); r.status != 200 {
					t.Fatalf("shared guest cannot read: %d %s", r.status, r.body)
				}
				if !listed(base, "search.notes", map[string]any{"q": word}) || !listed(base, "note.shared_with_me", map[string]any{}) {
					t.Fatalf("shared page missing from search/shared_with_me")
				}
			}
			e.must(owner, "note.unshare", map[string]any{"note_id": note, "user_id": guest.userID})
			for _, base := range []string{e.gosv.URL, e.rust} {
				for _, rd := range reads {
					if rd.cmd == "note.permissions" {
						continue // lists who can access; the owner-only view is covered by the matrix
					}
					r := e.cmd(base, guest, owner.ws, rd.cmd, rd.in, nil)
					if r.status != 404 {
						t.Errorf("%s after unshare via %s: want 404, got %d %s", rd.cmd, base, r.status, r.body)
					}
				}
				if listed(base, "search.notes", map[string]any{"q": word}) || listed(base, "note.shared_with_me", map[string]any{}) || listed(base, "note.list", map[string]any{}) {
					t.Errorf("revoked page still listed for the guest via %s", base)
				}
				for cmd, in := range map[string]map[string]any{
					"note.update": {"id": note, "content_md": "pwned"},
					"comment.add": {"note_id": note, "body": "late"},
					"note.delete": {"id": note},
				} {
					if r := e.cmd(base, guest, owner.ws, cmd, in, nil); r.status != 404 && r.status != 403 {
						t.Errorf("%s after unshare via %s: want 403/404, got %d %s", cmd, base, r.status, r.body)
					}
				}
			}
			if r := e.cmd(e.gosv.URL, owner, owner.ws, "note.get", map[string]any{"id": note}, nil); r.status != 200 || strings.Contains(string(r.body), "pwned") {
				t.Fatalf("owner view damaged: %d %s", r.status, r.body)
			}
		})
	}
	t.Run("downgrading edit to view stops writes at once", func(t *testing.T) {
		e.must(owner, "note.share", map[string]any{"note_id": note, "email": guest.email, "level": "edit"})
		if r := e.cmd(e.gosv.URL, guest, owner.ws, "comment.add", map[string]any{"note_id": note, "body": "ok"}, nil); r.status != 200 {
			t.Fatalf("edit guest cannot comment: %d %s", r.status, r.body)
		}
		e.must(owner, "note.share", map[string]any{"note_id": note, "email": guest.email, "level": "view"})
		r := e.cmd(e.gosv.URL, guest, owner.ws, "note.update", map[string]any{"id": note, "content_md": "x"}, nil)
		if r.status != 403 && r.status != 404 {
			t.Fatalf("view-level guest wrote: %d %s", r.status, r.body)
		}
	})
}

// SEC-014: an agent cannot reach admin, destructive or money commands, and cannot widen its own permissions.
func TestQAAgentCannotEscalate(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("EscOwner", true)
	agent := who{ws: owner.ws, bearer: e.must(owner, "agent.create", map[string]any{"name": "esc-bot"})["api_key"].(string)}
	other := e.signup("EscVictim", false)
	for _, c := range []struct {
		cmd string
		in  map[string]any
	}{
		{"member.add", map[string]any{"email": other.email, "role": "owner"}},
		{"apikey.create", map[string]any{"name": "mine"}},
		{"agent.create", map[string]any{"name": "clone"}},
		{"workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"admin", "destructive", "manage_invoices", "send_messages"}}}},
		{"confirmation.decide", map[string]any{"id": command.NilUUID, "approve": true}},
		{"note.delete", map[string]any{"id": command.NilUUID}},
		{"invoice.issue", map[string]any{"id": command.NilUUID}},
	} {
		for _, base := range []string{e.gosv.URL, e.rust} {
			r := e.cmd(base, agent, owner.ws, c.cmd, c.in, nil)
			if !gateDenied(r, c.cmd) {
				t.Errorf("agent ran %s via %s: %d %s", c.cmd, base, r.status, r.body)
			}
		}
	}
	// the workspace settings are unchanged
	ws := e.must(owner, "workspace.get", map[string]any{})
	if strings.Contains(mustJSON(ws), "destructive") {
		t.Fatalf("agent widened its tool groups: %v", ws)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
