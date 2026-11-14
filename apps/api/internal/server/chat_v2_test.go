package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Chat v2 (WP19): live stream, threads, reactions, agents in chat.

// sse is an open event stream; events arrive on ev as {"event": name, ...data}.
type sse struct {
	ev     chan map[string]any
	cancel context.CancelFunc
}

func (b *bt) openStream(w who, ws string, hdr map[string]string) (*sse, int) {
	b.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", b.gosv.URL+"/api/w/"+ws+"/chat/stream", nil)
	w.with(req.Header)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, res.StatusCode
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		b.t.Fatalf("content type %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-transform") {
		b.t.Fatalf("cache-control %q would let a proxy buffer the stream", cc)
	}
	s := &sse{ev: make(chan map[string]any, 100), cancel: cancel}
	go func() {
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				m := map[string]any{}
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m)
				m["event"] = name
				s.ev <- m
			}
		}
	}()
	return s, 200
}

func (b *bt) stream(w who) *sse {
	b.t.Helper()
	s, code := b.openStream(w, b.owner.ws, nil)
	if s == nil {
		b.t.Fatalf("stream: %d", code)
	}
	if e := s.next(b.t, 5*time.Second); e == nil || e["event"] != "ready" {
		b.t.Fatalf("want ready first, got %v", e)
	}
	return s
}

func (s *sse) next(t *testing.T, d time.Duration) map[string]any {
	t.Helper()
	select {
	case e := <-s.ev:
		return e
	case <-time.After(d):
		return nil
	}
}

// want waits for a chat event of kind k and returns it.
func (s *sse) want(t *testing.T, k string) map[string]any {
	t.Helper()
	e := s.next(t, 5*time.Second)
	if e == nil || e["k"] != k {
		t.Fatalf("want a %s event, got %v", k, e)
	}
	return e
}

func (s *sse) quiet(t *testing.T) {
	t.Helper()
	if e := s.next(t, 400*time.Millisecond); e != nil {
		t.Fatalf("unexpected event %v", e)
	}
}

func TestChatStreamAuthorisation(t *testing.T) {
	b := newBT(t, false)
	ann, bob, cy := b.memberAs("Ann", "member"), b.memberAs("Bob", "member"), b.memberAs("Cy", "member")
	priv := b.chan_(ann, "secret", true, bob.userID)
	pub := b.chan_(ann, "open", false)
	dm := b.okAs(ann, "chat.dm_open", map[string]any{"user_id": bob.userID})["id"].(string)
	other := b.env.signup("Other", true)

	cyS, bobS, otherS := b.stream(cy), b.stream(bob), func() *sse {
		s, code := b.openStream(other, other.ws, nil)
		if s == nil {
			t.Fatalf("other workspace stream: %d", code)
		}
		s.next(t, 5*time.Second) // ready
		return s
	}()
	b.say(ann, priv, "private words")
	b.say(ann, dm, "dm words")
	b.say(ann, pub, "public words")
	// Cy is in neither the private channel nor the DM: the first event Cy gets is the public one
	if e := cyS.want(t, "message"); e["c"] != pub {
		t.Fatalf("non-member was told about %v", e)
	}
	cyS.quiet(t)
	// Bob is in all three, in order
	for _, c := range []string{priv, dm, pub} {
		if e := bobS.want(t, "message"); e["c"] != c {
			t.Fatalf("want %s got %v", c, e)
		}
	}
	otherS.quiet(t) // another workspace hears nothing
	// who may open a stream: not another workspace's person on this workspace, not unauthenticated, not a guest, not a key
	if _, code := b.openStream(other, b.owner.ws, nil); code != 403 {
		t.Fatalf("other workspace on this stream: %d", code)
	}
	if _, code := b.openStream(who{}, b.owner.ws, nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	guest := b.env.addMember(b.owner, b.env.signup("Gus", false), "guest")
	if _, code := b.openStream(guest, b.owner.ws, nil); code != 403 {
		t.Fatalf("guest: %d", code)
	}
	if _, code := b.openStream(b.env.apiKey(b.owner), b.owner.ws, nil); code != 403 {
		t.Fatalf("api key: %d", code)
	}
}

func TestChatStreamDeliversEveryChange(t *testing.T) {
	b := newBT(t, false)
	ann, bob := b.memberAs("Ann", "member"), b.memberAs("Bob", "member")
	ch := b.chan_(ann, "live", false)
	s := b.stream(bob)
	id := b.say(ann, ch, "one")
	if e := s.want(t, "message"); e["m"] != id || e["c"] != ch {
		t.Fatalf("send: %v", e)
	}
	b.okAs(ann, "chat.edit", map[string]any{"id": id, "body": "uno"})
	s.want(t, "message")
	reply := b.okAs(ann, "chat.send", map[string]any{"channel_id": ch, "body": "in thread", "parent_id": id})["id"].(string)
	if e := s.want(t, "message"); e["m"] != reply || e["p"] != id {
		t.Fatalf("thread reply: %v", e)
	}
	b.okAs(ann, "chat.react", map[string]any{"message_id": id, "emoji": "👍"})
	if e := s.want(t, "reaction"); e["m"] != id {
		t.Fatalf("reaction: %v", e)
	}
	b.okAs(ann, "chat.react", map[string]any{"message_id": id, "emoji": "👍"}) // toggled off
	s.want(t, "reaction")
	b.okAs(ann, "chat.delete", map[string]any{"id": id})
	s.want(t, "message")
	// reading tells only the reader (their other tabs), not Ann
	annS := b.stream(ann)
	b.okAs(bob, "chat.read", map[string]any{"channel_id": ch})
	s.want(t, "read")
	annS.quiet(t)
	// a reconnect says to refetch; a drift of the clock does not matter
	again, code := b.openStream(bob, b.owner.ws, map[string]string{"Last-Event-ID": "123"})
	if again == nil {
		t.Fatalf("reconnect: %d", code)
	}
	again.next(t, 5*time.Second) // ready
	if e := again.next(t, 5*time.Second); e == nil || e["event"] != "resync" {
		t.Fatalf("want resync, got %v", e)
	}
}

func TestChatThreads(t *testing.T) {
	b := newBT(t, false)
	smtpLog := fakeSMTP(t)
	ann, bob, cy := b.memberAs("Ann", "member"), b.memberAs("Bob", "member"), b.memberAs("Cy", "member")
	ws := b.owner.ws
	priv := b.chan_(ann, "board", true, bob.userID)
	top := b.say(ann, priv, "top")
	r1 := b.okAs(bob, "chat.send", map[string]any{"channel_id": priv, "body": "reply " + mention(ws, ann.userID, "Ann"), "parent_id": top})["id"].(string)
	b.okAs(ann, "chat.send", map[string]any{"channel_id": priv, "body": "reply two", "parent_id": top})
	// replies are not in the channel list; the parent counts them
	m := b.msgs(ann, priv)
	eq(t, len(m), 1)
	eq(t, m[0]["reply_count"], float64(2))
	eq(t, m[0]["last_reply_at"] != nil, true)
	th := b.okAs(bob, "chat.thread", map[string]any{"id": top})
	eq(t, len(th["replies"].([]any)), 2)
	eq(t, th["parent"].(map[string]any)["body"], "top")
	eq(t, th["replies"].([]any)[0].(map[string]any)["parent_id"], top)
	// a mention in a thread reply notifies as usual
	n := b.call(ann, "notification.list", map[string]any{}).jsonList()
	eq(t, len(n), 1)
	eq(t, n[0]["channel_id"], priv)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(smtpLog()) < 1 {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(strings.Join(smtpLog(), " "), strings.ToLower(ann.email)) {
		t.Errorf("no mention email for a thread reply: %v", smtpLog())
	}
	// one level only; same channel only; not on a deleted parent
	b.errAs(ann, "chat.send", map[string]any{"channel_id": priv, "body": "x", "parent_id": r1}, 400, "invalid")
	b.errAs(ann, "chat.thread", map[string]any{"id": r1}, 400, "invalid")
	pub := b.chan_(ann, "elsewhere", false)
	b.errAs(ann, "chat.send", map[string]any{"channel_id": pub, "body": "x", "parent_id": top}, 404, "not_found")
	b.errAs(ann, "chat.send", map[string]any{"channel_id": priv, "body": "x", "parent_id": "nope"}, 400, "invalid")
	// a non-member can neither read, nor reply to, nor react in the thread
	b.errAs(cy, "chat.thread", map[string]any{"id": top}, 404, "not_found")
	b.errAs(cy, "chat.send", map[string]any{"channel_id": priv, "body": "x", "parent_id": top}, 404, "not_found")
	b.errAs(cy, "chat.react", map[string]any{"message_id": r1, "emoji": "👍"}, 404, "not_found")
	b.errAs(b.owner, "chat.thread", map[string]any{"id": top}, 404, "not_found")
	// another workspace cannot see it either
	o2 := b.env.signup("Other", true)
	eq(t, b.env.cmd(b.gosv.URL, o2, o2.ws, "chat.thread", map[string]any{"id": top}, nil).status, 404)
	// deleted replies leave the count; a deleted parent takes no more replies
	b.okAs(bob, "chat.delete", map[string]any{"id": r1})
	eq(t, b.msgs(ann, priv)[0]["reply_count"], float64(1))
	b.okAs(ann, "chat.delete", map[string]any{"id": top})
	b.errAs(ann, "chat.send", map[string]any{"channel_id": priv, "body": "x", "parent_id": top}, 409, "conflict")
	// unread counts replies from others
	eq(t, b.chats(cy)[pub] != nil, true)
}

func TestChatReactions(t *testing.T) {
	b := newBT(t, false)
	ann, bob, cy := b.memberAs("Ann", "member"), b.memberAs("Bob", "member"), b.memberAs("Cy", "member")
	ch := b.chan_(ann, "fun", false)
	id := b.say(ann, ch, "ship it")
	eq(t, b.okAs(bob, "chat.react", map[string]any{"message_id": id, "emoji": "🎉"})["on"], true)
	eq(t, b.okAs(cy, "chat.react", map[string]any{"message_id": id, "emoji": "🎉"})["on"], true)
	b.okAs(bob, "chat.react", map[string]any{"message_id": id, "emoji": "👍"})
	rs := b.msgs(bob, ch)[0]["reactions"].([]any)
	eq(t, len(rs), 2)
	first := rs[0].(map[string]any) // the fixed set's order: thumbs up before party
	eq(t, first["emoji"], "👍")
	party := rs[1].(map[string]any)
	eq(t, party["count"], float64(2))
	eq(t, party["users"], []any{"Bob", "Cy"})
	eq(t, party["mine"], true)
	eq(t, b.msgs(ann, ch)[0]["reactions"].([]any)[1].(map[string]any)["mine"], false)
	// toggle off
	eq(t, b.okAs(bob, "chat.react", map[string]any{"message_id": id, "emoji": "🎉"})["on"], false)
	eq(t, b.msgs(ann, ch)[0]["reactions"].([]any)[1].(map[string]any)["count"], float64(1))
	// only the fixed set, only existing live messages
	b.errAs(bob, "chat.react", map[string]any{"message_id": id, "emoji": "💩"}, 400, "invalid")
	b.errAs(bob, "chat.react", map[string]any{"message_id": id, "emoji": ""}, 400, "invalid")
	b.errAs(bob, "chat.react", map[string]any{"message_id": "00000000-0000-4000-8000-000000000000", "emoji": "👍"}, 404, "not_found")
	b.okAs(ann, "chat.delete", map[string]any{"id": id})
	b.errAs(bob, "chat.react", map[string]any{"message_id": id, "emoji": "👍"}, 409, "conflict")
	eq(t, len(b.msgs(bob, ch)[0]["reactions"].([]any)), 0)
	// private channel: a non-member cannot react; a key cannot
	priv := b.chan_(ann, "hush", true)
	pm := b.say(ann, priv, "x")
	b.errAs(cy, "chat.react", map[string]any{"message_id": pm, "emoji": "👍"}, 404, "not_found")
	b.errAs(b.env.apiKey(b.owner), "chat.react", map[string]any{"message_id": pm, "emoji": "👍"}, 403, "forbidden")
}

// makeAgent creates an agent owned by w (through the Rust server, as the other tests do) and returns its key and id.
func (b *bt) makeAgent(w who, name string) (who, string) {
	b.t.Helper()
	r := b.env.must(w, "agent.create", map[string]any{"name": name, "scopes": []string{"read", "write_notes"}})
	return who{ws: b.owner.ws, bearer: r["api_key"].(string)}, r["agent_id"].(string)
}

func (b *bt) chatAgents(on bool) {
	b.t.Helper()
	b.env.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"chat_agents": on}})
}

func TestChatAgents(t *testing.T) {
	b := newBT(t, false)
	ann, bob := b.memberAs("Ann", "member"), b.memberAs("Bob", "member")
	admin := b.memberAs("Ada", "admin")
	ws := b.owner.ws
	pub := b.chan_(ann, "general", false)
	cy := b.memberAs("Cy", "member")
	priv := b.chan_(ann, "private", true, bob.userID, admin.userID)
	dm := b.okAs(ann, "chat.dm_open", map[string]any{"user_id": bob.userID})["id"].(string)
	b.say(ann, dm, "secret DM")
	b.say(ann, priv, "private plan")
	agent, agentID := b.makeAgent(b.owner, "Helper")
	agentAda, adaAgentID := b.makeAgent(admin, "AdaBot")
	// default off: an agent is refused everywhere, and nobody can add one
	for cmd, in := range map[string]map[string]any{"chat.channel_list": {}, "chat.messages": {"channel_id": pub}, "chat.send": {"channel_id": pub, "body": "x"}} {
		b.errAs(agent, cmd, in, 403, "forbidden")
	}
	b.errAs(ann, "chat.agent_add", map[string]any{"channel_id": pub, "agent_id": adaAgentID}, 400, "invalid")
	eq(t, b.okAs(ann, "chat.channel_agents", map[string]any{"channel_id": pub})["enabled"], false)
	b.chatAgents(true)
	// enabled, but not in any channel: sees nothing, can post nowhere (public channels too)
	eq(t, len(b.call(agent, "chat.channel_list", map[string]any{}).jsonList()), 0)
	for _, c := range []string{pub, priv, dm} {
		b.errAs(agent, "chat.messages", map[string]any{"channel_id": c}, 404, "not_found")
		b.errAs(agent, "chat.send", map[string]any{"channel_id": c, "body": "x"}, 404, "not_found")
	}
	// who may add: your own agent; an admin any; not someone else's; not into a DM or a channel you cannot open
	b.errAs(bob, "chat.agent_add", map[string]any{"channel_id": priv, "agent_id": adaAgentID}, 403, "forbidden")
	b.errAs(ann, "chat.agent_add", map[string]any{"channel_id": priv, "agent_id": agentID}, 403, "forbidden") // the owner's agent
	b.errAs(ann, "chat.agent_add", map[string]any{"channel_id": dm, "agent_id": adaAgentID}, 400, "invalid")
	b.errAs(cy, "chat.agent_add", map[string]any{"channel_id": priv, "agent_id": adaAgentID}, 404, "not_found") // Cy cannot open the channel
	b.errAs(ann, "chat.agent_add", map[string]any{"channel_id": priv, "agent_id": "00000000-0000-4000-8000-000000000000"}, 404, "not_found")
	b.errAs(agent, "chat.agent_add", map[string]any{"channel_id": pub, "agent_id": agentID}, 403, "forbidden") // an agent cannot add itself
	b.okAs(admin, "chat.agent_add", map[string]any{"channel_id": pub, "agent_id": agentID})
	b.okAs(admin, "chat.agent_add", map[string]any{"channel_id": priv, "agent_id": adaAgentID}) // her own
	av := b.okAs(ann, "chat.channel_agents", map[string]any{"channel_id": pub})
	eq(t, len(av["agents"].([]any)), 1)
	eq(t, av["agents"].([]any)[0].(map[string]any)["name"], "Helper")
	// the agent reads and posts only where it was added, never a DM
	eq(t, len(b.call(agent, "chat.channel_list", map[string]any{}).jsonList()), 1)
	eq(t, len(b.msgs(agent, pub)), 0)
	b.errAs(agent, "chat.messages", map[string]any{"channel_id": priv}, 404, "not_found")
	b.errAs(agent, "chat.messages", map[string]any{"channel_id": dm}, 404, "not_found")
	b.errAs(agentAda, "chat.messages", map[string]any{"channel_id": dm}, 404, "not_found")
	if len(b.msgs(agentAda, priv)) != 1 || b.msgs(agentAda, priv)[0]["body"] != "private plan" {
		t.Fatal("an agent added to a private channel reads it")
	}
	// posts show the agent, never the owner
	id := b.okAs(agent, "chat.send", map[string]any{"channel_id": pub, "body": "hello from the agent"})["id"].(string)
	for _, w := range []who{ann, bob, b.owner} {
		m := b.msgs(w, pub)[0]
		eq(t, m["author"], "Helper")
		eq(t, m["agent"], true)
		eq(t, m["agent_id"], agentID)
		eq(t, m["user_id"], nil)
		eq(t, m["mine"], false) // not even the agent's owner
	}
	eq(t, b.msgs(agent, pub)[0]["mine"], true)
	eq(t, b.chats(bob)[pub]["unread"], float64(1))
	b.errAs(b.owner, "chat.edit", map[string]any{"id": id, "body": "hijack"}, 403, "forbidden")
	b.errAs(b.owner, "chat.delete", map[string]any{"id": id}, 403, "forbidden")
	b.okAs(agent, "chat.edit", map[string]any{"id": id, "body": "edited by the agent"})
	annMsg := b.say(ann, pub, "mine")
	b.errAs(agent, "chat.edit", map[string]any{"id": annMsg, "body": "x"}, 403, "forbidden")
	// a thread reply by an agent, with a mention that notifies as the agent (no actor person)
	b.okAs(agent, "chat.send", map[string]any{"channel_id": pub, "body": "ping " + mention(ws, bob.userID, "Bob"), "parent_id": annMsg})
	n := b.call(bob, "notification.list", map[string]any{}).jsonList()
	eq(t, len(n), 1)
	eq(t, n[0]["actor_name"], nil)
	eq(t, b.okAs(bob, "chat.thread", map[string]any{"id": annMsg})["replies"].([]any)[0].(map[string]any)["author"], "Helper")
	// agents are people-only for everything else
	for cmd, in := range map[string]map[string]any{
		"chat.channel_create": {"name": "bots"}, "chat.dm_open": {"user_id": ann.userID}, "chat.react": {"message_id": id, "emoji": "👍"},
		"chat.channel_agents": {"channel_id": pub}, "chat.agent_remove": {"channel_id": pub, "agent_id": agentID}, "chat.member_add": {"channel_id": priv, "user_id": bob.userID},
	} {
		b.errAs(agent, cmd, in, 403, "forbidden")
	}
	// a plain API key still has no chat identity
	for _, in := range []map[string]any{{"channel_id": pub}} {
		b.errAs(b.env.apiKey(b.owner), "chat.messages", in, 403, "forbidden")
	}
	// removing the agent, or turning the setting off, ends its access
	b.errAs(bob, "chat.agent_remove", map[string]any{"channel_id": pub, "agent_id": agentID}, 403, "forbidden")
	b.okAs(admin, "chat.agent_remove", map[string]any{"channel_id": pub, "agent_id": agentID})
	b.errAs(agent, "chat.messages", map[string]any{"channel_id": pub}, 404, "not_found")
	b.chatAgents(false)
	b.errAs(agentAda, "chat.messages", map[string]any{"channel_id": priv}, 403, "forbidden")
	// the other workspace's agent is not in this workspace
	o2 := b.env.signup("Other", true)
	b.errAs(ann, "chat.agent_add", map[string]any{"channel_id": pub, "agent_id": b.env.must(o2, "agent.create", map[string]any{"name": "Foreign", "scopes": []string{"read"}})["agent_id"]}, 400, "invalid") // setting off again
	b.chatAgents(true)
	b.errAs(ann, "chat.agent_add", map[string]any{"channel_id": pub, "agent_id": b.env.must(o2, "agent.create", map[string]any{"name": "Foreign", "scopes": []string{"read"}})["agent_id"]}, 404, "not_found")
}

func TestChatAgentPostIsStreamed(t *testing.T) {
	b := newBT(t, false)
	ann := b.memberAs("Ann", "member")
	b.chatAgents(true)
	agent, agentID := b.makeAgent(b.owner, "Streamer")
	ch := b.chan_(ann, "bots", false)
	b.okAs(b.owner, "chat.agent_add", map[string]any{"channel_id": ch, "agent_id": agentID})
	s := b.stream(ann)
	id := b.okAs(agent, "chat.send", map[string]any{"channel_id": ch, "body": "beep"})["id"].(string)
	if e := s.want(t, "message"); e["m"] != id {
		t.Fatalf("agent post: %v", e)
	}
}
