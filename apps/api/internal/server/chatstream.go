package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/chat"
)

var chatStreamRe = regexp.MustCompile(`^/api/w/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/chat/stream$`)

const (
	heartbeat          = 20 * time.Second
	chatStreamsPerUser = 5
	canSeeTTL          = 30 * time.Second
)

// Open streams on this instance: each holds a goroutine and a buffered channel, so they are capped per person and in
// total (CHAT_STREAMS_MAX, default 2000). Over the cap the answer is 429 and the web client polls instead.
var chatStreams struct {
	sync.Mutex
	total  int
	byUser map[string]int
}

func chatStreamsMax() int {
	if n, err := strconv.Atoi(os.Getenv("CHAT_STREAMS_MAX")); err == nil && n > 0 {
		return n
	}
	return 2000
}

// acquireChatStream reserves a slot; the returned func releases it.
func acquireChatStream(user string) (func(), bool) {
	chatStreams.Lock()
	defer chatStreams.Unlock()
	if chatStreams.byUser == nil {
		chatStreams.byUser = map[string]int{}
	}
	if chatStreams.total >= chatStreamsMax() || chatStreams.byUser[user] >= chatStreamsPerUser {
		return nil, false
	}
	chatStreams.total++
	chatStreams.byUser[user]++
	return func() {
		chatStreams.Lock()
		defer chatStreams.Unlock()
		chatStreams.total--
		if chatStreams.byUser[user]--; chatStreams.byUser[user] <= 0 {
			delete(chatStreams.byUser, user)
		}
	}, true
}

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
	release, ok := acquireChatStream(user)
	if !ok {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, []byte(`{"error":{"code":"rate_limited","message":"too many open chat streams"}}`))
		return
	}
	defer release()
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
	// Per-stream visibility cache so an event does not cost a transaction per open stream. Dropped on resync and when
	// this person's own membership row changes (join/read); other changes (removal, a channel turning private) are
	// not notified, so an entry also expires after canSeeTTL. Events are only hints: the refetch still authorises.
	type seen struct {
		ok bool
		at time.Time
	}
	vis := map[string]seen{}
	canSee := func(c string) bool {
		if v, hit := vis[c]; hit && time.Since(v.at) < canSeeTTL {
			return v.ok
		}
		ok := chat.CanSee(r.Context(), s.Pool, ws, user, c)
		vis[c] = seen{ok, time.Now()}
		return ok
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
			lag := sub.Lagged()
			if e.K == "resync" || lag {
				clear(vis)
			} else if e.K == "read" && e.U == user {
				delete(vis, e.C)
			}
			if lag && !send("event: resync\ndata: {\"k\":\"resync\"}\n\n") {
				return
			}
			if e.K == "read" && e.U != user {
				continue
			}
			if e.K != "resync" && e.K != "read" && !canSee(e.C) {
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
