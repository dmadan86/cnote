package server_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238, written out here on purpose: an implementation independent of the server's
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/db"
)

// WP18: login throttling, MFA (TOTP), workspace MFA policy. Accounts are created on the Go server; every "client" gets a
// private fake IP (PUBLIC_TRUST_PROXY=1 + X-Forwarded-For) so one test's lock-out never touches another run's logins.

type acct struct {
	email, pw, userID, ws, ip string
}

func sec(t *testing.T) *env {
	t.Helper()
	t.Setenv("PUBLIC_TRUST_PROXY", "1")
	return newEnv(t)
}

func fakeIP() string {
	return fmt.Sprintf("10.%d.%d.%d", time.Now().UnixNano()%250+1, time.Now().UnixNano()/7%250+1, time.Now().UnixNano()/13%250+1)
}

func (e *env) post(path, ip string, body any, hdr map[string]string, cookie string) hres {
	e.t.Helper()
	h := map[string]string{"Content-Type": "application/json", "X-Forwarded-For": ip}
	if cookie != "" {
		h["Cookie"] = "cn_session=" + cookie
	}
	for k, v := range hdr {
		h[k] = v
	}
	return hit(e.t, e.gosv.URL, "POST", path, h, jb(body))
}

func (e *env) newAcct(name string, ws bool) acct {
	e.t.Helper()
	a := acct{email: "sec-" + rnd() + "@example.test", pw: rnd() + rnd(), ip: fakeIP()}
	body := map[string]any{"email": a.email, "name": name, "password": a.pw}
	if ws {
		body["workspace_name"] = name + " WS"
	}
	r := e.post("/api/auth/signup", a.ip, body, nil, "")
	if r.status != 200 {
		e.t.Fatalf("signup: %d %s", r.status, r.body)
	}
	var out struct {
		UserID      string  `json:"user_id"`
		WorkspaceID *string `json:"workspace_id"`
	}
	_ = json.Unmarshal(r.body, &out)
	a.userID = out.UserID
	if out.WorkspaceID != nil {
		a.ws = *out.WorkspaceID
	}
	return a
}

func (e *env) login(a acct, pw string, hdr map[string]string) hres {
	e.t.Helper()
	return e.post("/api/auth/login", a.ip, map[string]any{"email": a.email, "password": pw}, hdr, "")
}

// session logs in and returns the cookie (the account must have no MFA).
func (e *env) session(a acct) string {
	e.t.Helper()
	r := e.login(a, a.pw, nil)
	if r.status != 200 {
		e.t.Fatalf("login: %d %s", r.status, r.body)
	}
	return sessionOf(e.t, r)
}

func (e *env) run(cookie, ws, name string, in any) resp {
	e.t.Helper()
	return e.cmd(e.gosv.URL, who{cookie: cookie}, ws, name, in, nil)
}

func (e *env) events(ws, kind string) int {
	e.t.Helper()
	tx, err := db.BeginTenant(context.Background(), e.pool, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var n int
	if err := tx.QueryRow(context.Background(), "select count(*) from core.events where kind = $1", kind).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) sql(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func acctKey(email string) string {
	h := sha256.Sum256([]byte(email))
	return "acct:" + hex.EncodeToString(h[:])
}

func lockSecs(e *env, key string) float64 {
	e.t.Helper()
	var s float64
	if err := e.pool.QueryRow(context.Background(), "select coalesce(extract(epoch from locked_until - now()), 0)::float8 from core.auth_throttle where key = $1", key).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func errBody(r hres) string {
	var o struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(r.body, &o)
	return o.Error.Code + ": " + o.Error.Message
}

func TestLoginBackoffIsPerAccountExponentialAndAudited(t *testing.T) {
	e := sec(t)
	a := e.newAcct("Throttled", true)
	for i := 0; i < 5; i++ {
		r := e.login(a, "wrong-"+rnd(), nil)
		if r.status != 401 || errBody(r) != "unauthenticated: invalid email or password" {
			t.Fatalf("attempt %d: %d %s", i, r.status, r.body)
		}
	}
	// five failures lock the account: even the right password is refused now, with the same words for everyone
	r := e.login(a, a.pw, nil)
	if r.status != 429 || errBody(r) != "rate_limited: too many attempts, try again later" || r.header.Get("Retry-After") == "" {
		t.Fatalf("locked: %d %s retry=%q", r.status, r.body, r.header.Get("Retry-After"))
	}
	key := acctKey(a.email)
	if s := lockSecs(e, key); s < 25 || s > 31 {
		t.Fatalf("first lock should be about 30s, got %.1f", s)
	}
	if e.events(a.ws, "auth.locked_out") != 1 {
		t.Fatal("lock-out must reach the workspace audit trail")
	}
	// back-off doubles on each further failure once the lock has run out
	for i, want := range []float64{60, 120, 240} {
		e.sql("update core.auth_throttle set locked_until = now() - interval '1 second' where key = $1", key)
		if r := e.login(a, "wrong-"+rnd(), nil); r.status != 401 {
			t.Fatalf("after expiry the attempt is judged again: %d", r.status)
		}
		if s := lockSecs(e, key); s < want-6 || s > want+1 {
			t.Fatalf("lock %d should be about %.0fs, got %.1f", i+2, want, s)
		}
	}
	// the right password works again once the lock has passed, and clears the count
	e.sql("update core.auth_throttle set locked_until = now() - interval '1 second' where key = $1", key)
	if r := e.login(a, a.pw, nil); r.status != 200 {
		t.Fatalf("login after lock: %d %s", r.status, r.body)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.auth_throttle where key = $1", key).Scan(&n)
	if n != 0 {
		t.Fatal("success must reset the account counter")
	}
}

func TestLoginThrottlingTreatsUnknownEmailsLikeRealOnes(t *testing.T) {
	e := sec(t)
	ip := fakeIP()
	ghost := acct{email: "ghost-" + rnd() + "@example.test", ip: ip}
	for i := 0; i < 5; i++ {
		e.login(ghost, "x"+rnd(), nil)
	}
	r := e.login(ghost, "x", nil)
	if r.status != 429 || errBody(r) != "rate_limited: too many attempts, try again later" {
		t.Fatalf("an unknown email locks exactly like a real one: %d %s", r.status, r.body)
	}
}

func TestLoginThrottlePerIPCoversManyAccounts(t *testing.T) {
	t.Setenv("AUTH_IP_FREE", "3")
	e := sec(t)
	victim := e.newAcct("Victim", true)
	attacker := fakeIP()
	for i := 0; i < 3; i++ { // a spray: one wrong guess per account
		e.login(acct{email: "spray" + rnd() + "@example.test", ip: attacker}, "guess", nil)
	}
	mine := victim
	mine.ip = attacker
	if r := e.login(mine, victim.pw, nil); r.status != 429 {
		t.Fatalf("the attacking address is locked out for everyone: %d %s", r.status, r.body)
	}
	if r := e.login(victim, victim.pw, nil); r.status != 200 {
		t.Fatalf("another address is unaffected: %d %s", r.status, r.body)
	}
	e.sql("delete from core.auth_throttle where key = $1", "ip:"+attacker)
}

func TestSignupIsThrottledPerIP(t *testing.T) {
	t.Setenv("AUTH_SIGNUP_FREE", "3")
	e := sec(t)
	ip := fakeIP()
	for i := 0; i < 2; i++ {
		if r := e.post("/api/auth/signup", ip, map[string]any{"email": "s" + rnd() + "@example.test", "name": "S", "password": rnd() + rnd()}, nil, ""); r.status != 200 {
			t.Fatalf("signup %d: %d %s", i, r.status, r.body)
		}
	}
	// the third attempt spends the allowance and still counts as a failure of the next one
	e.post("/api/auth/signup", ip, map[string]any{"email": "s" + rnd() + "@example.test", "name": "S", "password": rnd() + rnd()}, nil, "")
	r := e.post("/api/auth/signup", ip, map[string]any{"email": "s" + rnd() + "@example.test", "name": "S", "password": rnd() + rnd()}, nil, "")
	if r.status != 429 || r.header.Get("Retry-After") == "" {
		t.Fatalf("signup flood: %d %s", r.status, r.body)
	}
	e.sql("delete from core.auth_throttle where key = $1", "signup:"+ip)
}

func TestLoginTimingDoesNotRevealAccounts(t *testing.T) {
	e := sec(t)
	a := e.newAcct("Timed", false)
	med := func(f func(i int) hres) (time.Duration, hres) {
		var ds []time.Duration
		var last hres
		for i := 0; i < 4; i++ {
			s := time.Now()
			last = f(i)
			ds = append(ds, time.Since(s))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2], last
	}
	known, kr := med(func(int) hres { return e.login(a, "wrong"+rnd(), nil) })
	unknown, ur := med(func(int) hres {
		return e.login(acct{email: "nobody" + rnd() + "@example.test", ip: a.ip}, "wrong"+rnd(), nil)
	})
	if kr.status != ur.status || string(kr.body) != string(ur.body) {
		t.Fatalf("responses differ: %d %s vs %d %s", kr.status, kr.body, ur.status, ur.body)
	}
	if unknown*2 < known { // without the dummy hash the unknown path would take a few percent of the time
		t.Fatalf("unknown email answered in %v, a real account in %v", unknown, known)
	}
}

// ---------------------------------------------------------------------------------------------------- MFA

func tcode(secret string, step int64) string {
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, raw)
	m.Write(msg[:])
	s := m.Sum(nil)
	o := s[len(s)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(s[o:o+4])&0x7fffffff)%1000000)
}

func nowStep() int64 { return time.Now().Unix() / 30 }

type enrolled struct {
	acct
	secret   string
	recovery []string
	cookie   string
}

// enrol signs the account in, sets up TOTP and confirms it. The cookie it returns is the (now MFA-proven) session.
func (e *env) enrol(a acct) enrolled {
	e.t.Helper()
	ck := e.session(a)
	r := e.post("/api/auth/mfa/setup", a.ip, map[string]any{}, nil, ck)
	var su struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}
	_ = json.Unmarshal(r.body, &su)
	if r.status != 200 || su.Secret == "" || !strings.HasPrefix(su.URI, "otpauth://totp/") || !strings.Contains(su.URI, "secret="+su.Secret) {
		e.t.Fatalf("setup: %d %s", r.status, r.body)
	}
	r = e.post("/api/auth/mfa/confirm", a.ip, map[string]any{"code": tcode(su.Secret, nowStep())}, nil, ck)
	var cr struct {
		Codes []string `json:"recovery_codes"`
	}
	_ = json.Unmarshal(r.body, &cr)
	if r.status != 200 || len(cr.Codes) != 10 {
		e.t.Fatalf("confirm: %d %s", r.status, r.body)
	}
	return enrolled{a, su.Secret, cr.Codes, ck}
}

// stepUp is password login then the second step; code may be anything.
func (e *env) stepUp(a enrolled, code string, hdr map[string]string) (hres, hres) {
	e.t.Helper()
	r := e.login(a.acct, a.pw, hdr)
	var ch struct {
		Required bool   `json:"mfa_required"`
		Token    string `json:"mfa_token"`
	}
	_ = json.Unmarshal(r.body, &ch)
	if r.status != 200 || !ch.Required || ch.Token == "" || r.cookie() != "" || strings.Contains(string(r.body), "token\":\"cs_") {
		e.t.Fatalf("password step must hand out a challenge and no session: %d %s %q", r.status, r.body, r.cookie())
	}
	return r, e.post("/api/auth/mfa/verify", a.ip, map[string]any{"mfa_token": ch.Token, "code": code}, hdr, "")
}

func TestMFAEnrolmentStoresSecretsSafely(t *testing.T) {
	e := sec(t)
	a := e.newAcct("Enrol", true)
	m := e.enrol(a)
	var blob []byte
	var confirmed *time.Time
	if err := e.pool.QueryRow(context.Background(), "select secret_enc, confirmed_at from core.user_mfa where user_id = $1::uuid", a.userID).Scan(&blob, &confirmed); err != nil || confirmed == nil {
		t.Fatalf("%v %v", err, confirmed)
	}
	if strings.Contains(string(blob), m.secret) || len(blob) < 30 {
		t.Fatal("the TOTP secret must be encrypted at rest")
	}
	rows, _ := e.pool.Query(context.Background(), "select code_hash from core.mfa_recovery_codes where user_id = $1::uuid", a.userID)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		n++
		for _, c := range m.recovery {
			if h == c || h == strings.ReplaceAll(c, "-", "") {
				t.Fatal("recovery codes must be stored hashed")
			}
		}
	}
	if n != 10 {
		t.Fatalf("%d recovery codes stored", n)
	}
	st := hit(t, e.gosv.URL, "GET", "/api/auth/mfa", cookieHdr(m.cookie), "")
	if !strings.Contains(string(st.body), `"enabled":true`) || !strings.Contains(string(st.body), `"recovery_codes_left":10`) {
		t.Fatalf("status: %s", st.body)
	}
	// a second setup while MFA is on is refused
	if r := e.post("/api/auth/mfa/setup", a.ip, map[string]any{}, nil, m.cookie); r.status != 409 {
		t.Fatalf("setup while enrolled: %d", r.status)
	}
	// enrolment needs the right first code
	b := e.newAcct("Enrol2", false)
	ck := e.session(b)
	e.post("/api/auth/mfa/setup", b.ip, map[string]any{}, nil, ck)
	if r := e.post("/api/auth/mfa/confirm", b.ip, map[string]any{"code": "000000"}, nil, ck); r.status != 401 {
		t.Fatalf("wrong first code: %d %s", r.status, r.body)
	}
	if on := e.mfaOn(b); on {
		t.Fatal("a wrong code must not switch MFA on")
	}
}

func (e *env) mfaOn(a acct) bool {
	var on bool
	_ = e.pool.QueryRow(context.Background(), "select exists (select 1 from core.user_mfa where user_id = $1::uuid and confirmed_at is not null)", a.userID).Scan(&on)
	return on
}

func TestTOTPWindowAndReplay(t *testing.T) {
	e := sec(t)
	m := e.enrol(e.newAcct("Window", false))
	cur := nowStep()
	// the code that confirmed enrolment is spent: the same step cannot sign in
	if _, r := e.stepUp(m, tcode(m.secret, cur), nil); r.status != 401 {
		t.Fatalf("a code already used for enrolment must not log in: %d %s", r.status, r.body)
	}
	for _, off := range []int64{-2, 2} { // outside the +-1 step window
		if _, r := e.stepUp(m, tcode(m.secret, cur+off), nil); r.status != 401 {
			t.Fatalf("step %+d accepted: %d", off, r.status)
		}
	}
	e.sql("delete from core.auth_throttle where key = $1", "mfa:"+m.userID)
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	// -1 and +1 are inside the window; each one moves the "last used" mark forward
	if _, r := e.stepUp(m, tcode(m.secret, cur-1), nil); r.status != 200 || sessionOf(t, r) == "" {
		t.Fatalf("one step back: %d %s", r.status, r.body)
	}
	if _, r := e.stepUp(m, tcode(m.secret, cur+1), nil); r.status != 200 {
		t.Fatalf("one step ahead: %d %s", r.status, r.body)
	}
	// replay of the code just used, and of an older step, are refused
	if _, r := e.stepUp(m, tcode(m.secret, cur+1), nil); r.status != 401 {
		t.Fatalf("replayed code accepted: %d", r.status)
	}
	if _, r := e.stepUp(m, tcode(m.secret, cur), nil); r.status != 401 {
		t.Fatalf("older code accepted after a newer one: %d", r.status)
	}
}

func TestRecoveryCodeWorksOnce(t *testing.T) {
	e := sec(t)
	m := e.enrol(e.newAcct("Recover", false))
	code := m.recovery[3]
	if _, r := e.stepUp(m, strings.ToUpper(code), nil); r.status != 200 { // case and dash are forgiving
		t.Fatalf("recovery code: %d %s", r.status, r.body)
	}
	if _, r := e.stepUp(m, code, nil); r.status != 401 {
		t.Fatalf("a recovery code must work once: %d", r.status)
	}
	if _, r := e.stepUp(m, m.recovery[4], nil); r.status != 200 {
		t.Fatalf("other codes still work: %d", r.status)
	}
	st := hit(t, e.gosv.URL, "GET", "/api/auth/mfa", cookieHdr(m.cookie), "")
	if !strings.Contains(string(st.body), `"recovery_codes_left":8`) {
		t.Fatalf("%s", st.body)
	}
}

func TestMFAChallengeIsShortLivedAndLimited(t *testing.T) {
	e := sec(t)
	m := e.enrol(e.newAcct("Chal", false))
	login := func() string {
		r := e.login(m.acct, m.pw, nil)
		var c struct {
			Token string `json:"mfa_token"`
		}
		_ = json.Unmarshal(r.body, &c)
		return c.Token
	}
	// five wrong codes kill the challenge; the right code no longer helps
	tok := login()
	for i := 0; i < 5; i++ {
		e.post("/api/auth/mfa/verify", m.ip, map[string]any{"mfa_token": tok, "code": fmt.Sprintf("%06d", i)}, nil, "")
	}
	e.sql("delete from core.auth_throttle where key = $1", "mfa:"+m.userID)
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	r := e.post("/api/auth/mfa/verify", m.ip, map[string]any{"mfa_token": tok, "code": tcode(m.secret, nowStep())}, nil, "")
	if r.status != 401 || !strings.Contains(string(r.body), "expired") {
		t.Fatalf("exhausted challenge: %d %s", r.status, r.body)
	}
	// an expired one is refused, an unknown one too
	tok = login()
	e.sql("update core.mfa_challenges set expires_at = now() - interval '1 second' where id = $1", hex.EncodeToString(sum(tok)))
	if r := e.post("/api/auth/mfa/verify", m.ip, map[string]any{"mfa_token": tok, "code": tcode(m.secret, nowStep())}, nil, ""); r.status != 401 {
		t.Fatalf("expired challenge: %d", r.status)
	}
	if r := e.post("/api/auth/mfa/verify", m.ip, map[string]any{"mfa_token": "nope", "code": "123456"}, nil, ""); r.status != 401 {
		t.Fatalf("unknown challenge: %d", r.status)
	}
	// the second step is throttled per person too: five wrong codes lock it
	for i := 0; i < 6; i++ {
		tok = login()
		r = e.post("/api/auth/mfa/verify", m.ip, map[string]any{"mfa_token": tok, "code": "111111"}, nil, "")
	}
	if r.status != 429 {
		t.Fatalf("code guessing must be throttled: %d %s", r.status, r.body)
	}
}

func sum(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestNativeAppSessionsNeedTheSecondStepToo(t *testing.T) {
	e := sec(t)
	m := e.enrol(e.newAcct("Native", true))
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	native := map[string]string{"X-Cnote-Client": "native"}
	_, r := e.stepUp(m, tcode(m.secret, nowStep()), native)
	var out struct{ Token string }
	_ = json.Unmarshal(r.body, &out)
	if r.status != 200 || !strings.HasPrefix(out.Token, "cs_") {
		t.Fatalf("native verify: %d %s", r.status, r.body)
	}
	me := hit(t, e.gosv.URL, "GET", "/api/auth/me", map[string]string{"Authorization": "Bearer " + out.Token}, "")
	if me.status != 200 {
		t.Fatalf("bearer session: %d %s", me.status, me.body)
	}
}

func TestMFADisableNeedsPasswordAndCode(t *testing.T) {
	e := sec(t)
	m := e.enrol(e.newAcct("Disable", false))
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", m.userID)
	for name, body := range map[string]map[string]any{
		"wrong password": {"password": "nope-" + rnd(), "code": tcode(m.secret, nowStep())},
		"wrong code":     {"password": m.pw, "code": "000001"},
	} {
		if r := e.post("/api/auth/mfa/disable", m.ip, body, nil, m.cookie); r.status != 401 || !strings.Contains(string(r.body), "invalid password or code") {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
	}
	if !e.mfaOn(m.acct) {
		t.Fatal("still on after refused attempts")
	}
	e.sql("delete from core.auth_throttle where key = $1", "mfa:"+m.userID)
	if r := e.post("/api/auth/mfa/disable", m.ip, map[string]any{"password": m.pw, "code": tcode(m.secret, nowStep())}, nil, m.cookie); r.status != 200 {
		t.Fatalf("disable: %d %s", r.status, r.body)
	}
	if e.mfaOn(m.acct) {
		t.Fatal("off after disable")
	}
	if r := e.login(m.acct, m.pw, nil); r.status != 200 || strings.Contains(string(r.body), "mfa_required") {
		t.Fatalf("login without MFA: %d %s", r.status, r.body)
	}
	var left int
	_ = e.pool.QueryRow(context.Background(), "select count(*) from core.mfa_recovery_codes where user_id = $1::uuid", m.userID).Scan(&left)
	if left != 0 {
		t.Fatal("recovery codes must go with MFA")
	}
}

// -------------------------------------------------------------------------------------- workspace MFA policy

func TestRequireMFAForAdmins(t *testing.T) {
	e := sec(t)
	owner := e.newAcct("Owner", true)
	ownerCk := e.session(owner)
	// the owner cannot turn the rule on without MFA of their own (they would lock themselves out)
	if r := e.run(ownerCk, owner.ws, "security.update", map[string]any{"require_mfa_admins": true}); r.status != 400 || !strings.Contains(string(r.body), "your own account first") {
		t.Fatalf("owner without MFA: %d %s", r.status, r.body)
	}
	om := e.enrol(owner)
	if r := e.run(om.cookie, owner.ws, "security.update", map[string]any{"require_mfa_admins": true}); r.status != 200 {
		t.Fatalf("enable: %d %s", r.status, r.body)
	}
	if e.events(owner.ws, "security.updated") != 1 {
		t.Fatal("policy changes are audited")
	}
	// promotion: someone without MFA cannot become an admin, by either command; with MFA they can
	bob := e.newAcct("Bob", false)
	for name, c := range map[string][2]any{
		"member.add as admin": {"member.add", map[string]any{"email": bob.email, "role": "admin"}},
	} {
		if r := e.run(om.cookie, owner.ws, c[0].(string), c[1]); r.status != 400 || !strings.Contains(string(r.body), "requires multi-factor authentication") {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := e.run(om.cookie, owner.ws, "member.add", map[string]any{"email": bob.email, "role": "member"}); r.status != 200 {
		t.Fatalf("add as member: %d %s", r.status, r.body)
	}
	if r := e.run(om.cookie, owner.ws, "member.update", map[string]any{"user_id": bob.userID, "role": "admin"}); r.status != 400 || !strings.Contains(string(r.body), "requires multi-factor authentication") {
		t.Fatalf("member.update promotion: %d %s", r.status, r.body)
	}
	// other fields of a member are untouched by the rule
	if r := e.run(om.cookie, owner.ws, "member.update", map[string]any{"user_id": bob.userID, "designation": "Ops"}); r.status != 200 {
		t.Fatalf("non-role update: %d %s", r.status, r.body)
	}
	bm := e.enrol(bob)
	if r := e.run(om.cookie, owner.ws, "member.update", map[string]any{"user_id": bob.userID, "role": "admin"}); r.status != 200 {
		t.Fatalf("promotion after enrolment: %d %s", r.status, r.body)
	}
	// login: an admin who has MFA is stepped up; an admin who has none is told to set it up and is held back
	cara := e.newAcct("Cara", false)
	if _, err := adminPool(t).Exec(context.Background(), "insert into core.memberships (workspace_id, user_id, role) values ($1::uuid,$2::uuid,'admin')", owner.ws, cara.userID); err != nil { // legacy admin, before the rule
		t.Fatal(err)
	}
	r := e.login(cara, cara.pw, nil)
	if r.status != 200 || !strings.Contains(string(r.body), `"mfa_setup_required":true`) {
		t.Fatalf("admin without MFA at login: %d %s", r.status, r.body)
	}
	caraCk := sessionOf(t, r)
	if r := e.run(caraCk, owner.ws, "workspace.get", map[string]any{}); r.status != 403 || !strings.Contains(string(r.body), "requires multi-factor authentication") {
		t.Fatalf("admin session without MFA must not reach the workspace: %d %s", r.status, r.body)
	}
	// after enrolling, the same session is MFA-proven and works
	cm := e.enrol(cara)
	e.sql("update core.sessions set amr = 'pwd' where id = $1", hex.EncodeToString(sum(cm.cookie)))
	if r := e.run(cm.cookie, owner.ws, "workspace.get", map[string]any{}); r.status != 403 {
		t.Fatal("a password-only session stays blocked")
	}
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", cara.userID)
	_, vr := e.stepUp(cm, tcode(cm.secret, nowStep()), nil)
	if r := e.run(sessionOf(t, vr), owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("MFA session: %d %s", r.status, r.body)
	}
	// plain members are not affected, and MFA cannot be disabled while the rule applies
	dan := e.newAcct("Dan", false)
	e.run(om.cookie, owner.ws, "member.add", map[string]any{"email": dan.email, "role": "member"})
	if r := e.run(e.session(dan), owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
		t.Fatalf("member: %d %s", r.status, r.body)
	}
	e.sql("update core.user_mfa set last_step = 0 where user_id = $1::uuid", bob.userID)
	if r := e.post("/api/auth/mfa/disable", bob.ip, map[string]any{"password": bob.pw, "code": tcode(bm.secret, nowStep())}, nil, bm.cookie); r.status != 403 {
		t.Fatalf("an admin cannot switch MFA off while the workspace requires it: %d %s", r.status, r.body)
	}
	sg := e.run(om.cookie, owner.ws, "security.get", map[string]any{})
	if sg.status != 200 || !strings.Contains(string(sg.body), `"require_mfa_admins":true`) {
		t.Fatalf("security.get: %d %s", sg.status, sg.body)
	}
	// admins (not owners) cannot change the policy
	if r := e.run(sessionOf(t, vr), owner.ws, "security.update", map[string]any{"require_mfa_admins": false}); r.status != 403 {
		t.Fatalf("admin changing policy: %d %s", r.status, r.body)
	}
}

func TestOAuthConsentRespectsWorkspaceMFA(t *testing.T) {
	e := sec(t)
	owner := e.newAcct("OAuthOwner", true)
	om := e.enrol(owner)
	if r := e.run(om.cookie, owner.ws, "security.update", map[string]any{"require_mfa_admins": true}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	o := newOCFor(t, e)
	client := o.register(redirect)
	_, challenge := pkce()
	// a password-only session (older, or made by Rust) of an admin cannot hand an agent the workspace...
	pwd := who{cookie: e.sessionNoMFA(owner), ws: owner.ws}
	if r := o.authorize(pwd, o.authzBody(client, redirect, challenge), owner.ws, true, nil); r.status != 403 {
		t.Fatalf("consent from a password-only admin session: %d %s", r.status, r.body)
	}
	// ...the MFA-proven one can
	out := o.authorize(who{cookie: om.cookie, ws: owner.ws}, o.authzBody(client, redirect, challenge), owner.ws, true, nil).json()
	if out["ok"] != true {
		t.Fatalf("consent with MFA: %v", out)
	}
	_ = query(t, out["redirect_to"], "code")
}

// sessionNoMFA makes a password-only session for an account that has MFA (as a Rust-created or older session would be).
func (e *env) sessionNoMFA(a acct) string {
	e.t.Helper()
	tok := rnd() + rnd()
	e.sql("insert into core.sessions (id, user_id, expires_at, amr) values ($1,$2::uuid, now() + interval '1 day', 'pwd')", hex.EncodeToString(sum(tok)), a.userID)
	return tok
}

func TestPasswordOnlySessionOfMFAAdminIsRefusedAtTheWorkspace(t *testing.T) {
	e := sec(t)
	owner := e.newAcct("Legacy", true)
	om := e.enrol(owner)
	if r := e.run(om.cookie, owner.ws, "security.update", map[string]any{"require_mfa_admins": true}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	old := e.sessionNoMFA(owner) // e.g. created by the Rust server before the rule
	if r := e.run(old, owner.ws, "workspace.get", map[string]any{}); r.status != 403 || !strings.Contains(string(r.body), "multi-factor") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestAgentsAndKeysCannotChangeSignInPolicy(t *testing.T) {
	e := sec(t)
	owner := e.newAcct("PolicyOwner", true)
	ck := e.session(owner)
	k := e.run(ck, owner.ws, "apikey.create", map[string]any{"name": "ci"})
	var kv struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(k.body, &kv)
	if kv.APIKey == "" {
		t.Fatalf("apikey: %d %s", k.status, k.body)
	}
	r := e.cmd(e.gosv.URL, who{bearer: kv.APIKey}, owner.ws, "security.update", map[string]any{"require_mfa_admins": false}, nil)
	if r.status != 403 {
		t.Fatalf("API key changing sign-in policy: %d %s", r.status, r.body)
	}
	r = e.cmd(e.gosv.URL, who{bearer: kv.APIKey}, owner.ws, "security.get", map[string]any{}, nil)
	if r.status != 200 || strings.Contains(string(r.body), "client_secret\":\"") {
		t.Fatalf("security.get via key: %d %s", r.status, r.body)
	}
	other := e.newAcct("Other", true)
	if r := e.run(e.session(other), owner.ws, "security.get", map[string]any{}); r.status != 403 {
		t.Fatalf("other workspace: %d", r.status)
	}
}
