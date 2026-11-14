package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/chat"
)

var chatStreamRe = regexp.MustCompile(`^/api/w/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/chat/stream$`)

const heartbeat = 20 * time.Second

// GET /api/w/{ws}/chat/stream: server-sent events for the signed-in person. Each event is a hint (channel, message);
// the client refetches through chat.messages, which authorises, and a channel the person may not read sends nothing.
// Reconnects (Last-Event-ID) start with a resync event. People only: agents and keys use the commands.
func (s *Server) chatStream(w http.ResponseWriter, r *http.Request, ws string) {
	fail := func(err error) {
		var ce *command.Error
		if !errors.As(err, &ce) {
			ce = command.NewError(command.Internal, "internal error")
		}
		body, _ := command.Marshal(map[string]any{"error": ce})
		writeJSON(w, ce.Code.Status(), body)
	}
	p, err := s.Auth.PrincipalForRequest(r.Context(), r.Header, r.Host, ws)
	if err == nil && (p.Kind != command.KindUser || p.UserID == nil || p.Role < command.Member) {
		err = command.ErrForbidden("chat belongs to people")
	}
	if err != nil {
		fail(err)
		return
	}
	user := *p.UserID
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform") // no-transform keeps compression (Next.js) from buffering the stream
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(format string, a ...any) bool {
		if _, err := fmt.Fprintf(w, format, a...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	sub, done := s.Chat.Subscribe(r.Context(), ws)
	defer done()
	if !send("retry: 3000\nid: %d\nevent: ready\ndata: {}\n\n", time.Now().UnixMicro()) {
		return
	}
	if r.Header.Get("Last-Event-ID") != "" && !send("event: resync\ndata: {\"k\":\"resync\"}\n\n") {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			// the session may have ended or the person left the workspace meanwhile
			if q, err := s.Auth.PrincipalForRequest(r.Context(), r.Header, r.Host, ws); err != nil || q.Kind != command.KindUser || q.Role < command.Member {
				return
			}
			if !send(": hb\n\n") {
				return
			}
		case e := <-sub.C:
			if sub.Lagged() && !send("event: resync\ndata: {\"k\":\"resync\"}\n\n") {
				return
			}
			if e.K == "read" && e.U != user {
				continue
			}
			if e.K != "resync" && e.K != "read" && !chat.CanSee(r.Context(), s.Pool, ws, user, e.C) {
				continue
			}
			b, _ := json.Marshal(e)
			id := ""
			if e.T != 0 {
				id = "id: " + strconv.FormatInt(e.T, 10) + "\n"
			}
			if !send("%sevent: chat\ndata: %s\n\n", id, b) {
				return
			}
		}
	}
}
