package server_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// WP23: device session list and revoke, last_seen throttling, recovery-code regeneration.

type sessRow struct {
	ID        string  `json:"id"`
	UserAgent string  `json:"user_agent"`
	IP        string  `json:"ip"`
	AMR       string  `json:"amr"`
	LastSeen  *string `json:"last_seen_at"`
	Current   bool    `json:"current"`
}

func (e *env) sessions(ck string) []sessRow {
	e.t.Helper()
	r := hit(e.t, e.gosv.URL, "GET", "/api/auth/sessions", cookieHdr(ck), "")
	var out struct{ Sessions []sessRow }
	if r.status != 200 || json.Unmarshal(r.body, &out) != nil {
		e.t.Fatalf("sessions: %d %s", r.status, r.body)
	}
	return out.Sessions
}

func (e *env) loginUA(a acct, ua string) string {
	e.t.Helper()
	r := e.login(a, a.pw, map[string]string{"User-Agent": ua})
	if r.status != 200 {
		e.t.Fatalf("login: %d %s", r.status, r.body)
	}
	return sessionOf(e.t, r)
}

func TestSessionListShowsDevicesAndRevokes(t *testing.T) {
	e := sec(t)
	a := e.newAcct("Devices", true)
	laptop := e.loginUA(a, "Mozilla/5.0 (Macintosh) Chrome/130")
	phone := e.loginUA(a, "Mozilla/5.0 (iPhone) Safari/18")
	other := e.newAcct("Someone Else", true)
	otherCk := e.loginUA(other, "OtherBrowser/1")

	rows := e.sessions(laptop)
	if len(rows) != 3 { // signup, laptop, phone
		t.Fatalf("want 3 sessions, got %+v", rows)
	}
	var cur, ph sessRow
	for _, r := range rows {
		switch {
		case r.Current:
			cur = r
		case strings.Contains(r.UserAgent, "iPhone"):
			ph = r
		}
	}
	if !strings.Contains(cur.UserAgent, "Chrome") || !strings.Contains(ph.UserAgent, "iPhone") || cur.IP != a.ip || cur.AMR != "pwd" || cur.LastSeen == nil {
		t.Fatalf("device details: current=%+v other=%+v", cur, ph)
	}
	for _, r := range rows { // the handle is not the credential's hash
		var n int
		_ = e.pool.QueryRow(context.Background(), "select count(*) from core.sessions where id = $1", r.ID).Scan(&n)
		if n != 0 {
			t.Fatal("the public id must not be the session hash")
		}
	}
	if r := hit(t, e.gosv.URL, "GET", "/api/auth/sessions", nil, ""); r.status != 401 {
		t.Fatalf("anonymous: %d", r.status)
	}
	// somebody else's session id, and the current one, cannot be revoked
	if r := e.post("/api/auth/sessions/revoke", a.ip, map[string]any{"id": e.sessions(otherCk)[0].ID}, nil, laptop); r.status != 404 {
		t.Fatalf("another person's session: %d %s", r.status, r.body)
	}
	if r := e.post("/api/auth/sessions/revoke", a.ip, map[string]any{"id": cur.ID}, nil, laptop); r.status != 400 {
		t.Fatalf("the current session: %d %s", r.status, r.body)
	}
	if r := e.post("/api/auth/sessions/revoke", a.ip, map[string]any{"id": "nope"}, nil, laptop); r.status != 404 {
		t.Fatalf("bad id: %d %s", r.status, r.body)
	}
	if r := e.post("/api/auth/sessions/revoke", a.ip, map[string]any{"id": ph.ID}, nil, laptop); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if r := e.run(phone, a.ws, "workspace.get", map[string]any{}); r.status != 401 {
		t.Fatalf("a revoked session must stop working: %d %s", r.status, r.body)
	}
	if len(e.sessions(otherCk)) != 2 {
		t.Fatal("another person's sessions are untouched")
	}
}

func TestRevokeOtherSessionsKeepsTheCurrentOne(t *testing.T) {
	e := sec(t)
	a := e.newAcct("Many", true)
	keep := e.loginUA(a, "A/1")
	var others []string
	for i := 0; i < 3; i++ {
		others = append(others, e.loginUA(a, "B/2"))
	}
	r := e.post("/api/auth/sessions/revoke_others", a.ip, map[string]any{}, nil, keep)
	if r.status != 200 || !strings.Contains(string(r.body), `"revoked":4`) { // the signup session is one of them
		t.Fatalf("%d %s", r.status, r.body)
	}
	if rows := e.sessions(keep); len(rows) != 1 || !rows[0].Current {
		t.Fatalf("%+v", rows)
	}
	for _, o := range others {
		if r := e.run(o, a.ws, "workspace.get", map[string]any{}); r.status != 401 {
			t.Fatalf("revoked: %d", r.status)
		}
	}
	if r := e.run(keep, a.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("current: %d %s", r.status, r.body)
	}
}

func TestLastSeenIsRefreshedAtMostEveryFewMinutes(t *testing.T) {
	e := sec(t)
	a := e.newAcct("Seen", true)
	ck := e.session(a)
	seen := func() time.Time {
		var ts time.Time
		if err := e.pool.QueryRow(context.Background(), "select last_seen_at from core.sessions where id = $1", hex.EncodeToString(sum(ck))).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	e.sql("update core.sessions set last_seen_at = now() - interval '1 hour' where user_id = $1::uuid", a.userID)
	old := seen()
	if r := e.run(ck, a.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fresh := seen()
	if !fresh.After(old.Add(30 * time.Minute)) {
		t.Fatalf("a stale last_seen_at is refreshed: %v -> %v", old, fresh)
	}
	e.run(ck, a.ws, "workspace.get", map[string]any{})
	e.run(ck, a.ws, "workspace.get", map[string]any{})
	if again := seen(); !again.Equal(fresh) {
		t.Fatalf("a recent last_seen_at is left alone: %v -> %v", fresh, again)
	}
}

func TestRecoveryCodesRegenerate(t *testing.T) {
	e := sec(t)
	m := e.enrol(e.newAcct("Regen", true))
	regen := func(pw, code string, cookie string) hres {
		return e.post("/api/auth/mfa/recovery", m.ip, map[string]any{"password": pw, "code": code}, nil, cookie)
	}
	if r := hit(t, e.gosv.URL, "POST", "/api/auth/mfa/recovery", map[string]string{"Content-Type": "application/json", "X-Forwarded-For": m.ip}, jb(map[string]any{"password": m.pw, "code": "000000"})); r.status != 401 {
		t.Fatalf("anonymous: %d", r.status)
	}
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	if r := regen("wrong-password", tcode(m.secret, nowStep()), m.cookie); r.status != 401 {
		t.Fatalf("wrong password: %d %s", r.status, r.body)
	}
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	if r := regen(m.pw, "000000", m.cookie); r.status != 401 {
		t.Fatalf("wrong code: %d %s", r.status, r.body)
	}
	if r := regen(m.pw, m.recovery[0], m.cookie); r.status != 401 { // a recovery code cannot mint more of itself
		t.Fatalf("recovery code as the second factor: %d %s", r.status, r.body)
	}
	e.sql("delete from core.auth_throttle where key = $1", "mfa:"+m.userID)
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	r := regen(m.pw, tcode(m.secret, nowStep()), m.cookie)
	var out struct {
		Codes []string `json:"recovery_codes"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &out) != nil || len(out.Codes) != 10 || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("regenerate: %d %s", r.status, r.body)
	}
	seen := map[string]bool{}
	for _, c := range m.recovery {
		seen[c] = true
	}
	for _, c := range out.Codes {
		if seen[c] {
			t.Fatal("new codes must differ from the old set")
		}
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.mfa_recovery_codes where user_id = $1::uuid", m.userID).Scan(&n)
	if n != 10 {
		t.Fatalf("the old set is replaced, not extended: %d rows", n)
	}
	// old code is dead, a new one works (once)
	if _, v := e.stepUp(m, m.recovery[1], nil); v.status != 401 {
		t.Fatalf("an old recovery code after regeneration: %d %s", v.status, v.body)
	}
	e.sql("delete from core.auth_throttle where key = $1", "mfa:"+m.userID)
	if _, v := e.stepUp(m, out.Codes[0], nil); v.status != 200 {
		t.Fatalf("a new recovery code: %d %s", v.status, v.body)
	}
	// a person without MFA gets nothing
	plain := e.newAcct("NoMFA", true)
	if r := e.post("/api/auth/mfa/recovery", plain.ip, map[string]any{"password": plain.pw, "code": "123456"}, nil, e.session(plain)); r.status != 401 {
		t.Fatalf("no MFA: %d %s", r.status, r.body)
	}
}
