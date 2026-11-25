package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Email invitations (docs/design/onboarding.md 6.6).

type inviteEnv struct {
	*env
	owner who
}

func newInv(t *testing.T) *inviteEnv {
	e := newEnv(t)
	return &inviteEnv{e, e.signup("Olive Owner", true)}
}

var ipN int
var ipMu sync.Mutex

// invIP gives each public request its own client address, so the per-address limit never couples tests.
func invIP() map[string]string {
	ipMu.Lock()
	defer ipMu.Unlock()
	ipN++
	return map[string]string{"X-Forwarded-For": fmt.Sprintf("10.%d.%d.%d", ipN>>16&255, ipN>>8&255, ipN&255)}
}

func (e *env) sqlRows(ws, q string, args ...any) [][]string {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(context.Background(), q, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		vs, _ := rows.Values()
		var r []string
		for _, v := range vs {
			r = append(r, fmt.Sprint(v))
		}
		out = append(out, r)
	}
	return out
}

func (e *env) sqlExec(ws, q string, args ...any) {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *inviteEnv) invite(w who, role string, emails ...string) resp {
	return e.cmd(e.api, w, e.owner.ws, "member.invite", map[string]any{"emails": emails, "role": role}, nil)
}

func (e *inviteEnv) newEmail() string { return "inv-" + rnd() + "@example.test" }

// link issues an invitation (mail off) and returns its token.
func (e *inviteEnv) link(role, email string) string {
	e.t.Helper()
	r := e.invite(e.owner, role, email)
	if r.status != 200 {
		e.t.Fatalf("invite: %d %s", r.status, r.body)
	}
	links, _ := r.json()["links"].([]any)
	if len(links) != 1 {
		e.t.Fatalf("want one link: %s", r.body)
	}
	p := links[0].(map[string]any)["path"].(string)
	return strings.TrimPrefix(p, "/invite/")
}

func (e *inviteEnv) peek(tok string, w who) resp {
	return e.do(e.api, "GET", "/api/invite/"+tok, w, nil, invIP())
}

func (e *inviteEnv) accept(tok string, w who, body any) resp {
	return e.do(e.api, "POST", "/api/invite/"+tok+"/accept", w, body, invIP())
}

func state(r resp) string { s, _ := r.json()["state"].(string); return s }

func sessionFor(t *testing.T, e *env, userID string) who {
	tok := rnd() + rnd()
	h := sha256.Sum256([]byte(tok))
	if _, err := e.pool.Exec(context.Background(), "insert into core.sessions (id, user_id, expires_at) values ($1, $2::uuid, now() + interval '1 day')", hex.EncodeToString(h[:]), userID); err != nil {
		t.Fatal(err)
	}
	return who{cookie: tok, userID: userID}
}

func (e *inviteEnv) invRows() [][]string {
	return e.sqlRows(e.owner.ws, "select email, role, (accepted_at is not null)::text, (revoked_at is not null)::text from core.invitations order by created_at")
}

func TestInviteNewPersonSetsPasswordAndLandsInWorkspace(t *testing.T) {
	e := newInv(t)
	email := e.newEmail()
	r := e.invite(e.owner, "member", "  "+strings.ToUpper(email)+" ")
	eq(t, r.status, 200)
	eq(t, r.json()["mailed"], false)
	tok := strings.TrimPrefix(r.json()["links"].([]any)[0].(map[string]any)["path"].(string), "/invite/")
	if len(tok) != 43 {
		t.Fatalf("token %q", tok)
	}
	// only the hash is kept
	h := sha256.Sum256(invB64(t, tok))
	eq(t, e.sqlRows(e.owner.ws, "select email, token_hash, (token_enc is null)::text from core.invitations"), [][]string{{email, hex.EncodeToString(h[:]), "true"}})
	// no user and no membership yet
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.users where email = $1", email), [][]string{{"0"}})

	p := e.peek(tok, who{})
	eq(t, p.status, 200)
	j := p.json()
	eq(t, []any{j["state"], j["account"], j["workspace_name"], j["inviter_name"], j["email"], j["role"]}, []any{"ok", "new", "Olive Owner WS", "Olive Owner", email, "member"})
	for _, bad := range []map[string]any{{}, {"password": "short"}, {"password": "short", "name": "N"}} {
		if a := e.accept(tok, who{}, bad); a.status != 400 {
			t.Fatalf("%v: %d %s", bad, a.status, a.body)
		}
	}
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.users where email = $1", email), [][]string{{"0"}}) // refused tries consumed nothing
	pw := rnd() + rnd()
	a := e.accept(tok, who{}, map[string]any{"name": "Nia New", "password": pw})
	eq(t, a.status, 200)
	eq(t, state(a), "joined")
	eq(t, a.json()["workspace_id"], e.owner.ws)
	// the new account is verified, has the password and a session that reaches the workspace
	eq(t, e.sqlRows(e.owner.ws, "select name, (email_verified_at is not null)::text, (password_hash is not null)::text from core.users where email = $1", email), [][]string{{"Nia New", "true", "true"}})
	login := e.do(e.api, "POST", "/api/auth/login", who{}, map[string]any{"email": email, "password": pw}, invIP())
	eq(t, login.status, 200)
	eq(t, e.sqlRows(e.owner.ws, "select m.role from core.memberships m join core.users u on u.id = m.user_id where u.email = $1", email), [][]string{{"member"}})
	eq(t, e.invRows(), [][]string{{email, "member", "true", "false"}})
	// the setup guide counts this accepted, active teammate
	eq(t, teamDone(t, e), true)
	// replay: the link is spent
	eq(t, state(e.accept(tok, who{}, map[string]any{"password": pw})), "invalid")
	eq(t, state(e.peek(tok, who{})), "invalid")
}

func invB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64url(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func teamDone(t *testing.T, e *inviteEnv) bool {
	t.Helper()
	r := e.cmd(e.api, e.owner, e.owner.ws, "setup.status", map[string]any{}, nil)
	if r.status != 200 {
		t.Fatalf("setup.status %d %s", r.status, r.body)
	}
	for _, x := range jsonArr(r.body) {
		if m := x.(map[string]any); m["key"] == "team" {
			return m["done"] == true
		}
	}
	t.Fatalf("no team row: %s", r.body)
	return false
}

func TestInviteExistingPasswordUserMustSignInAndKeepsAccount(t *testing.T) {
	e := newInv(t)
	u := e.signup("Eve Existing", false)
	before := e.sqlRows(e.owner.ws, "select password_hash from core.users where id = $1::uuid", u.userID)
	tok := e.link("member", u.email)
	eq(t, e.peek(tok, who{}).json()["account"], "existing")
	// not signed in, signed in as someone else, or trying to set a password: all refused, and nothing is consumed
	other := e.signup("Other", false)
	for _, c := range []struct {
		w    who
		body any
	}{{who{}, nil}, {who{}, map[string]any{"password": "attacker-chosen-pw", "name": "x"}}, {other, nil}, {other, map[string]any{"password": "attacker-chosen-pw"}}} {
		a := e.accept(tok, c.w, c.body)
		if a.status != 401 || state(a) != "signin" {
			t.Fatalf("%d %s", a.status, a.body)
		}
	}
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.memberships where user_id = $1::uuid", u.userID), [][]string{{"0"}})
	eq(t, state(e.peek(tok, u)), "ok")
	sessions := e.sqlRows(e.owner.ws, "select count(*)::text from core.sessions where user_id = $1::uuid", u.userID)
	a := e.accept(tok, u, map[string]any{"password": "attacker-chosen-pw"}) // a password in the body is ignored for an existing account
	eq(t, a.status, 200)
	eq(t, state(a), "joined")
	eq(t, e.sqlRows(e.owner.ws, "select password_hash from core.users where id = $1::uuid", u.userID), before)
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.sessions where user_id = $1::uuid", u.userID), sessions)
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.user_mfa where user_id = $1::uuid", u.userID), [][]string{{"0"}})
	eq(t, e.sqlRows(e.owner.ws, "select role from core.memberships where user_id = $1::uuid", u.userID), [][]string{{"member"}})
	eq(t, state(e.accept(tok, u, nil)), "invalid")
}

func TestInviteSSOUserWithNoPasswordClaimsOnlyFromTheirOwnSession(t *testing.T) {
	e := newInv(t)
	email := e.newEmail()
	var uid string
	if err := e.pool.QueryRow(context.Background(), "insert into core.users (email, name) values ($1, 'Sam SSO') returning id::text", email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	tok := e.link("guest", email)
	// nobody can set a password through the link
	a := e.accept(tok, who{}, map[string]any{"password": "attacker-chosen-pw"})
	eq(t, a.status, 401)
	eq(t, e.sqlRows(e.owner.ws, "select (password_hash is null)::text from core.users where id = $1::uuid", uid), [][]string{{"true"}})
	sso := sessionFor(t, e.env, uid)
	a = e.accept(tok, sso, nil)
	eq(t, a.status, 200)
	eq(t, e.sqlRows(e.owner.ws, "select m.role, (u.password_hash is null)::text from core.memberships m join core.users u on u.id = m.user_id where u.id = $1::uuid", uid), [][]string{{"guest", "true"}})
}

func TestInviteExpiredRevokedResentReplayed(t *testing.T) {
	e := newInv(t)
	email := e.newEmail()
	tok := e.link("member", email)
	eq(t, len(e.listInvites()), 1)
	// expired
	e.sqlExec(e.owner.ws, "update core.invitations set expires_at = now() - interval '1 minute'")
	eq(t, state(e.peek(tok, who{})), "expired")
	eq(t, state(e.accept(tok, who{}, map[string]any{"password": "longenough1"})), "expired")
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.users where email = $1", email), [][]string{{"0"}})
	// resend renews an expired invitation with a new token; the old link stays dead
	id := e.listInvites()[0]["id"].(string)
	eq(t, e.listInvites()[0]["expired"], true)
	r := e.cmd(e.api, e.owner, e.owner.ws, "member.invite_resend", map[string]any{"id": id}, nil)
	eq(t, r.status, 200)
	tok2 := strings.TrimPrefix(r.json()["links"].([]any)[0].(map[string]any)["path"].(string), "/invite/")
	if tok2 == tok {
		t.Fatal("resend must rotate the token")
	}
	eq(t, state(e.peek(tok, who{})), "invalid")
	eq(t, state(e.peek(tok2, who{})), "ok")
	eq(t, e.sqlRows(e.owner.ws, "select (expires_at > now() + interval '6 days 23 hours')::text from core.invitations"), [][]string{{"true"}})
	// revoked
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.invite_revoke", map[string]any{"id": id}, nil).status, 200)
	eq(t, state(e.peek(tok2, who{})), "invalid")
	eq(t, state(e.accept(tok2, who{}, map[string]any{"password": "longenough1"})), "invalid")
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.invite_revoke", map[string]any{"id": id}, nil).status, 404)
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.invite_resend", map[string]any{"id": id}, nil).status, 404)
	eq(t, len(e.listInvites()), 0)
	// the same address can be invited again after a revoke
	tok3 := e.link("member", email)
	eq(t, state(e.peek(tok3, who{})), "ok")
	// unknown and malformed tokens read like a revoked one
	want := e.peek(tok2, who{})
	for _, bad := range []string{"x", strings.Repeat("A", 43), strings.Repeat("A", 44), tok3[:42] + "!"} {
		g := e.peek(bad, who{})
		eq(t, [2]any{g.status, string(g.body)}, [2]any{want.status, string(want.body)})
	}
}

func (e *inviteEnv) listInvites() []map[string]any {
	e.t.Helper()
	r := e.cmd(e.api, e.owner, e.owner.ws, "member.invite_list", map[string]any{}, nil)
	if r.status != 200 {
		e.t.Fatalf("list %d %s", r.status, r.body)
	}
	var out []map[string]any
	for _, x := range jsonArr(r.body) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestInviteConcurrentAcceptCreatesOneAccount(t *testing.T) {
	e := newInv(t)
	email := e.newEmail()
	tok := e.link("admin", email)
	var wg sync.WaitGroup
	res := make([]resp, 8)
	for i := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i] = e.accept(tok, who{}, map[string]any{"password": rnd() + rnd(), "name": "Racer"})
		}()
	}
	wg.Wait()
	joined := 0
	for _, r := range res {
		switch state(r) {
		case "joined":
			joined++
		case "invalid":
		default:
			t.Fatalf("%d %s", r.status, r.body)
		}
	}
	eq(t, joined, 1)
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.users where email = $1", email), [][]string{{"1"}})
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.memberships m join core.users u on u.id = m.user_id where u.email = $1", email), [][]string{{"1"}})
}

func TestInviteAlreadyMemberAndDeactivatedChangeNothing(t *testing.T) {
	e := newInv(t)
	m := e.addMember(e.owner, e.signup("Mia", false), "guest")
	// issuing for a member: no invitation, and the same answer as for anyone else
	r := e.invite(e.owner, "admin", m.email)
	eq(t, r.status, 200)
	eq(t, len(e.invRows()), 0)
	// invited first, then added another way: the link says so and changes nothing
	u := e.signup("Una", false)
	tok := e.link("admin", u.email)
	e.addMember(e.owner, u, "member")
	p := e.peek(tok, u)
	eq(t, state(p), "member")
	a := e.accept(tok, u, nil)
	eq(t, state(a), "member")
	eq(t, e.sqlRows(e.owner.ws, "select role from core.memberships where user_id = $1::uuid", u.userID), [][]string{{"member"}})
	eq(t, e.invRows()[0][2], "false") // not consumed
	// deactivated
	e.must(e.owner, "member.deactivate", map[string]any{"user_id": u.userID})
	eq(t, state(e.peek(tok, u)), "deactivated")
	a = e.accept(tok, u, nil)
	eq(t, state(a), "deactivated")
	eq(t, e.sqlRows(e.owner.ws, "select role, (deactivated_at is not null)::text from core.memberships where user_id = $1::uuid", u.userID), [][]string{{"member", "true"}})
	// and inviting a deactivated person creates nothing
	e.invite(e.owner, "member", u.email)
	eq(t, len(e.invRows()), 1)
}

func TestInviteRoleRules(t *testing.T) {
	e := newInv(t)
	admin := e.addMember(e.owner, e.signup("Ada", false), "admin")
	member := e.addMember(e.owner, e.signup("Max", false), "member")
	guest := e.addMember(e.owner, e.signup("Gus", false), "guest")
	eq(t, e.invite(admin, "owner", e.newEmail()).status, 403) // only owners invite owners
	eq(t, e.invite(admin, "admin", e.newEmail()).status, 200)
	eq(t, e.invite(e.owner, "owner", e.newEmail()).status, 200)
	for _, w := range []who{member, guest} {
		eq(t, e.invite(w, "guest", e.newEmail()).status, 403)
		eq(t, e.cmd(e.api, w, e.owner.ws, "member.invite_list", map[string]any{}, nil).status, 403)
	}
	eq(t, e.invite(who{}, "guest", e.newEmail()).status, 401)
	eq(t, e.invite(e.owner, "superuser", e.newEmail()).status, 400)
	eq(t, e.invite(e.owner, "member").status, 400)
	// an admin cannot re-issue, resend or revoke an owner invitation, nor lower it by inviting the address again
	oe := e.newEmail()
	e.link("owner", oe)
	var oid string
	for _, x := range e.listInvites() {
		if x["email"] == oe {
			oid = x["id"].(string)
		}
	}
	eq(t, e.invite(admin, "member", oe).status, 403)
	eq(t, e.cmd(e.api, admin, e.owner.ws, "member.invite_resend", map[string]any{"id": oid}, nil).status, 403)
	eq(t, e.cmd(e.api, admin, e.owner.ws, "member.invite_revoke", map[string]any{"id": oid}, nil).status, 403)
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.invite_resend", map[string]any{"id": oid}, nil).status, 200)
	// the role comes from the stored invitation: nothing in the accept request can raise it
	tok := e.link("guest", e.newEmail())
	a := e.accept(tok, who{}, map[string]any{"password": "longenough1", "role": "owner"})
	eq(t, a.status, 200)
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.memberships where role = 'guest'"), [][]string{{"2"}})
	// another workspace's admin cannot touch these
	o2 := e.signup("Other Owner", true)
	eq(t, e.cmd(e.api, o2, o2.ws, "member.invite_revoke", map[string]any{"id": oid}, nil).status, 404)
	eq(t, e.cmd(e.api, o2, o2.ws, "member.invite_resend", map[string]any{"id": oid}, nil).status, 404)
	r := e.cmd(e.api, o2, o2.ws, "member.invite_list", map[string]any{}, nil)
	eq(t, string(r.body), "[]")
}

func TestInviteResponsesDoNotRevealAccounts(t *testing.T) {
	e := newInv(t)
	startMail(t, e.env)
	member := e.addMember(e.owner, e.signup("Mem", false), "member")
	gone := e.addMember(e.owner, e.signup("Gone", false), "member")
	e.must(e.owner, "member.deactivate", map[string]any{"user_id": gone.userID})
	stranger := e.signup("Stranger", false) // has an account elsewhere
	var first resp
	for i, email := range []string{member.email, gone.email, stranger.email, e.newEmail()} {
		r := e.invite(e.owner, "member", email)
		if i == 0 {
			first = r
		}
		eq(t, [2]any{r.status, string(r.body)}, [2]any{first.status, string(first.body)}) // identical: mail is on, so no link is returned
	}
	eq(t, first.json()["mailed"], true)
	eq(t, len(first.json()["links"].([]any)), 0)
	// the invitation page: unknown, malformed, revoked and replayed links answer alike
	eq(t, e.peek("nope", who{}).status, 200)
}

func TestInviteTokenNeverInEventsLogsOrJobs(t *testing.T) {
	var buf lockedBuf
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	e := newInv(t)
	email := e.newEmail()
	tok := e.link("member", email)
	tok2 := ""
	e.peek(tok, who{})
	e.accept(tok, who{}, map[string]any{"password": "short"})
	id := e.listInvites()[0]["id"].(string)
	r := e.cmd(e.api, e.owner, e.owner.ws, "member.invite_resend", map[string]any{"id": id}, nil)
	tok2 = strings.TrimPrefix(r.json()["links"].([]any)[0].(map[string]any)["path"].(string), "/invite/")
	e.accept(tok, who{}, map[string]any{"password": "longenough1"}) // stale link
	e.accept(tok2, who{}, map[string]any{"password": "longenough1", "name": "T"})
	e.peek(tok2, who{})
	e.accept("not-a-token", who{}, nil)
	for _, q := range []string{"select payload::text from core.events", "select payload::text from core.jobs", "select to_jsonb(i)::text from core.invitations i", "select coalesce(result::text,'') from core.idempotency"} {
		for _, row := range e.sqlRows(e.owner.ws, q) {
			for _, tk := range []string{tok, tok2} {
				if strings.Contains(row[0], tk) {
					t.Fatalf("token stored by %q", q)
				}
			}
		}
	}
	for _, tk := range []string{tok, tok2} {
		if strings.Contains(buf.String(), tk) {
			t.Fatal("token in the log")
		}
	}
	if kinds := e.sqlRows(e.owner.ws, "select kind from core.events where kind like 'member.%' order by id"); fmt.Sprint(kinds) != "[[member.invited] [member.invite_resent] [member.added]]" {
		t.Fatal(kinds)
	}
	// every answer of the invitation endpoints carries the no-referrer policy
	req, _ := http.NewRequest("GET", e.api+"/api/invite/"+tok2, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.Header.Get("Referrer-Policy"), "no-referrer")
	eq(t, res.Header.Get("Cache-Control"), "no-store")
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func startMail(t *testing.T, e *env) *smtpSink {
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "invites@example.test")
	t.Setenv("PUBLIC_WEB_URL", "https://app.example.test")
	return sink
}

var softBreak = regexp.MustCompile(`=\r?\n`)

func mailToken(t *testing.T, m string) string {
	t.Helper()
	m = strings.ReplaceAll(softBreak.ReplaceAllString(m, ""), "=3D", "=")
	g := regexp.MustCompile(`https://app\.example\.test/invite/([A-Za-z0-9_-]{43})`).FindStringSubmatch(m)
	if g == nil {
		t.Fatalf("no link in %q", m)
	}
	return g[1]
}

func TestInviteMailHoldsOnlyNamesAndLinkAndResendRotates(t *testing.T) {
	e := newInv(t)
	sink := startMail(t, e.env)
	email := e.newEmail()
	r := e.invite(e.owner, "member", email)
	eq(t, r.status, 200)
	eq(t, r.json()["mailed"], true)
	eq(t, len(r.json()["links"].([]any)), 0) // nothing to copy: the mail carries it
	m := waitMail(t, sink, email)
	for _, w := range []string{"To: " + email, "Olive Owner invited you to Olive Owner WS on C Note"} {
		if !strings.Contains(m, w) {
			t.Fatalf("%q missing in %q", w, m)
		}
	}
	tok := mailToken(t, m)
	eq(t, state(e.peek(tok, who{})), "ok")
	// the outbox job held references only, and the sealed token is gone once sent
	eq(t, e.sqlRows(e.owner.ws, "select count(*)::text from core.jobs where kind = 'invite.mail' and (payload::text like '%' || $1 || '%' or payload::text like '%' || $2 || '%')", tok, email), [][]string{{"0"}})
	waitCond(t, func() bool {
		return e.sqlRows(e.owner.ws, "select (token_enc is null)::text from core.invitations")[0][0] == "true"
	})
	// resend: a second mail, a new token, the first stops working
	id := e.listInvites()[0]["id"].(string)
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.invite_resend", map[string]any{"id": id}, nil).status, 200)
	waitCond(t, func() bool { return countMail(sink, email) == 2 })
	sink.mu.Lock()
	tok2 := mailToken(t, sink.msgs[len(sink.msgs)-1])
	sink.mu.Unlock()
	if tok2 == tok {
		t.Fatal("same token")
	}
	eq(t, state(e.peek(tok, who{})), "invalid")
	a := e.accept(tok2, who{}, map[string]any{"password": "longenough1"})
	eq(t, state(a), "joined")
}

func waitCond(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f() {
			return
		}
		sleep100()
	}
	t.Fatal("condition not reached")
}

func TestInviteRateLimits(t *testing.T) {
	t.Setenv("INVITE_PER_INVITER_HOUR", "3")
	t.Setenv("INVITE_PER_WORKSPACE_DAY", "5")
	e := newInv(t)
	admin := e.addMember(e.owner, e.signup("Ada", false), "admin")
	for i := 0; i < 3; i++ {
		eq(t, e.invite(e.owner, "member", e.newEmail()).status, 200)
	}
	r := e.invite(e.owner, "member", e.newEmail())
	eq(t, r.status, 429)
	eq(t, r.json()["error"].(map[string]any)["code"], "rate_limited")
	eq(t, len(e.invRows()), 3) // the refused one left nothing behind
	// resends count as issues too
	id := e.listInvites()[0]["id"].(string)
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.invite_resend", map[string]any{"id": id}, nil).status, 429)
	// another inviter has their own allowance, but the workspace's allowance is shared
	eq(t, e.invite(admin, "member", e.newEmail()).status, 200)
	eq(t, e.invite(admin, "member", e.newEmail()).status, 200)
	eq(t, e.invite(admin, "member", e.newEmail()).status, 429)
	// a batch is limited per address
	t.Setenv("INVITE_PER_INVITER_HOUR", "1000")
	eq(t, e.invite(e.owner, "member", e.newEmail(), e.newEmail()).status, 429)
	// many tries from one address stop
	t.Setenv("INVITE_TRIES_PER_IP_HOUR", "3")
	h := map[string]string{"X-Forwarded-For": "203.0.113." + fmt.Sprint(len(rnd()))}
	codes := []int{}
	for i := 0; i < 5; i++ {
		codes = append(codes, e.do(e.api, "GET", "/api/invite/"+strings.Repeat("A", 43), who{}, nil, h).status)
	}
	if codes[3] != 429 || codes[4] != 429 {
		t.Fatal(codes)
	}
}

func TestMemberAddOnlyCreatesPasswordlessAccountsWithSSO(t *testing.T) {
	e := newInv(t)
	email := e.newEmail()
	r := e.cmd(e.api, e.owner, e.owner.ws, "member.add", map[string]any{"email": email, "role": "member"}, nil)
	eq(t, r.status, 400)
	e.sqlExec(e.owner.ws, "insert into core.workspace_security (workspace_id, sso_enabled) values ($1::uuid, true) on conflict (workspace_id) do update set sso_enabled = true", e.owner.ws)
	eq(t, e.cmd(e.api, e.owner, e.owner.ws, "member.add", map[string]any{"email": email, "role": "member"}, nil).status, 200)
	eq(t, e.sqlRows(e.owner.ws, "select (password_hash is null)::text from core.users where email = $1", email), [][]string{{"true"}})
}

func TestSetupGuideTeamRowCountsAcceptedActiveMembersOtherThanOwner(t *testing.T) {
	e := newInv(t)
	eq(t, teamDone(t, e), false)
	tok := e.link("admin", e.newEmail())
	eq(t, teamDone(t, e), false) // pending does not count
	a := e.accept(tok, who{}, map[string]any{"password": "longenough1"})
	eq(t, state(a), "joined")
	eq(t, teamDone(t, e), true)
	uid := e.sqlRows(e.owner.ws, "select user_id::text from core.memberships where role = 'admin'")[0][0]
	e.must(e.owner, "member.deactivate", map[string]any{"user_id": uid})
	eq(t, teamDone(t, e), false) // deactivated again
	// a member added without an invitation is not "invited"
	e.addMember(e.owner, e.signup("Zed", false), "member")
	eq(t, teamDone(t, e), false)
}
