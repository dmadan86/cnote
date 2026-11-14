package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Twin-fixture differential test for the note write commands: the same script runs against fixture A through
// the Rust server and against an identical fixture B through the Go server. Responses (status and body, ids and
// timestamps normalized by order of appearance) and the resulting database rows must be equal. Afterwards every
// read command is compared raw (bytes) between both servers on each fixture.

var (
	uuidTok = regexp.MustCompile(`(?i)[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}`)
	tsTok   = regexp.MustCompile(`\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(\.\d+)?(Z|\+00:00)?`)
)

type fixture struct {
	owner, member, guest, bob, carol, agent, apikey, other, noauth, badkey who
	space                                                                  string
}

func (e *env) fixture() fixture {
	o := e.signup("Owner", true)
	f := fixture{owner: o, other: e.signup("Other", true), noauth: who{}, badkey: who{bearer: "cn_x_y"}}
	f.member = e.addMember(o, e.signup("Member", false), "member")
	f.guest = e.addMember(o, e.signup("Guest", false), "guest")
	f.bob = e.addMember(o, e.signup("Bob", false), "member")
	f.carol = e.addMember(o, e.signup("Carol", false), "member")
	f.agent = who{ws: o.ws, bearer: e.must(o, "agent.create", map[string]any{"name": "diff-bot"})["api_key"].(string)}
	f.apikey = e.apiKey(o)
	f.space = e.space(o)
	return f
}

func (f fixture) who(n string) who {
	return map[string]who{"owner": f.owner, "member": f.member, "guest": f.guest, "bob": f.bob, "carol": f.carol,
		"agent": f.agent, "apikey": f.apikey, "other": f.other, "noauth": f.noauth, "badkey": f.badkey}[n]
}

type runner struct {
	e          *env
	base       string
	f          fixture
	vars       map[string]string
	toks       map[string]string
	transcript []string
}

func (r *runner) norm(s string) string {
	s = tsTok.ReplaceAllString(s, "TS")
	return uuidTok.ReplaceAllStringFunc(s, func(u string) string {
		u = strings.ReplaceAll(strings.ToLower(u), "-", "")
		if t, ok := r.toks[u]; ok {
			return t
		}
		t := fmt.Sprintf("<id%d>", len(r.toks))
		r.toks[u] = t
		return t
	})
}

// call runs a command as a named caller and records the normalized response. Returns the parsed body.
func (r *runner) call(caller, cmd string, in any) map[string]any {
	r.e.t.Helper()
	time.Sleep(30 * time.Millisecond) // stay under the fair-use refill rate (owner, agent and API key share a bucket)
	res := r.e.cmd(r.base, r.f.who(caller), r.f.owner.ws, cmd, in, nil)
	body := string(res.body)
	if cmd == "search.notes" && res.status == 200 {
		// rows with equal rank come back in physical order, which differs between twin fixtures; the
		// same-fixture raw comparison below checks the real order
		var hits []json.RawMessage
		if json.Unmarshal(res.body, &hits) == nil {
			parts := make([]string, len(hits))
			for i, h := range hits {
				parts[i] = r.norm(string(h))
			}
			sort.Strings(parts)
			body = strings.Join(parts, "\n")
		}
	}
	r.transcript = append(r.transcript, fmt.Sprintf("%s %s -> %d %s", caller, cmd, res.status, r.norm(body)))
	return res.json()
}

func (r *runner) raw(caller, cmd, body string) {
	r.e.t.Helper()
	time.Sleep(30 * time.Millisecond)
	w := r.f.who(caller)
	w.ws = r.f.owner.ws
	res := r.e.rawJSON(r.base, w, cmd, body)
	r.transcript = append(r.transcript, fmt.Sprintf("%s %s RAW %s -> %d %s", caller, cmd, r.norm(body), res.status, r.norm(string(res.body))))
}

func (r *runner) id(name, caller, cmd string, in any) {
	r.e.t.Helper()
	m := r.call(caller, cmd, in)
	if id, ok := m["id"].(string); ok {
		r.vars[name] = id
	}
}

func (r *runner) v(n string) string { return r.vars[n] }

func script(r *runner) {
	sp := r.f.space
	long := strings.Repeat("y", 501)
	// ---- space.create
	r.id("eng", "owner", "space.create", map[string]any{"key": " eng ", "name": " Engineering "})
	r.id("prv", "owner", "space.create", map[string]any{"key": "prv", "name": "Private", "visibility": "private", "template": "docs"})
	r.id("mprv", "member", "space.create", map[string]any{"key": "MPV", "name": "Member private", "visibility": "private"})
	r.id("agsp", "agent", "space.create", map[string]any{"key": "AG1", "name": "Agents"})
	r.call("apikey", "space.create", map[string]any{"key": "APK", "name": "Key space"})
	for _, in := range []map[string]any{
		{"key": "bad key!", "name": "x"}, {"key": "", "name": "x"}, {"key": "   ", "name": "x"}, {"key": "toolongkey123", "name": "x"},
		{"key": "ENG", "name": "dup"}, {"key": "VIS", "name": "x", "visibility": "bogus"}, {"key": "TPL", "name": "tpl", "template": "bogus"},
		{"key": "ß", "name": "sharp"}, {"key": "ﬁx", "name": "ligature"}, {"key": "é", "name": "accent"}, {"key": "UNI", "name": " ünï 🚀 "},
		{"key": "NUL", "name": "a\u0000b"}, {"key": "X", "name": nil}, {"key": 5, "name": "x"}, {"name": "x"}, {"key": "NULLS", "name": "nulls", "template": nil, "visibility": nil},
	} {
		r.call("owner", "space.create", in)
	}
	r.call("guest", "space.create", map[string]any{"key": "GST", "name": "x"})
	r.call("noauth", "space.create", map[string]any{"key": "NOA", "name": "x"})
	r.call("badkey", "space.create", map[string]any{"key": "BAD", "name": "x"})
	r.call("other", "space.create", map[string]any{"key": "OTH", "name": "x"})
	r.raw("owner", "space.create", "[]")
	r.raw("owner", "space.create", "{bad")
	r.call("owner", "space.list", map[string]any{})
	r.call("member", "space.list", map[string]any{})

	// ---- note.create
	r.id("top", "owner", "note.create", map[string]any{"space_id": sp, "title": "  Top <b>&  \"q\" ünï 🚀  ", "content_md": "zebrafish plan 日本語\n\n<script>&"})
	r.id("mid", "owner", "note.create", map[string]any{"space_id": sp, "title": "Mid", "parent_id": r.v("top"), "content_md": "zebrafish mid"})
	r.id("leaf", "member", "note.create", map[string]any{"space_id": sp, "title": "Leaf", "parent_id": r.v("mid")})
	r.id("task", "owner", "note.create", map[string]any{"space_id": sp, "title": "A task", "types": []string{"task", "meeting"}, "owner_id": r.f.bob.userID, "verify_by": "2030-02-28"})
	r.id("typed", "member", "note.create", map[string]any{"space_id": sp, "title": "Typed no verify", "types": []string{"deal"}})
	r.id("engnote", "owner", "note.create", map[string]any{"space_id": r.v("eng"), "title": "In ENG", "content_md": "quokka"})
	r.id("prvnote", "owner", "note.create", map[string]any{"space_id": r.v("prv"), "title": "Private note", "content_md": "quokka secret"})
	r.id("agnote", "agent", "note.create", map[string]any{"space_id": sp, "title": "by agent"})
	r.id("apnote", "apikey", "note.create", map[string]any{"space_id": sp, "title": "by api key"})
	r.id("empty", "owner", "note.create", map[string]any{"space_id": sp, "title": "", "content_md": ""})
	r.call("owner", "note.create", map[string]any{"space_id": r.v("eng"), "title": "x", "parent_id": r.v("top")}) // cross space
	r.call("owner", "note.create", map[string]any{"space_id": sp, "title": "x", "parent_id": "00000000-0000-4000-8000-000000000000"})
	r.call("owner", "note.create", map[string]any{"space_id": "00000000-0000-4000-8000-000000000000", "title": "x"})
	r.call("member", "note.create", map[string]any{"space_id": r.v("prv"), "title": "x"}) // private, not a member
	r.call("guest", "note.create", map[string]any{"space_id": sp, "title": "x"})
	r.call("noauth", "note.create", map[string]any{"space_id": sp, "title": "x"})
	r.call("other", "note.create", map[string]any{"space_id": sp, "title": "x"})
	r.call("owner", "note.create", map[string]any{"space_id": sp, "title": "x", "owner_id": "00000000-0000-4000-8000-000000000000"}) // FK violation
	r.call("owner", "note.create", map[string]any{"space_id": sp, "title": "nul\u0000"})
	for _, raw := range []string{
		`{"space_id":"` + sp + `"}`, `{"title":"x"}`, `{"space_id":"` + sp + `","title":5}`, `{"space_id":"nope","title":"x"}`,
		`{"space_id":"` + sp + `","title":"x","types":null}`, `{"space_id":"` + sp + `","title":"x","types":[1]}`,
		`{"space_id":"` + sp + `","title":"x","verify_by":"2024-13-45"}`, `{"space_id":"` + sp + `","title":"x","verify_by":"2024-1-5"}`,
		`{"space_id":"` + sp + `","title":"x","verify_by":"2024-02-30"}`, `{"space_id":"` + strings.ReplaceAll(sp, "-", "") + `","title":"x"}`,
		`{"space_id":"` + sp + `","title":"\ud800"}`, `null`, `[]`, `{bad`,
	} {
		r.raw("owner", "note.create", raw)
	}
	r.call("owner", "note.create", map[string]any{"space_id": sp, "title": "nulls ok", "parent_id": nil, "content_md": nil, "owner_id": nil, "verify_by": nil})

	// ---- note.update
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "title": " Top v2 ", "content_md": "zebrafish v2"})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "content_md": "zebrafish v2"}) // same content: no checkpoint, version still bumps
	r.call("owner", "note.update", map[string]any{"id": r.v("top")})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "title": nil, "content_md": nil, "parent_id": nil})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "expected_version": 3, "title": "ok v"})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "expected_version": 3, "title": "stale"})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "expected_epoch": 7, "content_md": "stale epoch"})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "expected_epoch": 7, "title": "epoch only matters for content"})
	r.call("owner", "note.update", map[string]any{"id": r.v("top"), "expected_epoch": 0, "content_md": "right epoch ünï"})
	r.call("owner", "note.update", map[string]any{"id": r.v("task"), "owner_id": r.f.carol.userID, "verify_by": "2031-01-01"})
	r.call("owner", "note.update", map[string]any{"id": r.v("task"), "owner_id": "00000000-0000-4000-8000-000000000000"})
	for _, c := range [][2]string{{"top", "top"}, {"top", "leaf"}, {"mid", "leaf"}, {"top", "engnote"}, {"mid", "missing"}} {
		p := r.v(c[1])
		if c[1] == "missing" {
			p = "00000000-0000-4000-8000-000000000000"
		}
		r.call("owner", "note.update", map[string]any{"id": r.v(c[0]), "parent_id": p})
	}
	r.call("owner", "note.update", map[string]any{"id": r.v("leaf"), "parent_id": r.v("top")}) // legal move
	r.call("member", "note.update", map[string]any{"id": r.v("prvnote"), "title": "x"})
	r.call("guest", "note.update", map[string]any{"id": r.v("top"), "title": "x"})
	r.call("noauth", "note.update", map[string]any{"id": r.v("top"), "title": "x"})
	r.call("other", "note.update", map[string]any{"id": r.v("top"), "title": "x"})
	r.call("agent", "note.update", map[string]any{"id": r.v("agnote"), "title": "agent edit", "content_md": "agent wrote"})
	r.call("owner", "note.update", map[string]any{"id": "00000000-0000-4000-8000-000000000000", "title": "x"})
	for _, raw := range []string{`{}`, `{"id":"nope"}`, `{"id":"` + r.v("top") + `","expected_version":1.5}`, `{"id":"` + r.v("top") + `","expected_version":"1"}`,
		`{"id":"` + r.v("top") + `","expected_version":99999999999}`, `{"id":"` + r.v("top") + `","verify_by":"2024-1-5"}`, `{"id":"` + r.v("top") + `","title":7}`} {
		r.raw("owner", "note.update", raw)
	}
	// shares and restrictions (proxied to Rust in both runs)
	r.call("owner", "note.share", map[string]any{"note_id": r.v("task"), "email": r.f.guest.email, "level": "edit"})
	r.call("owner", "note.share", map[string]any{"note_id": r.v("typed"), "email": r.f.guest.email, "level": "view"})
	r.call("guest", "note.update", map[string]any{"id": r.v("task"), "title": "guest edit", "content_md": "guest wrote"})
	r.call("guest", "note.update", map[string]any{"id": r.v("typed"), "title": "x"})
	r.call("guest", "note.update", map[string]any{"id": r.v("empty"), "title": "x"})
	r.call("guest", "comment.add", map[string]any{"note_id": r.v("task"), "body": "guest comment"})
	r.call("owner", "note.restrict", map[string]any{"note_id": r.v("mid"), "restriction": "edit"})
	r.call("bob", "note.update", map[string]any{"id": r.v("mid"), "title": "x"})
	r.call("bob", "note.update", map[string]any{"id": r.v("leaf"), "title": "x"})
	r.call("bob", "note.create", map[string]any{"space_id": sp, "title": "x", "parent_id": r.v("mid")})
	r.call("owner", "note.restrict", map[string]any{"note_id": r.v("empty"), "restriction": "view"})
	r.call("bob", "note.update", map[string]any{"id": r.v("empty"), "title": "x"})
	r.call("bob", "note.update", map[string]any{"id": r.v("top"), "parent_id": r.v("empty")})
	r.call("bob", "note.create", map[string]any{"space_id": sp, "title": "x", "parent_id": r.v("empty")})

	// ---- mentions
	link := func(u string) string { return fmt.Sprintf("[@x](/w/%s/people/%s)", r.f.owner.ws, u) }
	r.id("men", "owner", "note.create", map[string]any{"space_id": sp, "title": "Mentions", "content_md": ""})
	body := "hi " + link(r.f.bob.userID) + " and " + link(r.f.owner.userID) + " " + strings.ToUpper(link(r.f.member.userID))
	r.call("owner", "note.update", map[string]any{"id": r.v("men"), "content_md": body})
	r.call("owner", "note.update", map[string]any{"id": r.v("men"), "content_md": body + " more"})
	r.call("owner", "note.update", map[string]any{"id": r.v("men"), "content_md": body + " more [@x](/w/00000000-0000-4000-8000-000000000000/people/" + r.f.guest.userID + ")"})
	r.call("owner", "note.restrict", map[string]any{"note_id": r.v("men"), "restriction": "view"})
	r.call("owner", "note.update", map[string]any{"id": r.v("men"), "content_md": body + " " + link(r.f.carol.userID) + " " + link(r.f.guest.userID) + " " + link("00000000-0000-4000-8000-000000000000")})
	r.call("bob", "notification.unread_count", map[string]any{})
	r.call("carol", "notification.unread_count", map[string]any{})
	r.call("bob", "notification.list", map[string]any{})

	// ---- note.list
	for _, c := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth", "badkey"} {
		for _, in := range []any{
			map[string]any{}, map[string]any{"space_id": sp}, map[string]any{"space_id": r.v("eng")}, map[string]any{"space_id": r.v("prv")},
			map[string]any{"parent_id": r.v("top")}, map[string]any{"parent_id": r.v("mid")}, map[string]any{"type": "page"}, map[string]any{"type": "task"},
			map[string]any{"type": "meeting", "limit": 1}, map[string]any{"limit": 0}, map[string]any{"limit": -5}, map[string]any{"limit": 1000000}, map[string]any{"limit": nil, "type": nil},
			map[string]any{"space_id": "00000000-0000-4000-8000-000000000000"}, map[string]any{"type": ""},
		} {
			r.call(c, "note.list", in)
		}
	}
	r.raw("owner", "note.list", `{"limit":1.5}`)
	r.raw("owner", "note.list", `{"limit":"5"}`)
	r.raw("owner", "note.list", `{"space_id":"x"}`)
	r.raw("owner", "note.list", `[]`)
	r.call("owner", "note.list", nil)

	// ---- relations
	r.call("owner", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": r.v("task"), "kind": "blocks"})
	r.call("owner", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": r.v("task"), "kind": "blocks"}) // duplicate: no-op
	r.call("owner", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": r.v("top"), "kind": "relates"})
	r.call("owner", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": r.v("prvnote"), "kind": "relates"})
	r.call("owner", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": r.v("engnote"), "kind": ""})
	r.call("owner", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": "00000000-0000-4000-8000-000000000000", "kind": "relates"})
	r.call("owner", "relation.add", map[string]any{"from_id": "00000000-0000-4000-8000-000000000000", "to_id": r.v("top"), "kind": "relates"})
	r.call("member", "relation.add", map[string]any{"from_id": r.v("typed"), "to_id": r.v("prvnote"), "kind": "relates"})
	r.call("member", "relation.add", map[string]any{"from_id": r.v("typed"), "to_id": r.v("top"), "kind": "depends_on"})
	r.call("bob", "relation.add", map[string]any{"from_id": r.v("typed"), "to_id": r.v("empty"), "kind": "relates"})
	r.call("bob", "relation.add", map[string]any{"from_id": r.v("empty"), "to_id": r.v("typed"), "kind": "relates"})
	r.call("guest", "relation.add", map[string]any{"from_id": r.v("task"), "to_id": r.v("top"), "kind": "relates"})
	r.call("noauth", "relation.add", map[string]any{"from_id": r.v("top"), "to_id": r.v("task"), "kind": "x"})
	r.call("agent", "relation.add", map[string]any{"from_id": r.v("agnote"), "to_id": r.v("top"), "kind": "relates"})
	r.raw("owner", "relation.add", `{"from_id":"nope","to_id":"nope","kind":"x"}`)
	r.raw("owner", "relation.add", `{"from_id":"`+r.v("top")+`"}`)
	r.call("owner", "note.get", map[string]any{"id": r.v("top")})
	r.call("member", "note.get", map[string]any{"id": r.v("typed")})
	r.call("owner", "relation.remove", map[string]any{"from_id": r.v("top"), "to_id": r.v("task"), "kind": "blocks"})
	r.call("owner", "relation.remove", map[string]any{"from_id": r.v("top"), "to_id": r.v("task"), "kind": "blocks"}) // nothing left
	r.call("owner", "relation.remove", map[string]any{"from_id": r.v("top"), "to_id": "00000000-0000-4000-8000-000000000000", "kind": "x"})
	r.call("bob", "relation.remove", map[string]any{"from_id": r.v("empty"), "to_id": r.v("typed"), "kind": "relates"})
	r.call("guest", "relation.remove", map[string]any{"from_id": r.v("top"), "to_id": r.v("typed"), "kind": "depends_on"})
	r.call("member", "relation.remove", map[string]any{"from_id": r.v("typed"), "to_id": r.v("top"), "kind": "depends_on"})
	r.raw("owner", "relation.remove", `{"from_id":"nope","to_id":"nope"}`)

	// ---- comments
	r.id("c1", "bob", "comment.add", map[string]any{"note_id": r.v("top"), "body": "  Which one?  ", "anchor_text": "zebrafish", "anchor_prefix": "right epoch "})
	r.id("c2", "owner", "comment.add", map[string]any{"note_id": r.v("top"), "body": "plain comment ünï 🚀", "anchor_text": "   ", "anchor_prefix": "ignored"})
	r.id("c3", "owner", "comment.add", map[string]any{"note_id": r.v("top"), "body": "reply " + link(r.f.bob.userID), "parent_id": r.v("c1")})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "parent_id": r.v("c3")})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "parent_id": r.v("c1"), "anchor_text": "ship"})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("task"), "body": "x", "parent_id": r.v("c1")}) // parent on another note
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "parent_id": "00000000-0000-4000-8000-000000000000"})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "anchor_text": long})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "anchor_text": "ok", "anchor_prefix": strings.Repeat("p", 65)})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "anchor_text": strings.Repeat("é", 500), "anchor_prefix": strings.Repeat("🚀", 64)})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x", "anchor_text": strings.Repeat("é", 501)})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("top"), "body": "   "})
	r.call("member", "comment.add", map[string]any{"note_id": r.v("empty"), "body": "x"})
	r.call("bob", "comment.add", map[string]any{"note_id": r.v("empty"), "body": "x"}) // view-restricted: bob listed? no
	r.call("guest", "comment.add", map[string]any{"note_id": r.v("top"), "body": "guest no share"})
	r.call("guest", "comment.add", map[string]any{"note_id": r.v("typed"), "body": "guest view share"})
	r.call("noauth", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x"})
	r.call("other", "comment.add", map[string]any{"note_id": r.v("top"), "body": "x"})
	r.call("agent", "comment.add", map[string]any{"note_id": r.v("top"), "body": "agent says hi"})
	r.call("apikey", "comment.add", map[string]any{"note_id": r.v("top"), "body": "key says hi"})
	r.call("member", "comment.add", map[string]any{"note_id": "00000000-0000-4000-8000-000000000000", "body": "x"})
	r.raw("owner", "comment.add", `{"note_id":"`+r.v("top")+`"}`)
	r.raw("owner", "comment.add", `{"note_id":"`+r.v("top")+`","body":"x","anchor_text":5}`)
	r.raw("owner", "comment.add", `{"note_id":"`+r.v("top")+`","body":"\ud800"}`)
	r.call("bob", "notification.unread_count", map[string]any{})
	for _, c := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth"} {
		r.call(c, "comment.list", map[string]any{"note_id": r.v("top")})
	}
	r.call("owner", "comment.list", map[string]any{"note_id": r.v("empty")})
	r.call("bob", "comment.list", map[string]any{"note_id": r.v("empty")})
	r.call("guest", "comment.list", map[string]any{"note_id": r.v("task")})
	r.call("owner", "comment.list", map[string]any{"note_id": "00000000-0000-4000-8000-000000000000"})
	r.raw("owner", "comment.list", `{}`)
	r.call("bob", "comment.resolve", map[string]any{"id": r.v("c1")})
	r.call("owner", "comment.resolve", map[string]any{"id": r.v("c1"), "resolved": false})
	r.call("owner", "comment.resolve", map[string]any{"id": r.v("c1"), "resolved": true})
	r.call("owner", "comment.resolve", map[string]any{"id": r.v("c1")}) // already resolved
	r.call("owner", "comment.resolve", map[string]any{"id": r.v("c3")}) // a reply: not found
	r.call("owner", "comment.resolve", map[string]any{"id": "00000000-0000-4000-8000-000000000000"})
	r.call("guest", "comment.resolve", map[string]any{"id": r.v("c2")})
	r.call("noauth", "comment.resolve", map[string]any{"id": r.v("c2")})
	r.call("other", "comment.resolve", map[string]any{"id": r.v("c2")})
	r.raw("owner", "comment.resolve", `{"id":"`+r.v("c1")+`","resolved":null}`)
	r.raw("owner", "comment.resolve", `{"id":"`+r.v("c1")+`","resolved":"yes"}`)
	r.call("owner", "comment.add", map[string]any{"note_id": r.v("empty"), "body": "on the view-restricted page"})
	r.id("c9", "owner", "comment.add", map[string]any{"note_id": r.v("empty"), "body": "restricted thread"})
	r.call("bob", "comment.resolve", map[string]any{"id": r.v("c9")})

	// ---- search.notes
	for _, c := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth"} {
		for _, in := range []any{
			map[string]any{"q": "zebrafish"}, map[string]any{"q": "  quokka  "}, map[string]any{"q": "quo"}, map[string]any{"q": "Top"},
			map[string]any{"q": "Mentions", "limit": 1}, map[string]any{"q": "Mentions", "limit": 0}, map[string]any{"q": "zebrafish", "limit": 500},
			map[string]any{"q": "\"zebrafish plan\" -mid"}, map[string]any{"q": "日本語"}, map[string]any{"q": "ünï"}, map[string]any{"q": ""}, map[string]any{"q": "   "},
			map[string]any{"q": "%"}, map[string]any{"q": "_"}, map[string]any{"q": "a' or 1=1 --"}, map[string]any{"q": "by"},
			map[string]any{"q": "zebrafish", "include_archived": true}, map[string]any{"q": "Mentions"}, map[string]any{"q": "private"},
		} {
			r.call(c, "search.notes", in)
		}
	}
	r.raw("owner", "search.notes", `{}`)
	r.raw("owner", "search.notes", `{"q":5}`)
	r.raw("owner", "search.notes", `{"q":"x","include_archived":null}`)
	r.raw("owner", "search.notes", `{"q":"x","limit":"1"}`)

	// ---- views
	r.id("v1", "owner", "view.create", map[string]any{"name": "Open tasks", "layout": "table", "query": "project = ENG AND status != Done", "options": map[string]any{"b": 1, "a": []any{1, "x", nil, true}, "n": map[string]any{"z": 1, "y": "ünï"}}})
	r.id("v2", "member", "view.create", map[string]any{"space_id": sp, "name": "Board", "layout": "board", "query": "type = task"})
	r.id("v3", "owner", "view.create", map[string]any{"space_id": r.v("prv"), "name": "Private view", "layout": "list", "query": "x", "options": nil})
	r.call("owner", "view.create", map[string]any{"name": "Bad layout", "layout": "bogus", "query": "x"})
	r.call("owner", "view.create", map[string]any{"name": "Bad space", "layout": "list", "query": "x", "space_id": "00000000-0000-4000-8000-000000000000"})
	r.call("guest", "view.create", map[string]any{"name": "g", "layout": "list", "query": "x"})
	r.call("noauth", "view.create", map[string]any{"name": "g", "layout": "list", "query": "x"})
	r.call("agent", "view.create", map[string]any{"name": "agent view", "layout": "calendar", "query": "x", "options": map[string]any{"k": "v"}})
	r.raw("owner", "view.create", `{"name":"f1","layout":"list","query":"x","options":{"f":1.5}}`)
	r.raw("owner", "view.create", `{"name":"f2","layout":"list","query":"x","options":{"f":1e2}}`)
	r.raw("owner", "view.create", `{"name":"f3","layout":"list","query":"x","options":[1,2,{"a":99999999999999999999}]}`)
	r.raw("owner", "view.create", `{"name":"f4","layout":"list","query":"x","options":"text"}`)
	r.raw("owner", "view.create", `{"name":"f5","layout":"list"}`)
	for _, c := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth", "badkey"} {
		for _, in := range []any{map[string]any{}, map[string]any{"space_id": sp}, map[string]any{"space_id": r.v("prv")}, map[string]any{"space_id": nil}} {
			r.call(c, "view.list", in)
		}
	}
	r.raw("owner", "view.list", `{"space_id":5}`)

	// ---- note.delete (last: it hides notes from everything above)
	r.call("agent", "note.delete", map[string]any{"id": r.v("agnote")}) // destructive group off for agents
	r.call("guest", "note.delete", map[string]any{"id": r.v("task")})
	r.call("bob", "note.delete", map[string]any{"id": r.v("leaf")}) // under a restricted parent
	r.call("member", "note.delete", map[string]any{"id": r.v("prvnote")})
	r.call("noauth", "note.delete", map[string]any{"id": r.v("apnote")})
	r.call("other", "note.delete", map[string]any{"id": r.v("apnote")})
	r.call("member", "note.delete", map[string]any{"id": r.v("apnote")})
	r.call("member", "note.delete", map[string]any{"id": r.v("apnote")}) // already deleted
	r.call("owner", "note.delete", map[string]any{"id": "00000000-0000-4000-8000-000000000000"})
	r.call("owner", "note.delete", map[string]any{"id": r.v("engnote")})
	r.raw("owner", "note.delete", `{}`)
	r.raw("owner", "note.delete", `{"id":"nope"}`)
	r.call("owner", "note.list", map[string]any{"space_id": sp})
	r.call("owner", "search.notes", map[string]any{"q": "quokka"})
	r.call("owner", "comment.list", map[string]any{"note_id": r.v("engnote")})
}

// dump reads the rows the commands touch, normalized, in a deterministic order.
func (r *runner) dump() []string {
	r.e.t.Helper()
	tx, err := dbTenant(r.e, r.f.owner.ws)
	if err != nil {
		r.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out []string
	for _, q := range []struct{ name, sql string }{
		{"spaces", "select to_jsonb(s)::text from core.spaces s order by s.key"},
		{"space_members", "select to_jsonb(m)::text from core.space_members m join core.spaces s on s.id = m.space_id order by s.key, m.role"},
		{"notes", "select (to_jsonb(n) - 'search')::text from core.notes n order by n.title, n.created_at, n.content_md"},
		{"relations", "select to_jsonb(x)::text from core.relations x order by x.kind, x.created_at"},
		{"comments", "select to_jsonb(x)::text from core.comments x order by x.created_at"},
		{"revisions", "select to_jsonb(x)::text from core.note_revisions x order by x.created_at, x.number"},
		{"notifications", "select to_jsonb(x)::text from core.notifications x order by x.kind, x.title, x.created_at"},
		{"views", "select to_jsonb(x)::text from core.views x order by x.name, x.options::text"},
		// the Rust worker adds asynchronous system events; compare what the commands emitted
		{"events", "select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind not like 'doc.%' order by x.id"},
	} {
		rows, err := tx.Query(context.Background(), q.sql)
		if err != nil {
			r.e.t.Fatalf("%s: %v", q.name, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				r.e.t.Fatal(err)
			}
			out = append(out, q.name+" "+r.norm(s))
		}
		rows.Close()
	}
	return out
}

func TestDifferentialNoteWrites(t *testing.T) {
	e := newEnv(t)
	run := func(base string) *runner {
		r := &runner{e: e, base: base, f: e.fixture(), vars: map[string]string{}, toks: map[string]string{}}
		script(r)
		r.transcript = append(r.transcript, r.dump()...)
		return r
	}
	rust, gov := run(e.rust), run(e.gosv.URL)
	if len(rust.transcript) != len(gov.transcript) {
		seen := map[string]int{}
		for _, l := range rust.transcript {
			seen[l]++
		}
		for _, l := range gov.transcript {
			seen[l]--
		}
		for l, n := range seen {
			if n != 0 {
				t.Logf("%+d %.600s", n, l)
			}
		}
		t.Fatalf("transcript lengths differ: rust %d go %d", len(rust.transcript), len(gov.transcript))
	}
	bad, ok2xx, errs := 0, 0, 0
	for i := range rust.transcript {
		if rust.transcript[i] != gov.transcript[i] {
			bad++
			if bad <= 15 {
				t.Errorf("step %d differs\n rust %.700s\n go   %.700s", i, rust.transcript[i], gov.transcript[i])
			}
		}
		if strings.Contains(rust.transcript[i], "-> 200 ") {
			ok2xx++
		} else if strings.Contains(rust.transcript[i], " -> 4") {
			errs++
		}
	}
	if ok2xx < 150 || errs < 100 {
		t.Fatalf("script not rich enough: %d ok, %d errors", ok2xx, errs)
	}
	t.Logf("compared %d steps and rows (%d ok responses, %d 4xx); %d differ", len(rust.transcript), ok2xx, errs, bad)

	// reads: raw bytes, Go vs Rust, on both fixtures (same database state, so ids need no normalization)
	for _, r := range []*runner{rust, gov} {
		reads := 0
		for _, cn := range []string{"owner", "member", "guest", "bob", "agent", "apikey", "other", "noauth", "badkey"} {
			w := r.f.who(cn)
			var calls []struct {
				cmd string
				in  any
			}
			add := func(cmd string, in any) {
				calls = append(calls, struct {
					cmd string
					in  any
				}{cmd, in})
			}
			add("note.list", map[string]any{})
			add("note.list", map[string]any{"space_id": r.f.space, "type": "page", "limit": 3})
			add("note.list", map[string]any{"parent_id": r.v("top")})
			add("search.notes", map[string]any{"q": "zebrafish"})
			add("search.notes", map[string]any{"q": "top", "include_archived": true})
			add("view.list", map[string]any{})
			for _, n := range []string{"top", "mid", "task", "typed", "empty", "men", "prvnote"} {
				add("note.get", map[string]any{"id": r.v(n)})
				add("comment.list", map[string]any{"note_id": r.v(n)})
			}
			for _, c := range calls {
				time.Sleep(55 * time.Millisecond) // stay under the fair-use refill rate of the shared Rust server
				g := e.cmd(e.gosv.URL, w, r.f.owner.ws, c.cmd, c.in, nil)
				ru := e.cmd(e.rust, w, r.f.owner.ws, c.cmd, c.in, nil)
				reads++
				if g.status != ru.status || string(g.body) != string(ru.body) {
					t.Errorf("%s %s %v\n go   %d %.700s\n rust %d %.700s", cn, c.cmd, c.in, g.status, g.body, ru.status, ru.body)
				}
			}
		}
		t.Logf("raw-compared %d read pairs", reads)
	}
}
