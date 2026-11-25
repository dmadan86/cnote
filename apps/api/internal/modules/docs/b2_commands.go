package docs

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

var b2Specs = []su.Spec{{Name: "doc.archive", Group: command.GroupWriteNotes, MinRole: command.Member, Desc: "Archive a plain page and its live subtree under the space lock, requiring edit access to every page, up to 500 pages. Returns {archived: n}.", In: su.Obj([]string{"id"}, "id", su.ID), Out: su.Obj([]string{"archived"}, "archived", su.Int)}}
var b2Handlers = map[string]command.Handler{}

func b2Commands() []command.Command              { return su.Commands(b2Specs, b2Handlers) }
func B2OutputSchema(name string) json.RawMessage { return su.OutputSchema(b2Specs, name) }

func templateRenderCommand() command.Command {
	c := command.NewCommand("template.render", command.GroupWriteNotes, templateRender)
	c.Schema = json.RawMessage(su.Obj([]string{"template_id"}, "template_id", su.Str, "source_id", su.ID, "space_id", su.ID, "parent_id", su.ID, "title", su.Str, "answers", `{"type":"object","maxProperties":20,"additionalProperties":{"type":"string","maxLength":500,"pattern":"^[^\\r\\n]*$"}}`))
	return c
}
