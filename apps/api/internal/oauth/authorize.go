package oauth

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/cnote/api/internal/command"
)

type authzParams struct {
	clientID, redirectURI, responseType, challenge, method, resource string
	state, scope                                                     *string
}

type validated struct {
	client      client
	redirectURI string
	challenge   string
	resource    string
	state       *string
	scope       string
}

// authzErr is an authorization request problem. redirectTo is set only once client_id and redirect_uri are verified;
// before that the user must be shown the error instead (RFC 6749 4.1.2.1).
type authzErr struct {
	code, desc string
	redirectTo *string
}

var b64urlRe = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

func errorRedirect(redirectURI string, state *string, iss, code, desc string) *string {
	u, ok := plainURI(redirectURI)
	if !ok || !u.normalized() {
		return nil
	}
	pairs := [][2]string{{"error", code}, {"error_description", desc}, {"iss", iss}}
	if state != nil {
		pairs = append(pairs, [2]string{"state", *state})
	}
	s := withQuery(redirectURI, u, pairs)
	return &s
}

// errNeedsRust: the redirect URI is one whose WHATWG serialization we do not reproduce.
type needsRust struct{}

func (h *Handler) validateAuthz(r *http.Request, base string, p authzParams) (*validated, *authzErr, *needsRust) {
	fail := func(code, desc string) *authzErr { return &authzErr{code: code, desc: desc} }
	if p.clientID == "" || len(p.clientID) > 2048 {
		return nil, fail("invalid_request", "client_id is required"), nil
	}
	c, msg := h.resolveClient(r.Context(), p.clientID)
	if msg != "" {
		return nil, fail("invalid_request", msg), nil
	}
	if !contains(c.redirectURIs, p.redirectURI) { // exact string match against the registered URIs
		return nil, fail("invalid_request", "redirect_uri does not match a registered redirect URI"), nil
	}
	if u, ok := plainURI(p.redirectURI); !ok || !u.normalized() {
		return nil, nil, &needsRust{}
	}
	redirectable := func(code, desc string) *authzErr {
		return &authzErr{code: code, desc: desc, redirectTo: errorRedirect(p.redirectURI, p.state, base, code, desc)}
	}
	switch {
	case p.responseType != "code":
		return nil, redirectable("unsupported_response_type", "response_type must be code"), nil
	case p.method != "S256":
		return nil, redirectable("invalid_request", "PKCE with code_challenge_method S256 is required"), nil
	case len(p.challenge) != 43 || !b64urlRe.MatchString(p.challenge):
		return nil, redirectable("invalid_request", "code_challenge must be a base64url SHA-256 digest"), nil
	case p.resource == "":
		return nil, redirectable("invalid_target", "resource parameter is required"), nil
	}
	resource, ok := canonicalResource(p.resource, base)
	if !ok {
		return nil, redirectable("invalid_target", "resource must be this server's MCP endpoint"), nil
	}
	scope := ""
	if p.scope != nil {
		scope = *p.scope
		if r := []rune(scope); len(r) > 200 {
			scope = string(r[:200])
		}
	}
	return &validated{client: c, redirectURI: p.redirectURI, challenge: p.challenge, resource: resource, state: p.state, scope: scope}, nil, nil
}

func authzJSON(w http.ResponseWriter, e *authzErr) {
	noStore(w)
	var to any
	if e.redirectTo != nil {
		to = *e.redirectTo
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": e.code, "description": e.desc, "redirect_to": to})
}

// parseAuthz reads the consent parameters; ok=false when serde would reject the body.
func parseAuthz(f *fields) authzParams {
	var p authzParams
	p.clientID, _ = f.str("client_id", false)
	p.redirectURI, _ = f.str("redirect_uri", false)
	p.responseType, _ = f.str("response_type", false)
	p.challenge, _ = f.str("code_challenge", false)
	p.method, _ = f.str("code_challenge_method", false)
	p.resource, _ = f.str("resource", false)
	if s, has := f.str("state", true); has {
		p.state = &s
	}
	if s, has := f.str("scope", true); has {
		p.scope = &s
	}
	return p
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// authorizeContext is the consent page's data: who is asking, where the browser returns, which workspaces to pick.
func (h *Handler) authorizeContext(w http.ResponseWriter, r *http.Request) {
	buf, ok := h.readBody(w, r, jsonBodyLimit)
	if !ok {
		return
	}
	o, valid := jsonObject(buf)
	f := &fields{o: o, ok: valid && isJSON(r)}
	var p authzParams
	if f.ok {
		p = parseAuthz(f)
	}
	if !f.ok {
		h.proxy(w, r, buf)
		return
	}
	user, err := h.Auth.SessionUser(r.Context(), r.Header)
	if err != nil {
		apiErr(w, err)
		return
	}
	base, ok := baseOf(r)
	if !ok {
		apiErr(w, command.NewError(command.Internal, "OAuth is not configured"))
		return
	}
	// Each call may trigger an outbound metadata fetch and a cache row, so it is bounded per user.
	if err := command.CheckRateWith("oauth:authz:"+user.ID, 20, 0.5); err != nil {
		apiErr(w, err)
		return
	}
	v, e, rust := h.validateAuthz(r, base, p)
	if rust != nil {
		h.proxy(w, r, buf)
		return
	}
	if e != nil {
		authzJSON(w, e)
		return
	}
	u, _ := plainURI(v.redirectURI)
	workspaces, err := h.Auth.UserWorkspaces(r.Context(), user.ID)
	if err != nil {
		apiErr(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "client_name": v.client.name, "redirect_host": u.hostLabel(), "workspaces": workspaces})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	buf, ok := h.readBody(w, r, jsonBodyLimit)
	if !ok {
		return
	}
	o, valid := jsonObject(buf)
	f := &fields{o: o, ok: valid && isJSON(r)}
	var p authzParams
	var wsID string
	var approve bool
	if f.ok {
		p = parseAuthz(f)
		wsID, _ = f.str("workspace_id", false)
		approve = f.boolean("approve")
		if !uuidRe.MatchString(wsID) {
			f.ok = false
		}
	}
	if !f.ok {
		h.proxy(w, r, buf)
		return
	}
	// Belt and braces next to JSON-only bodies: a cross-site page cannot trigger an approval.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		apiErr(w, command.ErrForbidden("cross-site request refused"))
		return
	}
	user, err := h.Auth.SessionUser(r.Context(), r.Header)
	if err != nil {
		apiErr(w, err)
		return
	}
	base, ok := baseOf(r)
	if !ok {
		apiErr(w, command.NewError(command.Internal, "OAuth is not configured"))
		return
	}
	if err := command.CheckRateWith("oauth:authz:"+user.ID, 20, 0.5); err != nil {
		apiErr(w, err)
		return
	}
	v, e, rust := h.validateAuthz(r, base, p)
	if rust != nil {
		h.proxy(w, r, buf)
		return
	}
	if e != nil {
		authzJSON(w, e)
		return
	}
	noStore(w)
	ru, _ := plainURI(v.redirectURI)
	if !approve {
		to := errorRedirect(v.redirectURI, v.state, base, "access_denied", "the user denied the request")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect_to": to})
		return
	}
	// Membership is re-checked here: the workspace id comes from the browser.
	if _, err := h.Auth.UserPrincipal(r.Context(), user.ID, strings.ToLower(wsID)); err != nil {
		w.Header().Del("Cache-Control")
		w.Header().Del("Pragma")
		apiErr(w, err)
		return
	}
	code := randomSecret("cnx_")
	if _, err := h.Pool.Exec(r.Context(), `insert into core.oauth_codes (code_hash, client_id, user_id, workspace_id, redirect_uri, code_challenge, resource, scope, expires_at)
		values ($1,$2,$3::uuid,$4::uuid,$5,$6,$7,$8, now() + make_interval(secs => $9))`,
		sha256Hex(code), v.client.id, user.ID, wsID, v.redirectURI, v.challenge, v.resource, v.scope, float64(codeTTLSecs)); err != nil {
		slog.Error("storing authorization code failed", "error", err)
		w.Header().Del("Cache-Control")
		w.Header().Del("Pragma")
		apiErr(w, command.NewError(command.Internal, "could not complete the authorization"))
		return
	}
	pairs := [][2]string{{"code", code}, {"iss", base}}
	if v.state != nil {
		pairs = append(pairs, [2]string{"state", *v.state})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect_to": withQuery(v.redirectURI, ru, pairs)})
}
