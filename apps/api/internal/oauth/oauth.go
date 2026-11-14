// Package oauth is the OAuth 2.1 authorization server for MCP clients (crates/cnote-server/src/oauth.rs): metadata,
// dynamic client registration, consent endpoints, authorization code + PKCE S256, rotating refresh tokens with reuse
// detection and revocation. Codes and tokens are opaque; only SHA-256 hashes are stored (migration 0600), in the same
// tables and with the same hashing as Rust, so a token issued by either server is valid on both.
//
// Anything this package cannot answer byte for byte (bodies serde would reject, URIs the url crate might parse
// differently) is handed to Upstream before any side effect, like the command router does.
package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
)

const (
	accessTTLSecs   = 3600
	refreshTTLDays  = 30
	codeTTLSecs     = 300
	metadataTTLSecs = 3600
	metadataMax     = 16 * 1024
	maxRedirectURIs = 10
	publicBodyLimit = 32 * 1024 // DefaultBodyLimit on the machine endpoints
	jsonBodyLimit   = 2 << 20   // axum's default for the consent endpoints
)

type Handler struct {
	Pool     *pgxpool.Pool
	Auth     *auth.Auth
	Upstream http.Handler
}

// Handles reports whether the route belongs to the authorization server and is one this package serves; other
// methods on these paths (OPTIONS preflight, HEAD, a wrong verb) stay with Rust.
func Handles(r *http.Request) bool {
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server":
		return r.Method == http.MethodGet
	case "/oauth/register", "/oauth/token", "/oauth/revoke", "/api/oauth/authorize", "/api/oauth/authorize/context":
		return r.Method == http.MethodPost
	}
	return false
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		h.protectedResource(w, r)
	case "/.well-known/oauth-authorization-server":
		h.authorizationServer(w, r)
	case "/oauth/register":
		h.register(w, r)
	case "/oauth/token":
		h.token(w, r)
	case "/oauth/revoke":
		h.revoke(w, r)
	case "/api/oauth/authorize/context":
		h.authorizeContext(w, r)
	default:
		h.authorize(w, r)
	}
}

// ---------- responses ----------

func publicHeaders(w http.ResponseWriter) { w.Header().Set("Access-Control-Allow-Origin", "*") }

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := command.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// oerr is OauthError: an RFC 6749 error body, never cached.
func oerr(w http.ResponseWriter, status int, code, desc string) {
	noStore(w)
	writeJSON(w, status, map[string]any{"error": code, "error_description": desc})
}

func bad(w http.ResponseWriter, code, desc string) { oerr(w, http.StatusBadRequest, code, desc) }

func serverError(w http.ResponseWriter, err error) {
	slog.Error("oauth database error", "error", err)
	oerr(w, http.StatusInternalServerError, "server_error", "internal error")
}

func throttled(w http.ResponseWriter, key string, burst, perSec float64) bool {
	if command.CheckRateWith(key, burst, perSec) != nil {
		oerr(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many requests, retry shortly")
		return true
	}
	return false
}

// apiErr is ApiError: {"error":{code,message}} with the code's status.
func apiErr(w http.ResponseWriter, err error) {
	var ce *command.Error
	if !errors.As(err, &ce) {
		slog.Error("oauth internal error", "error", err)
		ce = command.NewError(command.Internal, "internal error")
	}
	writeJSON(w, ce.Code.Status(), map[string]any{"error": ce})
}

func baseOf(r *http.Request) (string, bool) { return auth.PublicBase(r.Host) }

func unavailable(w http.ResponseWriter) {
	oerr(w, http.StatusServiceUnavailable, "server_error", "OAuth is not available: the server has no PUBLIC_BASE_URL configured")
}

func jsonCached(w http.ResponseWriter, v any) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) protectedResource(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	base, ok := baseOf(r)
	if !ok {
		unavailable(w)
		return
	}
	jsonCached(w, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base},
		"bearer_methods_supported": []string{"header"}, "resource_name": "C Note"})
}

func (h *Handler) authorizationServer(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	base, ok := baseOf(r)
	if !ok {
		unavailable(w)
		return
	}
	web := base
	if v, set := os.LookupEnv("PUBLIC_WEB_URL"); set {
		web = strings.TrimRight(v, "/")
	}
	jsonCached(w, map[string]any{
		"issuer": base, "authorization_endpoint": web + "/oauth/authorize", "token_endpoint": base + "/oauth/token",
		"registration_endpoint": base + "/oauth/register", "revocation_endpoint": base + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"}, "client_id_metadata_document_supported": true,
		"authorization_response_iss_parameter_supported": true,
	})
}

// ---------- request parsing (anything serde would reject goes to Rust) ----------

func isJSON(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "application/json" || (strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json")) // axum json_content_type
}

func isForm(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "application/x-www-form-urlencoded"
}

// readBody reads up to limit bytes; ok=false (body already consumed, replayed to Rust) when it is larger.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request, limit int) ([]byte, bool) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil || len(buf) > limit {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		h.Upstream.ServeHTTP(w, r)
		return nil, false
	}
	return buf, true
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.Upstream.ServeHTTP(w, r)
}

var surrogateEsc = regexp.MustCompile(`(?i)\\u[d][89a-f]`)

// jsonObject parses a flat-enough JSON object. ok=false for anything but a well-formed object without duplicate
// top-level keys or surrogate escapes (serde_json rejects those with its own message).
func jsonObject(b []byte) (map[string]json.RawMessage, bool) {
	if !utf8.Valid(b) || surrogateEsc.Match(b) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	o := map[string]json.RawMessage{}
	for dec.More() {
		k, err := dec.Token()
		ks, isStr := k.(string)
		if err != nil || !isStr {
			return nil, false
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, false
		}
		if _, dup := o[ks]; dup {
			return nil, false
		}
		o[ks] = v
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return o, true
}

type fields struct {
	o  map[string]json.RawMessage
	ok bool
}

// str is a `String` (or `Option<String>`, nullOK) field: absent gives "", anything but a string is a serde error.
func (f *fields) str(k string, nullOK bool) (string, bool) {
	raw, has := f.o[k]
	if !has {
		return "", false
	}
	if string(raw) == "null" && nullOK {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		f.ok = false
		return "", false
	}
	return s, true
}

func (f *fields) strList(k string, nullOK bool) ([]string, bool) {
	raw, has := f.o[k]
	if !has {
		return nil, false
	}
	if string(raw) == "null" && nullOK {
		return nil, false
	}
	var l []string
	if json.Unmarshal(raw, &l) != nil || l == nil {
		f.ok = false
		return nil, false
	}
	return l, true
}

func (f *fields) boolean(k string) bool {
	var v bool
	if raw, has := f.o[k]; !has || json.Unmarshal(raw, &v) != nil {
		f.ok = false
	}
	return v
}

// form parses an urlencoded body; ok=false when serde_urlencoded might disagree (bad escapes, ';', repeated keys).
func form(b []byte, keys ...string) (map[string]string, bool) {
	if !utf8.Valid(b) {
		return nil, false
	}
	vals, err := url.ParseQuery(string(b))
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, k := range keys {
		if v := vals[k]; len(v) > 1 {
			return nil, false
		} else if len(v) == 1 {
			out[k] = v[0]
		}
	}
	return out, true
}

// ---------- helpers ----------

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func randomSecret(prefix string) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}

func cleanName(raw string) string {
	var b strings.Builder
	n := 0
	for _, r := range raw {
		if unicode.IsControl(r) {
			continue
		}
		if n == 80 {
			break
		}
		b.WriteRune(r)
		n++
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		return s
	}
	return "Unnamed app"
}

func canonicalResource(raw, base string) (string, bool) {
	r := strings.TrimRight(raw, "/")
	return r, r == base+"/mcp"
}

func clientIP(r *http.Request) string {
	if v := os.Getenv("TRUST_PROXY"); v == "1" || v == "true" {
		first := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
		if _, err := parseIP(first); err == nil {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

func parseIP(s string) (string, error) {
	if ip := net.ParseIP(s); ip != nil {
		return s, nil
	}
	return "", errors.New("not an ip")
}

// ---------- clients ----------

type client struct {
	id, name     string
	redirectURIs []string
}

func (h *Handler) loadClient(ctx context.Context, id string) (c client, fetched time.Time, found bool, err error) {
	err = h.Pool.QueryRow(ctx, "select client_id, name, redirect_uris, fetched_at from core.oauth_clients where client_id = $1", id).
		Scan(&c.id, &c.name, &c.redirectURIs, &fetched)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fetched, false, nil
	}
	return c, fetched, err == nil, err
}

// resolveClient: registered clients come from the table; https:// client IDs are metadata documents, re-fetched when stale.
func (h *Handler) resolveClient(ctx context.Context, id string) (client, string) {
	const dbErr = "server error"
	cached, fetched, found, err := h.loadClient(ctx, id)
	if err != nil {
		return client{}, dbErr
	}
	isDoc := strings.HasPrefix(id, "https://") || (strings.HasPrefix(id, "http://") && allowPrivate())
	if !isDoc {
		if !found {
			return client{}, "unknown client_id"
		}
		return cached, ""
	}
	if found && time.Since(fetched) < metadataTTLSecs*time.Second {
		return cached, ""
	}
	doc, msg := fetchClientDocument(ctx, id)
	if msg != "" {
		return client{}, msg
	}
	if _, err := h.Pool.Exec(ctx, `insert into core.oauth_clients (client_id, kind, name, redirect_uris) values ($1,'metadata',$2,$3)
		on conflict (client_id) do update set name = $2, redirect_uris = $3, fetched_at = now()`, id, doc.name, doc.redirectURIs); err != nil {
		return client{}, dbErr
	}
	return client{id: id, name: doc.name, redirectURIs: doc.redirectURIs}, ""
}

func fetchClientDocument(ctx context.Context, id string) (client, string) {
	u, err := url.Parse(id)
	if err != nil {
		return client{}, "invalid client_id URL"
	}
	if u.Path == "/" || u.Path == "" || u.Fragment != "" || strings.Contains(id, "#") {
		return client{}, "client_id URL must have a path and no fragment"
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if seg == "." || seg == ".." {
			return client{}, "client_id URL must not contain dot segments"
		}
	}
	doc, err := fetchJSON(ctx, id, metadataMax)
	if err != nil {
		return client{}, err.Error()
	}
	return parseClientDocument(id, doc)
}

func parseClientDocument(id string, v any) (client, string) {
	doc, _ := v.(map[string]any)
	if s, _ := doc["client_id"].(string); doc == nil || s != id {
		return client{}, "metadata client_id does not match its URL"
	}
	_, secret := doc["client_secret"]
	if m, has := doc["token_endpoint_auth_method"]; secret || (has && m != "none") {
		return client{}, "only public clients (token_endpoint_auth_method none) are supported"
	}
	arr, isArr := doc["redirect_uris"].([]any)
	if !isArr {
		return client{}, "metadata has no redirect_uris"
	}
	uris := make([]string, 0, len(arr))
	for _, x := range arr {
		s, isStr := x.(string)
		if !isStr {
			return client{}, "redirect_uris must be strings"
		}
		uris = append(uris, s)
	}
	if len(uris) == 0 || len(uris) > maxRedirectURIs {
		return client{}, "redirect_uris must hold 1 to 10 entries"
	}
	for _, u := range uris {
		if msg, _ := validateRedirectURI(u); msg != "" {
			return client{}, msg
		}
	}
	fallback := ""
	if u, err := url.Parse(id); err == nil {
		fallback = u.Hostname()
	}
	name := fallback
	if s, ok := doc["client_name"].(string); ok {
		name = s
	}
	return client{id: id, name: cleanName(name), redirectURIs: uris}, ""
}

// ---------- dynamic client registration ----------

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	buf, ok := h.readBody(w, r, publicBodyLimit)
	if !ok {
		return
	}
	o, valid := jsonObject(buf)
	f := &fields{o: o, ok: valid && isJSON(r)}
	if !f.ok {
		h.proxy(w, r, buf)
		return
	}
	name, _ := f.str("client_name", false)
	uris, _ := f.strList("redirect_uris", false)
	method, hasMethod := f.str("token_endpoint_auth_method", true)
	grants, hasGrants := f.strList("grant_types", true)
	resps, hasResps := f.strList("response_types", true)
	if !f.ok {
		h.proxy(w, r, buf)
		return
	}
	if _, ok := baseOf(r); !ok {
		unavailable(w)
		return
	}
	if throttled(w, "oauth:register:"+clientIP(r), 10, 0.1) {
		return
	}
	if hasMethod && method != "none" {
		bad(w, "invalid_client_metadata", "only token_endpoint_auth_method none is supported")
		return
	}
	subset := func(v []string, has bool, allowed ...string) bool {
		for _, x := range v {
			if has && !contains(allowed, x) {
				return false
			}
		}
		return true
	}
	if !subset(grants, hasGrants, "authorization_code", "refresh_token") || !subset(resps, hasResps, "code") {
		bad(w, "invalid_client_metadata", "unsupported grant_types or response_types")
		return
	}
	if len(uris) == 0 || len(uris) > maxRedirectURIs {
		bad(w, "invalid_redirect_uri", "provide 1 to 10 redirect_uris")
		return
	}
	for _, u := range uris {
		msg, sure := validateRedirectURI(u)
		if !sure {
			h.proxy(w, r, buf)
			return
		}
		if msg != "" {
			bad(w, "invalid_redirect_uri", msg)
			return
		}
	}
	name = cleanName(name)
	clientID := randomSecret("cnc_")
	if _, err := h.Pool.Exec(r.Context(), "insert into core.oauth_clients (client_id, kind, name, redirect_uris) values ($1,'registered',$2,$3)", clientID, name, uris); err != nil {
		serverError(w, err)
		return
	}
	// Abandoned registrations (never approved by anyone) do not pile up; cached metadata documents nobody approved go quickly.
	_, _ = h.Pool.Exec(r.Context(), `delete from core.oauth_clients c where c.kind = 'registered' and c.created_at < now() - interval '30 days'
		and not exists (select 1 from core.oauth_grants g where g.client_id = c.client_id)`)
	_, _ = h.Pool.Exec(r.Context(), `delete from core.oauth_clients c where c.kind = 'metadata' and c.fetched_at < now() - interval '1 day'
		and not exists (select 1 from core.oauth_grants g where g.client_id = c.client_id)`)
	noStore(w)
	writeJSON(w, http.StatusCreated, map[string]any{"client_id": clientID, "client_name": name, "redirect_uris": uris,
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
