package auth

// Accounts: sign up, sign in, sessions and workspace creation. Port of the account half of
// crates/cnote-core/src/auth.rs. Password hashes (argon2id PHC strings) and sessions (sha256 of the token in
// core.sessions) are interchangeable with Rust in both directions.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const sessionDays = 30

// argon2 crate defaults (Argon2::default): argon2id, v19, m=19456 KiB, t=2, p=1, 32-byte output, 16-byte salt.
const (
	argonMem  = 19456
	argonTime = 2
	argonPar  = 1
)

var b64 = base64.RawStdEncoding

func RandomToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, argonTime, argonMem, argonPar, 32)
	return "$argon2id$v=19$m=" + strconv.Itoa(argonMem) + ",t=" + strconv.Itoa(argonTime) + ",p=" + strconv.Itoa(argonPar) +
		"$" + b64.EncodeToString(salt) + "$" + b64.EncodeToString(h), nil
}

// VerifyPassword checks pw against any argon2i/argon2id v19 PHC string, using the parameters stored in it
// (so hashes made under older settings keep working). Anything it cannot parse is a mismatch, like Rust.
func VerifyPassword(pw, stored string) bool {
	f := strings.Split(stored, "$") // "", alg, [v=19], params, salt, hash
	if len(f) == 6 && f[2] == "v=19" {
		f = append(f[:2], f[3:]...)
	}
	if len(f) != 5 || f[0] != "" {
		return false
	}
	var m, t, p uint64
	for _, kv := range strings.Split(f[2], ",") {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil {
			return false
		}
		switch k {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			p = n
		default:
			return false
		}
	}
	salt, err1 := b64.DecodeString(f[3])
	want, err2 := b64.DecodeString(f[4])
	if err1 != nil || err2 != nil || len(want) < 4 || p == 0 || p > 255 || t == 0 || m < 8*p {
		return false
	}
	var got []byte
	switch f[1] {
	case "argon2id":
		got = argon2.IDKey([]byte(pw), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	case "argon2i":
		got = argon2.Key([]byte(pw), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	default:
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// rustLower is Rust's str::to_lowercase where Go's differs: U+0130 expands to i + combining dot, and a capital
// sigma ending a word becomes final sigma.
func rustLower(s string) string {
	s = strings.ReplaceAll(s, "İ", "i̇")
	if !strings.ContainsRune(s, 'Σ') {
		return strings.ToLower(s)
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if r != 'Σ' {
			b.WriteString(strings.ToLower(string(r)))
			continue
		}
		before, after := false, false
		for j := i - 1; j >= 0; j-- {
			if !caseIgnorable(rs[j]) {
				before = cased(rs[j])
				break
			}
		}
		for j := i + 1; j < len(rs); j++ {
			if !caseIgnorable(rs[j]) {
				after = cased(rs[j])
				break
			}
		}
		if before && !after {
			b.WriteRune('ς')
		} else {
			b.WriteRune('σ')
		}
	}
	return b.String()
}

func cased(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }
func caseIgnorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) || strings.ContainsRune("'.:·’․‧︓﹒﹕＇．：", r)
}

// NormEmail is `email.trim().to_lowercase()`.
func NormEmail(e string) string { return rustLower(strings.TrimSpace(e)) }

// SignUp returns the new user id.
func (a *Auth) SignUp(ctx context.Context, email, name, password string) (string, error) {
	email = NormEmail(email)
	if !strings.Contains(email, "@") {
		return "", command.ErrInvalid("a valid email is required")
	}
	if len(password) < 8 {
		return "", command.ErrInvalid("password must be at least 8 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", command.NewError(command.Internal, "hash: "+err.Error())
	}
	var id string
	err = a.Pool.QueryRow(ctx,
		"insert into core.users (email, name, password_hash) values ($1,$2,$3) on conflict (email) do nothing returning id::text",
		email, strings.TrimSpace(name), hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrConflict("an account with this email already exists")
	}
	return id, command.DB(err)
}

// LoginResult is what a correct password leads to: a session, or (MFA enrolled) a step-up token and no session yet.
type LoginResult struct {
	UserID, Token, MFAToken string
	MFASetupRequired        bool // a workspace needs this person's MFA and none is set up: the browser sends them to enrol
}

// dummyHash is verified for emails with no password, so the unknown-account path costs one argon2 run like any other.
var dummyHash, _ = HashPassword("dummy password for the unknown-email path")

// SignIn checks the password. Throttle first (per account and per IP, before any hashing), then one argon2 check
// that runs for unknown emails too, against a dummy hash, so timing does not reveal which accounts exist. Every
// failure reads the same: "invalid email or password".
func (a *Auth) SignIn(ctx context.Context, email, password, ip string) (LoginResult, error) {
	email = NormEmail(email)
	acct, ipKey := "acct:"+sha256Hex(email), "ip:"+ip
	if d, err := a.locked(ctx, acct, ipKey); err != nil || d > 0 {
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, throttled(d)
	}
	var id string
	var hash *string
	err := a.Pool.QueryRow(ctx, "select id::text, password_hash from core.users where email = $1", email).Scan(&id, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, command.DB(err)
	}
	known := err == nil && hash != nil
	if !known {
		VerifyPassword(password, dummyHash)
	}
	if !known || !VerifyPassword(password, *hash) {
		if d, err := a.fail(ctx, acct, freeAccount()); err == nil && d > 0 {
			a.auditLockout(ctx, id, "account", d)
		}
		if d, err := a.fail(ctx, ipKey, freeIP()); err == nil && d > 0 {
			a.auditLockout(ctx, "", "ip", d)
		}
		return LoginResult{}, command.NewError(command.Unauthenticated, "invalid email or password")
	}
	a.forget(ctx, acct)
	if on, err := a.MFAEnabled(ctx, id); err != nil {
		return LoginResult{}, err
	} else if on {
		tok, err := a.NewChallenge(ctx, id)
		return LoginResult{UserID: id, MFAToken: tok}, err
	}
	var need bool
	if err := a.Pool.QueryRow(ctx, "select core.mfa_required_for($1::uuid)", id).Scan(&need); err != nil {
		return LoginResult{}, command.DB(err)
	}
	tok, err := a.CreateSession(ctx, id)
	return LoginResult{UserID: id, Token: tok, MFASetupRequired: need}, err
}

func (a *Auth) CreateSession(ctx context.Context, userID string) (string, error) {
	return a.createSession(ctx, userID, "pwd", "")
}

// createSession: amr is how the person proved themselves (pwd, mfa, sso); ssoWS pins an SSO session to one workspace.
func (a *Auth) createSession(ctx context.Context, userID, amr, ssoWS string) (string, error) {
	tok := RandomToken(32)
	c := clientOf(ctx)
	_, err := a.Pool.Exec(ctx, `insert into core.sessions (id, user_id, expires_at, amr, sso_workspace_id, ip, user_agent, last_seen_at)
		values ($1, $2::uuid, now() + make_interval(days => $3), $4, nullif($5,'')::uuid, nullif($6,''), nullif($7,''), now())`,
		sha256Hex(tok), userID, sessionDays, amr, ssoWS, c.ip, c.ua)
	return tok, command.DB(err)
}

func (a *Auth) DestroySession(ctx context.Context, token string) error {
	_, err := a.Pool.Exec(ctx, "delete from core.sessions where id = $1", sha256Hex(token))
	return command.DB(err)
}

// SessionToken is the credential of a request: `cs_` bearer (native apps) or the cn_session cookie.
func SessionToken(h http.Header) (string, bool) { return sessionToken(h) }

type SessionUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`

	AMR          string `json:"-"` // pwd, mfa or sso
	SSOWorkspace string `json:"-"` // set for an SSO session: the only workspace it may use
}

// SessionUser resolves the caller's session; errors are the Rust ones ("sign in required" / "session expired").
func (a *Auth) SessionUser(ctx context.Context, h http.Header) (SessionUser, error) {
	tok, present := sessionToken(h)
	if !present {
		return SessionUser{}, command.NewError(command.Unauthenticated, "sign in required")
	}
	var u SessionUser
	var stale bool
	err := a.Pool.QueryRow(ctx,
		`select u.id::text, u.email, u.name, s.amr, coalesce(s.sso_workspace_id::text, ''), (s.last_seen_at is null or s.last_seen_at < now() - make_interval(secs => $2))
		 from core.sessions s join core.users u on u.id = s.user_id
		 where s.id = $1 and s.expires_at > now()`, sha256Hex(tok), seenEvery.Seconds()).Scan(&u.ID, &u.Email, &u.Name, &u.AMR, &u.SSOWorkspace, &stale)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, command.NewError(command.Unauthenticated, "session expired")
	}
	if err == nil && stale {
		a.touch(ctx, sha256Hex(tok))
	}
	return u, command.DB(err)
}

type WorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

func (a *Auth) UserWorkspaces(ctx context.Context, userID string) ([]WorkspaceRef, error) {
	rows, err := a.Pool.Query(ctx, "select id::text, name, slug, role from core.user_workspaces($1::uuid)", userID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []WorkspaceRef{}
	for rows.Next() {
		var w WorkspaceRef
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Role); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, w)
	}
	return out, command.DB(rows.Err())
}

// CreateWorkspace creates a workspace owned by userID, then runs the module bootstrap hooks Rust registers
// (core: General space, docs: two templates, finance: chart of accounts) inside the new tenant.
func (a *Auth) CreateWorkspace(ctx context.Context, userID, name, slug string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", command.ErrInvalid("name is required")
	}
	var id string
	if err := a.Pool.QueryRow(ctx, "select core.create_workspace($1, $2, $3::uuid)::text", strings.TrimSpace(name), slug, userID).Scan(&id); err != nil {
		return "", command.DB(err)
	}
	tx, err := db.BeginTenant(ctx, a.Pool, id)
	if err != nil {
		return "", command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "insert into core.spaces (workspace_id, key, name, template) values ($1::uuid, 'GEN', 'General', 'blank')", id); err != nil {
		return "", command.DB(err)
	}
	for _, t := range [][3]string{{"Proposal", "deal", "Proposal: {{deal.title}}"}, {"Invoice cover letter", "invoice", "Invoice {{invoice.number}}: cover letter"}} {
		body := PROPOSAL
		if t[1] == "invoice" {
			body = COVER
		}
		if _, err := tx.Exec(ctx, `insert into docs.templates (workspace_id, name, kind, source_kind, title, content_md, created_by)
			values ($1::uuid,$2,'page',$3,$4,$5,$6::uuid) on conflict do nothing`, id, t[0], t[1], t[2], body, userID); err != nil {
			return "", command.DB(err)
		}
	}
	for _, c := range [][3]string{{"1100", "Accounts receivable", "asset"}, {"1200", "Bank / UPI clearing", "asset"}, {"2201", "CGST output", "liability"},
		{"2202", "SGST output", "liability"}, {"2203", "IGST output", "liability"}, {"4000", "Sales revenue", "revenue"}, {"5100", "Bad debt expense", "expense"}} {
		if _, err := tx.Exec(ctx, "insert into billing.accounts (workspace_id, code, name, kind) values ($1::uuid,$2,$3,$4) on conflict do nothing", id, c[0], c[1], c[2]); err != nil {
			return "", command.DB(err)
		}
	}
	return id, command.DB(tx.Commit(ctx))
}
