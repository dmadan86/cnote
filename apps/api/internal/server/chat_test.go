package server_test

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Team chat (WP16). Fixtures come from the Rust server; every chat call runs on the Go server.

func (b *bt) chan_(w who, name string, private bool, members ...string) string {
	b.t.Helper()
	return b.okAs(w, "chat.channel_create", map[string]any{"name": name, "private": private, "member_ids": members})["id"].(string)
}

func (b *bt) say(w who, channel, body string) string {
	b.t.Helper()
	return b.okAs(w, "chat.send", map[string]any{"channel_id": channel, "body": body})["id"].(string)
}

func (b *bt) msgs(w who, channel string) []map[string]any {
	b.t.Helper()
	out := []map[string]any{}
	for _, m := range b.okAs(w, "chat.messages", map[string]any{"channel_id": channel})["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func (b *bt) chats(w who) map[string]map[string]any {
	b.t.Helper()
	out := map[string]map[string]any{}
	for _, r := range b.call(w, "chat.channel_list", map[string]any{}).jsonList() {
		out[r["id"].(string)] = r
	}
	return out
}

func mention(ws, user, name string) string {
	return "[@" + name + "](/w/" + ws + "/people/" + user + ")"
}

func TestChatPrivateChannelAccess(t *testing.T) {
	b := newBT(t, false)
	ann, bob, cy := b.memberAs("Ann", "member"), b.memberAs("Bob", "member"), b.memberAs("Cy", "member")
	pub := b.chan_(ann, "general", false)
	priv := b.chan_(ann, "secret", true, bob.userID)
	b.say(ann, priv, "launch plan")
	b.say(ann, pub, "hello all")

	// a public channel is open to every member; a private one only to its members
	eq(t, len(b.msgs(cy, pub)), 1)
	eq(t, len(b.msgs(bob, priv)), 1)
	for _, in := range []struct {
		cmd string
		in  map[string]any
	}{
		{"chat.messages", map[string]any{"channel_id": priv}},
		{"chat.send", map[string]any{"channel_id": priv, "body": "hi"}},
		{"chat.read", map[string]any{"channel_id": priv}},
		{"chat.member_add", map[string]any{"channel_id": priv, "user_id": cy.userID}},
	} {
		b.errAs(cy, in.cmd, in.in, 404, "not_found") // not even its existence leaks
	}
	if _, ok := b.chats(cy)[priv]; ok {
		t.Fatal("private channel listed for a non-member")
	}
	// even the owner and admins are outside a private channel they were not added to
	b.errAs(b.owner, "chat.messages", map[string]any{"channel_id": priv}, 404, "not_found")
	// a member of a private channel adds someone; a guest cannot be added or use chat
	b.okAs(bob, "chat.member_add", map[string]any{"channel_id": priv, "user_id": cy.userID})
	eq(t, len(b.msgs(cy, priv)), 1)
	guest := b.env.addMember(b.owner, b.env.signup("Gus", false), "guest")
	b.errAs(bob, "chat.member_add", map[string]any{"channel_id": priv, "user_id": guest.userID}, 404, "not_found")
	b.errAs(guest, "chat.channel_list", map[string]any{}, 403, "forbidden")
	b.errAs(ann, "chat.member_add", map[string]any{"channel_id": pub, "user_id": cy.userID}, 400, "invalid")
	// names are unique (case-insensitive); input is checked
	b.errAs(bob, "chat.channel_create", map[string]any{"name": "General"}, 409, "conflict")
	b.errAs(bob, "chat.channel_create", map[string]any{"name": " "}, 400, "invalid")
	b.errAs(bob, "chat.channel_create", map[string]any{"name": "x", "member_ids": []string{cy.userID}}, 400, "invalid")
	b.errAs(bob, "chat.send", map[string]any{"channel_id": pub, "body": "   "}, 400, "invalid")
	b.errAs(bob, "chat.send", map[string]any{"channel_id": pub, "body": strings.Repeat("x", 4001)}, 400, "invalid")
	b.errAs(bob, "chat.send", map[string]any{"channel_id": "nope", "body": "x"}, 400, "invalid")
}

func TestChatDirectMessagesOnlyForTheTwo(t *testing.T) {
	b := newBT(t, false)
	ann, bob, cy := b.memberAs("Ann", "member"), b.memberAs("Bob", "member"), b.memberAs("Cy", "member")
	dm := b.okAs(ann, "chat.dm_open", map[string]any{"user_id": bob.userID})["id"].(string)
	eq(t, b.okAs(bob, "chat.dm_open", map[string]any{"user_id": ann.userID})["id"], dm) // one DM per pair, from either side
	b.say(ann, dm, "psst")
	eq(t, b.chats(bob)[dm]["name"], "Ann")
	eq(t, b.chats(bob)[dm]["unread"], float64(1))
	eq(t, b.chats(ann)[dm]["unread"], float64(0)) // your own message is never unread
	for _, w := range []who{cy, b.owner} {
		b.errAs(w, "chat.messages", map[string]any{"channel_id": dm}, 404, "not_found")
		b.errAs(w, "chat.send", map[string]any{"channel_id": dm, "body": "x"}, 404, "not_found")
		if _, ok := b.chats(w)[dm]; ok {
			t.Fatal("DM listed for a third person")
		}
	}
	b.errAs(ann, "chat.dm_open", map[string]any{"user_id": ann.userID}, 400, "invalid")
	b.errAs(ann, "chat.dm_open", map[string]any{"user_id": "00000000-0000-4000-8000-000000000000"}, 404, "not_found")
	// a DM cannot take a third person
	b.errAs(ann, "chat.member_add", map[string]any{"channel_id": dm, "user_id": cy.userID}, 400, "invalid")
}

func TestChatUnreadAndRead(t *testing.T) {
	b := newBT(t, false)
	ann, bob := b.memberAs("Ann", "member"), b.memberAs("Bob", "member")
	ch := b.chan_(ann, "ops", false)
	b.say(ann, ch, "one")
	gone := b.say(ann, ch, "two")
	b.say(ann, ch, "three")
	eq(t, b.chats(bob)[ch]["unread"], float64(3))
	b.okAs(ann, "chat.delete", map[string]any{"id": gone}) // a deleted message is not unread
	eq(t, b.chats(bob)[ch]["unread"], float64(2))
	b.okAs(bob, "chat.read", map[string]any{"channel_id": ch})
	eq(t, b.chats(bob)[ch]["unread"], float64(0))
	b.say(ann, ch, "four")
	eq(t, b.chats(bob)[ch]["unread"], float64(1))
	b.say(bob, ch, "five") // replying reads the channel
	eq(t, b.chats(bob)[ch]["unread"], float64(0))
	// paging: newest first window, oldest first inside it
	r := b.okAs(bob, "chat.messages", map[string]any{"channel_id": ch, "limit": 2})
	eq(t, r["has_more"], true)
	eq(t, len(r["messages"].([]any)), 2)
	eq(t, r["messages"].([]any)[1].(map[string]any)["body"], "five")
	older := b.okAs(bob, "chat.messages", map[string]any{"channel_id": ch, "limit": 10, "before": r["messages"].([]any)[0].(map[string]any)["id"]})
	eq(t, len(older["messages"].([]any)), 3)
	eq(t, older["has_more"], false)
}

func TestChatEditDeleteOnlyOwn(t *testing.T) {
	b := newBT(t, false)
	ann, bob := b.memberAs("Ann", "member"), b.memberAs("Bob", "member")
	ch := b.chan_(ann, "dev", false)
	id := b.say(ann, ch, "teh plan")
	b.errAs(bob, "chat.edit", map[string]any{"id": id, "body": "hijack"}, 403, "forbidden")
	b.errAs(bob, "chat.delete", map[string]any{"id": id}, 403, "forbidden")
	b.errAs(b.owner, "chat.delete", map[string]any{"id": id}, 403, "forbidden") // not even the workspace owner
	b.okAs(ann, "chat.edit", map[string]any{"id": id, "body": "the plan"})
	m := b.msgs(bob, ch)[0]
	eq(t, m["body"], "the plan")
	eq(t, m["edited_at"] != nil, true)
	eq(t, m["mine"], false)
	b.errAs(ann, "chat.edit", map[string]any{"id": id, "body": ""}, 400, "invalid")
	b.okAs(ann, "chat.delete", map[string]any{"id": id})
	b.okAs(ann, "chat.delete", map[string]any{"id": id}) // again: no error
	m = b.msgs(bob, ch)[0]
	eq(t, m["deleted"], true)
	eq(t, m["body"], "")
	b.errAs(ann, "chat.edit", map[string]any{"id": id, "body": "back"}, 409, "conflict")
	// a message in a channel you cannot see is not found, not forbidden
	priv := b.chan_(ann, "hush", true)
	pm := b.say(ann, priv, "x")
	b.errAs(bob, "chat.edit", map[string]any{"id": pm, "body": "y"}, 404, "not_found")
	b.errAs(bob, "chat.delete", map[string]any{"id": "00000000-0000-4000-8000-000000000000"}, 404, "not_found")
}

func TestChatMentionsNotifyAndEmail(t *testing.T) {
	smtpLog := fakeSMTP(t)
	b := newBT(t, false)
	ann, bob, cy := b.memberAs("Ann", "member"), b.memberAs("Bob", "member"), b.memberAs("Cy", "member")
	ws := b.owner.ws
	priv := b.chan_(ann, "board", true, bob.userID)
	b.say(ann, priv, "ping "+mention(ws, bob.userID, "Bob")+" and "+mention(ws, cy.userID, "Cy")+" and myself "+mention(ws, ann.userID, "Ann"))
	n := b.call(bob, "notification.list", map[string]any{}).jsonList()
	eq(t, len(n), 1)
	eq(t, n[0]["kind"], "chat_mention")
	eq(t, n[0]["channel_id"], priv)
	eq(t, n[0]["note_title"], "#board")
	eq(t, n[0]["actor_name"], "Ann")
	eq(t, len(b.call(cy, "notification.list", map[string]any{}).jsonList()), 0) // outside the private channel: not told
	eq(t, len(b.call(ann, "notification.list", map[string]any{}).jsonList()), 0)
	eq(t, b.okAs(bob, "notification.unread_count", map[string]any{})["count"], float64(1))
	// a mention of someone in another workspace is ignored; editing only notifies newly added people
	o2 := b.env.signup("Other", true)
	id := b.say(ann, priv, "x "+mention(ws, bob.userID, "Bob"))
	b.okAs(ann, "chat.edit", map[string]any{"id": id, "body": "x " + mention(ws, bob.userID, "Bob") + mention(ws, o2.userID, "Other") + " " + mention(ws, b.owner.userID, "Tess")})
	eq(t, len(b.call(bob, "notification.list", map[string]any{}).jsonList()), 2)
	eq(t, len(b.call(b.owner, "notification.list", map[string]any{}).jsonList()), 0) // owner is not in the private channel
	// public channel: any member can be mentioned
	pub := b.chan_(ann, "all", false)
	b.say(ann, pub, "hi "+mention(ws, cy.userID, "Cy"))
	eq(t, len(b.call(cy, "notification.list", map[string]any{}).jsonList()), 1)
	// the email went out for Bob's first mention and Cy's, but not for the filtered ones
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(smtpLog()) < 3 {
		time.Sleep(50 * time.Millisecond)
	}
	got := strings.Join(smtpLog(), "\n")
	for _, w := range []who{bob, cy} {
		if !strings.Contains(got, strings.ToLower(w.email)) {
			t.Errorf("no mention email to %s in %q", w.email, got)
		}
	}
	if strings.Contains(got, strings.ToLower(ann.email)) || strings.Contains(got, strings.ToLower(o2.email)) {
		t.Errorf("email to someone who was not mentioned or cannot see the channel: %q", got)
	}
}

// fakeSMTP is a plain SMTP sink; it returns a reader of the RCPT TO lines. SMTP_URL points the server at it.
func fakeSMTP(t *testing.T) func() []string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	t.Setenv("SMTP_URL", "smtp://"+l.Addr().String())
	t.Setenv("EMAIL_FROM", "chat@example.test")
	var mu sync.Mutex
	var rcpt []string
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("220 x\r\n"))
				sc, inData := bufio.NewScanner(c), false
				for sc.Scan() {
					line := sc.Text()
					switch {
					case inData:
						if line == "." {
							inData = false
							c.Write([]byte("250 ok\r\n"))
						}
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						c.Write([]byte("250 x\r\n"))
					case strings.HasPrefix(line, "RCPT TO:"):
						mu.Lock()
						rcpt = append(rcpt, strings.ToLower(line))
						mu.Unlock()
						c.Write([]byte("250 ok\r\n"))
					case line == "DATA":
						inData = true
						c.Write([]byte("354 go\r\n"))
					case line == "QUIT":
						c.Write([]byte("221 bye\r\n"))
						return
					default:
						c.Write([]byte("250 ok\r\n"))
					}
				}
			}()
		}
	}()
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), rcpt...) }
}

func TestChatLinksShowOnlyPagesTheReaderMayOpen(t *testing.T) {
	b := newBT(t, false)
	ann, bob := b.memberAs("Ann", "member"), b.memberAs("Bob", "member")
	ws := b.owner.ws
	open := b.env.note(b.owner, "Q3 plan")
	hidden := b.must(b.owner, "space.create", map[string]any{"key": "HID", "name": "Hidden", "visibility": "private"})["id"].(string)
	secret := b.must(b.owner, "note.create", map[string]any{"space_id": hidden, "title": "Salaries"})["id"].(string)
	ch := b.chan_(ann, "links", false)
	b.say(b.owner, ch, "see /w/"+ws+"/n/"+open+" and [pay](/w/"+ws+"/n/"+secret+") and /w/"+ws+"/n/"+open)
	for _, w := range []who{b.owner, bob} {
		l := b.msgs(w, ch)[0]["links"].([]any)
		eq(t, len(l), 2) // the same page twice is one chip
		eq(t, l[0].(map[string]any)["title"], "Q3 plan")
		if w.userID == bob.userID {
			eq(t, l[1].(map[string]any)["title"], nil) // a private space's page: no title for Bob
		} else {
			eq(t, l[1].(map[string]any)["title"], "Salaries")
		}
	}
	_ = ann
}

func TestChatMachinesAndOtherWorkspace(t *testing.T) {
	b := newBT(t, false)
	ann := b.memberAs("Ann", "member")
	ch := b.chan_(ann, "ops", false)
	// a plain API key is not a person
	key := b.env.apiKey(b.owner)
	for cmd, in := range map[string]map[string]any{
		"chat.channel_list": {}, "chat.messages": {"channel_id": ch}, "chat.send": {"channel_id": ch, "body": "x"},
		"chat.channel_create": {"name": "bot"}, "chat.dm_open": {"user_id": ann.userID}, "chat.read": {"channel_id": ch},
	} {
		b.errAs(key, cmd, in, 403, "forbidden")
	}
	// an agent acts for its owner, but chat is people only, whatever its tool groups
	b.enableGroups("read", "write_notes")
	agent := b.agentKey("chatbot")
	for cmd, in := range map[string]map[string]any{"chat.channel_list": {}, "chat.send": {"channel_id": ch, "body": "x"}, "chat.messages": {"channel_id": ch}} {
		b.errAs(agent, cmd, in, 403, "forbidden")
	}
	// another workspace sees nothing and can touch nothing
	o2 := b.env.signup("Other", true)
	r := b.env.cmd(b.gosv.URL, o2, o2.ws, "chat.channel_list", map[string]any{}, nil)
	eq(t, len(r.jsonList()), 0)
	for _, cmd := range []string{"chat.messages", "chat.send", "chat.read"} {
		r := b.env.cmd(b.gosv.URL, o2, o2.ws, cmd, map[string]any{"channel_id": ch, "body": "x"}, nil)
		eq(t, r.status, 404)
	}
	r = b.env.cmd(b.gosv.URL, o2, o2.ws, "chat.dm_open", map[string]any{"user_id": ann.userID}, nil)
	eq(t, r.status, 404)
	eq(t, len(b.msgs(ann, ch)), 0) // nothing was posted through the machines
}

func TestChatSendReplayIsIdempotent(t *testing.T) {
	b := newBT(t, false)
	ch := b.chan_(b.owner, "idem", false)
	h := map[string]string{"Idempotency-Key": "chat-" + rnd()}
	in := map[string]any{"channel_id": ch, "body": "once"}
	a := b.env.cmd(b.gosv.URL, b.owner, b.owner.ws, "chat.send", in, h)
	c := b.env.cmd(b.gosv.URL, b.owner, b.owner.ws, "chat.send", in, h)
	eq(t, a.status, 200)
	eq(t, string(a.body), string(c.body))
	eq(t, len(b.msgs(b.owner, ch)), 1)
}
