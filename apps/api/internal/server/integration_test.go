package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
)

const unauth = `{"error":{"code":"unauthenticated","message":"sign in required"}}`

func TestAuth(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Alice", true)
	b := e.signup("Bob", true)
	nid := e.note(a, "auth note")
	in := map[string]any{"id": nid}

	t.Run("cookie", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, a, a.ws, "note.get", in, nil)
		if r.status != 200 || r.json()["title"] != "auth note" || r.json()["level"] != "edit" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("native cs_ bearer", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, who{bearer: "cs_" + a.cookie}, a.ws, "note.get", in, nil)
		if r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("api key", func(t *testing.T) {
		k := e.apiKey(a)
		r := e.cmd(e.gosv.URL, k, a.ws, "note.get", in, nil)
		if r.status != 200 || r.json()["title"] != "auth note" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("api key of another workspace is 403", func(t *testing.T) {
		k := e.apiKey(a)
		r := e.cmd(e.gosv.URL, k, b.ws, "space.list", map[string]any{}, nil)
		if r.status != 403 || string(r.body) != `{"error":{"code":"forbidden","message":"API key belongs to another workspace"}}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("unknown api key is 401", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, who{bearer: "cn_deadbeef_nope"}, a.ws, "space.list", map[string]any{}, nil)
		if r.status != 401 || string(r.body) != `{"error":{"code":"unauthenticated","message":"invalid API key or access token"}}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("no auth is 401 with the Rust body", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, who{}, a.ws, "space.list", map[string]any{}, nil)
		if r.status != 401 || string(r.body) != unauth {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("expired or unknown session", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, who{cookie: "nope"}, a.ws, "space.list", map[string]any{}, nil)
		if r.status != 401 || r.json()["error"].(map[string]any)["message"] != "session expired" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("non-member of the workspace is 403", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, b, a.ws, "space.list", map[string]any{}, nil)
		if r.status != 403 || r.json()["error"].(map[string]any)["message"] != "not a member of this workspace" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("OAuth tokens fall back to Rust", func(t *testing.T) {
		g := e.cmd(e.gosv.URL, who{bearer: "cno_whatever"}, a.ws, "space.list", map[string]any{}, nil)
		r := e.cmd(e.rust, who{bearer: "cno_whatever"}, a.ws, "space.list", map[string]any{}, nil)
		if g.status != r.status || string(g.body) != string(r.body) {
			t.Fatalf("go %d %s vs rust %d %s", g.status, g.body, r.status, r.body)
		}
	})
}

func TestTenantIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Alice", true)
	b := e.signup("Bob", true)
	nid := e.note(a, "secret of A")

	// B calls note.get in B's own workspace with A's note id: RLS hides it, same NotFound as Rust.
	g := e.cmd(e.gosv.URL, b, b.ws, "note.get", map[string]any{"id": nid}, nil)
	r := e.cmd(e.rust, b, b.ws, "note.get", map[string]any{"id": nid}, nil)
	if g.status != 404 || string(g.body) != `{"error":{"code":"not_found","message":"note not found"}}` || string(g.body) != string(r.body) {
		t.Fatalf("go %d %s rust %d %s", g.status, g.body, r.status, r.body)
	}
	if h := e.cmd(e.gosv.URL, b, b.ws, "note.history", map[string]any{"id": nid}, nil); h.status != 404 {
		t.Fatalf("history leaked: %d %s", h.status, h.body)
	}
	// ...and space.list only ever shows B's own spaces.
	if strings.Contains(string(e.cmd(e.gosv.URL, b, b.ws, "space.list", map[string]any{}, nil).body), e.space(a)) {
		t.Fatal("A's space visible to B")
	}
}

func TestRestrictedPageHidden(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	m := e.addMember(owner, e.signup("Member", false), "member")
	open := e.note(owner, "open page")
	hidden := e.note(owner, "restricted page")
	e.must(owner, "note.restrict", map[string]any{"note_id": hidden, "restriction": "view"})

	if r := e.cmd(e.gosv.URL, m, m.ws, "note.get", map[string]any{"id": open}, nil); r.status != 200 || r.json()["level"] != "edit" {
		t.Fatalf("open page: %d %s", r.status, r.body)
	}
	for _, c := range []string{"note.get", "note.history"} {
		r := e.cmd(e.gosv.URL, m, m.ws, c, map[string]any{"id": hidden}, nil)
		if r.status != 404 || string(r.body) != `{"error":{"code":"not_found","message":"note not found"}}` {
			t.Fatalf("%s: %d %s", c, r.status, r.body)
		}
	}
	if r := e.cmd(e.gosv.URL, owner, owner.ws, "note.get", map[string]any{"id": hidden}, nil); r.status != 200 {
		t.Fatalf("owner lost access: %d %s", r.status, r.body)
	}
}

func TestGuestSharedPageLevels(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	g := e.addMember(owner, e.signup("Guest", false), "guest")
	editable, viewable, private := e.note(owner, "shared edit"), e.note(owner, "shared view"), e.note(owner, "not shared")
	e.must(owner, "note.share", map[string]any{"note_id": editable, "email": g.email, "level": "edit"})
	e.must(owner, "note.share", map[string]any{"note_id": viewable, "email": g.email, "level": "view"})

	if r := e.cmd(e.gosv.URL, g, g.ws, "note.get", map[string]any{"id": editable}, nil); r.status != 200 || r.json()["level"] != "edit" {
		t.Fatalf("edit share: %d %s", r.status, r.body)
	}
	if r := e.cmd(e.gosv.URL, g, g.ws, "note.get", map[string]any{"id": viewable}, nil); r.status != 200 || r.json()["level"] != "view" {
		t.Fatalf("view share: %d %s", r.status, r.body)
	}
	if r := e.cmd(e.gosv.URL, g, g.ws, "note.get", map[string]any{"id": private}, nil); r.status != 404 {
		t.Fatalf("unshared page: %d %s", r.status, r.body)
	}
	if r := e.cmd(e.gosv.URL, g, g.ws, "space.list", map[string]any{}, nil); string(r.body) != "[]" {
		t.Fatalf("guest without space membership sees spaces: %s", r.body)
	}
}

func TestNoteHistory(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Owner", true)
	m := e.addMember(owner, e.signup("Member", false), "member")
	nid := e.note(owner, "history page")
	e.must(owner, "note.restrict", map[string]any{"note_id": nid, "restriction": "edit"})
	e.must(owner, "note.share", map[string]any{"note_id": nid, "email": m.email, "level": "view"})

	r := e.cmd(e.gosv.URL, owner, owner.ws, "note.history", map[string]any{"id": nid}, nil)
	var h []map[string]any
	var all []map[string]any
	if err := json.Unmarshal(r.body, &all); r.status != 200 || err != nil {
		t.Fatalf("%d %v %s", r.status, err, r.body)
	}
	for _, ev := range all { // the Rust worker adds asynchronous system events (doc.bindings_synced); ignore them
		if ev["actor_type"] == "user" {
			h = append(h, ev)
		}
	}
	if len(h) != 3 {
		t.Fatalf("expected 3 user events: %s", r.body)
	}
	// newest first, with actor and subject names resolved
	if h[0]["kind"] != "note.shared" || h[1]["kind"] != "note.restricted" || h[2]["kind"] != "note.created" {
		t.Fatalf("order: %s", r.body)
	}
	if h[0]["actor_name"] != "Owner" || h[0]["subject_name"] != "Member" || h[0]["actor_type"] != "user" {
		t.Fatalf("names: %v", h[0])
	}
	if h[2]["subject_name"] != nil || h[2]["on_behalf_of"] != nil {
		t.Fatalf("nulls: %v", h[2])
	}
}

// Role, idempotency, confirmation and event emission run through the real command gate
// with test-only commands, since the proving slice has no ported write command yet.
func TestCommandGate(t *testing.T) {
	type in struct {
		Title string `json:"title"`
	}
	write := command.NewCommand("test.write", command.GroupWriteNotes, func(c *command.Ctx, raw json.RawMessage) (any, error) {
		var i in
		_ = json.Unmarshal(raw, &i)
		var space string
		if err := c.Tx.QueryRow(c.Context, "select id::text from core.spaces limit 1").Scan(&space); err != nil {
			return nil, err
		}
		if _, err := c.Emit("test.written", nil, map[string]string{"title": i.Title}); err != nil {
			return nil, err
		}
		return map[string]any{"wrote": i.Title, "when": command.Ts(timeNowMicro())}, nil
	})
	admin := command.NewCommand("test.admin", command.GroupAdmin, func(*command.Ctx, json.RawMessage) (any, error) { return map[string]bool{"ok": true}, nil })
	admin.MinRole = command.Admin
	confirm := command.NewCommand("test.confirm", command.GroupWriteNotes, func(*command.Ctx, json.RawMessage) (any, error) { return map[string]bool{"done": true}, nil })
	confirm.Confirm = true
	e := newEnv(t, write, admin, confirm)
	owner := e.signup("Owner", true)
	m := e.addMember(owner, e.signup("Member", false), "member")
	g := e.addMember(owner, e.signup("Guest", false), "guest")

	events := func(kind string) int {
		var n int
		tx, _ := dbTenant(e, owner.ws)
		defer tx.Rollback(context.Background())
		if err := tx.QueryRow(context.Background(), "select count(*) from core.events where kind = $1", kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("role gate", func(t *testing.T) {
		r := e.cmd(e.gosv.URL, g, owner.ws, "test.write", in{"x"}, nil)
		if r.status != 403 || string(r.body) != `{"error":{"code":"forbidden","message":"test.write requires role member"}}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
		if r := e.cmd(e.gosv.URL, m, owner.ws, "test.admin", map[string]any{}, nil); r.status != 403 {
			t.Fatalf("member ran admin command: %d", r.status)
		}
		if r := e.cmd(e.gosv.URL, owner, owner.ws, "test.admin", map[string]any{}, nil); r.status != 200 {
			t.Fatalf("owner: %d %s", r.status, r.body)
		}
	})
	t.Run("emit writes event and job in the same transaction", func(t *testing.T) {
		before := events("test.written")
		if r := e.cmd(e.gosv.URL, owner, owner.ws, "test.write", in{"one"}, nil); r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		if events("test.written") != before+1 {
			t.Fatal("event not written")
		}
	})
	t.Run("idempotency replays the first result and does not re-run", func(t *testing.T) {
		before := events("test.written")
		h := map[string]string{"Idempotency-Key": "k-" + rnd()}
		first := e.cmd(e.gosv.URL, owner, owner.ws, "test.write", in{"idem"}, h)
		second := e.cmd(e.gosv.URL, owner, owner.ws, "test.write", in{"changed"}, h)
		if first.status != 200 || string(first.body) != string(second.body) || events("test.written") != before+1 {
			t.Fatalf("%s vs %s, events +%d", first.body, second.body, events("test.written")-before)
		}
		r := e.cmd(e.gosv.URL, owner, owner.ws, "test.admin", map[string]any{}, h)
		if r.status != 409 || string(r.body) != `{"error":{"code":"conflict","message":"idempotency key reused for a different command"}}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
		// a different actor with the same key is a different slot
		if r := e.cmd(e.gosv.URL, m, owner.ws, "test.write", in{"m"}, h); r.status != 200 || string(r.body) == string(first.body) {
			t.Fatalf("actor not part of key: %d %s", r.status, r.body)
		}
	})
	t.Run("idempotency rows are readable by Rust's table layout", func(t *testing.T) {
		key := "layout-" + rnd()
		e.cmd(e.gosv.URL, owner, owner.ws, "test.write", in{"l"}, map[string]string{"Idempotency-Key": key})
		tx, _ := dbTenant(e, owner.ws)
		defer tx.Rollback(context.Background())
		var actor string
		if err := tx.QueryRow(context.Background(), "select actor from core.idempotency where key = $1", key).Scan(&actor); err != nil {
			t.Fatal(err)
		}
		if actor != "User:"+owner.userID+":"+command.NilUUID {
			t.Fatalf("actor %q", actor)
		}
	})
	t.Run("agent keys: usage recorded, confirmation requested", func(t *testing.T) {
		agent := e.must(owner, "agent.create", map[string]any{"name": "bot-" + rnd()})
		k := who{ws: owner.ws, bearer: agent["api_key"].(string)}
		if r := e.cmd(e.gosv.URL, k, owner.ws, "test.write", in{"agent"}, nil); r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		r := e.cmd(e.gosv.URL, k, owner.ws, "test.confirm", map[string]any{"a": 1}, nil)
		j := r.json()
		if r.status != 200 || j["confirmation_required"] != true || len(j["confirmation_id"].(string)) != 36 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		bad := e.cmd(e.gosv.URL, k, owner.ws, "test.confirm", map[string]any{"a": 1}, map[string]string{"X-Confirmation-Id": j["confirmation_id"].(string)})
		if bad.status != 403 { // not confirmed by a human yet
			t.Fatalf("unconfirmed id accepted: %d %s", bad.status, bad.body)
		}
		// an admin-group command is off for agents by default
		if r := e.cmd(e.gosv.URL, k, owner.ws, "test.admin", map[string]any{}, nil); r.status != 403 {
			t.Fatalf("agent ran admin: %d %s", r.status, r.body)
		}
	})
}

func TestFallbackToRustIsIdentical(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Alice", true)
	nid := e.note(a, "x")
	cases := []struct {
		name, cmd string
		body      any
		raw       string
	}{
		{"unknown command (not in Go)", "does.not.exist", map[string]any{}, ""},
		{"note.create (not ported)", "note.list", map[string]any{"space_id": e.space(a)}, ""},
		{"missing id", "note.get", map[string]any{}, ""},
		{"bad uuid", "note.get", map[string]any{"id": "nope"}, ""},
		{"wrong type", "note.get", map[string]any{"id": 5}, ""},
		{"invalid json body", "note.get", nil, "{not json"},
		{"array body for space.list", "space.list", nil, "[]"},
		{"simple uuid form", "note.get", map[string]any{"id": strings.ReplaceAll(nid, "-", "")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var g, r resp
			if c.raw != "" {
				g = e.rawJSON(e.gosv.URL, a, c.cmd, c.raw)
				r = e.rawJSON(e.rust, a, c.cmd, c.raw)
			} else {
				g = e.cmd(e.gosv.URL, a, a.ws, c.cmd, c.body, nil)
				r = e.cmd(e.rust, a, a.ws, c.cmd, c.body, nil)
			}
			if g.status != r.status || string(g.body) != string(r.body) {
				t.Fatalf("go %d %s\nrust %d %s", g.status, g.body, r.status, r.body)
			}
		})
	}
}

func TestRateLimit(t *testing.T) {
	t.Setenv("FAIR_USE_BURST", "3")
	t.Setenv("FAIR_USE_PER_SEC", "0.001")
	e := newEnv(t)
	a := e.signup("Rate", true)
	var last resp
	for range 4 {
		last = e.cmd(e.gosv.URL, a, a.ws, "space.list", map[string]any{}, nil)
	}
	if last.status != 429 || string(last.body) != `{"error":{"code":"rate_limited","message":"fair-use rate limit reached, retry shortly"}}` {
		t.Fatalf("%d %s", last.status, last.body)
	}
}
