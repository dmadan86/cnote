package server

// The non-command HTTP surface of crates/cnote-server/src/api.rs and marketplace.rs: auth, workspaces, the command
// catalog, the marketplace token exchange and the GitHub webhook. Anything axum would reject before the handler
// runs (non-JSON content type, malformed or mistyped body, odd UUIDs) is replayed to Rust, so Rust words those
// errors itself; the happy paths and every authentication decision are native.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/cnote/api/internal/mcp"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/admin"
	"github.com/cnote/api/internal/modules/marketplace"
)

// sessionCookie is api.rs session_cookie, byte for byte (COOKIE_SECURE=1|true adds Secure).
func sessionCookie(token string, maxAge int) string {
	secure := ""
	if v := os.Getenv("COOKIE_SECURE"); v == "1" || v == "true" {
		secure = "; Secure"
	}
	return auth.SessionCookie + "=" + token + "; Path=/; HttpOnly; SameSite=Lax; Max-Age=" + strconv.Itoa(maxAge) + secure
}

// authSurface serves the request when it is one of this surface's routes and reports whether it did.
func (s *Server) authSurface(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.EscapedPath()
	switch {
	case r.Method == http.MethodPost && path == "/api/auth/signup":
		s.signup(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/login":
		s.login(w, r)
	case r.Method == http.MethodPost && path == "/api/auth/logout":
		s.logout(w, r)
	case r.Method == http.MethodGet && path == "/api/auth/me":
		s.me(w, r)
	case r.Method == http.MethodPost && path == "/api/workspaces":
		s.createWorkspace(w, r)
	case r.Method == http.MethodGet && path == "/api/commands":
		s.catalog(w)
	case r.Method == http.MethodPost && path == "/api/marketplace/token":
		s.marketToken(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/hooks/github/") && wsRe.MatchString(path[len("/api/hooks/github/"):]):
		s.githubHook(w, r, path[len("/api/hooks/github/"):])
	default:
		return false
	}
	return true
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce *command.Error
	if !errors.As(err, &ce) {
		slog.Error("internal error", "path", r.URL.Path, "request_id", w.Header().Get("X-Request-Id"), "error", err)
		ce = command.NewError(command.Internal, "internal error") // Rust echoes the cause here; logs carry it instead
	}
	body, _ := command.Marshal(map[string]any{"error": ce})
	writeJSON(w, ce.Code.Status(), body)
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.Upstream.ServeHTTP(w, r)
}

// surrogateOrNUL: escapes serde rejects (lone surrogates) or Postgres cannot store (NUL); let Rust answer.
var surrogateOrNUL = regexp.MustCompile(`(?i)\\u(d[89a-f]..|0000)`)

// readBody reads at most limit bytes. When the body is longer it is proxied untouched (or refused with axum's
// 413 when the declared size is known) and ok is false.
func (s *Server) readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if r.ContentLength > limit {
		tooLarge(w)
		return nil, false
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(buf)) > limit {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		s.Upstream.ServeHTTP(w, r)
		return nil, false
	}
	return buf, true
}

// jsonObject is the axum `Json<T>` extractor for a struct of string fields: a JSON content type and a body serde
// would accept. Field names match exactly (serde, unlike encoding/json, is case sensitive), unknown fields are
// ignored, duplicates are an error. On anything else the request goes to Rust and ok is false.
func (s *Server) jsonObject(w http.ResponseWriter, r *http.Request, limit int64, strs map[string]*string, opts map[string]**string) ([]byte, bool) {
	if !isJSON(r) {
		// axum's Json extractor refuses before reading the body; answering here keeps a big upload from
		// reaching a server that hangs up on it
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = io.WriteString(w, "Expected request with `Content-Type: application/json`")
		return nil, false
	}
	buf, ok := s.readBody(w, r, limit)
	if !ok {
		return nil, false
	}
	if obj, ok := parseObject(buf); ok {
		good := true
		for k, dst := range strs {
			good = good && (obj[k] != nil && isString(obj[k]) && json.Unmarshal(obj[k], dst) == nil)
		}
		for k, dst := range opts {
			if v := obj[k]; v != nil && string(v) != "null" {
				var x string
				good = good && isString(v) && json.Unmarshal(v, &x) == nil
				*dst = &x
			}
		}
		if good {
			return buf, true
		}
	}
	s.replay(w, r, buf)
	return nil, false
}

func isString(b json.RawMessage) bool { return len(b) > 0 && b[0] == '"' }

// parseObject splits a JSON object into its members. Not ok for anything serde_json would not take as an object
// with distinct keys: invalid UTF-8, trailing data, duplicate keys, nesting beyond serde's limit, escapes above.
func parseObject(buf []byte) (map[string]json.RawMessage, bool) {
	if !utf8.Valid(buf) || surrogateOrNUL.Match(buf) || depth(buf) > 100 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	obj := map[string]json.RawMessage{}
	for dec.More() {
		k, err := dec.Token()
		key, isStr := k.(string)
		var v json.RawMessage
		if err != nil || !isStr || dec.Decode(&v) != nil {
			return nil, false
		}
		if _, dup := obj[key]; dup {
			return nil, false
		}
		obj[key] = v
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return obj, true
}

func depth(b []byte) int {
	d, max, in, esc := 0, 0, false, false
	for _, c := range b {
		switch {
		case esc:
			esc = false
		case in && c == '\\':
			esc = true
		case c == '"':
			in = !in
		case !in && (c == '{' || c == '['):
			if d++; d > max {
				max = d
			}
		case !in && (c == '}' || c == ']'):
			d--
		}
	}
	return max
}

// withSession sets the cookie and, for native clients (x-cnote-client: native), also returns the token (`cs_` prefix).
func withSession(w http.ResponseWriter, r *http.Request, body map[string]any, token string) {
	if r.Header.Get("X-Cnote-Client") == "native" {
		body["token"] = "cs_" + token
	}
	out, _ := command.Marshal(body)
	w.Header().Set("Set-Cookie", sessionCookie(token, 30*86400))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var email, name, password string
	var ws *string
	if _, ok := s.jsonObject(w, r, maxBody, map[string]*string{"email": &email, "name": &name, "password": &password}, map[string]**string{"workspace_name": &ws}); !ok {
		return
	}
	userID, err := s.Auth.SignUp(r.Context(), email, name, password)
	var wsID *string
	if err == nil && ws != nil && strings.TrimSpace(*ws) != "" {
		var id string
		if id, err = s.Auth.CreateWorkspace(r.Context(), userID, *ws, admin.Slugify(*ws)); err == nil {
			wsID = &id
		}
	}
	var token string
	if err == nil {
		token, err = s.Auth.CreateSession(r.Context(), userID)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	withSession(w, r, map[string]any{"user_id": userID, "workspace_id": wsID}, token)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var email, password string
	if _, ok := s.jsonObject(w, r, maxBody, map[string]*string{"email": &email, "password": &password}, nil); !ok {
		return
	}
	userID, token, err := s.Auth.SignIn(r.Context(), email, password)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	withSession(w, r, map[string]any{"user_id": userID}, token)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if t, present := auth.SessionToken(r.Header); present {
		if err := s.Auth.DestroySession(r.Context(), t); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	w.Header().Set("Set-Cookie", sessionCookie("", 0))
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, err := s.Auth.SessionUser(r.Context(), r.Header)
	var wss []auth.WorkspaceRef
	if err == nil {
		wss, err = s.Auth.UserWorkspaces(r.Context(), u.ID)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	// maps, not structs: serde_json::Value sorts keys
	list := make([]map[string]string, len(wss))
	for i, x := range wss {
		list[i] = map[string]string{"id": x.ID, "name": x.Name, "slug": x.Slug, "role": x.Role}
	}
	out, _ := command.Marshal(map[string]any{"user": map[string]string{"id": u.ID, "email": u.Email, "name": u.Name}, "workspaces": list})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var name string
	if _, ok := s.jsonObject(w, r, maxBody, map[string]*string{"name": &name}, nil); !ok {
		return
	}
	u, err := s.Auth.SessionUser(r.Context(), r.Header)
	var id string
	if err == nil {
		id, err = s.Auth.CreateWorkspace(r.Context(), u.ID, name, admin.Slugify(name))
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, _ := command.Marshal(map[string]any{"id": id})
	writeJSON(w, http.StatusOK, out)
}

// catalog is GET /api/commands. Rust's list (descriptions and schemars input schemas) is the embedded snapshot;
// commands that exist only in Go are added from their MCP metadata, so the list stays sorted by name.
func (s *Server) catalog(w http.ResponseWriter) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(mcp.RustCatalog, &rows); err != nil {
		panic(err) // embedded file
	}
	known := map[string]bool{}
	for _, row := range rows {
		var n string
		_ = json.Unmarshal(row["name"], &n)
		known[n] = true
	}
	added := false
	for _, c := range s.Registry.List() {
		if known[c.Name] || c.Description == "" || c.Schema == nil {
			continue
		}
		part := func(v any) json.RawMessage { b, _ := command.Marshal(v); return b }
		rows = append(rows, map[string]json.RawMessage{"name": part(c.Name), "description": part(c.Description), "group": part(string(c.Group)),
			"min_role": part(c.MinRole.String()), "confirm": part(c.Confirm), "core": []byte("false"), "input_schema": c.Schema})
		added = true
	}
	if !added {
		writeJSON(w, http.StatusOK, mcp.RustCatalog)
		return
	}
	name := func(i int) string { var n string; _ = json.Unmarshal(rows[i]["name"], &n); return n }
	sort.Slice(rows, func(i, j int) bool { return name(i) < name(j) })
	out, _ := command.Marshal(rows)
	writeJSON(w, http.StatusOK, out)
}

// oauthClientIP is oauth.rs client_ip: X-Forwarded-For (first entry, if an IP) only when TRUST_PROXY is 1|true.
func oauthClientIP(r *http.Request) string {
	if v := os.Getenv("TRUST_PROXY"); v == "1" || v == "true" {
		first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
		if ip, err := netip.ParseAddr(strings.TrimSpace(first)); err == nil {
			return ip.String()
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// marketToken is POST /api/marketplace/token (4 KiB body limit, 20 burst then 1/s per client IP, one error for
// every failure).
func (s *Server) marketToken(w http.ResponseWriter, r *http.Request) {
	var id, secret, code string
	raw, ok := s.jsonObject(w, r, 4<<10, map[string]*string{"client_id": &id, "client_secret": &secret, "code": &code}, nil)
	if !ok {
		return
	}
	if command.CheckRateWith("market:token:ip:"+oauthClientIP(r), 20, 1) != nil {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if !wsRe.MatchString(id) {
		// Rust parses other UUID spellings; let it decide (it also counts this request against its own bucket)
		s.replay(w, r, raw)
		return
	}
	out, ok, err := marketplace.ExchangeToken(r.Context(), s.Pool, id, secret, code)
	switch {
	case err != nil:
		slog.Error("marketplace token", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
	case !ok:
		writeJSON(w, http.StatusBadRequest, []byte(`{"error":"invalid_grant"}`))
	default:
		body, _ := command.Marshal(out)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, body)
	}
}

// githubHook is POST /api/hooks/github/{ws} (cnote-projects github.rs): signature first, then evidence.record as
// the system principal. A body that is not JSON after a valid signature goes to Rust for its exact error text.
func (s *Server) githubHook(w http.ResponseWriter, r *http.Request, ws string) {
	body, ok := s.readBody(w, r, maxBody)
	if !ok {
		return
	}
	var secret *string
	if err := s.workspaceSecret(r, ws, &secret); err != nil {
		s.writeErr(w, r, err)
		return
	}
	unauth := func(m string) { s.writeErr(w, r, command.NewError(command.Unauthenticated, m)) }
	if secret == nil || *secret == "" {
		unauth("github webhook is not configured for this workspace")
		return
	}
	if !signatureOK(*secret, r.Header.Get("X-Hub-Signature-256"), body) {
		unauth("invalid webhook signature")
		return
	}
	var payload any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if !utf8.Valid(body) || surrogateOrNUL.Match(body) || depth(body) > 100 || dec.Decode(&payload) != nil || dec.More() {
		s.replay(w, r, body)
		return
	}
	kind, keys, url := interpret(payload)
	switch {
	case kind == "":
		writeJSON(w, http.StatusOK, []byte(`{"ignored":true}`))
	case len(keys) == 0:
		writeJSON(w, http.StatusOK, []byte(`{"ignored":true,"reason":"no item keys found"}`))
	default:
		in, _ := command.Marshal(map[string]any{"source": "github", "kind": kind, "item_keys": keys, "url": url})
		out, err := s.Registry.Execute(r.Context(), s.Pool, "evidence.record", in, command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Owner}, command.ExecOptions{})
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// path walks objects like github.rs `s`: anything missing or not a string is "".
func path(v any, keys ...string) string {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[k]
	}
	str, _ := v.(string)
	return str
}

// interpret maps a webhook payload to (evidence kind, item keys, url); kind "" means not something we act on.
func interpret(p any) (string, []string, string) {
	m, _ := p.(map[string]any)
	if pr, has := m["pull_request"]; has {
		pm, _ := pr.(map[string]any)
		merged, _ := pm["merged"].(bool)
		var kind string
		switch a := path(p, "action"); {
		case a == "opened" || a == "reopened" || a == "ready_for_review":
			kind = "pr_opened"
		case a == "closed" && merged:
			kind = "pr_merged"
		default:
			return "", nil, ""
		}
		return kind, extractKeys(path(pr, "title"), path(pr, "head", "ref"), path(pr, "body")), path(pr, "html_url")
	}
	if _, has := m["deployment_status"]; has && path(p, "deployment_status", "state") == "success" {
		keys := extractKeys(path(p, "deployment", "ref"), path(p, "deployment", "description"), path(p, "deployment_status", "description"))
		url := path(p, "deployment_status", "target_url")
		if url == "" {
			url = path(p, "deployment_status", "log_url")
		}
		return "deploy_succeeded", keys, url
	}
	return "", nil, ""
}

// isWordRune is the regex crate's \w (Alphabetic, M, Nd, Pc, Join_Control); Go's \b is ASCII-only, so
// extractKeys scans by hand to keep Rust's Unicode word boundaries.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_Alphabetic, r) || unicode.IsMark(r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || r == '‌' || r == '‍'
}

// alnum is `[a-z0-9]` under (?i) with Unicode folding: also U+017F (long s) and U+212A (Kelvin).
func alnum(r rune, first bool) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == 'ſ' || r == 'K' || !first && r >= '0' && r <= '9'
}

// extractKeys finds `(?i)\b([a-z][a-z0-9]{0,9})-(\d+)\b` (\d is any Nd digit) and returns the matches uppercased,
// deduplicated and sorted.
func extractKeys(texts ...string) []string {
	set := map[string]bool{}
	for _, t := range texts {
		rs := []rune(t)
		for i := 0; i < len(rs); {
			if !alnum(rs[i], true) || i > 0 && isWordRune(rs[i-1]) {
				i++
				continue
			}
			j := i
			for j < len(rs) && alnum(rs[j], j == i) {
				j++
			}
			k := j + 1
			for k < len(rs) && unicode.Is(unicode.Nd, rs[k]) {
				k++
			}
			if j-i > 10 || j >= len(rs) || rs[j] != '-' || k == j+1 || k < len(rs) && isWordRune(rs[k]) {
				i++
				continue
			}
			set[strings.ToUpper(string(rs[i:j]))+"-"+string(rs[j+1:k])] = true
			i = k
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// workspaceSecret reads settings.github_webhook_secret inside the workspace's tenant (nil when unset or no such workspace).
func (s *Server) workspaceSecret(r *http.Request, ws string, out **string) error {
	tx, err := db.BeginTenant(r.Context(), s.Pool, ws)
	if err != nil {
		return command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()
	err = tx.QueryRow(r.Context(), "select settings->>'github_webhook_secret' from core.workspaces where id = $1::uuid", ws).Scan(out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return command.DB(err)
}

// signatureOK checks `sha256=<hex>` against HMAC-SHA256(secret, body) in constant time.
func signatureOK(secret, header string, body []byte) bool {
	h, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	got, err := hex.DecodeString(h)
	if !ok || err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
