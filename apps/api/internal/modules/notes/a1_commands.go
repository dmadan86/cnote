package notes

import (
	"encoding/json"
	"strings"

	"github.com/cnote/api/internal/command"
)

// Phase A1 command contract (docs-parity 5.4 as revised by 7.2 and 7.6). Every command has an input schema, an
// output schema (A1OutputSchema) and a description that also says what it returns, because MCP shows only the
// description and the input. The handlers are stubs: the work package that owns a command (7.8) registers the real
// one from an init() in its own file, as handlers["label.set"] = labelSet.
//
// Not here, on purpose:
//   - note.update, search.notes, template.list and template.render are extended in place by their packages. The new
//     fields are in apps/web/src/lib/docs.ts (status, icon, wide, schema; search filters; built-in templates).
//   - trash, undelete, duplicate and move are A2 (7.2).
//   - version compare uses note.revisions and note.revision; Compare needs read access, restore edit access (7.6).
//
// Roles are the minimum workspace role; "page view" and "page edit" are core.note_access on the page, checked with
// access.AssertNoteAccess. Every list applies the full access predicate inside the query (7.6). star, watch and
// viewed change only the caller's own row but are in write_notes, not read: the read-only AI scope must not hold
// them (7.6). Guests may use them on pages shared with them.

const (
	sA1ID   = `{"type":"string","description":"UUID"}`
	sA1Str  = `{"type":"string"}`
	sA1Int  = `{"type":"integer"}`
	sA1Bool = `{"type":"boolean"}`
	sA1TS   = `{"type":"string","description":"RFC 3339 timestamp"}`
)

func a1Obj(required []string, kv ...string) string {
	var p []string
	for i := 0; i < len(kv); i += 2 {
		p = append(p, `"`+kv[i]+`":`+kv[i+1])
	}
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return `{"type":"object","properties":{` + strings.Join(p, ",") + `},"required":` + string(req) + `}`
}
func a1Arr(item string) string   { return `{"type":"array","items":` + item + `}` }
func a1Nullable(s string) string { return `{"anyOf":[` + s + `,{"type":"null"}]}` }
func a1Enum(vals ...string) string {
	b, _ := json.Marshal(vals)
	return `{"type":"string","enum":` + string(b) + `}`
}
func a1Max(item string, n int) string {
	b, _ := json.Marshal(n)
	return `{"type":"array","maxItems":` + string(b) + `,"items":` + item + `}`
}

var (
	sA1Label = `{"type":"string","pattern":"^[^\\s,]{1,40}$","description":"Lower case, 1 to 40 characters, no spaces or commas. Upper case input is lowered"}`
	// A row of note.mine: enough for a sidebar entry or a home list, no body.
	sA1PageRef = a1Obj([]string{"id", "space_id", "title", "icon", "status", "updated_at", "at"},
		"id", sA1ID, "space_id", sA1ID, "title", sA1Str, "icon", a1Nullable(sA1Str),
		"status", a1Nullable(a1Enum("draft", "in_review", "final")), "updated_at", sA1TS,
		"at", sA1TS)
)

type a1Spec struct {
	name, desc, in, out string
	group               command.Group
	guest               bool
}

var a1Specs = []a1Spec{
	{"label.set", "Replace the labels of a page (at most 20; each lower case, 1 to 40 characters, no spaces or commas; duplicates are merged). Needs edit access to the page. Emits note.labels_changed. Returns {note_id, labels}: the stored labels, sorted.",
		a1Obj([]string{"note_id", "labels"}, "note_id", sA1ID, "labels", a1Max(sA1Label, 20)),
		a1Obj([]string{"note_id", "labels"}, "note_id", sA1ID, "labels", a1Arr(sA1Str)),
		command.GroupWriteNotes, false},
	{"label.list", "Labels used on pages the caller can read, with how many such pages carry each, most used first. prefix narrows by start of name (autocomplete); limit defaults to 50, at most 200. Counts and names come only from readable pages. Returns [{label, count}].",
		a1Obj(nil, "prefix", sA1Str, "limit", sA1Int),
		a1Arr(a1Obj([]string{"label", "count"}, "label", sA1Str, "count", sA1Int)),
		command.GroupRead, true},
	{"note.star", "Star or unstar a page for the caller only. Needs view access; guests may use it on pages shared with them. Returns {note_id, starred}.",
		a1Obj([]string{"note_id", "starred"}, "note_id", sA1ID, "starred", sA1Bool),
		a1Obj([]string{"note_id", "starred"}, "note_id", sA1ID, "starred", sA1Bool),
		command.GroupWriteNotes, true},
	{"note.watch", "Watch or unwatch a page for the caller only. Watchers get a notification for each comment and, at most once a day per page, for a content change. Creating a page, becoming its owner or commenting on it starts a watch. Needs view access; guests may use it on pages shared with them. Returns {note_id, watching}.",
		a1Obj([]string{"note_id", "watching"}, "note_id", sA1ID, "watching", sA1Bool),
		a1Obj([]string{"note_id", "watching"}, "note_id", sA1ID, "watching", sA1Bool),
		command.GroupWriteNotes, true},
	{"note.viewed", "Record that the caller opened a page, for the Recent list: sets last_viewed_at to now. Needs view access; guests may use it on pages shared with them. There is no view count. Returns {note_id, at}.",
		a1Obj([]string{"note_id"}, "note_id", sA1ID),
		a1Obj([]string{"note_id", "at"}, "note_id", sA1ID, "at", sA1TS),
		command.GroupWriteNotes, true},
	{"note.mine", "The caller's own page lists: recent (last opened, newest first), starred (newest star first) or watching. Pages the caller can no longer read, or that were deleted, are left out before the limit applies. limit defaults to 20, at most 100. `at` is the time of the opening, the star or the watch. Returns [{id, space_id, title, icon, status, updated_at, at}].",
		a1Obj([]string{"list"}, "list", a1Enum("recent", "starred", "watching"), "limit", sA1Int),
		a1Arr(sA1PageRef),
		command.GroupRead, true},
}

// a1Handlers is where the work packages register real implementations, from an init() in their own files.
var a1Handlers = map[string]command.Handler{}

func a1NotImplemented(name string) command.Handler {
	return func(*command.Ctx, json.RawMessage) (any, error) {
		return nil, command.NewError(command.Internal, name+" is not implemented yet")
	}
}

func a1Commands() []command.Command {
	cmds := make([]command.Command, 0, len(a1Specs))
	for _, s := range a1Specs {
		h := a1Handlers[s.name]
		if h == nil {
			h = a1NotImplemented(s.name)
		}
		c := command.NewCommand(s.name, s.group, h)
		c.Description, c.Schema = s.desc, json.RawMessage(s.in)
		if s.guest {
			c.MinRole = command.Guest // page access is checked per note
		}
		cmds = append(cmds, c)
	}
	return cmds
}

// A1OutputSchema is the JSON Schema of an A1 command's result; apps/web/src/lib/docs.ts mirrors it.
func A1OutputSchema(name string) json.RawMessage {
	for _, s := range a1Specs {
		if s.name == name {
			return json.RawMessage(s.out)
		}
	}
	return nil
}
