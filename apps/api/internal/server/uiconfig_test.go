package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
)

// ui_config.*: per-person screen settings. Ownership, limits and the 200-row quota.

type uic struct {
	*env
	owner, member, guest who
}

func newUIC(t *testing.T) *uic {
	e := newEnv(t)
	o := e.signup("Uma", true)
	return &uic{e, o, e.addMember(o, e.signup("Mo", false), "member"), e.addMember(o, e.signup("Gil", false), "guest")}
}

func (u *uic) call(w who, cmd string, in any) resp {
	return u.cmd(u.gosv.URL, w, u.owner.ws, cmd, in, nil)
}

func (u *uic) set(w who, screen string, cfg any) resp {
	return u.call(w, "ui_config.set", map[string]any{"screen": screen, "config": cfg})
}

func (u *uic) get(t *testing.T, w who, screen string) any {
	t.Helper()
	r := u.call(w, "ui_config.get", map[string]any{"screen": screen})
	if r.status != 200 {
		t.Fatalf("get %s: %d %s", screen, r.status, r.body)
	}
	return r.json()["config"]
}

func wantStatus(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && !strings.Contains(string(r.body), `"code":"`+code+`"`)) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.status, r.body)
	}
}

func TestUIConfigRoundTripAndGuests(t *testing.T) {
	u := newUIC(t)
	eq(t, u.get(t, u.owner, "home"), nil)
	cfg := map[string]any{"v": float64(1), "widgets": []any{map[string]any{"id": "a", "type": "kpi.sales", "size": "m"}}}
	r := u.set(u.owner, "home", cfg)
	wantStatus(t, r, 200, "")
	if r.json()["updated_at"] == nil {
		t.Fatal("updated_at missing")
	}
	eq(t, u.get(t, u.owner, "home"), any(cfg))
	// a second set replaces, it does not add a row
	cfg["v"] = float64(2)
	wantStatus(t, u.set(u.owner, "home", cfg), 200, "")
	eq(t, u.get(t, u.owner, "home").(map[string]any)["v"], float64(2))
	eq(t, adminScalar(u.env, u.owner.ws, "select count(*)::text from core.ui_configs"), "1")
	// guests arrange their own screens too
	wantStatus(t, u.set(u.guest, "home", map[string]any{"v": 1}), 200, "")
	eq(t, u.get(t, u.guest, "home"), any(map[string]any{"v": float64(1)}))
	// reset deletes, and resetting nothing is fine
	wantStatus(t, u.call(u.owner, "ui_config.reset", map[string]any{"screen": "home"}), 200, "")
	eq(t, u.get(t, u.owner, "home"), nil)
	wantStatus(t, u.call(u.owner, "ui_config.reset", map[string]any{"screen": "home"}), 200, "")
}

func TestUIConfigIsPerPersonAndPerWorkspace(t *testing.T) {
	u := newUIC(t)
	wantStatus(t, u.set(u.owner, "home", map[string]any{"v": 1, "secret": "owner-only"}), 200, "")
	eq(t, u.get(t, u.member, "home"), nil)
	// someone else's reset and overwrite touch only their own row
	wantStatus(t, u.call(u.member, "ui_config.reset", map[string]any{"screen": "home"}), 200, "")
	wantStatus(t, u.set(u.member, "home", map[string]any{"v": 1, "secret": "member"}), 200, "")
	eq(t, u.get(t, u.owner, "home").(map[string]any)["secret"], "owner-only")
	eq(t, u.get(t, u.member, "home").(map[string]any)["secret"], "member")
	// another workspace never sees it, even for the same screen name
	other := u.signup("Olly", true)
	r := u.cmd(u.gosv.URL, other, other.ws, "ui_config.get", map[string]any{"screen": "home"}, nil)
	wantStatus(t, r, 200, "")
	eq(t, r.json()["config"], nil)
	// ...and a workspace id that is not the caller's is refused outright
	wantStatus(t, u.cmd(u.gosv.URL, other, u.owner.ws, "ui_config.get", map[string]any{"screen": "home"}, nil), 403, "")
	// the row carries the caller's ids
	eq(t, adminScalar(u.env, u.owner.ws, "select count(*)::text from core.ui_configs where user_id = $1::uuid and workspace_id = $2::uuid", u.owner.userID, u.owner.ws), "1")
}

func TestUIConfigRefusesKeysAndAgents(t *testing.T) {
	u := newUIC(t)
	wantStatus(t, u.set(u.owner, "home", map[string]any{"v": 1}), 200, "")
	key := u.apiKey(u.owner)
	for cmd, in := range map[string]any{
		"ui_config.get":   map[string]any{"screen": "home"},
		"ui_config.set":   map[string]any{"screen": "home", "config": map[string]any{"v": 1, "evil": true}},
		"ui_config.reset": map[string]any{"screen": "home"},
	} {
		wantStatus(t, u.call(key, cmd, in), 403, "forbidden")
	}
	// an AI employee carries its person's id but is still not a person
	agent := agentFixture(u.env, u.owner, "read", "write_notes")
	ctx := context.Background()
	reg := command.NewRegistry(modules.Commands()...)
	sys := command.Principal{Kind: command.KindSystem, WorkspaceID: u.owner.ws, Role: command.Owner}
	var p command.Principal
	if err := reg.WithTx(ctx, u.pool, sys, func(c *command.Ctx) (err error) {
		p, err = command.AgentPrincipal(c, agent, u.owner.userID)
		return
	}); err != nil {
		t.Fatal(err)
	}
	if p.Kind != command.KindAgent || p.UserID == nil {
		t.Fatalf("fixture: %+v", p)
	}
	for cmd, in := range map[string]string{"ui_config.get": `{"screen":"home"}`, "ui_config.set": `{"screen":"home","config":{"v":1,"evil":true}}`, "ui_config.reset": `{"screen":"home"}`} {
		_, err := reg.Execute(ctx, u.pool, cmd, json.RawMessage(in), p, command.ExecOptions{})
		if ce, ok := err.(*command.Error); !ok || ce.Code != command.Forbidden || !strings.Contains(ce.Message, "person") {
			t.Fatalf("%s as agent: %v", cmd, err)
		}
	}
	eq(t, u.get(t, u.owner, "home"), any(map[string]any{"v": float64(1)})) // untouched
}

func TestUIConfigLimits(t *testing.T) {
	u := newUIC(t)
	w := u.owner
	nest := func(levels int) any { // levels nested containers, the top object counting as the first
		var v any = "x"
		for i := 0; i < levels; i++ {
			v = map[string]any{"n": v}
		}
		v.(map[string]any)["v"] = 1
		return v
	}
	strs := func(n, size int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = strings.Repeat("a", size)
		}
		return out
	}
	// rate limit is 30 per person, so the table below uses a fresh person per few cases
	for name, tc := range map[string]struct {
		screen string
		cfg    any
		ok     bool
	}{
		"depth 6 ok":         {"s", nest(6), true},
		"depth 7":            {"s", nest(7), false},
		"string 200 ok":      {"s", map[string]any{"v": 1, "s": strings.Repeat("é", 200)}, true},
		"string 201":         {"s", map[string]any{"v": 1, "s": strings.Repeat("é", 201)}, false},
		"key 201":            {"s", map[string]any{"v": 1, strings.Repeat("k", 201): 1}, false},
		"nul in string":      {"s", map[string]any{"v": 1, "s": "a\u0000b"}, false},
		"array 100 ok":       {"s", map[string]any{"v": 1, "a": strs(100, 1)}, true},
		"array 101":          {"s", map[string]any{"v": 1, "a": strs(101, 1)}, false},
		"over 16 KB":         {"s", map[string]any{"v": 1, "a": strs(100, 180), "b": strs(10, 100)}, false},
		"16 KB or less ok":   {"s", map[string]any{"v": 1, "a": strs(80, 190)}, true},
		"not an object":      {"s", []any{1}, false},
		"null":               {"s", nil, false},
		"no v":               {"s", map[string]any{"x": 1}, false},
		"v zero":             {"s", map[string]any{"v": 0}, false},
		"v fraction":         {"s", map[string]any{"v": 1.5}, false},
		"v string":           {"s", map[string]any{"v": "1"}, false},
		"screen empty":       {"", map[string]any{"v": 1}, false},
		"screen upper":       {"Home", map[string]any{"v": 1}, false},
		"screen space":       {"a b", map[string]any{"v": 1}, false},
		"screen 64 ok":       {strings.Repeat("a", 64), map[string]any{"v": 1}, true},
		"screen 65":          {strings.Repeat("a", 65), map[string]any{"v": 1}, false},
		"screen punctuation": {"list:invoices.v-2_x", map[string]any{"v": 1}, true},
		"screen sql":         {"a';--", map[string]any{"v": 1}, false},
	} {
		r := u.set(w, tc.screen, tc.cfg)
		if tc.ok {
			wantStatus(t, r, 200, "")
		} else {
			if r.status != 400 {
				t.Errorf("%s: want 400, got %d %s", name, r.status, r.body)
			}
		}
	}
	// the request body is bounded before it is decoded
	big := strings.Repeat("a", 33<<10)
	wantStatus(t, u.rawJSON(u.gosv.URL, u.member, "ui_config.set", `{"screen":"s","config":{"v":1},"pad":"`+big+`"}`), 400, "invalid")
	wantStatus(t, u.rawJSON(u.gosv.URL, u.member, "ui_config.get", `{"screen":"Bad"}`), 400, "invalid")
	// duplicate keys collapse to the last one, like jsonb
	wantStatus(t, u.rawJSON(u.gosv.URL, u.member, "ui_config.set", `{"screen":"dup","config":{"v":1,"a":1,"a":2}}`), 200, "")
	eq(t, u.get(t, u.member, "dup"), any(map[string]any{"v": float64(1), "a": float64(2)}))
}

func TestUIConfigSetRateLimit(t *testing.T) {
	u := newUIC(t)
	limited := 0
	for i := 0; i < 36; i++ {
		if u.set(u.guest, "rl", map[string]any{"v": 1}).status == 429 {
			limited++
		}
	}
	if limited < 4 || limited > 6 { // burst 30, 1 token a second
		t.Fatalf("limited %d of 36", limited)
	}
	wantStatus(t, u.set(u.owner, "rl", map[string]any{"v": 1}), 200, "") // someone else's bucket
}

func TestUIConfigQuotaHoldsUnderConcurrency(t *testing.T) {
	u := newUIC(t)
	adminSQL(u.env, u.owner.ws, `insert into core.ui_configs (workspace_id, user_id, screen, config)
		select $1::uuid, $2::uuid, 'fill' || g, '{"v":1}' from generate_series(1, 190) g`, u.owner.ws, u.owner.userID)
	var wg sync.WaitGroup
	status := make([]int, 30) // exactly the set burst of 30
	for i := range status {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status[i] = u.set(u.owner, "new"+string(rune('a'+i%26))+string(rune('a'+i/26)), map[string]any{"v": 1}).status
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, s := range status {
		switch s {
		case 200:
			ok++
		case 400:
			refused++
		default:
			t.Fatalf("unexpected status %d", s)
		}
	}
	if ok != 10 || refused != 20 {
		t.Fatalf("ok %d refused %d, want 10 and 20", ok, refused)
	}
	eq(t, adminScalar(u.env, u.owner.ws, "select count(*)::text from core.ui_configs where user_id = $1::uuid", u.owner.userID), "200")
	// at the quota an existing screen can still be changed and a reset frees a slot (other person's bucket)
	wantStatus(t, u.set(u.member, "x", map[string]any{"v": 1}), 200, "")
	adminSQL(u.env, u.owner.ws, "delete from core.ui_configs where user_id = $1::uuid and screen = 'fill1'", u.owner.userID)
	eq(t, adminScalar(u.env, u.owner.ws, "select count(*)::text from core.ui_configs where user_id = $1::uuid", u.owner.userID), "199")
}

func TestUIConfigQuotaAtLimitStillUpdatesAndResets(t *testing.T) {
	u := newUIC(t)
	adminSQL(u.env, u.owner.ws, `insert into core.ui_configs (workspace_id, user_id, screen, config)
		select $1::uuid, $2::uuid, 'fill' || g, '{"v":1}' from generate_series(1, 200) g`, u.owner.ws, u.owner.userID)
	wantStatus(t, u.set(u.owner, "fill7", map[string]any{"v": 2}), 200, "")
	wantStatus(t, u.set(u.owner, "one-too-many", map[string]any{"v": 1}), 400, "invalid")
	wantStatus(t, u.call(u.owner, "ui_config.reset", map[string]any{"screen": "fill7"}), 200, "")
	wantStatus(t, u.set(u.owner, "one-too-many", map[string]any{"v": 1}), 200, "")
}
