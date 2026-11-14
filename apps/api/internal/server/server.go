// Package server is the HTTP front of the Go API: commands, auth, OAuth, MCP, public links and the empty 404/405
// answers of the old router are all served here. Nothing is proxied to Rust any more except /collab when COLLAB_URL
// is unset (development without the TypeScript collab service); with COLLAB_URL set, Upstream is never called.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mcp"
	"github.com/cnote/api/internal/modules/chat"
	"github.com/cnote/api/internal/modules/publiclinks"
	"github.com/cnote/api/internal/oauth"
	"github.com/cnote/api/internal/serdecheck"
	"github.com/cnote/api/internal/serdejson"
	"github.com/cnote/api/internal/uuidparse"
)

type Server struct {
	Pool     *pgxpool.Pool
	Registry *command.Registry
	Auth     *auth.Auth
	Upstream http.Handler
	// Collab, when set (COLLAB_URL), serves the /collab/ live-editing WebSocket instead of Upstream.
	Collab http.Handler
	// Public serves the unauthenticated share-link API under /api/public/ (token in the path, no session).
	Public http.Handler
	// OAuth serves the MCP authorization server (metadata, registration, consent, token, revoke).
	OAuth http.Handler
	// MCP serves POST /mcp natively; what it does not understand it proxies to Upstream.
	MCP http.Handler
	// Chat feeds GET /api/w/{ws}/chat/stream from Postgres NOTIFY.
	Chat *chat.Hub
}

func New(pool *pgxpool.Pool, reg *command.Registry, upstream http.Handler) *Server {
	a := &auth.Auth{Pool: pool}
	return &Server{Pool: pool, Registry: reg, Auth: a, Upstream: upstream, Public: publiclinks.NewHandler(pool, reg),
		OAuth: &oauth.Handler{Pool: pool, Auth: a, Upstream: upstream},
		MCP:   &mcp.Server{Pool: pool, Registry: reg, Auth: a, Upstream: upstream}, Chat: chat.NewHub(pool)}
}

var wsRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// maxBody matches axum's default Json limit; larger bodies get axum's exact 413 rejection.
const maxBody = 2 << 20

// ServeHTTP dispatches by hand instead of http.ServeMux: ServeMux redirects "unclean" paths (//, ..),
// which would make the proxy visibly different from Rust.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Request-Id", requestID(r))
	if s.authSurface(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok")
	case strings.HasPrefix(r.URL.Path, inquiryPrefix) || strings.HasPrefix(r.URL.Path, ticketPrefix):
		s.inquiry(w, r)
	case strings.HasPrefix(r.URL.Path, mailPrefix):
		s.ticketMail(w, r)
	case strings.HasPrefix(r.URL.Path, portalPrefix):
		s.portal(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/public/"):
		s.Public.ServeHTTP(w, r)
	case r.Method == http.MethodGet && chatStreamRe.MatchString(r.URL.EscapedPath()):
		s.chatStream(w, r, chatStreamRe.FindStringSubmatch(r.URL.EscapedPath())[1])
	case strings.HasPrefix(r.URL.EscapedPath(), "/api/w/") && isFileRoute(r):
		ws, id, _ := fileRoute(r)
		s.files(w, r, ws, id)
	case oauth.Handles(r):
		s.OAuth.ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.EscapedPath(), "/api/w/"):
		s.runCommand(w, r)
	case r.URL.Path == "/mcp":
		s.MCP.ServeHTTP(w, r)
	case s.Collab != nil && strings.HasPrefix(r.URL.Path, "/collab/"):
		slog.Debug("collab", "path", r.URL.Path)
		s.Collab.ServeHTTP(w, r)
	default:
		s.unmatched(w, r)
	}
}

// routeMethods are the other routes the Rust router has, with the methods axum lists in `Allow`. Anything else is
// the router's empty 404, a known path with another method its empty 405.
var routeMethods = map[string]string{
	"/healthz": "GET,HEAD", "/api/auth/signup": "POST", "/api/auth/login": "POST", "/api/auth/logout": "POST", "/api/auth/me": "GET,HEAD",
	"/api/workspaces": "POST", "/api/commands": "GET,HEAD", "/api/marketplace/token": "POST",
	"/.well-known/oauth-protected-resource": "GET,HEAD", "/.well-known/oauth-protected-resource/mcp": "GET,HEAD",
	"/.well-known/oauth-authorization-server": "GET,HEAD", "/oauth/register": "POST", "/oauth/token": "POST", "/oauth/revoke": "POST",
	"/api/oauth/authorize": "POST", "/api/oauth/authorize/context": "POST",
}

var corsPaths = map[string]bool{"/.well-known/oauth-protected-resource": true, "/.well-known/oauth-protected-resource/mcp": true,
	"/.well-known/oauth-authorization-server": true, "/oauth/register": true, "/oauth/token": true, "/oauth/revoke": true}

func (s *Server) unmatched(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	if allow, ok := routeMethods[path]; ok {
		if r.Method == http.MethodHead && strings.Contains(allow, "HEAD") { // axum answers HEAD from the GET handler
			r2 := r.Clone(r.Context())
			r2.Method = http.MethodGet
			s.ServeHTTP(w, r2)
			return
		}
		if corsPaths[path] { // tower-http's CorsLayer in front of the machine endpoints: any origin
			w.Header().Set("Access-Control-Allow-Origin", "*")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST")
				w.Header().Set("Access-Control-Allow-Headers", "content-type,authorization")
				w.Header().Set("Allow", allow)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		methodNotAllowed(w, allow)
		return
	}
	if strings.HasPrefix(path, "/api/hooks/github/") {
		methodNotAllowed(w, "POST") // POST with a valid workspace id is served by authSurface; this is any other verb
		return
	}
	if s.Upstream != nil && (strings.HasPrefix(path, "/collab/") || path == "/collab") {
		s.Upstream.ServeHTTP(w, r) // only without COLLAB_URL: the TypeScript collab service is the production path
		return
	}
	notFound(w)
}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestID echoes a sane client-supplied X-Request-Id, else makes one, so a failure report can be traced in logs
// (CN-QA-001 API-013). Rust sends none; the header is additive and bodies stay byte-identical.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); requestIDRe.MatchString(id) {
		return id
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// commandRoute splits /api/w/{ws}/commands/{name} like axum's matcher: exactly four segments after /api/w/ ...
// {ws} may be empty (and then fails to parse as a UUID), {name} may not.
func commandRoute(r *http.Request) (ws, name string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/w/"), "/")
	if len(parts) != 3 || parts[1] != "commands" || parts[2] == "" {
		return "", "", false
	}
	w, err1 := url.PathUnescape(parts[0])
	n, err2 := url.PathUnescape(parts[2])
	if err1 != nil || err2 != nil {
		return "", "", false
	}
	return w, n, true
}

func plain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// notFound / methodNotAllowed are axum's empty router answers.
func notFound(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// POST /api/w/{ws}/commands/{name}: the same extractor order as axum (path, then body) before authentication.
func (s *Server) runCommand(w http.ResponseWriter, r *http.Request) {
	rawWS, name, ok := commandRoute(r)
	if !ok {
		notFound(w)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	ws, msg := uuidparse.Parse(rawWS)
	if msg != "" {
		plain(w, http.StatusBadRequest, "Invalid URL: Cannot parse `ws` with value `"+rawWS+"`: UUID parsing failed: "+msg)
		return
	}
	if r.ContentLength > maxBody && isJSON(r) {
		tooLarge(w) // declared size: refuse before reading an upload the server would hang up on
		return
	}
	// axum: Option<Json<Value>> is None without a Content-Type header; with one it must be JSON and the body must parse.
	input := json.RawMessage(`{}`)
	if r.Header.Get("Content-Type") != "" {
		if !isJSON(r) {
			plain(w, http.StatusUnsupportedMediaType, "Expected request with `Content-Type: application/json`")
			return
		}
		buf, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || len(buf) > maxBody {
			tooLarge(w)
			return
		}
		if e := serdejson.Check(buf, true); e != nil {
			plain(w, http.StatusBadRequest, "Failed to parse the request body as JSON: "+e.Error())
			return
		}
		input = buf
	}
	slog.Debug("command", "command", name)

	opts := command.ExecOptions{}
	if v := r.Header.Values("Idempotency-Key"); len(v) > 0 && visibleASCII(v[0]) { // to_str() fails on anything else: no key
		opts.IdempotencyKey = &v[0]
	}
	if v := r.Header.Values("X-Confirmation-Id"); len(v) > 0 {
		if id, bad := uuidparse.Parse(v[0]); bad == "" { // unparsable ids are ignored
			opts.ConfirmationID = &id
		}
	}

	p, err := s.Auth.PrincipalForRequest(r.Context(), r.Header, r.Host, ws)
	var out []byte
	if err == nil {
		if !s.Registry.Has(name) {
			err = command.ErrNotFound("command " + name)
		} else {
			input = serdecheck.NormalizeUUIDs(s.schemaOf(name), serdecheck.EmptySequence(name, input))
			out, err = s.Registry.Execute(r.Context(), s.Pool, name, input, p, opts)
		}
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, command.ErrFallback):
		// the handler declined (input serde would reject, or a rare case): answer like Rust's deserializer does
		s.writeErr(w, r, command.ErrInvalid("invalid input: "+serdecheck.Describe(name, s.schemaOf(name), input)))
	default:
		s.writeErr(w, r, err)
	}
}

// schemaOf is the input JSON Schema of a command: the Rust catalog snapshot, else the Go-only command's own.
func (s *Server) schemaOf(name string) json.RawMessage {
	return mcp.InputSchema(s.Registry, name)
}

func isJSON(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// tooLarge is axum's DefaultBodyLimit rejection, which Rust sends before authentication and then drops the
// connection mid-upload; a replaying proxy would turn that into a 502 (CN-QA-001 API-009).
func tooLarge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_, _ = io.WriteString(w, "Failed to buffer the request body: length limit exceeded")
}

func visibleASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 32 && c != '\t') || c > 126 {
			return false
		}
	}
	return true
}
