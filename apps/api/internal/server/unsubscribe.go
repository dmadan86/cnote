package server

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/unsub"
)

const unsubscribePrefix = "/api/public/unsubscribe/"

const unsubPageHead = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
	`<meta name="robots" content="noindex,nofollow"><title>Email settings</title><style>body{font:16px/1.5 system-ui,sans-serif;max-width:30rem;margin:12vh auto;padding:0 1rem;color:#1a1a1a}` +
	`button{font:inherit;padding:.6rem 1.1rem;border:0;border-radius:8px;background:#1a1a1a;color:#fff;cursor:pointer}p.s{color:#666;font-size:.9rem}</style></head><body>`

func unsubReply(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, unsubPageHead+body+"</body></html>")
}

// unsubscribe is the landing page and the RFC 8058 endpoint of the link in notification mail (13.2.5). The signed token in
// the path is the whole credential: no session, no login. GET only shows a button, because mail scanners follow links and a
// GET must change nothing; POST (the button, or a mail client's one-click POST) switches that kind of mail off.
func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w, "GET, POST")
		return
	}
	tok := strings.TrimPrefix(r.URL.Path, unsubscribePrefix)
	ws, user, kind, err := unsub.Parse(tok)
	if err != nil {
		unsubReply(w, http.StatusNotFound, "<h1>This link is not valid</h1><p>It may be incomplete. You can change email settings in C Note under Settings, Notifications.</p>")
		return
	}
	what := "these emails"
	if kind == unsub.Digest {
		what = "the daily digest"
	}
	if r.Method == http.MethodGet {
		unsubReply(w, http.StatusOK, fmt.Sprintf(`<h1>Stop %s?</h1><form method="post"><button type="submit">Stop %s</button></form>`+
			`<p class="s">You can turn them back on in Settings, Notifications.</p>`, html.EscapeString(what), html.EscapeString(what)))
		return
	}
	col := "direct_off"
	if kind == unsub.Digest {
		col = "digest_off"
	}
	tx, err := db.BeginTenant(r.Context(), s.Pool, ws)
	if err == nil {
		defer func() { _ = tx.Rollback(r.Context()) }()
		// The column name is one of two constants above, never input.
		_, err = tx.Exec(r.Context(),
			`insert into core.notify_prefs (workspace_id, user_id, `+col+`) select $1::uuid, $2::uuid, true
			 where exists(select 1 from core.memberships where user_id = $2::uuid and workspace_id = $1::uuid)
			 on conflict (workspace_id, user_id) do update set `+col+` = true, updated_at = now()`, ws, user)
		if err == nil {
			err = tx.Commit(r.Context())
		}
	}
	if err != nil {
		slog.Error("unsubscribe", "error", err)
		unsubReply(w, http.StatusInternalServerError, "<h1>Something went wrong</h1><p>Please try again later.</p>")
		return
	}
	base := strings.TrimRight(os.Getenv("PUBLIC_WEB_URL"), "/")
	link := ""
	if base != "" {
		link = fmt.Sprintf(`<p class="s"><a href="%s">Notification settings</a></p>`, html.EscapeString(base+"/w/"+ws+"/settings"))
	}
	unsubReply(w, http.StatusOK, fmt.Sprintf("<h1>You will not get %s any more</h1>%s", html.EscapeString(what), link))
}
