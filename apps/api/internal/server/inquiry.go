package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/leads"
)

// Public inquiry form: GET /api/public/inquiry/{token} (workspace name for the page) and POST (submit).
// No session: the token is the whole authorization, and it can only ever create a contact and a lead.
const inquiryPrefix = "/api/public/inquiry/"

var (
	inqMu   sync.Mutex
	inqSeen = map[string][]time.Time{}
)

// inquiryLimited allows 10 submissions per client per 10 minutes. shortcut: in-process and keyed on the
// X-Forwarded-For hop the Next rewrite adds, so one instance only; move to a shared store when scaled out.
func inquiryLimited(ip string) bool {
	inqMu.Lock()
	defer inqMu.Unlock()
	now := time.Now()
	if len(inqSeen) > 4096 {
		clear(inqSeen)
	}
	keep := inqSeen[ip][:0]
	for _, t := range inqSeen[ip] {
		if now.Sub(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	inqSeen[ip] = append(keep, now)
	return len(keep) >= 10
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

func (s *Server) inquiry(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, inquiryPrefix)
	fail := func(err error) {
		var ce *command.Error
		if !errors.As(err, &ce) {
			slog.Error("inquiry", "error", err)
			ce = command.NewError(command.Internal, "internal error")
		}
		body, _ := command.Marshal(map[string]any{"error": ce})
		writeJSON(w, ce.Code.Status(), body)
	}
	form, err := leads.Lookup(r.Context(), s.Pool, token)
	switch {
	case errors.Is(err, leads.ErrNoForm) || len(token) > 64:
		fail(command.ErrNotFound("form"))
		return
	case err != nil:
		fail(err)
		return
	}
	if r.Method == http.MethodGet {
		body, _ := command.Marshal(map[string]any{"workspace": form.WorkspaceName})
		writeJSON(w, http.StatusOK, body)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Name    string `json:"name"`
		Phone   string `json:"phone"`
		Email   string `json:"email"`
		Message string `json:"message"`
		Hp      string `json:"hp"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		fail(command.ErrInvalid("invalid request"))
		return
	}
	// A bot that fills the hidden field or floods gets the same 200 as a person, and nothing is stored.
	if in.Hp == "" && !inquiryLimited(clientIP(r)) {
		if err := leads.Receive(r.Context(), s.Pool, form, leads.Inquiry{Name: in.Name, Phone: in.Phone, Email: in.Email, Message: in.Message}); err != nil {
			fail(err)
			return
		}
	}
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}
