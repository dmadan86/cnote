package server

// Accepting an email invitation (docs/design/onboarding.md 6.6): GET /api/invite/{token} says what the link is for, POST
// /api/invite/{token}/accept uses it. The token in the path is the credential. Nothing here logs the path or the token, every
// reply carries Referrer-Policy: no-referrer, and the token is consumed by one UPDATE in the transaction that creates the
// membership, so a replay or a second concurrent accept finds nothing to consume.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/admin"
	"github.com/cnote/api/internal/modules/publiclinks"
)

const invitePrefix = "/api/invite/"

func inviteReply(w http.ResponseWriter, status int, body map[string]any) {
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	out, _ := command.Marshal(body)
	writeJSON(w, status, out)
}

func inviteState(w http.ResponseWriter, state string) {
	msg := map[string]string{
		"invalid":     "This invitation link is not valid any more. Ask the person who invited you to send a new one.",
		"expired":     "This invitation has expired. Ask the person who invited you to send a new one.",
		"member":      "You are already a member of this workspace. Nothing was changed.",
		"deactivated": "Your access to this workspace is turned off. Ask a workspace admin. Nothing was changed.",
		"busy":        "Too many attempts. Try again later.",
	}[state]
	status := http.StatusOK
	if state == "busy" {
		status = http.StatusTooManyRequests
	}
	inviteReply(w, status, map[string]any{"state": state, "message": msg})
}

func inviteFail(w http.ResponseWriter, err error) {
	var ce *command.Error
	if errors.As(err, &ce) {
		inviteReply(w, ce.Code.Status(), map[string]any{"state": "error", "error": ce})
		return
	}
	slog.Error("invitation", "error", err) // never the request path: it holds the token
	inviteReply(w, http.StatusInternalServerError, map[string]any{"state": "error", "error": command.NewError(command.Internal, "internal error")})
}

func inviteTries() int {
	if n, err := strconv.Atoi(os.Getenv("INVITE_TRIES_PER_IP_HOUR")); err == nil && n > 0 {
		return n
	}
	return 120
}

// invite routes the two requests. The token never leaves this function except into hashing.
func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, invitePrefix)
	token, accept := strings.CutSuffix(rest, "/accept")
	if strings.Contains(token, "/") || (accept && r.Method != http.MethodPost) || (!accept && r.Method != http.MethodGet) {
		if strings.Contains(token, "/") {
			notFound(w)
			return
		}
		methodNotAllowed(w, map[bool]string{true: "POST", false: "GET"}[accept])
		return
	}
	if accept {
		if !isJSON(r) {
			inviteReply(w, http.StatusUnsupportedMediaType, map[string]any{"state": "error", "message": "Content-Type must be application/json."})
			return
		}
		if !inviteSameOrigin(r) {
			inviteReply(w, http.StatusForbidden, map[string]any{"state": "error", "message": "Request origin is not allowed."})
			return
		}
	}
	ctx := r.Context()
	// Counted for every try, so guessing links and spraying sign-ups from one address both stop.
	if n, err := db.Bump(ctx, s.Pool, "invite-ip:"+publiclinks.ClientIP(r), false); err != nil {
		inviteFail(w, err)
		return
	} else if n > inviteTries() {
		inviteState(w, "busy")
		return
	}
	hash, ok := admin.InviteHash(token)
	if !ok {
		inviteState(w, "invalid")
		return
	}
	var ws *string
	if err := s.Pool.QueryRow(ctx, "select core.invitation_workspace($1)::text", hash).Scan(&ws); err != nil {
		inviteFail(w, err)
		return
	}
	if ws == nil {
		inviteState(w, "invalid")
		return
	}
	if accept {
		s.inviteAccept(w, r, *ws, hash)
	} else {
		s.invitePeek(w, r, *ws, hash)
	}
}

// inviteSameOrigin requires the browser's origin (or its referrer when Origin is absent).
func inviteSameOrigin(r *http.Request) bool {
	base, ok := auth.PublicBase(r.Host)
	if !ok {
		return false
	}
	expected, err := url.Parse(base)
	if err != nil || expected.Host == "" || (expected.Scheme != "http" && expected.Scheme != "https") || expected.User != nil {
		return false
	}
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Referer()
	}
	actual, err := url.Parse(source)
	if err != nil || actual.User != nil || actual.Host == "" {
		return false
	}
	if r.Header.Get("Origin") != "" && (actual.Path != "" || actual.RawQuery != "" || actual.Fragment != "") {
		return false
	}
	return strings.EqualFold(actual.Scheme, expected.Scheme) && strings.EqualFold(actual.Host, expected.Host)
}

// who is the caller's session user, if any.
func (s *Server) sessionEmail(r *http.Request) (id, email string) {
	u, err := s.Auth.SessionUser(r.Context(), r.Header)
	if err != nil {
		return "", ""
	}
	return u.ID, u.Email
}

func (s *Server) invitePeek(w http.ResponseWriter, r *http.Request, ws, hash string) {
	ctx := r.Context()
	tx, err := db.BeginTenant(ctx, s.Pool, ws)
	if err != nil {
		inviteFail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var email, role, wsName, inviter string
	var expires time.Time
	var closed, expired bool
	var uid *string
	var hasMember, deactivated bool
	err = tx.QueryRow(ctx, `select i.email, i.role, w.name, coalesce(iu.name, ''), i.expires_at, i.accepted_at is not null or i.revoked_at is not null, i.expires_at <= now(),
		u.id::text, m.user_id is not null, coalesce(m.deactivated_at is not null, false)
		from core.invitations i join core.workspaces w on w.id = i.workspace_id
		left join core.users iu on iu.id = i.invited_by
		left join core.users u on u.email = i.email
		left join core.memberships m on m.workspace_id = i.workspace_id and m.user_id = u.id
		where i.token_hash = $1`, hash).Scan(&email, &role, &wsName, &inviter, &expires, &closed, &expired, &uid, &hasMember, &deactivated)
	switch {
	case errors.Is(err, pgx.ErrNoRows), err == nil && closed:
		inviteState(w, "invalid")
		return
	case err != nil:
		inviteFail(w, err)
		return
	case expired:
		inviteState(w, "expired")
		return
	case hasMember && deactivated:
		inviteState(w, "deactivated")
		return
	case hasMember:
		inviteState(w, "member")
		return
	}
	_, me := s.sessionEmail(r)
	account := "new"
	if uid != nil {
		account = "existing"
	}
	inviteReply(w, http.StatusOK, map[string]any{"state": "ok", "workspace_name": wsName, "inviter_name": inviter, "email": email, "role": role,
		"expires_at": expires, "account": account, "signed_in_as": me})
}

func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request, ws, hash string) {
	ctx := r.Context()
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if r.ContentLength != 0 && r.Body != nil {
		if buf, err := io.ReadAll(io.LimitReader(r.Body, 1<<16)); err != nil || (len(buf) > 0 && json.Unmarshal(buf, &in) != nil) {
			inviteFail(w, command.ErrInvalid("invalid input"))
			return
		}
	}
	tx, err := db.BeginTenant(ctx, s.Pool, ws)
	if err != nil {
		inviteFail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The one place a token is spent. A concurrent accept waits on this row, then finds it already accepted.
	var invID, email, role string
	err = tx.QueryRow(ctx, `update core.invitations set accepted_at = now(), token_enc = null, updated_at = now()
		where token_hash = $1 and accepted_at is null and revoked_at is null and expires_at > now() returning id::text, email, role`, hash).Scan(&invID, &email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		var expired bool
		if e := tx.QueryRow(ctx, "select expires_at <= now() and accepted_at is null and revoked_at is null from core.invitations where token_hash = $1", hash).Scan(&expired); e == nil && expired {
			inviteState(w, "expired")
		} else {
			inviteState(w, "invalid")
		}
		return
	}
	if err != nil {
		inviteFail(w, err)
		return
	}

	var uid string
	existing := true
	switch err := tx.QueryRow(ctx, "select id::text from core.users where email = $1 for share", email).Scan(&uid); {
	case errors.Is(err, pgx.ErrNoRows):
		existing = false
	case err != nil:
		inviteFail(w, err)
		return
	}
	if existing {
		// An existing account joins only from a session of that account. The invitation never touches its password, MFA or sessions.
		if sid, _ := s.sessionEmail(r); sid != uid {
			inviteReply(w, http.StatusUnauthorized, map[string]any{"state": "signin", "email": email,
				"message": "Sign in as " + email + " to accept this invitation."})
			return
		}
		var deactivated bool
		switch err := tx.QueryRow(ctx, "select deactivated_at is not null from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid for update", ws, uid).Scan(&deactivated); {
		case err == nil && deactivated:
			inviteState(w, "deactivated")
			return
		case err == nil:
			inviteState(w, "member")
			return
		case !errors.Is(err, pgx.ErrNoRows):
			inviteFail(w, err)
			return
		}
		var blocked bool // the same rule as member.add: no admin without MFA where the workspace requires it
		if err := tx.QueryRow(ctx, `select $3 in ('admin','owner') and coalesce((select require_mfa_admins from core.workspace_security where workspace_id = $1::uuid), false)
			and not exists (select 1 from core.user_mfa where user_id = $2::uuid and confirmed_at is not null)`, ws, uid, role).Scan(&blocked); err != nil {
			inviteFail(w, err)
			return
		}
		if blocked {
			inviteFail(w, command.ErrInvalid("this workspace requires multi-factor authentication for admins: turn it on in Account security, then open the link again"))
			return
		}
	} else {
		if sid, me := s.sessionEmail(r); sid != "" {
			inviteReply(w, http.StatusUnauthorized, map[string]any{"state": "signout", "signed_in_as": me, "message": "You're signed in as " + me + ". Sign out to accept this invite."})
			return
		}
		if err := s.Auth.CheckSignup(ctx, publiclinks.ClientIP(r)); err != nil {
			inviteFail(w, err)
			return
		}
		if len(in.Password) < 8 {
			inviteFail(w, command.ErrInvalid("choose a password of at least 8 characters"))
			return
		}
		pwHash, err := auth.HashPassword(in.Password)
		if err != nil {
			inviteFail(w, err)
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = strings.Split(email, "@")[0]
		}
		// The inbox is proven by the link, so the address is verified from the start.
		err = tx.QueryRow(ctx, `insert into core.users (email, name, password_hash, email_verified_at) values ($1, $2, $3, now())
			on conflict (email) do nothing returning id::text`, email, name[:min(len(name), 200)], pwHash).Scan(&uid)
		if errors.Is(err, pgx.ErrNoRows) { // an account with this address appeared since: the same sign-in path as above
			inviteReply(w, http.StatusUnauthorized, map[string]any{"state": "signin", "email": email, "message": "Sign in as " + email + " to accept this invitation."})
			return
		}
		if err != nil {
			inviteFail(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, "insert into core.memberships (workspace_id, user_id, role) values ($1::uuid, $2::uuid, $3)", ws, uid, role); err != nil {
		inviteFail(w, err)
		return
	}
	if _, err := tx.Exec(ctx, "update core.invitations set accepted_by = $2::uuid where id = $1::uuid", invID, uid); err != nil {
		inviteFail(w, err)
		return
	}
	c := &command.Ctx{Context: ctx, Tx: tx, Pool: s.Pool, Principal: command.Principal{Kind: command.KindUser, UserID: &uid, WorkspaceID: ws, Role: command.ParseRole(role)}}
	if _, err := c.Emit("member.added", nil, map[string]any{"user_id": uid, "role": role, "invitation_id": invID}); err != nil {
		inviteFail(w, err)
		return
	}
	var mfa bool
	if !existing {
		if err := tx.QueryRow(ctx, "select core.mfa_required_for($1::uuid)", uid).Scan(&mfa); err != nil {
			inviteFail(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		inviteFail(w, err)
		return
	}
	body := map[string]any{"state": "joined", "workspace_id": ws}
	if existing {
		inviteReply(w, http.StatusOK, body)
		return
	}
	token, err := s.Auth.CreateSession(ctx, uid)
	if err != nil {
		inviteFail(w, err)
		return
	}
	body["mfa_setup_required"] = mfa
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	withSession(w, r, body, token)
}
