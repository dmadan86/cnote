package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/db"
)

// Event is a hint that something changed in a channel; the reader refetches through the commands, which authorise.
// K is message | reaction | read | resync (the stream was interrupted: refetch everything you show).
type Event struct {
	K string `json:"k"`
	C string `json:"c,omitempty"`
	M string `json:"m,omitempty"`
	P string `json:"p,omitempty"`
	// not sent to browsers:
	W string `json:"-"` // workspace
	U string `json:"-"` // for k=read: the reader, the only one told
	T int64  `json:"-"` // microseconds, the SSE event id
}

// Hub listens to Postgres NOTIFY (fired by triggers on every chat change, from any API instance) and fans events out to
// the streams connected to this instance. It holds one database connection while at least one stream is open.
// shortcut: events are hints with no replay; a reconnect gets a resync. Move to a stored event log if a client must
// resume without refetching.
type Hub struct {
	pool   *pgxpool.Pool
	mu     sync.Mutex
	subs   map[*Sub]struct{}
	cancel context.CancelFunc
	ready  chan struct{}
}

type Sub struct {
	WS  string
	C   chan Event
	lag atomic.Bool
}

// Lagged reports (once) that events were dropped because the reader was too slow: send it a resync.
func (s *Sub) Lagged() bool { return s.lag.Swap(false) }

func NewHub(pool *pgxpool.Pool) *Hub { return &Hub{pool: pool, subs: map[*Sub]struct{}{}} }

// Subscribe starts listening when it is the first stream and returns once LISTEN is active (so nothing committed after
// this returns is missed). The returned func ends the subscription and stops listening after the last one.
func (h *Hub) Subscribe(ctx context.Context, ws string) (*Sub, func()) {
	s := &Sub{WS: ws, C: make(chan Event, 64)}
	h.mu.Lock()
	if len(h.subs) == 0 {
		var lctx context.Context
		lctx, h.cancel = context.WithCancel(context.Background())
		h.ready = make(chan struct{})
		go h.run(lctx, h.ready)
	}
	h.subs[s] = struct{}{}
	ready := h.ready
	h.mu.Unlock()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
	}
	return s, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, s)
		if len(h.subs) == 0 && h.cancel != nil {
			h.cancel()
			h.cancel = nil
		}
	}
}

func (h *Hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if e.K != "resync" && s.WS != e.W {
			continue
		}
		select {
		case s.C <- e:
		default:
			s.lag.Store(true)
		}
	}
}

func (h *Hub) run(ctx context.Context, ready chan struct{}) {
	var once sync.Once
	wait := time.Second
	for ctx.Err() == nil {
		err := h.listen(ctx, func() { once.Do(func() { close(ready) }) })
		if ctx.Err() != nil {
			return
		}
		slog.Warn("chat stream: listener lost, retrying", "error", err)
		h.publish(Event{K: "resync"}) // notifications may have been missed
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 10*time.Second)
	}
}

func (h *Hub) listen(ctx context.Context, listening func()) error {
	conn, err := pgx.ConnectConfig(ctx, h.pool.Config().ConnConfig)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "listen chat_events"); err != nil {
		return err
	}
	listening()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var raw struct {
			K string `json:"k"`
			C string `json:"c"`
			M string `json:"m"`
			P string `json:"p"`
			W string `json:"w"`
			U string `json:"u"`
			T int64  `json:"t"`
		}
		if json.Unmarshal([]byte(n.Payload), &raw) == nil {
			h.publish(Event{K: raw.K, C: raw.C, M: raw.M, P: raw.P, W: raw.W, U: raw.U, T: raw.T})
		}
	}
}

// CanSee reports whether a person may read the channel: public, or they are a member. The same rule as chat.messages.
// shortcut: one small query per event per open stream; cache per connection if streams reach the thousands.
func CanSee(ctx context.Context, pool *pgxpool.Pool, ws, user, channel string) bool {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return false
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var ok bool
	err = tx.QueryRow(ctx,
		`select exists (select 1 from chat.channels c where c.id = $1::uuid and (c.kind = 'public' or exists
		   (select 1 from chat.members m where m.channel_id = c.id and m.user_id = $2::uuid)))`, channel, user).Scan(&ok)
	return err == nil && ok
}
