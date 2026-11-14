package publiclinks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/billing"
)

// Handler serves the unauthenticated API:
//
//	GET  /api/public/{token}        the shared document (allowlisted fields only)
//	POST /api/public/{token}/quote  {decision: accept|decline, name, note}
//
// Security boundary: no session, no tenant until the token resolves. Per-IP limits apply to every request, a
// stricter per-IP limit to failed lookups, and a per-token limit to the rest.
type Handler struct {
	pool *pgxpool.Pool
	reg  *command.Registry
	ip   *limiter // every request
	fail *limiter // lookups that found nothing: slows token guessing
	tok  *limiter // per token hash
	// trustProxy: take the client IP from the right-most X-Forwarded-For entry (set by the one proxy in front,
	// e.g. the Next.js rewrite). Off by default because a direct client could forge the header.
	trustProxy bool
}

func NewHandler(pool *pgxpool.Pool, reg *command.Registry) *Handler {
	return &Handler{pool: pool, reg: reg,
		ip:         newLimiter(envFloat("PUBLIC_IP_BURST", 60), envFloat("PUBLIC_IP_PER_SEC", 2)),
		fail:       newLimiter(envFloat("PUBLIC_FAIL_BURST", 10), envFloat("PUBLIC_FAIL_PER_SEC", 0.1)),
		tok:        newLimiter(envFloat("PUBLIC_TOKEN_BURST", 30), envFloat("PUBLIC_TOKEN_PER_SEC", 0.5)),
		trustProxy: getenv("PUBLIC_TRUST_PROXY") == "1"}
}

type perr struct {
	status int
	code   string
	msg    string
}

var (
	errNotFound = perr{404, "not_found", "link not found"} // one body for unknown, revoked, expired and malformed
	errRate     = perr{429, "rate_limited", "too many requests, retry shortly"}
)

func write(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeErr(w http.ResponseWriter, e perr) {
	write(w, e.status, map[string]any{"error": map[string]any{"code": e.code, "message": e.msg}})
}

func (h *Handler) clientIP(r *http.Request) string {
	if h.trustProxy {
		if xs := strings.Split(r.Header.Get("X-Forwarded-For"), ","); len(xs) > 0 {
			if ip := strings.TrimSpace(xs[len(xs)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// validToken: 32 bytes in unpadded base64url is exactly 43 characters.
func validToken(t string) bool {
	if len(t) != 43 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/public/")
	token, sub, _ := strings.Cut(rest, "/")
	ip := h.clientIP(r)
	if !h.ip.allow(ip) || !h.fail.peek(ip) {
		writeErr(w, errRate)
		return
	}
	var err perr
	switch {
	case r.Method == http.MethodGet && sub == "":
		err = h.view(w, r, token)
	case r.Method == http.MethodPost && sub == "quote":
		err = h.decide(w, r, token)
	default:
		err = errNotFound
	}
	if err.status != 0 {
		if err == errNotFound {
			h.fail.allow(ip)
		}
		writeErr(w, err)
	}
}

type link struct {
	id, ws, note, purpose, creator string
	role                           command.Role
}

// open resolves the token and starts the tenant transaction. Every failure is errNotFound.
func (h *Handler) open(ctx context.Context, token string) (pgx.Tx, link, *perr) {
	var l link
	if !validToken(token) {
		return nil, l, &errNotFound
	}
	err := h.pool.QueryRow(ctx, "select id::text, workspace_id::text, note_id::text, purpose, created_by::text from core.resolve_public_link($1)",
		hashToken(token)).Scan(&l.id, &l.ws, &l.note, &l.purpose, &l.creator)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, l, &errNotFound
	}
	if err != nil {
		slog.Error("public link lookup", "error", err)
		return nil, l, &perr{500, "internal", "internal error"}
	}
	tx, err := db.BeginTenant(ctx, h.pool, l.ws)
	if err != nil {
		slog.Error("public link tx", "error", err)
		return nil, l, &perr{500, "internal", "internal error"}
	}
	// The creator must still be a member who may manage documents, and the page must not be deleted.
	var role string
	var live bool
	err = tx.QueryRow(ctx, `select m.role, exists(select 1 from core.notes where id = $2::uuid and deleted_at is null)
		from core.memberships m where m.user_id = $1::uuid`, l.creator, l.note).Scan(&role, &live)
	l.role = command.ParseRole(role)
	if err != nil || !live || l.role < command.Member {
		_ = tx.Rollback(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("public link member", "error", err)
		}
		return nil, l, &errNotFound
	}
	return tx, l, nil
}

func (h *Handler) principal(l link) command.Principal {
	return command.Principal{Kind: command.KindSystem, WorkspaceID: l.ws, UserID: &l.creator, Role: l.role}
}

func (h *Handler) view(w http.ResponseWriter, r *http.Request, token string) perr {
	ctx := r.Context()
	if !h.tok.allow(hashToken(token)) {
		return errRate
	}
	tx, l, e := h.open(ctx, token)
	if e != nil {
		return *e
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	d, err := loadPublic(ctx, tx, l)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		slog.Error("public document", "error", err)
		return perr{500, "internal", "internal error"}
	}
	// view_count is every view; the event only the first view of the day, so a customer refreshing does not flood the log.
	var first bool
	if err := tx.QueryRow(ctx, `select last_viewed_at is null or (last_viewed_at at time zone 'UTC')::date < (now() at time zone 'UTC')::date
		from core.public_links where id = $1::uuid for update`, l.id).Scan(&first); err != nil {
		slog.Error("public view count", "error", err)
		return perr{500, "internal", "internal error"}
	}
	if _, err := tx.Exec(ctx, "update core.public_links set view_count = view_count + 1, last_viewed_at = now() where id = $1::uuid", l.id); err != nil {
		slog.Error("public view count", "error", err)
		return perr{500, "internal", "internal error"}
	}
	if first {
		c := &command.Ctx{Context: ctx, Tx: tx, Principal: h.principal(l)}
		if _, err := c.Emit("share.viewed", &l.note, map[string]any{"link_id": l.id}); err != nil {
			slog.Error("public view event", "error", err)
			return perr{500, "internal", "internal error"}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return perr{500, "internal", "internal error"}
	}
	write(w, 200, d)
	return perr{}
}

type decision struct {
	Decision string `json:"decision"`
	Name     string `json:"name"`
	Note     string `json:"note"`
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, token string) perr {
	ctx := r.Context()
	if !h.tok.allow(hashToken(token)) {
		return errRate
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<10+1))
	var in decision
	if err != nil || len(body) > 8<<10 || json.Unmarshal(body, &in) != nil {
		return perr{400, "invalid", "invalid request"}
	}
	in.Name, in.Note = strings.TrimSpace(in.Name), strings.TrimSpace(in.Note)
	cmd := map[string]string{"accept": "quote.accept", "decline": "quote.reject"}[in.Decision]
	switch {
	case cmd == "":
		return perr{400, "invalid", "decision must be accept or decline"}
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 120:
		return perr{400, "invalid", "name is required (at most 120 characters)"}
	case utf8.RuneCountInString(in.Note) > 1000:
		return perr{400, "invalid", "note is too long (at most 1000 characters)"}
	}
	tx, l, e := h.open(ctx, token)
	if e != nil {
		return *e
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if l.purpose != "quote_accept" {
		return errNotFound
	}
	var status string
	var expired bool
	var via *string
	err = tx.QueryRow(ctx, `select status, status = 'sent' and valid_until < $2::date, accepted_via from billing.quotations
		where note_id = $1::uuid for update`, l.note, billing.TodayIST().Format("2006-01-02")).Scan(&status, &expired, &via)
	if err != nil {
		return errNotFound
	}
	want := map[string]string{"accept": "accepted", "decline": "rejected"}[in.Decision]
	if status == want && via != nil && *via == "link" { // a repeated tap or retry: same answer, no second event
		d, err := loadPublic(ctx, tx, l)
		if err != nil {
			return perr{500, "internal", "internal error"}
		}
		write(w, 200, d)
		return perr{}
	}
	if status != "sent" || expired {
		return perr{409, "conflict", "this quotation can no longer be accepted or declined"}
	}
	if _, err := tx.Exec(ctx, `update billing.quotations set accepted_via = 'link', accepted_by_name = $2, accepted_note = nullif($3,'')
		where note_id = $1::uuid`, l.note, in.Name, in.Note); err != nil {
		return perr{500, "internal", "internal error"}
	}
	c := &command.Ctx{Context: ctx, Tx: tx, Principal: h.principal(l)}
	h.reg.Bind(c)
	if _, err := c.Call(cmd, map[string]any{"id": l.note}); err != nil {
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.Conflict {
			return perr{409, "conflict", "this quotation can no longer be accepted or declined"}
		}
		slog.Warn("public decision refused", "command", cmd, "error", err)
		return errNotFound // e.g. the creator lost access to the page
	}
	d, err := loadPublic(ctx, tx, l)
	if err != nil {
		return perr{500, "internal", "internal error"}
	}
	if err := tx.Commit(ctx); err != nil {
		return perr{500, "internal", "internal error"}
	}
	write(w, 200, d)
	return perr{}
}
