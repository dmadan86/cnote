package notes

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

var b2Specs = []su.Spec{{Name: "note.request_access", Group: command.GroupWriteNotes, MinRole: command.Guest, Desc: "Request access without revealing whether a page exists or is readable. Limits and deduplication apply even when delivery is suppressed. Returns {ok: true}.", In: su.Obj([]string{"note_id"}, "note_id", su.ID, "message", `{"type":"string","maxLength":200}`), Out: su.Obj([]string{"ok"}, "ok", `{"const":true}`)},
	{Name: "note.viewers", Group: command.GroupRead, MinRole: command.Guest, Desc: "Count current workspace members who viewed a readable page. Only editors receive names and last viewed times; guests receive the count only. Returns {count, viewers?}.", In: su.Obj([]string{"note_id"}, "note_id", su.ID), Out: su.Obj([]string{"count"}, "count", su.Int, "viewers", `{"type":"array","maxItems":100,"items":`+su.Obj([]string{"user_id", "name", "last_viewed_at"}, "user_id", su.ID, "name", su.Str, "last_viewed_at", su.TS)+`}`)}}
var b2Handlers = map[string]command.Handler{}

func b2Commands() []command.Command              { return su.Commands(b2Specs, b2Handlers) }
func B2OutputSchema(name string) json.RawMessage { return su.OutputSchema(b2Specs, name) }
