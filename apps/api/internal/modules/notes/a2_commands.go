package notes

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
)

// Phase A2 command contract (docs-parity 7.5 and 7.9, the binding text). Same shape as a1_commands.go: input schema,
// output schema (A2OutputSchema), a description that says what comes back (MCP shows only description and input), and a
// stub that answers "not implemented" until work package L1 registers the real handler from an init() in its own file:
// a2Handlers["note.move"] = noteMove.
//
// note.delete is not registered here: it exists in write.go and L1 rewrites it there. Its spec is listed (existing: true)
// so its new output schema and description live beside the other lifecycle commands.
//
// Every command here is for members and above. Guests never change the tree, delete, restore or duplicate, even on
// pages shared with them: these commands act on other pages (children, siblings, parents). All of them except note.trash
// change state, so none is in the read-only AI scope (note.delete is destructive and off for agents by default).

var (
	sA2Restore = a1Enum("original", "space_root", "blocked")
	sA2Person  = a1Obj([]string{"user_id", "name"}, "user_id", sA1ID, "name", sA1Str)
)

type a2Spec struct {
	name, desc, in, out string
	group               command.Group
	existing            bool // registered elsewhere; only the schemas and description are owned here
}

var a2Specs = []a2Spec{
	{"note.move", "Move a page and everything under it to another parent in the same space, or to the space root (parent_id null). before_id places it before that sibling; omitted, it goes last. Needs edit access to every page in the subtree and to both the old and the new parent (or the space root); if the move would drop a restriction the pages inherit, also needs to be that page's owner or a space admin. Otherwise fails with 'Some pages here are restricted'. A page cannot move under itself, and cannot change space. Emits note.moved. Returns {id, space_id, parent_id, position}.",
		a1Obj([]string{"id", "parent_id"}, "id", sA1ID, "parent_id", a1Nullable(sA1ID), "before_id", a1Nullable(sA1ID)),
		a1Obj([]string{"id", "space_id", "parent_id", "position"}, "id", sA1ID, "space_id", sA1ID, "parent_id", a1Nullable(sA1ID), "position", sA1Int),
		command.GroupWriteNotes, false},
	{"note.reorder", "Change a page's place among its siblings without changing its parent: before_id is the sibling it goes before, null for last. Needs edit access to the page and to the parent (or the space root). Siblings you cannot see keep their order. Emits note.reordered. Returns {id, parent_id, position}.",
		a1Obj([]string{"id", "before_id"}, "id", sA1ID, "before_id", a1Nullable(sA1ID)),
		a1Obj([]string{"id", "parent_id", "position"}, "id", sA1ID, "parent_id", a1Nullable(sA1ID), "position", sA1Int),
		command.GroupWriteNotes, false},
	{"note.delete", "Move a page and all its sub-pages to the trash as one deletion. Needs edit access to every live page in the subtree; otherwise fails with 'Some pages here are restricted'. Pages already in the trash keep their own deletion. Emits note.deleted for each page. Restore it with note.undelete. Returns {deleted: true, deletion_id, count}.",
		a1Obj([]string{"id"}, "id", sA1ID),
		a1Obj([]string{"deleted", "deletion_id", "count"}, "deleted", sA1Bool, "deletion_id", sA1ID, "count", sA1Int),
		command.GroupDestructive, true},
	{"note.trash", "Deletions the caller could restore, newest first: a deletion is listed only if the caller could edit every page in it. limit defaults to 50, at most 200; the next page is asked with before (deleted_at) and before_id (deletion_id) of the last row of the previous page; a page shorter than limit is the last. restore_to says where note.undelete would put the page: its original parent, the space root, or blocked (with blocked_reason). Returns [{deletion_id, space_id, root: {id, title, icon}, page_count, deleted_at, deleted_by, restore_to, blocked_reason}].",
		a1Obj(nil, "space_id", sA1ID, "limit", sA1Int, "before", sA1TS, "before_id", sA1ID),
		a1Arr(a1Obj([]string{"deletion_id", "space_id", "root", "page_count", "deleted_at", "deleted_by", "restore_to", "blocked_reason"},
			"deletion_id", sA1ID, "space_id", sA1ID,
			"root", a1Obj([]string{"id", "title", "icon"}, "id", sA1ID, "title", sA1Str, "icon", a1Nullable(sA1Str)),
			"page_count", sA1Int, "deleted_at", sA1TS, "deleted_by", a1Nullable(sA2Person),
			"restore_to", sA2Restore, "blocked_reason", a1Nullable(sA1Str))),
		command.GroupRead, false},
	{"note.undelete", "Restore a deletion by deletion_id: every page it took, back under the original parent if that parent is live. If the parent is gone, the page returns to the space root, but only when it was not under a restriction it inherited or the caller is a space admin; otherwise fails with a reason. Needs edit access to the pages as they were. Restoring bumps the page's live-editing epoch, so open editors reload. Emits note.undeleted for each page. Returns {deletion_id, root_id, space_id, count, placed}.",
		a1Obj([]string{"deletion_id"}, "deletion_id", sA1ID),
		a1Obj([]string{"deletion_id", "root_id", "space_id", "count", "placed"}, "deletion_id", sA1ID, "root_id", sA1ID, "space_id", sA1ID, "count", sA1Int, "placed", a1Enum("original", "space_root")),
		command.GroupWriteNotes, false},
	{"note.duplicate", "Copy one page as its next sibling, titled '<title> (copy)': body, icon, width, labels, the page's own restrictions and permissions, and its attachments (new files, with the links in the body rewritten). Not sub-pages, comments, versions, shares, stars or watches. The caller owns the copy. Only plain pages can be duplicated. Needs view access to the page and edit access to its parent (or the space root). Emits note.created. Returns {id, space_id, parent_id, title, position, attachments_copied}.",
		a1Obj([]string{"id"}, "id", sA1ID),
		a1Obj([]string{"id", "space_id", "parent_id", "title", "position", "attachments_copied"}, "id", sA1ID, "space_id", sA1ID, "parent_id", a1Nullable(sA1ID), "title", sA1Str, "position", sA1Int, "attachments_copied", sA1Int),
		command.GroupWriteNotes, false},
}

// a2Handlers is where L1 registers real implementations, from an init() in its own file.
var a2Handlers = map[string]command.Handler{}

func a2NotImplemented(name string) command.Handler {
	return func(*command.Ctx, json.RawMessage) (any, error) {
		return nil, command.NewError(command.Internal, name+" is not implemented yet")
	}
}

func a2Commands() []command.Command {
	var cmds []command.Command
	for _, s := range a2Specs {
		if s.existing {
			continue
		}
		h := a2Handlers[s.name]
		if h == nil {
			h = a2NotImplemented(s.name)
		}
		c := command.NewCommand(s.name, s.group, h)
		c.Description, c.Schema = s.desc, json.RawMessage(s.in)
		c.MinRole = command.Member // a read command (trash) would default to Guest
		cmds = append(cmds, c)
	}
	return cmds
}

// A2OutputSchema is the JSON Schema of an A2 command's result; apps/web/src/lib/docs.ts mirrors it.
func A2OutputSchema(name string) json.RawMessage {
	for _, s := range a2Specs {
		if s.name == name {
			return json.RawMessage(s.out)
		}
	}
	return nil
}

// A2Describe is the description and input schema L1 sets on note.delete, which write.go registers.
func A2Describe(name string) (string, json.RawMessage) {
	for _, s := range a2Specs {
		if s.name == name {
			return s.desc, json.RawMessage(s.in)
		}
	}
	return "", nil
}
