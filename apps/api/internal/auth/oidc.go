package auth

// OpenID Connect sign-in per workspace (authorization code flow, PKCE S256, state + nonce, RS256 ID tokens checked
// against the provider's JWKS). Every request to the provider goes through netguard (public https only). The
// per-login secrets (state, nonce, PKCE verifier) travel in a sealed, short-lived cookie bound to the browser, so
// no server state is needed. ES256/PS256 tokens are not supported (SAML is in saml.go).

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/netguard"
	"github.com/cnote/api/internal/secretbox"
)

const (
	SSOCookie    = "cn_sso"
	SAMLCookie   = "cn_saml"
	ssoStateTTL  = 10 * time.Minute
	maxIDPBody   = 1 << 20
	clockLeeway  = time.Minute
	ssoFailPrefx = "single sign-on failed: "
)

// Mailbox providers anyone can register on: an allow-list entry for one of these would let a workspace owner mint
// accounts for strangers' addresses, so they cannot be allowed.
var freeMail = []string{"gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "yahoo.com", "icloud.com", "proton.me", "protonmail.com", "aol.com"}

func ssoErr(why string) *command.Error {
	return command.NewError(command.Unauthenticated, ssoFailPrefx+why)
}

// SSOConfig is a workspace's provider settings, secret decrypted.
type SSOConfig struct {
	WorkspaceID, Issuer, ClientID, ClientSecret string
	AllowedDomains                              []string
}

// ValidDomain reports whether d may go on an allow-list.
func ValidDomain(d string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	return strings.Contains(d, ".") && !strings.ContainsAny(d, " @/") && !slices.Contains(freeMail, d)
}

func (a *Auth) ssoConfig(ctx context.Context, slug string) (SSOConfig, error) {
	var c SSOConfig
	var blob []byte
	err := a.Pool.QueryRow(ctx, "select workspace_id::text, issuer, client_id, client_secret_enc, allowed_domains from core.sso_by_slug($1)", slug).
		Scan(&c.WorkspaceID, &c.Issuer, &c.ClientID, &blob, &c.AllowedDomains)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, command.ErrNotFound("single sign-on for this workspace")
	}
	if err != nil {
		return c, command.DB(err)
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return c, err
	}
	c.ClientSecret, err = secretbox.Decrypt(key, c.WorkspaceID, blob)
	return c, err
}

// idpFetch calls the provider through the SSRF guard and returns the (size-capped) 200 body.
func idpFetch(ctx context.Context, method, u string, form url.Values, accept string) ([]byte, error) {
	if err := netguard.CheckEndpoint(ctx, u); err != nil {
		return nil, errors.New("identity provider address refused: " + err.Error())
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(cctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", accept)
	res, err := netguard.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(res.Body, maxIDPBody+1))
	if err != nil || len(buf) > maxIDPBody || res.StatusCode != 200 {
		return nil, errors.New("identity provider answered with an error")
	}
	return buf, nil
}

func idpDo(ctx context.Context, method, u string, form url.Values, dst any) error {
	buf, err := idpFetch(ctx, method, u, form, "application/json")
	if err != nil {
		return err
	}
	return json.Unmarshal(buf, dst)
}

type discovery struct {
	Issuer, AuthorizationEndpoint, TokenEndpoint, JWKSURI string
}

func discover(ctx context.Context, issuer string) (discovery, error) {
	var raw struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	if err := idpDo(ctx, http.MethodGet, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", nil, &raw); err != nil {
		return discovery{}, err
	}
	if raw.Issuer != issuer || raw.AuthorizationEndpoint == "" || raw.TokenEndpoint == "" || raw.JWKSURI == "" {
		return discovery{}, errors.New("discovery document does not match the configured issuer")
	}
	return discovery{raw.Issuer, raw.AuthorizationEndpoint, raw.TokenEndpoint, raw.JWKSURI}, nil
}

// ValidateIssuer is what saving a configuration checks: discovery works, through the guard, for exactly this issuer.
func ValidateIssuer(ctx context.Context, issuer string) error {
	_, err := discover(ctx, issuer)
	return err
}

type ssoState struct {
	Slug, State, Nonce, Verifier string
	Exp                          int64
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// OIDCStart returns the provider URL to send the browser to and the sealed cookie value to set.
func (a *Auth) OIDCStart(ctx context.Context, slug, redirectURI string) (string, string, error) {
	cfg, err := a.ssoConfig(ctx, slug)
	if err != nil {
		return "", "", err
	}
	d, err := discover(ctx, cfg.Issuer)
	if err != nil {
		return "", "", ssoErr(err.Error())
	}
	st := ssoState{Slug: slug, State: RandomToken(24), Nonce: RandomToken(24), Verifier: RandomToken(48), Exp: time.Now().Add(ssoStateTTL).Unix()}
	raw, _ := json.Marshal(st)
	key, err := secretbox.MasterKey()
	if err != nil {
		return "", "", err
	}
	blob, err := secretbox.Encrypt(key, cfg.WorkspaceID, string(raw))
	if err != nil {
		return "", "", err
	}
	ch := sha256.Sum256([]byte(st.Verifier))
	u, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return "", "", ssoErr("bad authorization endpoint")
	}
	q := u.Query()
	for k, v := range map[string]string{"response_type": "code", "client_id": cfg.ClientID, "redirect_uri": redirectURI, "scope": "openid email profile",
		"state": st.State, "nonce": st.Nonce, "code_challenge": b64u(ch[:]), "code_challenge_method": "S256"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), slug + "." + b64u(blob), nil
}

type jwk struct{ Kty, Kid, N, E, Use, Alg string }

type claims struct {
	Iss           string          `json:"iss"`
	Sub           string          `json:"sub"`
	Aud           json.RawMessage `json:"aud"`
	Azp           string          `json:"azp"`
	Exp           float64         `json:"exp"`
	Nbf           float64         `json:"nbf"`
	Nonce         string          `json:"nonce"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	Name          string          `json:"name"`
}

func (c claims) audiences() []string {
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(c.Aud, &many)
	return many
}

func (c claims) verified() bool {
	s := strings.Trim(string(c.EmailVerified), `"`)
	return s == "true"
}

// verifyIDToken checks an RS256 compact JWS against the keys, then iss, aud, exp, nbf and nonce.
func verifyIDToken(tok string, keys []jwk, issuer, clientID, nonce string, now time.Time) (claims, error) {
	var c claims
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return c, errors.New("malformed id token")
	}
	var hdr struct{ Alg, Kid string }
	hb, e1 := base64.RawURLEncoding.DecodeString(parts[0])
	pb, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	sig, e3 := base64.RawURLEncoding.DecodeString(parts[2])
	if e1 != nil || e2 != nil || e3 != nil || json.Unmarshal(hb, &hdr) != nil || json.Unmarshal(pb, &c) != nil {
		return c, errors.New("malformed id token")
	}
	if hdr.Alg != "RS256" { // never trust the header to pick "none" or an HMAC with the public key
		return c, errors.New("unsupported id token algorithm")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	good := false
	for _, k := range keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (hdr.Kid != "" && k.Kid != hdr.Kid) {
			continue
		}
		n, e4 := base64.RawURLEncoding.DecodeString(k.N)
		e, e5 := base64.RawURLEncoding.DecodeString(k.E)
		if e4 != nil || e5 != nil || len(e) > 4 || len(n) < 256 { // 2048-bit keys at least
			continue
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil {
			good = true
			break
		}
	}
	if !good {
		return c, errors.New("id token signature is not valid")
	}
	if c.Iss != issuer {
		return c, errors.New("id token issuer does not match")
	}
	aud := c.audiences()
	if !slices.Contains(aud, clientID) || (len(aud) > 1 && c.Azp != clientID) {
		return c, errors.New("id token audience does not match")
	}
	if c.Exp == 0 || now.After(time.Unix(int64(c.Exp), 0).Add(clockLeeway)) {
		return c, errors.New("id token has expired")
	}
	if c.Nbf != 0 && now.Before(time.Unix(int64(c.Nbf), 0).Add(-clockLeeway)) {
		return c, errors.New("id token is not valid yet")
	}
	if c.Nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return c, errors.New("id token nonce does not match")
	}
	return c, nil
}

// OIDCFinish completes the login: cookie + returned state + code -> verified identity -> session token. It returns the
// workspace id (SSO sessions are pinned to it) with the user id and the session token.
func (a *Auth) OIDCFinish(ctx context.Context, cookie, state, code, redirectURI string) (userID, ws, token string, err error) {
	slug, blob, ok := strings.Cut(cookie, ".")
	if !ok || state == "" || code == "" {
		return "", "", "", ssoErr("the sign-in link is incomplete or expired")
	}
	cfg, err := a.ssoConfig(ctx, slug)
	if err != nil {
		return "", "", "", ssoErr("single sign-on is not set up for this workspace")
	}
	raw, derr := base64.RawURLEncoding.DecodeString(blob)
	key, kerr := secretbox.MasterKey()
	if kerr != nil {
		return "", "", "", kerr
	}
	var st ssoState
	plain, derr2 := secretbox.Decrypt(key, cfg.WorkspaceID, raw)
	if derr != nil || derr2 != nil || json.Unmarshal([]byte(plain), &st) != nil || st.Slug != slug || time.Now().Unix() > st.Exp {
		return "", "", "", ssoErr("the sign-in link is incomplete or expired")
	}
	if subtle.ConstantTimeCompare([]byte(st.State), []byte(state)) != 1 {
		return "", "", "", ssoErr("state does not match")
	}
	d, err := discover(ctx, cfg.Issuer)
	if err != nil {
		return "", "", "", ssoErr(err.Error())
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := idpDo(ctx, http.MethodPost, d.TokenEndpoint, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "code_verifier": {st.Verifier}}, &tr); err != nil {
		return "", "", "", ssoErr(err.Error())
	}
	var set struct{ Keys []jwk }
	if err := idpDo(ctx, http.MethodGet, d.JWKSURI, nil, &set); err != nil {
		return "", "", "", ssoErr(err.Error())
	}
	c, err := verifyIDToken(tr.IDToken, set.Keys, cfg.Issuer, cfg.ClientID, st.Nonce, time.Now())
	if err != nil {
		return "", "", "", ssoErr(err.Error())
	}
	email := NormEmail(c.Email)
	_, dom, hasAt := strings.Cut(email, "@")
	switch {
	case !hasAt || dom == "":
		return "", "", "", ssoErr("the provider sent no email address")
	case !c.verified():
		return "", "", "", ssoErr("your email is not verified with the provider")
	case !slices.Contains(cfg.AllowedDomains, dom):
		return "", "", "", ssoErr("your email domain is not allowed for this workspace")
	}
	userID, err = a.ssoMember(ctx, cfg.WorkspaceID, cfg.Issuer, email, c.Name, c.Sub)
	if err != nil {
		return "", "", "", err
	}
	token, err = a.createSession(ctx, userID, "sso", cfg.WorkspaceID)
	return userID, cfg.WorkspaceID, token, err
}

// ssoMember resolves the person for a verified identity and makes sure they are a member (role member when new to the
// workspace). The (issuer, sub) pair is the identity: once linked, the same sub always signs in as the same account, even
// if the email changed, and a different sub claiming a linked account's email is refused. A person created here has no
// password. Only domains the workspace has proven it owns reach this point.
func (a *Auth) ssoMember(ctx context.Context, ws, issuer, email, name, sub string) (string, error) {
	if sub == "" {
		return "", ssoErr("the provider sent no subject identifier")
	}
	tx, err := db.BeginTenant(ctx, a.Pool, ws)
	if err != nil {
		return "", command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var uid string
	linked := true
	err = tx.QueryRow(ctx, "select user_id::text from core.sso_identities where workspace_id = $1::uuid and issuer = $2 and sub = $3", ws, issuer, sub).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		linked = false
		if name = strings.TrimSpace(name); name == "" {
			name, _, _ = strings.Cut(email, "@")
		}
		if err = tx.QueryRow(ctx, "insert into core.users (email, name) values ($1,$2) on conflict (email) do update set email = excluded.email returning id::text", email, name).Scan(&uid); err != nil {
			return "", command.DB(err)
		}
		var other string
		err = tx.QueryRow(ctx, "select sub from core.sso_identities where workspace_id = $1::uuid and issuer = $2 and user_id = $3::uuid", ws, issuer, uid).Scan(&other)
		if err == nil {
			return "", ssoErr("this account is already linked to a different identity at the provider")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", command.DB(err)
		}
	} else if err != nil {
		return "", command.DB(err)
	}
	var deactivated *time.Time
	jit := false
	err = tx.QueryRow(ctx, "select deactivated_at from core.memberships where user_id = $1::uuid", uid).Scan(&deactivated)
	if errors.Is(err, pgx.ErrNoRows) {
		jit = true
		_, err = tx.Exec(ctx, "insert into core.memberships (workspace_id, user_id, role) values ($1::uuid, $2::uuid, 'member')", ws, uid)
	} else if err == nil && deactivated != nil {
		return "", ssoErr("your account in this workspace is deactivated")
	}
	if err != nil {
		return "", command.DB(err)
	}
	if !linked {
		if _, err := tx.Exec(ctx, "insert into core.sso_identities (workspace_id, issuer, sub, user_id) values ($1::uuid,$2,$3,$4::uuid)", ws, issuer, sub, uid); err != nil {
			return "", command.DB(err)
		}
	}
	payload, _ := json.Marshal(map[string]any{"user_id": uid, "jit": jit, "linked_now": !linked, "sub": sub})
	if _, err := tx.Exec(ctx, "insert into core.events (workspace_id, kind, actor_type, actor_id, payload) values ($1::uuid, 'auth.sso_login', 'system', null, $2)", ws, json.RawMessage(payload)); err != nil {
		return "", command.DB(err)
	}
	return uid, command.DB(tx.Commit(ctx))
}
