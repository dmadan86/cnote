package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/leads"
)

// Support v2 public surface. Email-in: POST /api/public/ticket-mail/{ws}, authenticated by an HMAC signature, no
// session. Portal: /api/public/portal/{ws}/{info,request,verify,tickets,reply}, authenticated by a per-workspace
// session cookie that a single-use emailed link starts.
const (
	mailPrefix   = "/api/public/ticket-mail/"
	portalPrefix = "/api/public/portal/"
)

func publicFail(w http.ResponseWriter, err error) {
	var ce *command.Error
	switch {
	case errors.Is(err, leads.ErrBadSignature), errors.Is(err, leads.ErrPortalAuth):
		ce = command.NewError(command.Unauthenticated, "not authorized")
	case errors.As(err, &ce):
	default:
		slog.Error("support public", "error", err)
		ce = command.NewError(command.Internal, "internal error")
	}
	body, _ := command.Marshal(map[string]any{"error": ce})
	writeJSON(w, ce.Code.Status(), body)
}

func (s *Server) ticketMail(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimPrefix(r.URL.Path, mailPrefix)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !wsRe.MatchString(ws) {
		publicFail(w, leads.ErrBadSignature)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, leads.MaxMailBody+1))
	if err != nil || len(body) > leads.MaxMailBody {
		publicFail(w, command.ErrInvalid("body too large (256 KB at most)"))
		return
	}
	res, err := leads.ReceiveEmail(r.Context(), s.Pool, ws, r.Header.Get("X-Cnote-Timestamp"), r.Header.Get("X-Cnote-Signature"), body)
	if err != nil {
		publicFail(w, err)
		return
	}
	out, _ := json.Marshal(res)
	writeJSON(w, http.StatusOK, out)
}

func portalCookie(ws string) string { return "cn_portal_" + ws }

func (s *Server) portal(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, portalPrefix), "/")
	if len(parts) != 2 || !wsRe.MatchString(parts[0]) {
		publicFail(w, command.ErrNotFound("portal"))
		return
	}
	ws, action := parts[0], parts[1]
	ip, ctx := clientIP(r), r.Context()
	ok := func(v any) {
		b, _ := json.Marshal(v)
		writeJSON(w, http.StatusOK, b)
	}
	session := func() string {
		if c, err := r.Cookie(portalCookie(ws)); err == nil {
			return c.Value
		}
		return ""
	}
	var in struct {
		Email, Token, Body string
		Ref                int
	}
	if r.Method == http.MethodPost {
		if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in) != nil {
			publicFail(w, command.ErrInvalid("invalid request"))
			return
		}
	}
	switch {
	case action == "info" && r.Method == http.MethodGet:
		name, err := leads.PortalInfo(ctx, s.Pool, ws)
		if err != nil {
			publicFail(w, command.ErrNotFound("portal"))
			return
		}
		ok(map[string]any{"workspace": name})
	case action == "request" && r.Method == http.MethodPost:
		// Always the same answer, limited or not, known address or not.
		if !inquiryLimited("pq:" + ip) {
			if err := leads.PortalRequestLink(ctx, s.Pool, ws, in.Email); err != nil {
				publicFail(w, err)
				return
			}
		}
		ok(map[string]any{"ok": true})
	case action == "verify" && r.Method == http.MethodPost:
		if inquiryLimited("pv:" + ip) {
			publicFail(w, command.NewError(command.RateLimited, "too many attempts; wait a few minutes"))
			return
		}
		sess, err := leads.PortalVerify(ctx, s.Pool, ws, strings.TrimSpace(in.Token))
		if err != nil {
			publicFail(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: portalCookie(ws), Value: sess, Path: portalPrefix + ws + "/", MaxAge: 12 * 3600, HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
		ok(map[string]any{"ok": true})
	case action == "tickets" && r.Method == http.MethodGet:
		name, list, err := leads.PortalTickets(ctx, s.Pool, ws, session())
		if err != nil {
			publicFail(w, err)
			return
		}
		ok(map[string]any{"workspace": name, "tickets": list})
	case action == "reply" && r.Method == http.MethodPost:
		if inquiryLimited("pr:" + ip) {
			publicFail(w, command.NewError(command.RateLimited, "too many replies; wait a few minutes"))
			return
		}
		if err := leads.PortalReply(ctx, s.Pool, ws, session(), in.Ref, in.Body); err != nil {
			publicFail(w, err)
			return
		}
		ok(map[string]any{"ok": true})
	default:
		publicFail(w, command.ErrNotFound("portal"))
	}
}
