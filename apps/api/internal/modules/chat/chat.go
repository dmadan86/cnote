// Package chat is team chat (WP16, v2 in WP19): public channels, private channels and 1:1 direct messages, with @mentions,
// unread counts, edit/delete of your own messages, one-level threads, a fixed set of reactions, live updates (stream.go)
// and agents as separate participants. Go-only. Chat belongs to people; a plain API key gets 403. An agent is a participant
// only when the workspace allows it (settings.chat_agents) and a person added it to the channel: it posts as itself, never
// as its owner, and can never see a direct message. Not built: files, calls, search.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
)

func Commands() []command.Command {
	mk := func(name string, g command.Group, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = command.Member // guests are not part of team chat
		return c
	}
	const R, W = command.GroupRead, command.GroupWriteNotes
	return []command.Command{
		mk("chat.channel_list", R, channelList),
		mk("chat.messages", R, messages),
		mk("chat.channel_create", W, channelCreate),
		mk("chat.dm_open", W, dmOpen),
		mk("chat.member_add", W, memberAdd),
		mk("chat.thread", R, thread),
		mk("chat.channel_agents", R, channelAgents),
		mk("chat.agent_add", W, agentAdd),
		mk("chat.agent_remove", W, agentRemove),
		mk("chat.react", W, react),
		mk("chat.send", W, send),
		mk("chat.edit", W, edit),
		mk("chat.delete", W, del),
		mk("chat.read", W, readChannel),
	}
}

const maxBody = 4000

var (
	uuidRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	mentionRe = regexp.MustCompile(`\]\(/w/([0-9a-fA-F-]{36})/people/([0-9a-fA-F-]{36})\)`)
	noteLink  = regexp.MustCompile(`/w/([0-9a-fA-F-]{36})/n/([0-9a-fA-F-]{36})`)
)

// me is for commands that are people only (reactions, creating channels, DMs, adding people or agents). Agents and API
// keys carry their owner's user id, but a message or DM read through one would be the owner's voice and private
// conversations would flow into the agent.
func me(c *command.Ctx) (string, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return "", command.ErrForbidden("chat belongs to people")
	}
	return *c.Principal.UserID, nil
}

// ident is who is speaking: a person, or an agent in its own right (user is nil then, never its owner).
type ident struct{ user, agent *string }

func (i ident) id() string {
	if i.agent != nil {
		return *i.agent
	}
	return *i.user
}

func agentsEnabled(c *command.Ctx) (bool, error) {
	var on bool
	err := c.Tx.QueryRow(c.Context, `select coalesce(settings->>'chat_agents' = 'true', false) from core.workspaces where id = $1::uuid`, c.WorkspaceID()).Scan(&on)
	return on, command.DB(err)
}

// who resolves the caller of a command agents may use: a person, or an agent while the workspace allows agents in chat.
func who(c *command.Ctx) (ident, error) {
	p := c.Principal
	if p.Kind == command.KindUser && p.UserID != nil {
		return ident{user: p.UserID}, nil
	}
	if p.Kind == command.KindAgent && p.AgentID != nil {
		on, err := agentsEnabled(c)
		if err != nil {
			return ident{}, err
		}
		if !on {
			return ident{}, command.ErrForbidden("agents are not allowed in chat in this workspace; an admin can turn it on in settings")
		}
		return ident{agent: p.AgentID}, nil
	}
	return ident{}, command.ErrForbidden("chat belongs to people, and to agents added to a channel")
}

func parse(raw json.RawMessage, v any) error {
	if json.Unmarshal(raw, v) != nil {
		return command.ErrInvalid("input must be a JSON object with the documented fields")
	}
	return nil
}

func uid(k, v string) (string, error) {
	if v = strings.ToLower(strings.TrimSpace(v)); !uuidRe.MatchString(v) {
		return "", command.ErrInvalid(k + " must be a UUID")
	}
	return v, nil
}

type chanInfo struct{ id, kind, name string }

// open returns a channel the caller may use. Private channels and DMs the caller is not in are "not found", so their
// existence does not leak.
func open(c *command.Ctx, id string) (chanInfo, error) {
	w, err := who(c)
	if err != nil {
		return chanInfo{}, err
	}
	ch := chanInfo{id: id}
	var name *string
	if w.agent != nil { // only channels it was added to, and never a direct message
		err = c.Tx.QueryRow(c.Context,
			`select c.kind, c.name from chat.channels c
			  where c.id = $1::uuid and c.kind <> 'dm' and exists
			        (select 1 from chat.agent_members m where m.channel_id = c.id and m.agent_id = $2::uuid)`, id, *w.agent).Scan(&ch.kind, &name)
	} else {
		err = c.Tx.QueryRow(c.Context,
			`select c.kind, c.name from chat.channels c
			  where c.id = $1::uuid and (c.kind = 'public' or exists
			        (select 1 from chat.members m where m.channel_id = c.id and m.user_id = $2::uuid))`, id, *w.user).Scan(&ch.kind, &name)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ch, command.ErrNotFound("channel")
	}
	if name != nil {
		ch.name = *name
	}
	return ch, command.DB(err)
}

// ---------- channels ----------

func channelList(c *command.Ctx, raw json.RawMessage) (any, error) {
	w, err := who(c)
	if err != nil {
		return nil, err
	}
	if w.agent != nil {
		return agentChannels(c, *w.agent)
	}
	u := *w.user
	// A public channel nobody opened yet counts unread from the later of its creation and your joining the workspace.
	rows, err := c.Tx.Query(c.Context,
		`select c.id::text, c.kind, coalesce(c.name, (select u.name from chat.members cm join core.users u on u.id = cm.user_id
		        where cm.channel_id = c.id and cm.user_id <> $1::uuid limit 1), 'Just you'),
		        (select cm.user_id::text from chat.members cm where c.kind = 'dm' and cm.channel_id = c.id and cm.user_id <> $1::uuid limit 1),
		        (select count(*) from chat.messages m where m.channel_id = c.id and m.deleted_at is null and m.user_id is distinct from $1::uuid
		            and m.created_at > coalesce(me.last_read_at, greatest(c.created_at, ws.created_at))),
		        (select max(created_at) from chat.messages m where m.channel_id = c.id)
		   from chat.channels c
		   join core.memberships ws on ws.workspace_id = c.workspace_id and ws.user_id = $1::uuid
		   left join chat.members me on me.channel_id = c.id and me.user_id = $1::uuid
		  where c.kind = 'public' or me.user_id is not null
		  order by 6 desc nulls last, c.created_at`, u)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, name string
		var other *string
		var unread int64
		var last *time.Time
		if err := rows.Scan(&id, &kind, &name, &other, &unread, &last); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "kind": kind, "name": name, "user_id": other, "unread": unread, "last_at": command.TsPtr(last)})
	}
	return out, command.DB(rows.Err())
}

func agentChannels(c *command.Ctx, agent string) (any, error) {
	rows, err := c.Tx.Query(c.Context,
		`select c.id::text, c.kind, c.name, (select max(created_at) from chat.messages m where m.channel_id = c.id)
		   from chat.channels c join chat.agent_members am on am.channel_id = c.id and am.agent_id = $1::uuid
		  where c.kind <> 'dm' order by c.created_at`, agent)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, name string
		var last *time.Time
		if err := rows.Scan(&id, &kind, &name, &last); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "kind": kind, "name": name, "user_id": nil, "unread": 0, "last_at": command.TsPtr(last)})
	}
	return out, command.DB(rows.Err())
}

func channelCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	u, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		Name      string   `json:"name"`
		Private   bool     `json:"private"`
		MemberIDs []string `json:"member_ids"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	name := strings.TrimLeft(strings.TrimSpace(in.Name), "#")
	if n := len([]rune(name)); n < 1 || n > 60 {
		return nil, command.ErrInvalid("name must be 1 to 60 characters")
	}
	kind := "public"
	if in.Private {
		kind = "private"
	}
	if kind == "public" && len(in.MemberIDs) > 0 {
		return nil, command.ErrInvalid("a public channel is open to everyone; member_ids is for private channels")
	}
	var id string
	if err := c.Tx.QueryRow(c.Context,
		`insert into chat.channels (workspace_id, kind, name, created_by) values ($1::uuid,$2,$3,$4::uuid)
		 on conflict do nothing returning id::text`, c.WorkspaceID(), kind, name, u).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("a channel with this name already exists")
	} else if err != nil {
		return nil, command.DB(err)
	}
	for _, m := range append([]string{u}, in.MemberIDs...) {
		if m, err = uid("member_ids", m); err != nil {
			return nil, err
		}
		if err := addMember(c, id, m); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("chat.channel_created", nil, map[string]any{"channel_id": id, "kind": kind}); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "kind": kind, "name": name}, nil
}

// addMember adds a workspace member (not a guest) to a channel; a person who is not one is "not found".
func addMember(c *command.Ctx, channel, user string) error {
	tag, err := c.Tx.Exec(c.Context,
		`insert into chat.members (workspace_id, channel_id, user_id)
		 select workspace_id, $1::uuid, user_id from core.memberships where workspace_id = $3::uuid and user_id = $2::uuid and role <> 'guest'
		 on conflict do nothing`, channel, user, c.WorkspaceID())
	if err != nil {
		return command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		var n int
		if err := c.Tx.QueryRow(c.Context, `select count(*) from chat.members where channel_id = $1::uuid and user_id = $2::uuid`, channel, user).Scan(&n); err != nil {
			return command.DB(err)
		}
		if n == 0 {
			return command.ErrNotFound("person")
		}
	}
	return nil
}

func dmOpen(c *command.Ctx, raw json.RawMessage) (any, error) {
	u, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	other, err := uid("user_id", in.UserID)
	if err != nil {
		return nil, err
	}
	if other == u {
		return nil, command.ErrInvalid("pick someone else to message")
	}
	pair := []string{u, other}
	sort.Strings(pair)
	key := pair[0] + ":" + pair[1]
	var id string
	err = c.Tx.QueryRow(c.Context, `select id::text from chat.channels where kind = 'dm' and dm_key = $1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = c.Tx.QueryRow(c.Context,
			`insert into chat.channels (workspace_id, kind, dm_key, created_by) values ($1::uuid,'dm',$2,$3::uuid) returning id::text`,
			c.WorkspaceID(), key, u).Scan(&id); err != nil {
			return nil, command.DB(err)
		}
		for _, m := range pair {
			if err := addMember(c, id, m); err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"id": id, "kind": "dm"}, nil
}

func memberAdd(c *command.Ctx, raw json.RawMessage) (any, error) {
	if _, err := me(c); err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.kind != "private" {
		return nil, command.ErrInvalid("people can only be added to a private channel")
	}
	user, err := uid("user_id", in.UserID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, addMember(c, ch.id, user)
}

func idOpen(c *command.Ctx, id string) (chanInfo, error) {
	id, err := uid("channel_id", id)
	if err != nil {
		return chanInfo{}, err
	}
	return open(c, id)
}

// ---------- messages ----------

// emojis is the fixed reaction set.
var emojis = []string{"👍", "❤️", "😂", "🎉", "🙏", "👀"}

type msg struct {
	ID          string      `json:"id"`
	UserID      *string     `json:"user_id"`
	AgentID     *string     `json:"agent_id"`
	Agent       bool        `json:"agent"`
	Author      string      `json:"author"`
	Body        string      `json:"body"`
	CreatedAt   command.Ts  `json:"created_at"`
	EditedAt    *command.Ts `json:"edited_at"`
	Deleted     bool        `json:"deleted"`
	Mine        bool        `json:"mine"`
	ParentID    *string     `json:"parent_id"`
	ReplyCount  int64       `json:"reply_count"`
	LastReplyAt *command.Ts `json:"last_reply_at"`
	Links       []link      `json:"links"`
	Reactions   []reaction  `json:"reactions"`
}

// reaction is one emoji on a message: how many people, who (names) and whether the reader is one of them.
type reaction struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	Users []string `json:"users"`
	Mine  bool     `json:"mine"`
}

// link is a page (note, task, invoice) the message points to; the title is only given when the reader may open it.
type link struct {
	NoteID string   `json:"note_id"`
	Title  *string  `json:"title"`
	Types  []string `json:"types"`
}

// fetch loads messages with `where` (on alias m) appended, then their links and reactions for the reader.
func fetch(c *command.Ctx, w ident, where, order string, args ...any) ([]msg, error) {
	rows, err := c.Tx.Query(c.Context,
		`select m.id::text, m.user_id::text, m.agent_id::text, coalesce(u.name, a.name, 'Removed member'), m.body, m.created_at, m.edited_at,
		        m.deleted_at is not null, m.parent_id::text,
		        (select count(*) from chat.messages r where r.parent_id = m.id and r.deleted_at is null),
		        (select max(r.created_at) from chat.messages r where r.parent_id = m.id and r.deleted_at is null)
		   from chat.messages m left join core.users u on u.id = m.user_id left join core.agents a on a.id = m.agent_id
		  where `+where+` order by `+order, args...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	list := []msg{}
	for rows.Next() {
		var m msg
		var at time.Time
		var ed, lr *time.Time
		if err := rows.Scan(&m.ID, &m.UserID, &m.AgentID, &m.Author, &m.Body, &at, &ed, &m.Deleted, &m.ParentID, &m.ReplyCount, &lr); err != nil {
			return nil, command.DB(err)
		}
		m.Agent = m.AgentID != nil
		m.CreatedAt, m.EditedAt, m.LastReplyAt, m.Links, m.Reactions = command.Ts(at), command.TsPtr(ed), command.TsPtr(lr), []link{}, []reaction{}
		m.Mine = (w.user != nil && m.UserID != nil && *m.UserID == *w.user) || (w.agent != nil && m.AgentID != nil && *m.AgentID == *w.agent)
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	rows.Close()
	var spaces []string
	ids := make([]string, len(list))
	for i := range list {
		ids[i] = list[i].ID
		if list[i].Links, err = links(c, list[i].Body, &spaces); err != nil {
			return nil, err
		}
	}
	return list, addReactions(c, w, list, ids)
}

func addReactions(c *command.Ctx, w ident, list []msg, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	reader := ""
	if w.user != nil {
		reader = *w.user
	}
	rows, err := c.Tx.Query(c.Context,
		`select r.message_id::text, r.emoji, u.name, r.user_id::text = $2 from chat.reactions r join core.users u on u.id = r.user_id
		  where r.message_id = any($1::uuid[]) order by r.created_at`, ids, reader)
	if err != nil {
		return command.DB(err)
	}
	defer rows.Close()
	at := map[string]int{}
	for i := range list {
		at[list[i].ID] = i
	}
	for rows.Next() {
		var id, emoji, name string
		var mine bool
		if err := rows.Scan(&id, &emoji, &name, &mine); err != nil {
			return command.DB(err)
		}
		m := &list[at[id]]
		if m.Deleted {
			continue
		}
		k := slices.IndexFunc(m.Reactions, func(r reaction) bool { return r.Emoji == emoji })
		if k < 0 {
			m.Reactions = append(m.Reactions, reaction{Emoji: emoji, Users: []string{}})
			k = len(m.Reactions) - 1
		}
		r := &m.Reactions[k]
		r.Count++
		r.Users = append(r.Users, name)
		r.Mine = r.Mine || mine
	}
	for i := range list {
		slices.SortStableFunc(list[i].Reactions, func(a, b reaction) int { return slices.Index(emojis, a.Emoji) - slices.Index(emojis, b.Emoji) })
	}
	return command.DB(rows.Err())
}

// messages lists the top-level messages of a channel (replies live in their thread; each parent carries reply_count).
func messages(c *command.Ctx, raw json.RawMessage) (any, error) {
	w, err := who(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		Before    string `json:"before"`
		Limit     int    `json:"limit"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 50
	}
	var before *string
	if in.Before != "" {
		b, err := uid("before", in.Before)
		if err != nil {
			return nil, err
		}
		before = &b
	}
	list, err := fetch(c, w, `m.channel_id = $1::uuid and m.parent_id is null
		    and ($2::uuid is null or (m.created_at, m.id) < (select created_at, id from chat.messages where id = $2::uuid and channel_id = $1::uuid))`,
		"m.created_at desc, m.id desc limit $3", ch.id, before, in.Limit+1)
	if err != nil {
		return nil, err
	}
	more := len(list) > in.Limit
	if more {
		list = list[:in.Limit]
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	if w.agent != nil { // agents have no read state
		return map[string]any{"messages": list, "has_more": more, "last_read_at": nil}, nil
	}
	var lastRead time.Time // same default as the unread count: a channel you never opened counts from the later of its creation and your joining
	if err := c.Tx.QueryRow(c.Context,
		`select coalesce((select last_read_at from chat.members where channel_id = $1::uuid and user_id = $2::uuid),
		        greatest((select created_at from chat.channels where id = $1::uuid), (select created_at from core.memberships where workspace_id = $3::uuid and user_id = $2::uuid)))`,
		ch.id, *w.user, c.WorkspaceID()).Scan(&lastRead); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"messages": list, "has_more": more, "last_read_at": command.Ts(lastRead)}, nil
}

// thread returns a top-level message and its replies, oldest first. shortcut: replies are not paged (capped at 500).
func thread(c *command.Ctx, raw json.RawMessage) (any, error) {
	w, err := who(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	id, err := uid("id", in.ID)
	if err != nil {
		return nil, err
	}
	var channel string
	var isReply bool
	err = c.Tx.QueryRow(c.Context, `select channel_id::text, parent_id is not null from chat.messages where id = $1::uuid`, id).Scan(&channel, &isReply)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("message")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := open(c, channel); err != nil {
		return nil, command.ErrNotFound("message")
	}
	if isReply {
		return nil, command.ErrInvalid("replies belong to the message above them; open that one")
	}
	parent, err := fetch(c, w, `m.id = $1::uuid`, "m.id", id)
	if err != nil {
		return nil, err
	}
	replies, err := fetch(c, w, `m.parent_id = $1::uuid`, "m.created_at, m.id limit 500", id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"parent": parent[0], "replies": replies, "channel_id": channel}, nil
}

// links resolves up to 5 page links of this workspace in body for the reader. spaces caches the readable spaces.
func links(c *command.Ctx, body string, spaces *[]string) ([]link, error) {
	out := []link{}
	seen := map[string]bool{}
	for _, m := range noteLink.FindAllStringSubmatch(body, -1) {
		id := strings.ToLower(m[2])
		if len(out) == 5 || seen[id] || strings.ToLower(m[1]) != c.WorkspaceID() {
			continue
		}
		seen[id] = true
		if *spaces == nil {
			var err error
			if *spaces, err = access.ReadableSpaceIDs(c); err != nil {
				return nil, err
			}
		}
		l := link{NoteID: id, Types: []string{}}
		level, err := access.NoteLevel(c, id, *spaces)
		if err != nil {
			return nil, err
		}
		if level != "none" {
			if err := c.Tx.QueryRow(c.Context, `select title, types from core.notes where id = $1::uuid and deleted_at is null`, id).Scan(&l.Title, &l.Types); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, command.DB(err)
			}
		}
		out = append(out, l)
	}
	return out, nil
}

func body(s string) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", command.ErrInvalid("write something first")
	}
	if len([]rune(s)) > maxBody {
		return "", command.ErrInvalid("a message can be up to 4000 characters")
	}
	return s, nil
}

func send(c *command.Ctx, raw json.RawMessage) (any, error) {
	w, err := who(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		Body      string `json:"body"`
		ParentID  string `json:"parent_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	text, err := body(in.Body)
	if err != nil {
		return nil, err
	}
	var parent *string
	if in.ParentID != "" { // one level: the parent is a top-level message of this channel
		p, err := uid("parent_id", in.ParentID)
		if err != nil {
			return nil, err
		}
		var pch string
		var isReply, gone bool
		err = c.Tx.QueryRow(c.Context, `select channel_id::text, parent_id is not null, deleted_at is not null from chat.messages where id = $1::uuid`, p).Scan(&pch, &isReply, &gone)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && pch != ch.id) {
			return nil, command.ErrNotFound("message")
		}
		if err != nil {
			return nil, command.DB(err)
		}
		if isReply {
			return nil, command.ErrInvalid("a reply goes to the top message of the thread, not to another reply")
		}
		if gone {
			return nil, command.ErrConflict("this message was deleted")
		}
		parent = &p
	}
	var id string
	var at time.Time
	if err := c.Tx.QueryRow(c.Context,
		`insert into chat.messages (workspace_id, channel_id, user_id, agent_id, parent_id, body) values ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6) returning id::text, created_at`,
		c.WorkspaceID(), ch.id, w.user, w.agent, parent, text).Scan(&id, &at); err != nil {
		return nil, command.DB(err)
	}
	// Writing in a public channel makes you a member, so its read state is kept; sending also reads the channel.
	if w.user != nil {
		if _, err := c.Tx.Exec(c.Context,
			`insert into chat.members (workspace_id, channel_id, user_id) values ($1::uuid,$2::uuid,$3::uuid)
			 on conflict (channel_id, user_id) do update set last_read_at = now()`, c.WorkspaceID(), ch.id, *w.user); err != nil {
			return nil, command.DB(err)
		}
	}
	if err := notify(c, ch, w, "", text); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "created_at": command.Ts(at)}, nil
}

func mentions(md, ws string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(md, -1) {
		if u := strings.ToLower(m[2]); strings.ToLower(m[1]) == ws && uuidRe.MatchString(u) && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// notify tells people newly @mentioned in newMD (in-app notification, then email when SMTP_URL is set). Only those who
// can see the channel are told: every member for a public one, its members otherwise. An agent's mention has no actor
// person (it is not its owner's words).
func notify(c *command.Ctx, ch chanInfo, actor ident, oldMD, newMD string) error {
	ws := strings.ToLower(c.WorkspaceID())
	old := map[string]bool{}
	for _, u := range mentions(oldMD, ws) {
		old[u] = true
	}
	title := "a direct message"
	if ch.kind != "dm" {
		title = "#" + ch.name
	}
	for _, user := range mentions(newMD, ws) {
		if old[user] || (actor.user != nil && user == *actor.user) {
			continue
		}
		var id, email string
		err := c.Tx.QueryRow(c.Context,
			`insert into core.notifications (workspace_id, user_id, kind, channel_id, actor_user_id, title)
			 select $1::uuid, m.user_id, 'chat_mention', $3::uuid, $4::uuid, $5 from core.memberships m
			  where m.workspace_id = $1::uuid and m.user_id = $2::uuid and m.role <> 'guest'
			    and ($6 or exists (select 1 from chat.members cm where cm.channel_id = $3::uuid and cm.user_id = m.user_id))
			 returning id::text, (select email from core.users where id = $2::uuid)`,
			ws, user, ch.id, actor.user, title, ch.kind == "public").Scan(&id, &email)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return command.DB(err)
		}
		if _, err := c.Emit("chat.mention", nil, map[string]any{"notification_id": id, "channel_id": ch.id}); err != nil {
			return err
		}
		mailMention(c, actor, ws, ch.id, email, title)
	}
	return nil
}

// mailMention emails the mentioned person. shortcut: sent from a goroutine, no retry and no queue (the in-app
// notification already exists); move to the job worker if delivery must be guaranteed. The Rust mention.created email
// handler needs a page, so chat has its own.
func mailMention(c *command.Ctx, actor ident, ws, channel, to, title string) {
	smtpURL, from := strings.TrimSpace(os.Getenv("SMTP_URL")), strings.TrimSpace(os.Getenv("EMAIL_FROM"))
	if smtpURL == "" || from == "" || to == "" {
		return
	}
	base := "http://localhost:3000"
	for _, k := range []string{"PUBLIC_WEB_URL", "PUBLIC_BASE_URL", "WEB_URL"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			base = v
			break
		}
	}
	who := "Someone"
	if actor.user != nil {
		_ = c.Tx.QueryRow(c.Context, `select name from core.users where id = $1::uuid`, *actor.user).Scan(&who)
	} else if actor.agent != nil {
		_ = c.Tx.QueryRow(c.Context, `select name || ' (agent)' from core.agents where id = $1::uuid`, *actor.agent).Scan(&who)
	}
	text := who + " mentioned you in " + title + "\n\n" + strings.TrimRight(base, "/") + "/w/" + ws + "/chat?c=" + channel + "\n"
	go func() {
		_, _ = mailer.Send(context.Background(), mailer.Mail{To: to, Subject: "You were mentioned in " + title, Text: text})
	}()
}

// own loads a message the caller wrote (a person's own, or an agent's own), in a channel the caller can see.
func own(c *command.Ctx, id string) (chanInfo, ident, string, bool, error) {
	w, err := who(c)
	if err != nil {
		return chanInfo{}, w, "", false, err
	}
	if id, err = uid("id", id); err != nil {
		return chanInfo{}, w, "", false, err
	}
	var channel, old string
	var author, agent *string
	var deleted bool
	err = c.Tx.QueryRow(c.Context, `select channel_id::text, user_id::text, agent_id::text, body, deleted_at is not null from chat.messages where id = $1::uuid for update`, id).Scan(&channel, &author, &agent, &old, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return chanInfo{}, w, "", false, command.ErrNotFound("message")
	}
	if err != nil {
		return chanInfo{}, w, "", false, command.DB(err)
	}
	ch, err := open(c, channel)
	if err != nil {
		return ch, w, "", false, command.ErrNotFound("message")
	}
	if !((w.user != nil && author != nil && *author == *w.user) || (w.agent != nil && agent != nil && *agent == *w.agent)) {
		return ch, w, "", false, command.ErrForbidden("you can only change your own messages")
	}
	return ch, w, old, deleted, nil
}

func edit(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, w, old, deleted, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	if deleted {
		return nil, command.ErrConflict("this message was deleted")
	}
	text, err := body(in.Body)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, `update chat.messages set body = $2, edited_at = now() where id = $1::uuid`, strings.ToLower(in.ID), text); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"ok": true}, notify(c, ch, w, old, text)
}

func del(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	if _, _, _, _, err := own(c, in.ID); err != nil {
		return nil, err
	}
	_, err := c.Tx.Exec(c.Context, `update chat.messages set body = '', deleted_at = coalesce(deleted_at, now()) where id = $1::uuid`, strings.ToLower(in.ID))
	return map[string]any{"ok": true}, command.DB(err)
}

func readChannel(c *command.Ctx, raw json.RawMessage) (any, error) {
	w, err := who(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	if w.user == nil { // an agent has no read state
		return map[string]any{"ok": true}, nil
	}
	_, err = c.Tx.Exec(c.Context,
		`insert into chat.members (workspace_id, channel_id, user_id) values ($1::uuid,$2::uuid,$3::uuid)
		 on conflict (channel_id, user_id) do update set last_read_at = now()`, c.WorkspaceID(), ch.id, *w.user)
	return map[string]any{"ok": true}, command.DB(err)
}

// react toggles your reaction (one of the fixed emoji) on a message you can see. People only. Returns whether it is on now.
func react(c *command.Ctx, raw json.RawMessage) (any, error) {
	u, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		MessageID string `json:"message_id"`
		Emoji     string `json:"emoji"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	if !slices.Contains(emojis, in.Emoji) {
		return nil, command.ErrInvalid("emoji must be one of " + strings.Join(emojis, " "))
	}
	id, err := uid("message_id", in.MessageID)
	if err != nil {
		return nil, err
	}
	var channel string
	var gone bool
	err = c.Tx.QueryRow(c.Context, `select channel_id::text, deleted_at is not null from chat.messages where id = $1::uuid for share`, id).Scan(&channel, &gone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("message")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := open(c, channel); err != nil {
		return nil, command.ErrNotFound("message")
	}
	if gone {
		return nil, command.ErrConflict("this message was deleted")
	}
	tag, err := c.Tx.Exec(c.Context, `delete from chat.reactions where message_id = $1::uuid and user_id = $2::uuid and emoji = $3`, id, u, in.Emoji)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() > 0 {
		return map[string]any{"on": false}, nil
	}
	_, err = c.Tx.Exec(c.Context,
		`insert into chat.reactions (workspace_id, channel_id, message_id, user_id, emoji) values ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5) on conflict do nothing`,
		c.WorkspaceID(), channel, id, u, in.Emoji)
	return map[string]any{"on": true}, command.DB(err)
}

// ---------- agents in channels ----------

type agentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func agentRows(c *command.Ctx, q string, args ...any) ([]agentRef, error) {
	rows, err := c.Tx.Query(c.Context, q, args...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []agentRef{}
	for rows.Next() {
		var a agentRef
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, a)
	}
	return out, command.DB(rows.Err())
}

// channelAgents: whether the workspace allows agents in chat, the agents in this channel, and the ones you may add
// (your own, or any when you are an admin). People only.
func channelAgents(c *command.Ctx, raw json.RawMessage) (any, error) {
	u, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	on, err := agentsEnabled(c)
	if err != nil {
		return nil, err
	}
	in_, err := agentRows(c, `select a.id::text, a.name from chat.agent_members m join core.agents a on a.id = m.agent_id where m.channel_id = $1::uuid order by a.name`, ch.id)
	if err != nil {
		return nil, err
	}
	avail := []agentRef{}
	if on && ch.kind != "dm" {
		avail, err = agentRows(c, `select a.id::text, a.name from core.agents a
			where a.workspace_id = $1::uuid and (a.owner_user_id = $2::uuid or $3) and not exists (select 1 from chat.agent_members m where m.channel_id = $4::uuid and m.agent_id = a.id) order by a.name`,
			c.WorkspaceID(), u, c.Principal.Role >= command.Admin, ch.id)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"enabled": on, "agents": in_, "available": avail}, nil
}

// agentAdd puts an agent in a public or private channel (never a direct message). The caller must be able to open the
// channel and own the agent or be an admin. The agent can then read and post there, as itself.
func agentAdd(c *command.Ctx, raw json.RawMessage) (any, error) {
	u, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.kind == "dm" {
		return nil, command.ErrInvalid("agents cannot join a direct message")
	}
	agent, err := uid("agent_id", in.AgentID)
	if err != nil {
		return nil, err
	}
	if on, err := agentsEnabled(c); err != nil {
		return nil, err
	} else if !on {
		return nil, command.ErrInvalid("agents in chat are off for this workspace; an admin can turn them on in settings")
	}
	var owner string
	err = c.Tx.QueryRow(c.Context, `select owner_user_id::text from core.agents where id = $1::uuid and workspace_id = $2::uuid`, agent, c.WorkspaceID()).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("agent")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if owner != u && c.Principal.Role < command.Admin {
		return nil, command.ErrForbidden("you can add your own agents; an admin can add any")
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into chat.agent_members (workspace_id, channel_id, agent_id, added_by) values ($1::uuid,$2::uuid,$3::uuid,$4::uuid) on conflict do nothing`,
		c.WorkspaceID(), ch.id, agent, u); err != nil {
		return nil, command.DB(err)
	}
	_, err = c.Emit("chat.agent_added", nil, map[string]any{"channel_id": ch.id, "agent_id": agent})
	return map[string]any{"ok": true}, err
}

func agentRemove(c *command.Ctx, raw json.RawMessage) (any, error) {
	u, err := me(c)
	if err != nil {
		return nil, err
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := parse(raw, &in); err != nil {
		return nil, err
	}
	ch, err := idOpen(c, in.ChannelID)
	if err != nil {
		return nil, err
	}
	agent, err := uid("agent_id", in.AgentID)
	if err != nil {
		return nil, err
	}
	var owner string
	if err := c.Tx.QueryRow(c.Context, `select owner_user_id::text from core.agents where id = $1::uuid and workspace_id = $2::uuid`, agent, c.WorkspaceID()).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("agent")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if owner != u && c.Principal.Role < command.Admin {
		return nil, command.ErrForbidden("you can remove your own agents; an admin can remove any")
	}
	_, err = c.Tx.Exec(c.Context, `delete from chat.agent_members where channel_id = $1::uuid and agent_id = $2::uuid`, ch.id, agent)
	return map[string]any{"ok": true}, command.DB(err)
}
