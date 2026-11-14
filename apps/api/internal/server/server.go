// Package server is the strangler front door: commands registered in Go run natively, everything else
// (all other HTTP paths, WebSocket upgrades on /collab and /mcp) is proxied to the Rust server;
// /collab goes to the TypeScript collab service instead when COLLAB_URL is set.
package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/command"
)

type Server struct {
	Pool     *pgxpool.Pool
	Registry *command.Registry
	Auth     *auth.Auth
	Upstream http.Handler
	// Collab, when set (COLLAB_URL), serves the /collab/ live-editing WebSocket instead of Upstream.
	Collab http.Handler
}

func New(pool *pgxpool.Pool, reg *command.Registry, upstream http.Handler) *Server {
	return &Server{Pool: pool, Registry: reg, Auth: &auth.Auth{Pool: pool}, Upstream: upstream}
}

var wsRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// maxBody matches axum's default Json limit; larger bodies go to Rust to get its exact rejection.
const maxBody = 2 << 20

// ServeHTTP dispatches by hand instead of http.ServeMux: ServeMux redirects "unclean" paths (//, ..),
// which would make the proxy visibly different from Rust.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Request-Id", requestID(r))
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok")
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.EscapedPath(), "/api/w/"):
		s.runCommand(w, r)
	case s.Collab != nil && strings.HasPrefix(r.URL.Path, "/collab/"):
		slog.Debug("collab", "path", r.URL.Path)
		s.Collab.ServeHTTP(w, r)
	default:
		slog.Debug("proxy", "method", r.Method, "path", r.URL.Path)
		s.Upstream.ServeHTTP(w, r)
	}
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

// POST /api/w/{ws}/commands/{name}
func (s *Server) runCommand(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/w/"), "/")
	if len(parts) == 3 && parts[1] == "commands" && wsRe.MatchString(parts[0]) && r.ContentLength > maxBody && isJSON(r) {
		tooLarge(w) // declared size: refuse before the proxy starts uploading to a server that will hang up
		return
	}
	if len(parts) != 3 || parts[1] != "commands" || !wsRe.MatchString(parts[0]) || !s.Registry.Has(parts[2]) {
		s.Upstream.ServeHTTP(w, r)
		return
	}
	ws, name := parts[0], parts[2]
	slog.Debug("native command", "command", name)

	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(buf) > maxBody {
		if err == nil && isJSON(r) {
			tooLarge(w)
			return
		}
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		s.Upstream.ServeHTTP(w, r)
		return
	}
	fallback := func() {
		r.Body = io.NopCloser(bytes.NewReader(buf))
		s.Upstream.ServeHTTP(w, r)
	}

	// axum: Option<Json<Value>> is None without a JSON content type; with one the body must parse.
	input := json.RawMessage(`{}`)
	if isJSON(r) {
		if !json.Valid(buf) {
			fallback()
			return
		}
		input = buf
	}

	opts := command.ExecOptions{}
	if v := r.Header.Values("Idempotency-Key"); len(v) > 0 {
		if !visibleASCII(v[0]) {
			fallback()
			return
		}
		opts.IdempotencyKey = &v[0]
	}
	if v := r.Header.Values("X-Confirmation-Id"); len(v) > 0 {
		if !wsRe.MatchString(v[0]) {
			fallback() // Rust ignores unparsable ids, accepts other UUID spellings: let it decide
			return
		}
		opts.ConfirmationID = &v[0]
	}

	p, err := s.Auth.PrincipalFor(r.Context(), r.Header, ws)
	var out []byte
	if err == nil {
		out, err = s.Registry.Execute(r.Context(), s.Pool, name, input, p, opts)
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, command.ErrFallback):
		fallback()
	default:
		var ce *command.Error
		if !errors.As(err, &ce) {
			slog.Error("internal error", "command", name, "request_id", w.Header().Get("X-Request-Id"), "error", err)
			ce = command.NewError(command.Internal, "internal error") // Rust echoes the cause here; logs carry it instead
		}
		body, _ := command.Marshal(map[string]any{"error": ce})
		writeJSON(w, ce.Code.Status(), body)
	}
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
