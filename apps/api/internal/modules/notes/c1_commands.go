package notes

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

// C1 step 0 contracts (design 13). Stubs answer "not implemented" until R2 (cover), R3 (tree) and R4 (purge, retention) register
// handlers from c1Handlers. Access is the handler's job: the tree runs the full access predicate before LIMIT, the cursor and
// has_children; purge is for people who could list the deletion in Trash and rechecks eligibility under the structural lock.
const (
	treeRow = `{"type":"object","properties":{"id":{"type":"string"},"space_id":{"type":"string"},"parent_id":{"type":["string","null"]},"title":{"type":"string"},"icon":{"type":["string","null"]},"position":{"type":"number"},"status":{"type":["string","null"]},"has_children":{"type":"boolean"}},"required":["id","space_id","parent_id","title","position","has_children"]}`
	level   = `{"type":"object","properties":{"parent_id":{"type":["string","null"],"description":"a page id, null for the space root, or \"shared\" for the synthetic Shared with you root"},"rows":{"type":"array","items":` + treeRow + `},"next":{"type":["string","null"]},"shared":{"type":"boolean","description":"root level only: true when pages exist whose ancestors the reader cannot see"}},"required":["parent_id","rows","next"]}`
	cover   = `{"anyOf":[{"type":"object","properties":{"file_id":{"type":"string"},"y":{"type":"integer","minimum":0,"maximum":100}},"required":["file_id"]},{"type":"null"}]}`
)

var c1Specs = []su.Spec{
	{Name: "note.tree", Group: command.GroupRead, MinRole: command.Guest,
		Desc: "Load one readable level of the page tree, or reveal a readable page with its readable ancestors. parent_id null is the space root, \"shared\" is the Shared with you root. limit defaults to 100, at most 200. Returns {parent_id, rows, next, shared} for a level or {levels} for reveal; bodies are never included.",
		In:   su.Obj(nil, "space_id", su.ID, "parent_id", `{"type":["string","null"]}`, "after", su.Str, "limit", su.IntRange(1, 200), "reveal", su.ID),
		Out:  `{"type":"object","properties":{"parent_id":{"type":["string","null"]},"rows":{"type":"array","items":` + treeRow + `},"next":{"type":["string","null"]},"shared":{"type":"boolean"},"levels":{"type":"array","items":` + level + `}}}`},
	{Name: "note.cover_set", Group: command.GroupWriteNotes, MinRole: command.Guest,
		Desc: "Set, reposition or clear the cover of a page (file_id null clears). The file must be an image attached to this page; y is 0 to 100 and defaults to 50. Needs edit access to the page. Returns {cover}.",
		In:   su.Obj([]string{"note_id"}, "note_id", su.ID, "file_id", su.Nullable(su.ID), "y", su.IntRange(0, 100)),
		Out:  su.Obj([]string{"cover"}, "cover", cover)},
	{Name: "note.purge", Group: command.GroupDestructive, MinRole: command.Member, Confirm: true,
		Desc: "Permanently delete one deletion (a set of trashed pages) now. Only a person who could list it in Trash and is a workspace admin or admin of its space; guests never; an agent or API key waits for a person's approval. Cannot be undone. Returns {purged, pages, files, bytes}.",
		In:   su.Obj([]string{"deletion_id"}, "deletion_id", su.ID),
		Out:  su.Obj([]string{"purged"}, "purged", su.Bool, "pages", su.Int, "files", su.Int, "bytes", su.Int)},
	{Name: "trash.retention_get", Group: command.GroupRead, MinRole: command.Member,
		Desc: "How long deleted pages stay in the trash for this workspace. Returns {days, purge_from}; days 0 means never, purge_from is when the current rule took effect.",
		In:   su.Obj(nil),
		Out:  su.Obj([]string{"days"}, "days", su.Int, "purge_from", su.Nullable(su.TS))},
	{Name: "trash.retention_set", Group: command.GroupAdmin, MinRole: command.Admin,
		Desc: "Choose how long deleted pages stay in the trash: 30, 90 or 365 days, or 0 for never. A change restarts the clock: nothing is purged sooner than one full new period from now. Returns {days, purge_from}.",
		In:   su.Obj([]string{"days"}, "days", `{"type":"integer","enum":[0,30,90,365]}`),
		Out:  su.Obj([]string{"days"}, "days", su.Int, "purge_from", su.Nullable(su.TS))},
}
var c1Handlers = map[string]command.Handler{}

func c1Commands() []command.Command              { return su.Commands(c1Specs, c1Handlers) }
func C1OutputSchema(name string) json.RawMessage { return su.OutputSchema(c1Specs, name) }
