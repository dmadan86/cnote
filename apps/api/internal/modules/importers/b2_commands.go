package importers

import (
	"encoding/json"
	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

var b2Specs = []su.Spec{{Name: "import.docx", Group: command.GroupWriteNotes, MinRole: command.Member, Desc: "Import one Word document into a space, requiring create access and edit access to its parent. Returns {id, title, warnings}.", In: su.Obj([]string{"space_id", "filename", "data_base64"}, "space_id", su.ID, "parent_id", su.ID, "filename", su.Str, "data_base64", su.Str), Out: su.Obj([]string{"id", "title", "warnings"}, "id", su.ID, "title", su.Str, "warnings", su.Arr(su.Str))}}
var b2Handlers = map[string]command.Handler{}

func b2Commands() []command.Command              { return su.Commands(b2Specs, b2Handlers) }
func B2OutputSchema(name string) json.RawMessage { return su.OutputSchema(b2Specs, name) }
