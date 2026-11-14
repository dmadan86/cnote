package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

type tokenReq struct{ grantType, code, redirectURI, clientID, verifier, refreshToken, resource *string }

type oauthErr struct {
	status     int
	code, desc string
}

func (e *oauthErr) Error() string { return e.code + ": " + e.desc }

func invalidRequest(desc string) *oauthErr { return &oauthErr{400, "invalid_request", desc} }
func invalidGrant(desc string) *oauthErr   { return &oauthErr{400, "invalid_grant", desc} }

func writeErr(w http.ResponseWriter, err error) {
	var oe *oauthErr
	if errors.As(err, &oe) {
		oerr(w, oe.status, oe.code, oe.desc)
		return
	}
	serverError(w, err)
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	buf, ok := h.readBody(w, r, publicBodyLimit)
	if !ok {
		return
	}
	vals, valid := form(buf, "grant_type", "code", "redirect_uri", "client_id", "code_verifier", "refresh_token", "resource")
	if !valid || !isForm(r) {
		h.proxy(w, r, buf)
		return
	}
	get := func(k string) *string {
		if v, has := vals[k]; has {
			return &v
		}
		return nil
	}
	b := tokenReq{get("grant_type"), get("code"), get("redirect_uri"), get("client_id"), get("code_verifier"), get("refresh_token"), get("resource")}
	out, err := h.tokenInner(r, b)
	if err != nil {
		writeErr(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) tokenInner(r *http.Request, b tokenReq) (map[string]any, error) {
	// Per caller address only: junk from one source cannot starve anyone else. The per-client bucket is charged
	// later, once a real code or refresh token was found, so strangers cannot block a public client.
	if err := throttle("oauth:token:ip:"+clientIP(r), 60, 2); err != nil {
		return nil, err
	}
	if b.clientID == nil || *b.clientID == "" {
		return nil, invalidRequest("client_id is required")
	}
	base, ok := baseOf(r)
	if !ok {
		return nil, &oauthErr{http.StatusServiceUnavailable, "server_error", "OAuth is not available: the server has no PUBLIC_BASE_URL configured"}
	}
	switch {
	case b.grantType != nil && *b.grantType == "authorization_code":
		return h.exchangeCode(r.Context(), base, *b.clientID, b)
	case b.grantType != nil && *b.grantType == "refresh_token":
		return h.refresh(r.Context(), base, *b.clientID, b)
	}
	return nil, invalidRequest2("unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
}

func invalidRequest2(code, desc string) *oauthErr { return &oauthErr{400, code, desc} }

func throttle(key string, burst, perSec float64) error {
	if command.CheckRateWith(key, burst, perSec) != nil {
		return &oauthErr{http.StatusTooManyRequests, "temporarily_unavailable", "too many requests, retry shortly"}
	}
	return nil
}

func checkResource(requested *string, bound, base string) error {
	if requested == nil || *requested == "" {
		return nil
	}
	if r, ok := canonicalResource(*requested, base); !ok || r != bound {
		return invalidRequest2("invalid_target", "resource does not match the one this grant was issued for")
	}
	return nil
}

// issueTokens inserts a fresh access + refresh pair for a grant and builds the response body.
func issueTokens(ctx context.Context, tx pgx.Tx, grant, scope string) (map[string]any, error) {
	access, refresh := randomSecret("cno_"), randomSecret("cnr_")
	for _, t := range []struct {
		tok, kind string
		ttl       float64
	}{{access, "access", accessTTLSecs}, {refresh, "refresh", refreshTTLDays * 86400}} {
		if _, err := tx.Exec(ctx, "insert into core.oauth_tokens (token_hash, kind, grant_id, expires_at) values ($1,$2,$3::uuid, now() + make_interval(secs => $4))",
			sha256Hex(t.tok), t.kind, grant, t.ttl); err != nil {
			return nil, err
		}
	}
	return map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": accessTTLSecs, "refresh_token": refresh, "scope": scope}, nil
}

func (h *Handler) revokeGrant(ctx context.Context, grant string) error {
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := revokeGrantTx(ctx, tx, grant); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func revokeGrantTx(ctx context.Context, tx pgx.Tx, grant string) error {
	if _, err := tx.Exec(ctx, "update core.oauth_grants set revoked_at = coalesce(revoked_at, now()) where id = $1::uuid", grant); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "delete from core.oauth_tokens where grant_id = $1::uuid", grant)
	return err
}

var verifierRe = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

func (h *Handler) exchangeCode(ctx context.Context, base, clientID string, b tokenReq) (map[string]any, error) {
	if b.code == nil || *b.code == "" {
		return nil, invalidRequest("code is required")
	}
	if b.verifier == nil {
		return nil, invalidRequest("code_verifier is required")
	}
	if !verifierRe.MatchString(*b.verifier) { // RFC 7636: 43..128 unreserved characters
		return nil, invalidRequest("malformed code_verifier")
	}
	hash := sha256Hex(*b.code)
	var cid, user, ws, redirect, challenge, resource, scope string
	var used, expired bool
	var grant *string
	err := h.Pool.QueryRow(ctx, `select client_id, user_id::text, workspace_id::text, redirect_uri, code_challenge, resource, scope,
			used_at is not null, expires_at < now(), grant_id::text from core.oauth_codes where code_hash = $1`, hash).
		Scan(&cid, &user, &ws, &redirect, &challenge, &resource, &scope, &used, &expired, &grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, invalidGrant("unknown or expired code")
	}
	if err != nil {
		return nil, err
	}
	if err := throttle("oauth:client:"+cid, 30, 1); err != nil {
		return nil, err
	}
	if used {
		// A replayed code means it leaked: revoke what it already produced.
		if grant != nil {
			if err := h.revokeGrant(ctx, *grant); err != nil {
				return nil, err
			}
		}
		return nil, invalidGrant("code already used")
	}
	if expired || cid != clientID || b.redirectURI == nil || *b.redirectURI != redirect {
		return nil, invalidGrant("code, client or redirect_uri mismatch")
	}
	sum := sha256.Sum256([]byte(*b.verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		return nil, invalidGrant("PKCE verification failed")
	}
	if err := checkResource(b.resource, resource, base); err != nil {
		return nil, err
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Single-use: the conditional update is the arbiter if two exchanges race.
	tag, err := tx.Exec(ctx, "update core.oauth_codes set used_at = now() where code_hash = $1 and used_at is null", hash)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, invalidGrant("code already used")
	}
	var g string
	if err := tx.QueryRow(ctx, "insert into core.oauth_grants (client_id, user_id, workspace_id, resource, scope) values ($1,$2::uuid,$3::uuid,$4,$5) returning id::text",
		clientID, user, ws, resource, scope).Scan(&g); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update core.oauth_codes set grant_id = $2::uuid where code_hash = $1", hash, g); err != nil {
		return nil, err
	}
	out, err := issueTokens(ctx, tx, g, scope)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// Housekeeping for spent material, off the hot path of any one client.
	_, _ = h.Pool.Exec(ctx, "delete from core.oauth_codes where expires_at < now() - interval '1 day'")
	_, _ = h.Pool.Exec(ctx, "delete from core.oauth_tokens where expires_at < now() - interval '1 day'")
	return out, nil
}

func (h *Handler) refresh(ctx context.Context, base, clientID string, b tokenReq) (map[string]any, error) {
	if b.refreshToken == nil || !strings.HasPrefix(*b.refreshToken, "cnr_") {
		return nil, invalidRequest("refresh_token is required")
	}
	presented := *b.refreshToken
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var grant, cid, resource, scope, user, ws string
	var used, expired, revoked bool
	err = tx.QueryRow(ctx, `select t.grant_id::text, t.used_at is not null, t.expires_at < now(), g.client_id, g.resource,
			g.revoked_at is not null, g.scope, g.user_id::text, g.workspace_id::text
		from core.oauth_tokens t join core.oauth_grants g on g.id = t.grant_id
		where t.token_hash = $1 and t.kind = 'refresh' for update of t`, sha256Hex(presented)).
		Scan(&grant, &used, &expired, &cid, &resource, &revoked, &scope, &user, &ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, invalidGrant("unknown refresh token")
	}
	if err != nil {
		return nil, err
	}
	if cid != clientID || revoked {
		return nil, invalidGrant("refresh token is not valid for this client")
	}
	if err := throttle("oauth:client:"+cid, 30, 1); err != nil {
		return nil, err
	}
	if used {
		// Rotation means each refresh token works once; a second use is a stolen copy. Kill the whole family.
		if err := revokeGrantTx(ctx, tx, grant); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, invalidGrant("refresh token reuse detected; grant revoked")
	}
	if expired {
		return nil, invalidGrant("refresh token expired")
	}
	if err := checkResource(b.resource, resource, base); err != nil {
		return nil, err
	}
	// Whoever approved this grant must still belong to the workspace.
	if _, err := h.Auth.UserPrincipal(ctx, user, ws); err != nil {
		if err := revokeGrantTx(ctx, tx, grant); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, invalidGrant("the user is no longer a member of the workspace")
	}
	if _, err := tx.Exec(ctx, "update core.oauth_tokens set used_at = now() where token_hash = $1", sha256Hex(presented)); err != nil {
		return nil, err
	}
	// The previous access token dies with the refresh token that minted it.
	if _, err := tx.Exec(ctx, "delete from core.oauth_tokens where grant_id = $1::uuid and kind = 'access'", grant); err != nil {
		return nil, err
	}
	out, err := issueTokens(ctx, tx, grant, scope)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// revoke is RFC 7009: always 200, whether or not the token existed. Revoking a refresh token ends the whole grant;
// the caller must be the client the token was issued to.
func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	buf, ok := h.readBody(w, r, publicBodyLimit)
	if !ok {
		return
	}
	vals, valid := form(buf, "token", "client_id")
	if !valid || !isForm(r) {
		h.proxy(w, r, buf)
		return
	}
	if throttled(w, "oauth:revoke:"+clientIP(r), 30, 1) {
		return
	}
	token, hasT := vals["token"]
	clientID, hasC := vals["client_id"]
	if hasT && hasC {
		hash := sha256Hex(token)
		var grant, kind, cid string
		err := h.Pool.QueryRow(r.Context(), `select t.grant_id::text, t.kind, g.client_id from core.oauth_tokens t
			join core.oauth_grants g on g.id = t.grant_id where t.token_hash = $1`, hash).Scan(&grant, &kind, &cid)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			serverError(w, err)
			return
		case cid == clientID:
			if kind == "refresh" {
				err = h.revokeGrant(r.Context(), grant)
			} else {
				_, err = h.Pool.Exec(r.Context(), "delete from core.oauth_tokens where token_hash = $1", hash)
			}
			if err != nil {
				serverError(w, err)
				return
			}
		}
	}
	noStore(w)
	w.WriteHeader(http.StatusOK)
}
