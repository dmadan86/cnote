package notes

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

// Phase B1 command contract for the notes module (docs-parity-b 4.1, 4.4, 4.5, 6 and 13): space members and settings,
// comment edit, delete and reactions, and "My tasks". Same shape as a1_commands.go: schemas, descriptions that say what comes
// back, and stubs that answer "not implemented" until the owning package registers the real handler from an init() in its own
// file: b1Handlers["space.members"] = spaceMembers (P1: notes/spaces.go), comment.* (P5: notes/reactions.go and comments.go),
// task.mine (P4: notes/tasks.go).
//
// Rules every handler follows, here so they are not lost between packages:
//   - A space admin manages members and settings only. A page restriction still applies to a space admin (13.2.1), so no
//     handler here ever reads or writes a page on the strength of space admin; page access is access.AssertNoteAccess.
//   - Every write is refused in an archived space (13.2.2). The central gate does it for AssertNoteAccess(write) and
//     AssertSpaceAccess(write). A command that writes after a read-level check (a comment, a reaction) must call
//     access.AssertSpaceWritable with the page's space.
//   - A command that belongs to a person (reactions, task.mine) refuses a principal with no user.
//   - Lists apply the full access predicate inside the query, before counting or limiting (7.6).

var (
	sB1Role  = su.Enum("admin", "editor", "viewer")
	sB1Space = su.Obj([]string{"id", "key", "name", "template", "visibility", "config", "description", "archived", "public_links", "my_role"},
		"id", su.ID, "key", su.Str, "name", su.Str, "template", su.Str, "visibility", su.Enum("open", "private"), "config", `{"type":"object"}`,
		"description", su.Nullable(su.Str), "archived", su.Bool, "public_links", su.Bool, "my_role", su.Nullable(sB1Role))
	sB1Member = su.Obj([]string{"user_id", "name", "email", "role", "workspace_role"},
		"user_id", su.ID, "name", su.Str, "email", su.Str, "role", sB1Role, "workspace_role", su.Enum("owner", "admin", "member", "guest"))
	sB1Reaction = su.Obj([]string{"emoji", "count", "mine"}, "emoji", su.Enum("👍", "❤️", "😂", "🎉", "👀", "✅"), "count", su.Int, "mine", su.Bool)
	sB1Comment  = su.Obj([]string{"id", "note_id", "body", "created_at", "edited_at", "deleted", "reactions"},
		"id", su.ID, "note_id", su.ID, "parent_id", su.Nullable(su.ID), "author_id", su.Nullable(su.ID), "author_name", su.Nullable(su.Str),
		"body", su.Str, "created_at", su.TS, "edited_at", su.Nullable(su.TS), "resolved_at", su.Nullable(su.TS),
		"deleted", su.Bool, "reactions", su.Arr(sB1Reaction))
	sB1Task = su.Obj([]string{"note_id", "note_title", "icon", "space_id", "ord", "text", "due", "done"},
		"note_id", su.ID, "note_title", su.Str, "icon", su.Nullable(su.Str), "space_id", su.ID, "ord", su.Int, "text", su.Str,
		"due", su.Nullable(su.Date), "done", su.Bool)
)

var b1Specs = []su.Spec{
	{Name: "space.members", Group: command.GroupRead, MinRole: command.Member,
		Desc: "Members of a space with their space role, for the settings page. A workspace admin or owner is listed with role admin even without a row. visibility says whether the space is open (every workspace member can edit unless a row says viewer) or private (only the listed). can_manage says whether the caller may change members and settings. Needs read access to the space. Returns {visibility, members: [{user_id, name, email, role, workspace_role}], can_manage}.",
		In:   su.Obj([]string{"space_id"}, "space_id", su.ID),
		Out:  su.Obj([]string{"visibility", "members", "can_manage"}, "visibility", su.Enum("open", "private"), "members", su.Arr(sB1Member), "can_manage", su.Bool)},
	{Name: "space.member_set", Group: command.GroupWriteNotes, MinRole: command.Member, Confirm: true,
		Desc: "Add a workspace member to a space, change their space role, or remove them (role null). Roles: admin (manages members and settings, nothing else: a page restriction still applies), editor, viewer. Needs the caller to be a space admin or a workspace admin or owner. The person must be a workspace member and not a guest. A private space always keeps one admin: removing or demoting the last one is refused. Removal takes effect at once. Emits space.member_changed {space_id, user_id, role, previous}; a person added gets a shared notification. Returns the member row {user_id, name, email, role, workspace_role}, or {removed: true}.",
		In:   su.Obj([]string{"space_id", "user_id", "role"}, "space_id", su.ID, "user_id", su.ID, "role", su.Nullable(sB1Role)),
		Out:  su.AnyOf(sB1Member, su.Obj([]string{"removed"}, "removed", su.Bool))},
	{Name: "space.update", Group: command.GroupWriteNotes, MinRole: command.Member, Confirm: true,
		Desc: "Change a space's name (1 to 80 characters), description (at most 500), visibility (open or private) or public_links (false turns off page links: every active public page link of the space is revoked in the same transaction, and turning it back on revives nothing). The key never changes. Turning a space private keeps the listed members and adds the caller as admin. Needs the caller to be a space admin or a workspace admin or owner. Works on an archived space. Emits space.updated {space_id, changed}. Returns the space.",
		In:   su.Obj([]string{"id"}, "id", su.ID, "name", su.Str, "description", su.Nullable(su.Str), "visibility", su.Enum("open", "private"), "public_links", su.Bool),
		Out:  sB1Space},
	{Name: "space.archive", Group: command.GroupWriteNotes, MinRole: command.Member, Confirm: true,
		Desc: "Archive a space or bring it back (archived true or false). An archived space is read-only for everyone, workspace admins included, on every route (pages, comments, files, tasks, Tables, Projects, direct shares, live editing); it leaves the sidebar and space.list unless include_archived is set, and stays readable and searchable. Its public page links stop serving. Needs the caller to be a space admin or a workspace admin or owner. Emits space.archived or space.unarchived. Returns the space.",
		In:   su.Obj([]string{"id", "archived"}, "id", su.ID, "archived", su.Bool),
		Out:  sB1Space},
	{Name: "comment.edit", Group: command.GroupWriteNotes, MinRole: command.Guest,
		Desc: "Edit the text of one's own comment; it then shows as edited. Needs view access to the page (not found otherwise, so a person who lost access cannot tell whether the comment exists), a space that is not archived, and a comment that is not deleted. Only the author may edit. People newly mentioned in the new text are notified. Emits comment.edited {comment_id}, without the text. Returns the comment.",
		In:   su.Obj([]string{"id", "body"}, "id", su.ID, "body", su.Str), Out: sB1Comment},
	{Name: "comment.delete", Group: command.GroupWriteNotes, MinRole: command.Guest, Confirm: true,
		Desc: "Delete a comment. The body is erased from the row at once. A top-level comment that still has replies stays as a 'Comment deleted' line so the thread holds together; any other deleted comment disappears from comment.list. The author, the page owner, a space admin or a workspace admin may delete. Needs view access to the page (not found otherwise) and a space that is not archived. Emits comment.deleted {comment_id, by_author}, without the text. Returns {deleted: true}.",
		In:   su.Obj([]string{"id"}, "id", su.ID), Out: su.Obj([]string{"deleted"}, "deleted", su.Bool)},
	{Name: "comment.react", Group: command.GroupWriteNotes, MinRole: command.Guest,
		Desc: "Add or remove the caller's reaction on a comment: one of 👍 ❤️ 😂 🎉 👀 ✅, each at most once per person and comment (on true; false removes it). Needs view access to the page (not found otherwise), a space that is not archived and a comment that is not deleted. Belongs to a person: a bare API key is refused. Emits nothing. Returns {id, reactions: [{emoji, count, mine}]}.",
		In:   su.Obj([]string{"id", "emoji", "on"}, "id", su.ID, "emoji", su.Enum("👍", "❤️", "😂", "🎉", "👀", "✅"), "on", su.Bool),
		Out:  su.Obj([]string{"id", "reactions"}, "id", su.ID, "reactions", su.Arr(sB1Reaction))},
	{Name: "task.mine", Group: command.GroupRead, MinRole: command.Guest,
		Desc: "The caller's tasks: to-do items on pages whose first mention is the caller. due is the first date in the item. Overdue first, then by due date, undated last; done false by default (done true lists completed ones too). limit defaults to 100, at most 200. A task on a page the caller can no longer read, in the trash or archived is left out before the limit applies. A guest sees tasks on pages shared with them only. Belongs to a person: a bare API key is refused. Returns [{note_id, note_title, icon, space_id, ord, text, due, done}].",
		In:   su.Obj(nil, "done", su.Bool, "limit", su.IntRange(1, 200)), Out: su.Arr(sB1Task)},
}

// b1Handlers is where the work packages register real implementations, from an init() in their own files.
var b1Handlers = map[string]command.Handler{}

func b1Commands() []command.Command { return su.Commands(b1Specs, b1Handlers) }

// B1OutputSchema is the JSON Schema of a B1 command's result; apps/web/src/lib/docs.ts mirrors it.
func B1OutputSchema(name string) json.RawMessage { return su.OutputSchema(b1Specs, name) }

// B1SpaceSchema is the one space object (space.list, space.update, space.archive): exported so tests and the web types name the
// same shape.
func B1SpaceSchema() json.RawMessage { return json.RawMessage(sB1Space) }
