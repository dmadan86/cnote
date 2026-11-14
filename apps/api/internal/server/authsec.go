package server

// Sign-in hardening (WP18): MFA step-up and enrolment endpoints and OpenID Connect SSO. Unlike the Rust-parity
// endpoints these never replay a body to Rust: a request the Go parser does not accept is simply invalid, so a
// quirky body cannot slip a password login past throttling and MFA.

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
)

// strictJSON fills the string fields (opts may be absent or null); anything else answers 400/413/415 here.
func (s *Server) strictJSON(w http.ResponseWriter, r *http.Request, strs map[string]*string, opts map[string]**string) bool {
	if !isJSON(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = io.WriteString(w, "Expected request with `Content-Type: application/json`")
		return false
	}
	if r.ContentLength > maxBody {
		tooLarge(w)
		return false
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	obj, ok := parseObject(buf)
	if err != nil || int64(len(buf)) > maxBody || !ok {
		s.writeErr(w, r, command.ErrInvalid("request body must be a JSON object"))
		return false
	}
	for k, dst := range strs {
		if obj[k] == nil || !isString(obj[k]) || json.Unmarshal(obj[k], dst) != nil {
			s.writeErr(w, r, command.ErrInvalid(k+" is required and must be a string"))
			return false
		}
	}
	for k, dst := range opts {
		if v := obj[k]; v != nil && string(v) != "null" {
			var x string
			if !isString(v) || json.Unmarshal(v, &x) != nil {
				s.writeErr(w, r, command.ErrInvalid(k+" must be a string"))
				return false
			}
			*dst = &x
		}
	}
	return true
}

// writeThrottle is writeErr plus Retry-After for a throttled caller.
func (s *Server) writeThrottle(w http.ResponseWriter, r *http.Request, err error) {
	if ce, ok := err.(*command.Error); ok && ce.Code == command.RateLimited {
		if d, ok := ce.Details.(map[string]int); ok {
			w.Header().Set("Retry-After", strconv.Itoa(d["retry_after"]))
		}
	}
	s.writeErr(w, r, err)
}

// loginResponse is the reply to a correct password (or a correct second factor).
func (s *Server) loginResponse(w http.ResponseWriter, r *http.Request, res auth.LoginResult) {
	if res.MFAToken != "" { // no session yet: a code is still owed
		out, _ := command.Marshal(map[string]any{"mfa_required": true, "mfa_token": res.MFAToken})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
		return
	}
	body := map[string]any{"user_id": res.UserID}
	if res.MFASetupRequired {
		body["mfa_setup_required"] = true
	}
	withSession(w, r, body, res.Token)
}

func (s *Server) authSecurity(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == http.MethodPost && path == "/api/auth/mfa/verify":
		s.mfaVerify(w, r)
	case r.Method == http.MethodGet && path == "/api/auth/mfa":
		s.mfaStatus(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/mfa/setup":
		s.mfaSetup(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/mfa/confirm":
		s.mfaConfirm(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/mfa/disable":
		s.mfaDisable(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/mfa/recovery":
		s.mfaRecovery(w, r)
	case r.Method == http.MethodGet && path == "/api/auth/sessions":
		s.sessionList(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/sessions/revoke":
		s.sessionRevoke(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/sessions/revoke_others":
		s.sessionRevokeOthers(w, r)
	case r.Method == http.MethodGet && path == "/api/auth/saml/start":
		s.samlStart(w, r)
	case r.Method == http.MethodGet && samlPath.MatchString(path) && strings.HasSuffix(path, "/metadata"):
		s.samlMetadata(w, r, samlPath.FindStringSubmatch(path)[1])
	case r.Method == http.MethodPost && samlPath.MatchString(path) && strings.HasSuffix(path, "/acs"):
		s.samlACS(w, r, samlPath.FindStringSubmatch(path)[1])
	case r.Method == http.MethodGet && path == "/api/auth/sso/start":
		s.ssoStart(w, r)
	case r.Method == http.MethodGet && path == "/api/auth/sso/callback":
		s.ssoCallback(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var tok, code string
	if !s.strictJSON(w, r, map[string]*string{"mfa_token": &tok, "code": &code}, nil) {
		return
	}
	uid, session, err := s.Auth.CompleteChallenge(r.Context(), tok, code)
	if err != nil {
		s.writeThrottle(w, r, err)
		return
	}
	s.loginResponse(w, r, auth.LoginResult{UserID: uid, Token: session})
}

// me returns the signed-in person for the MFA endpoints (session only; an API key cannot manage a person's factors).
func (s *Server) mfaUser(w http.ResponseWriter, r *http.Request) (auth.SessionUser, string, bool) {
	u, err := s.Auth.SessionUser(r.Context(), r.Header)
	tok, _ := auth.SessionToken(r.Header)
	if err != nil {
		s.writeErr(w, r, err)
		return u, "", false
	}
	return u, tok, true
}

// accountLevel is mfaUser for actions on the person's account itself (factors, new workspaces). An SSO session is
// vouched for by one workspace's provider, so it must not change anything that outlives that workspace.
func (s *Server) accountLevel(w http.ResponseWriter, r *http.Request) (auth.SessionUser, string, bool) {
	u, tok, ok := s.mfaUser(w, r)
	if ok && u.AMR == "sso" {
		s.writeErr(w, r, command.ErrForbidden("a single sign-on session cannot change account-level settings: sign in with your password"))
		return u, tok, false
	}
	return u, tok, ok
}

func (s *Server) mfaStatus(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.mfaUser(w, r)
	if !ok {
		return
	}
	on, err := s.Auth.MFAEnabled(r.Context(), u.ID)
	left := 0
	if err == nil && on {
		left, err = s.Auth.RecoveryRemaining(r.Context(), u.ID)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"enabled": on, "recovery_codes_left": left, "session": u.AMR})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) mfaSetup(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.accountLevel(w, r)
	if !ok {
		return
	}
	secret, uri, err := s.Auth.MFAStart(r.Context(), u.ID, u.Email)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"secret": secret, "otpauth_uri": uri})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) mfaConfirm(w http.ResponseWriter, r *http.Request) {
	var code string
	if !s.strictJSON(w, r, map[string]*string{"code": &code}, nil) {
		return
	}
	u, tok, ok := s.accountLevel(w, r)
	if !ok {
		return
	}
	codes, err := s.Auth.MFAConfirm(r.Context(), u.ID, code)
	if err == nil {
		err = s.Auth.UpgradeSession(r.Context(), tok)
	}
	if err != nil {
		s.writeThrottle(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"recovery_codes": codes})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request) {
	var pw, code string
	if !s.strictJSON(w, r, map[string]*string{"password": &pw, "code": &code}, nil) {
		return
	}
	u, _, ok := s.accountLevel(w, r)
	if !ok {
		return
	}
	if err := s.Auth.MFADisable(r.Context(), u.ID, pw, code); err != nil {
		s.writeThrottle(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}

// ssoRedirectURI is where the provider returns the browser; it must be registered with the provider.
func (s *Server) ssoRedirectURI(r *http.Request) (string, bool) {
	base, ok := auth.PublicBase(r.Host)
	return base + "/api/auth/sso/callback", ok
}

func ssoCookie(v string, maxAge int) string {
	return auth.SSOCookie + "=" + v + "; Path=/api/auth/sso; HttpOnly; SameSite=Lax; Max-Age=" + strconv.Itoa(maxAge) + secureAttr()
}

// toLogin sends the browser back to the sign-in page with a message.
func toLogin(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Add("Set-Cookie", ssoCookie("", 0))
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	redirect, ok := s.ssoRedirectURI(r)
	if !ok {
		toLogin(w, r, "single sign-on is not available on this address")
		return
	}
	if err := command.CheckRateWith("sso:start:"+publiclinksIP(r), 20, 0.2); err != nil {
		toLogin(w, r, "too many attempts, try again later")
		return
	}
	if s.Auth.IsSAML(r.Context(), strings.TrimSpace(r.URL.Query().Get("workspace"))) {
		s.samlStart(w, r)
		return
	}
	to, cookie, err := s.Auth.OIDCStart(r.Context(), strings.TrimSpace(r.URL.Query().Get("workspace")), redirect)
	if err != nil {
		toLogin(w, r, errText(err))
		return
	}
	w.Header().Set("Set-Cookie", ssoCookie(cookie, 600))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound)
}

func errText(err error) string {
	if ce, ok := err.(*command.Error); ok {
		return ce.Message
	}
	return "single sign-on failed"
}

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	redirect, ok := s.ssoRedirectURI(r)
	c, cerr := r.Cookie(auth.SSOCookie)
	q := r.URL.Query()
	switch {
	case !ok || cerr != nil:
		toLogin(w, r, "the sign-in link is incomplete or expired")
		return
	case q.Get("error") != "":
		toLogin(w, r, "the identity provider refused the sign-in")
		return
	}
	_, _, token, err := s.Auth.OIDCFinish(r.Context(), c.Value, q.Get("state"), q.Get("code"), redirect)
	if err != nil {
		toLogin(w, r, errText(err))
		return
	}
	w.Header().Add("Set-Cookie", ssoCookie("", 0))
	w.Header().Add("Set-Cookie", sessionCookie(token, 30*86400))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusFound)
}

// mfaRecovery replaces the recovery codes: password and a current authenticator code, the old set stops working.
func (s *Server) mfaRecovery(w http.ResponseWriter, r *http.Request) {
	var pw, code string
	if !s.strictJSON(w, r, map[string]*string{"password": &pw, "code": &code}, nil) {
		return
	}
	u, _, ok := s.accountLevel(w, r)
	if !ok {
		return
	}
	codes, err := s.Auth.RegenerateRecovery(r.Context(), u.ID, pw, code)
	if err != nil {
		s.writeThrottle(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"recovery_codes": codes})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sessionList(w http.ResponseWriter, r *http.Request) {
	u, tok, ok := s.mfaUser(w, r)
	if !ok {
		return
	}
	list, err := s.Auth.ListSessions(r.Context(), u.ID, tok)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"sessions": list})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sessionRevoke(w http.ResponseWriter, r *http.Request) {
	var id string
	if !s.strictJSON(w, r, map[string]*string{"id": &id}, nil) {
		return
	}
	u, tok, ok := s.mfaUser(w, r)
	if !ok {
		return
	}
	if err := s.Auth.RevokeSession(r.Context(), u.ID, tok, id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}

func (s *Server) sessionRevokeOthers(w http.ResponseWriter, r *http.Request) {
	u, tok, ok := s.mfaUser(w, r)
	if !ok {
		return
	}
	n, err := s.Auth.RevokeOtherSessions(r.Context(), u.ID, tok)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"revoked": n})
	writeJSON(w, http.StatusOK, out)
}

// ---- SAML 2.0 (the IdP's administrator needs the metadata URL; the response comes back as a cross-site form POST, so it
// carries no cookie: the stored request id is what binds it to a sign-in this server started)

var samlPath = regexp.MustCompile(`^/api/auth/saml/([a-z0-9][a-z0-9-]{0,62})/(?:acs|metadata)$`)

func (s *Server) samlStart(w http.ResponseWriter, r *http.Request) {
	base, ok := auth.PublicBase(r.Host)
	if !ok {
		toLogin(w, r, "single sign-on is not available on this address")
		return
	}
	if err := command.CheckRateWith("sso:start:"+publiclinksIP(r), 20, 0.2); err != nil {
		toLogin(w, r, "too many attempts, try again later")
		return
	}
	to, err := s.Auth.SAMLStart(r.Context(), strings.TrimSpace(r.URL.Query().Get("workspace")), base)
	if err != nil {
		toLogin(w, r, errText(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *Server) samlMetadata(w http.ResponseWriter, r *http.Request, slug string) {
	base, ok := auth.PublicBase(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	doc, err := auth.SPMetadata(base, slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml; charset=utf-8")
	_, _ = w.Write(append([]byte(xml.Header), doc...))
}

func (s *Server) samlACS(w http.ResponseWriter, r *http.Request, slug string) {
	base, ok := auth.PublicBase(r.Host)
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if !ok || r.ParseForm() != nil || r.PostForm.Get("SAMLResponse") == "" {
		toLogin(w, r, "the sign-in response is incomplete or expired")
		return
	}
	_, _, token, err := s.Auth.SAMLFinish(r.Context(), slug, base, r.PostForm.Get("SAMLResponse"))
	if err != nil {
		toLogin(w, r, errText(err))
		return
	}
	w.Header().Add("Set-Cookie", sessionCookie(token, 30*86400))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
